package sqlstore

// import.go — the one-time JSON→sqlite bridge. The promotion made the
// single database the ledger family's only persistence; this file is
// the JSON layout's LAST reader (the deleted LocalStores used to be
// the first). Per-domain idempotence: a side that already holds rows
// under the target namespace is LEFT ALONE (never mix two sources),
// and the file side stays untouched as the forensic copy. Boot guards
// the whole run with a one-shot stamp (~/.niuma/.ledger_imported) so a
// quarantined-or-deleted database is never re-seeded from the stale
// JSON era — the anti-resurrection rule the fallback matrix rests on.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// ImportReport is the bridge's take (the boot log's line and the
// verify record's numbers).
type ImportReport struct {
	ReqShelves int // requirement shelves created (counters installed)
	Reqs       int // requirement rows inside those shelves
	Plans      int // projects with plan slot state imported (pending or counter > 1)
	Merges     int // ditto, merge slots
	Meetings   int // meeting shelves imported (counters + finished history)
	Notices    int // notice rows imported
}

// ImportLocal is the single-user bridge (target namespace "").
func (d *DB) ImportLocal(root string) (ImportReport, error) {
	return d.ImportLocalInto(root, "")
}

// ImportLocalInto is the bridge into an explicit target namespace —
// the multi-user first boot imports the JSON era directly into the
// admin account's namespace (no ” intermediate to re-home, and the
// per-side emptiness gate is judged at the target ns so the forensic
// JSON copy is never re-imported after the migration). root is the
// studio root (~/.niuma): requirements/, plan/, merge/, meetings/,
// notice/ live under it (tasks/ and the flat ranks file ride the
// tasks bridge, ImportTasksLedgerInto).
func (d *DB) ImportLocalInto(root, ns string) (ImportReport, error) {
	var rep ImportReport
	if root == "" {
		return rep, nil
	}
	if err := d.importReqShelves(ns, filepath.Join(root, "requirements"), &rep); err != nil {
		return rep, err
	}
	if err := d.importSlots(ns, filepath.Join(root, "plan"), "plan_slots", &rep.Plans,
		func(raw json.RawMessage) (any, int, error) {
			p := new(plan.Plan)
			if err := json.Unmarshal(raw, p); err != nil {
				return nil, 0, err
			}
			var n int
			fmt.Sscanf(p.ID, "p_%d", &n)
			return *p, n, nil
		}); err != nil {
		return rep, err
	}
	if err := d.importSlots(ns, filepath.Join(root, "merge"), "merge_slots", &rep.Merges,
		func(raw json.RawMessage) (any, int, error) {
			m := new(merge.Merge)
			if err := json.Unmarshal(raw, m); err != nil {
				return nil, 0, err
			}
			var n int
			fmt.Sscanf(m.ID, "m_%d", &n)
			return *m, n, nil
		}); err != nil {
		return rep, err
	}
	if err := d.importMeetingShelves(ns, filepath.Join(root, "meetings"), &rep); err != nil {
		return rep, err
	}
	if err := d.importNotices(ns, filepath.Join(root, "notice"), &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// importReqShelves bridges the requirements rack: each {next, reqs}
// envelope becomes ONE req_shelves row (the shelf IS one document —
// the shape the engine's seam speaks; the v1 normalized rows are
// gone with the full-face implementation they served).
func (d *DB) importReqShelves(ns, dir string, rep *ImportReport) error {
	entries, err := shelfEntries(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	return d.Tx(func(tx *TxStores) error {
		q := tx.q
		var existing int
		if err := q.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM req_shelves WHERE ns=?`, ns).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return nil // already bridged: never mix two sources
		}
		for key, raw := range entries {
			var shelf struct {
				Next int                `json:"next"`
				Reqs []requirements.Req `json:"reqs"`
			}
			if err := json.Unmarshal(raw, &shelf); err != nil {
				return fmt.Errorf("parse %s/%s.json: %w", dir, key, err)
			}
			if shelf.Next < 1 {
				shelf.Next = 1
			}
			for i, r := range shelf.Reqs {
				var n int
				if _, err := fmt.Sscanf(r.ID, "r_%d", &n); err == nil && n >= shelf.Next {
					shelf.Next = n + 1
				}
				if shelf.Reqs[i].ProjectKey == "" {
					shelf.Reqs[i].ProjectKey = key // hand-edited rows missing their stamp
				}
			}
			doc, err := json.Marshal(shelf.Reqs)
			if err != nil {
				return err
			}
			if _, err := q.ExecContext(context.Background(),
				`INSERT INTO req_shelves (ns, project, next, doc) VALUES (?, ?, ?, ?)
				ON CONFLICT (ns, project) DO UPDATE SET next=excluded.next, doc=excluded.doc`,
				ns, key, shelf.Next, string(doc)); err != nil {
				return err
			}
			rep.ReqShelves++
			rep.Reqs += len(shelf.Reqs)
		}
		return nil
	})
}

// importSlots bridges one slot-shaped domain (plan/merge): {next,
// pending} files become slot rows; the counter floors at the envelope
// next and lifts past a pending proposal's own id.
func (d *DB) importSlots(ns, dir, table string, count *int,
	decode func(json.RawMessage) (any, int, error)) error {
	entries, err := shelfEntries(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	return d.Tx(func(tx *TxStores) error {
		q := tx.q
		var existing int
		if err := q.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM `+table+` WHERE ns=?`, ns).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}
		for key, raw := range entries {
			var shelf struct {
				Next    int             `json:"next"`
				Pending json.RawMessage `json:"pending"`
			}
			if err := json.Unmarshal(raw, &shelf); err != nil {
				return fmt.Errorf("parse %s/%s.json: %w", dir, key, err)
			}
			if shelf.Next < 1 {
				shelf.Next = 1
			}
			var doc any
			if len(shelf.Pending) > 0 && string(shelf.Pending) != "null" {
				v, n, err := decode(shelf.Pending)
				if err != nil {
					return fmt.Errorf("parse %s/%s.json pending: %w", dir, key, err)
				}
				if n >= shelf.Next {
					shelf.Next = n + 1
				}
				doc = v
			}
			var arg any
			if doc != nil {
				b, err := json.Marshal(doc)
				if err != nil {
					return err
				}
				arg = string(b)
			}
			if _, err := q.ExecContext(context.Background(),
				`INSERT INTO `+table+` (ns, project, next, pending) VALUES (?, ?, ?, ?)
				ON CONFLICT (ns, project) DO UPDATE SET next=excluded.next, pending=excluded.pending`,
				ns, key, shelf.Next, arg); err != nil {
				return err
			}
			if doc != nil || shelf.Next > 1 {
				*count++
			}
		}
		return nil
	})
}

// importMeetingShelves bridges the meeting rack: each {next, hist}
// envelope becomes ONE meeting_shelves row.
func (d *DB) importMeetingShelves(ns, dir string, rep *ImportReport) error {
	entries, err := shelfEntries(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	return d.Tx(func(tx *TxStores) error {
		q := tx.q
		var existing int
		if err := q.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM meeting_shelves WHERE ns=?`, ns).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}
		for key, raw := range entries {
			var shelf struct {
				Next int               `json:"next"`
				Hist []meeting.Meeting `json:"hist"`
			}
			if err := json.Unmarshal(raw, &shelf); err != nil {
				return fmt.Errorf("parse %s/%s.json: %w", dir, key, err)
			}
			if shelf.Next < 1 {
				shelf.Next = 1
			}
			for _, m := range shelf.Hist {
				var n int
				if _, err := fmt.Sscanf(m.ID, "m_%d", &n); err == nil && n >= shelf.Next {
					shelf.Next = n + 1
				}
			}
			doc, err := json.Marshal(shelf.Hist)
			if err != nil {
				return err
			}
			if _, err := q.ExecContext(context.Background(),
				`INSERT INTO meeting_shelves (ns, project, next, doc) VALUES (?, ?, ?, ?)
				ON CONFLICT (ns, project) DO UPDATE SET next=excluded.next, doc=excluded.doc`,
				ns, key, shelf.Next, string(doc)); err != nil {
				return err
			}
			rep.Meetings++
		}
		return nil
	})
}

// importNotices bridges the notice rack: each <key>.json (one Notice
// document) becomes one notice_rows row.
func (d *DB) importNotices(ns, dir string, rep *ImportReport) error {
	entries, err := shelfEntries(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	return d.Tx(func(tx *TxStores) error {
		q := tx.q
		var existing int
		if err := q.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM notice_rows WHERE ns=?`, ns).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}
		for key, raw := range entries {
			var n notice.Notice
			if err := json.Unmarshal(raw, &n); err != nil {
				return fmt.Errorf("parse %s/%s.json: %w", dir, key, err)
			}
			if n.Rev <= 0 || strings.TrimSpace(n.Content) == "" {
				continue // not a published notice: not ours to import
			}
			if _, err := q.ExecContext(context.Background(),
				`INSERT INTO notice_rows (ns, room, doc) VALUES (?, ?, ?)
				ON CONFLICT (ns, room) DO UPDATE SET doc=excluded.doc`,
				ns, key, string(raw)); err != nil {
				return err
			}
			rep.Notices++
		}
		return nil
	})
}

// TasksImportReport is the tasks-side bridge's take (the boot log's
// line and the verify record's numbers).
type TasksImportReport struct {
	Shelves int // task shelves imported (rows + counters)
	Tasks   int // task rows inside those shelves
	Ranks   int // rank registry rows imported
}

// ImportTasksLedger folds the JSON era's task shelves
// (<root>/tasks/<key>.json, the {next, tasks} envelopes) and the flat
// ranks file (~/.niuma_ranks.json — deliberately flat-legacy, so the
// path is passed, not derived) into this database. Same idempotence
// discipline as ImportLocal.
// ImportTasksLedger is the single-user tasks bridge (target ns "").
func (d *DB) ImportTasksLedger(taskDir, rankPath string) (TasksImportReport, error) {
	return d.ImportTasksLedgerInto(taskDir, rankPath, "")
}

// ImportTasksLedgerInto is the tasks bridge into an explicit target
// namespace (the multi-user first boot's half of ImportLocalInto).
func (d *DB) ImportTasksLedgerInto(taskDir, rankPath, ns string) (TasksImportReport, error) {
	var rep TasksImportReport
	entries, err := shelfEntries(taskDir)
	if err != nil {
		return rep, err
	}
	var rankRows map[string]int
	if rankPath != "" {
		b, err := os.ReadFile(rankPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return rep, err
		}
		if err == nil && len(b) > 0 {
			rankRows = map[string]int{}
			if err := json.Unmarshal(b, &rankRows); err != nil {
				return rep, fmt.Errorf("parse %s: %w", rankPath, err)
			}
		}
	}
	err = d.Tx(func(tx *TxStores) error {
		q := tx.q
		var existing int
		if err := q.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM tasks_shelves WHERE ns=?`, ns).Scan(&existing); err != nil {
			return err
		}
		if existing == 0 {
			for key, raw := range entries {
				var shelf struct {
					Next  int           `json:"next"`
					Tasks []*tasks.Task `json:"tasks"`
				}
				if err := json.Unmarshal(raw, &shelf); err != nil {
					return fmt.Errorf("parse %s/%s.json: %w", taskDir, key, err)
				}
				if shelf.Next < 1 {
					shelf.Next = 1
				}
				for _, t := range shelf.Tasks {
					var n int
					if _, err := fmt.Sscanf(t.ID, "t_%d", &n); err == nil && n >= shelf.Next {
						shelf.Next = n + 1
					}
				}
				if err := tx.tasks.SaveShelf(ns, key, &tasks.Shelf{Next: shelf.Next, Tasks: shelf.Tasks}); err != nil {
					return err
				}
				rep.Shelves++
				rep.Tasks += len(shelf.Tasks)
			}
		}
		if len(rankRows) > 0 {
			var one int
			err := q.QueryRowContext(context.Background(),
				`SELECT 1 FROM task_ranks WHERE ns=? LIMIT 1`, ns).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				for name, lv := range rankRows {
					if _, err := q.ExecContext(context.Background(),
						`INSERT INTO task_ranks (ns, name, lv) VALUES (?, ?, ?)`, ns, name, lv); err != nil {
						return err
					}
					rep.Ranks++
				}
			} else if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return TasksImportReport{}, err
	}
	return rep, nil
}

// shelfEntries reads every <key>.json under dir (missing dir = none),
// skipping foreign keys — not ours to read or import (the deleted
// LocalStore's discipline, kept for the bridge).
func shelfEntries(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		key := strings.TrimSuffix(ent.Name(), ".json")
		if !projects.ValidKey(key) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			return nil, err
		}
		out[key] = b
	}
	return out, nil
}
