package sqlstore

// tasks.go — the task ledger + rank registry over one database
// (Phase 1 second batch). Translation baseline (Phase 1 store
// semantics): one row per project shelf holding
// the p_NN-style counter plus the shelf's tasks as a JSON document
// (the shelf IS one document, plan/merge slot shape; the array order
// is the insertion order RenumberPlan renumbers by), and one row per
// rank in a studio-level table (ns stays '' — ranks are studio-wide
// even under Phase 3, per the inventory).
//
// Failure semantics: the Engine speaks A-族 discipline (a save
// failure is logged, the in-memory truth stays ahead until the next
// save — phase1b-rejection-audit.md §4's explicit ruling); this side
// just has to make each whole-shelf replace one transaction, so a
// torn shelf is never readable and the cross-store Tx seam
// (TxStores.Tasks) can hold "任务账不半" over plan-acceptance.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/WWestC/Niuma_Studio/tasks"
)

// TaskStore is the tasks.Store seam over one database.
type TaskStore struct {
	db DBTX
}

var _ tasks.Store = (*TaskStore)(nil)

// OpenShelves loads every shelf row (counter + document) and the rank
// rows. An empty database is the fresh-install state, not an error; a
// corrupt document is (the boot corrupt-reset path owns what happens
// next — never silently continue on a half-readable ledger).
func (s *TaskStore) OpenShelves(subject string) (map[string]*tasks.Shelf, map[string]int, error) {
	shelves := map[string]*tasks.Shelf{}
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT project, next, doc FROM tasks_shelves WHERE ns=? ORDER BY project ASC`, subject)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var project string
		var next int
		var doc string
		if err := rows.Scan(&project, &next, &doc); err != nil {
			return nil, nil, err
		}
		var list []*tasks.Task
		if err := json.Unmarshal([]byte(doc), &list); err != nil {
			return nil, nil, fmt.Errorf("tasks shelf %s 损坏: %w", project, err)
		}
		shelves[project] = &tasks.Shelf{Next: next, Tasks: list}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	ranks := map[string]int{}
	rrows, err := s.db.QueryContext(context.Background(),
		`SELECT name, lv FROM task_ranks WHERE ns=?`, subject)
	if err != nil {
		return nil, nil, err
	}
	defer rrows.Close()
	for rrows.Next() {
		var name string
		var lv int
		if err := rrows.Scan(&name, &lv); err != nil {
			return nil, nil, err
		}
		ranks[name] = lv
	}
	return shelves, ranks, rrows.Err()
}

// SaveShelf replaces one project's shelf — rows-as-document and
// counter in ONE transaction (a torn shelf is never readable).
func (s *TaskStore) SaveShelf(subject, slot string, f *tasks.Shelf) error {
	b, err := json.Marshal(f.Tasks)
	if err != nil {
		return err
	}
	return runInTx(s.db, func(q DBTX) error {
		_, err := q.ExecContext(context.Background(),
			`INSERT INTO tasks_shelves (ns, project, next, doc) VALUES (?, ?, ?, ?)
			ON CONFLICT (ns, project) DO UPDATE SET next=excluded.next, doc=excluded.doc`,
			subject, slot, f.Next, string(b))
		return err
	})
}

// DeleteShelf removes one project's shelf row outright (idempotent —
// the project-wipe half; numbering restarts from t_01 with the row).
func (s *TaskStore) DeleteShelf(subject, slot string) error {
	return runInTx(s.db, func(q DBTX) error {
		_, err := q.ExecContext(context.Background(),
			`DELETE FROM tasks_shelves WHERE ns=? AND project=?`, subject, slot)
		return err
	})
}

// SaveRanks replaces the whole rank registry in one transaction (the
// JSON era's whole-file rewrite; the registry is studio-small).
func (s *TaskStore) SaveRanks(subject string, ranks map[string]int) error {
	return runInTx(s.db, func(q DBTX) error {
		if _, err := q.ExecContext(context.Background(),
			`DELETE FROM task_ranks WHERE ns=?`, subject); err != nil {
			return err
		}
		for name, lv := range ranks {
			if _, err := q.ExecContext(context.Background(),
				`INSERT INTO task_ranks (ns, name, lv) VALUES (?, ?, ?)`, subject, name, lv); err != nil {
				return err
			}
		}
		return nil
	})
}

// shelfExists reports whether the tasks side holds any row under this
// namespace (the import bridge's tables-empty gate).
func (s *TaskStore) shelfExists(q DBTX, ns string) (bool, error) {
	var one int
	err := q.QueryRowContext(context.Background(),
		`SELECT 1 FROM tasks_shelves WHERE ns=? LIMIT 1`, ns).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ledgerDocTables are the family's (ns, key, next, doc) tables plus
// the two side tables — everything a subject's ledger reset clears.
var ledgerDocTables = []string{
	"req_shelves", "plan_slots", "merge_slots", "tasks_shelves", "task_ranks",
	"meeting_shelves", "notice_rows",
}

// ExportLedgerSnapshot writes every ledger row (all seven tables,
// every namespace) to path as one JSON document — the corrupt-reset
// path's forensic copy (a database row cannot be renamed aside, so
// the bad bytes get exported before the reset clears them).
func (d *DB) ExportLedgerSnapshot(path string) error {
	snap := map[string]any{}
	for _, t := range ledgerDocTables {
		rows := []map[string]any{}
		q, err := d.db.Query(`SELECT * FROM ` + t)
		if err != nil {
			return err
		}
		cols, _ := q.Columns()
		for q.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := q.Scan(ptrs...); err != nil {
				q.Close()
				return err
			}
			row := map[string]any{}
			for i, c := range cols {
				row[c] = vals[i]
			}
			rows = append(rows, row)
		}
		q.Close()
		if err := q.Err(); err != nil {
			return err
		}
		snap[t] = rows
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ResetLedgerNS clears one subject's rows across the whole ledger
// family — the nuclear option. One transaction: whole or not at all.
func (d *DB) ResetLedgerNS(ns string) error {
	return d.ResetTablesNS(ledgerDocTables, ns)
}

// ResetTablesNS clears one subject's rows in exactly the named tables
// — the per-domain corrupt-reset's data half (a corrupt requirements
// shelf must not cost the task ledger its good rows). The boot path
// backs the bytes up (ExportLedgerSnapshot) and announces in the room
// before calling this.
func (d *DB) ResetTablesNS(tables []string, ns string) error {
	return d.Tx(func(tx *TxStores) error {
		for _, t := range tables {
			if _, err := tx.q.ExecContext(context.Background(),
				`DELETE FROM `+t+` WHERE ns=?`, ns); err != nil {
				return err
			}
		}
		return nil
	})
}
