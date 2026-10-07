package sqlstore

// multiuser.go — the multi-user first-boot migration's data half:
// every namespaced table's ns='' rows (the single-user era's, or a
// single-user sqlite stint's) move into the studio owner account's
// namespace, all-or-nothing in one transaction. Idempotent by shape
// (WHERE ns='' is self-draining); a PK collision between a '' row and
// an already-homed row rolls the whole move back and the error rides
// out — fail-close beats serving half-migrated data.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// namespacedTables is every table carrying the ns column and rows
// that belong to the studio subject (the ledger family's seven — the
// v1 normalized requirements tables are gone with the full-face
// implementation; convertLegacyRequirements folds any v3-era rows
// into req_shelves before this list is ever walked).
var namespacedTables = []string{
	"req_shelves", "plan_slots", "merge_slots", "tasks_shelves", "task_ranks",
	"meeting_shelves", "notice_rows",
}

// RehomeNamespace moves every ns=” row across the namespaced tables
// into subject. Returns how many rows moved (0 on an already-migrated
// database — the idempotence proof second boot relies on).
func (d *DB) RehomeNamespace(subject string) (int64, error) {
	if subject == "" {
		return 0, errors.New("sqlstore: RehomeNamespace 需要非空主体（空串是单用户历史形状）")
	}
	var total int64
	err := d.Tx(func(tx *TxStores) error {
		q := tx.q
		for _, t := range namespacedTables {
			res, err := q.ExecContext(context.Background(),
				`UPDATE `+t+` SET ns=? WHERE ns=''`, subject)
			if err != nil {
				return fmt.Errorf("%s: %w", t, err)
			}
			if n, err := res.RowsAffected(); err == nil {
				total += n
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("sqlstore: 存量归位主体 %s 失败（事务已回滚，库未动）: %w", subject, err)
	}
	return total, nil
}

// HasDefaultNSRows reports whether any ns=” row exists across the
// namespaced tables — the single-user sqlite era's leftovers. The
// identity migration uses it to decide the JSON bridge's role: when
// ” rows exist they ARE the bridged JSON already (the single-user
// boots imported them), so the multi-user first boot rehomes instead
// of re-importing (which would collide on every primary key).
func (d *DB) HasDefaultNSRows() (bool, error) {
	for _, t := range namespacedTables {
		var one int
		err := d.db.QueryRowContext(context.Background(),
			`SELECT 1 FROM `+t+` WHERE ns='' LIMIT 1`).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
