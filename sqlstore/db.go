// Package sqlstore is the single-database Store implementation family —
// Phase 1's engine decision A, made on these grounds: modernc.org/
// sqlite is pure Go (no cgo: the SAME toolchain builds darwin, windows
// and linux, which the three-platform CI matrix builds as-is), and it
// has query power — the multi-user data plane's read shape is filter
// by user/project, which a key-value engine cannot serve. The cost is
// the repository's third external dependency (2→3, recorded in
// ARCHITECTURE.md); bbolt was rejected as lighter but queryless.
//
// One SQLite file holds every domain's tables (表按域分); a cross-store
// write is one BEGIN/COMMIT, so "三处写中途死" leaves no half-written
// state — the atomicity the per-file JSON era could not offer (see
// DB.Tx and the fault-injection proof in tx_test.go). WAL mode keeps
// readers off the writer's lock and crashes mid-commit roll back to
// the last complete transaction.
//
// Every table carries an ns (namespace) column, reserved for Phase 3's
// identity work: single-user mode is permanently ns=” and no table
// ever grows the column later — Phase 3 must not re-migrate data.
//
// Failure semantics (the deliberate translation of the JSON era's
// "log, never refuse; memory stays ahead"): methods that CAN refuse
// (they return an error) surface transaction failures as errors — a
// transactional store honestly refuses rather than pretending the
// write landed; methods whose signatures carry no error (plan/merge
// Submit) keep the log-not-refuse discipline and let the DB stay the
// single truth (a failed Submit reads back as not-submitted; nothing
// in-memory can drift ahead of it).
package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"

	_ "modernc.org/sqlite" // the engine decision itself (see package comment)
)

// DB is one SQLite file's worth of stores. The per-domain Store views
// (Requirements/Plan/Merge) share it; DB.Tx binds all three to a
// single transaction. ns is the namespace the views read/write under
// ("" in single-user mode).
type DB struct {
	db   *sql.DB
	path string
}

// DBTX is the subset of *sql.DB and *sql.Tx the store views use — the
// type that lets one ReqStore run either autocommit (over the DB) or
// inside a caller's transaction (over the Tx).
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// schema is append-only: every migration step lands as one more
// statements slice entry guarded by the schema version in store_meta,
// never an edit to history (the repo's 迁移纪律, same posture as the
// wire contract's additive-only rule).
var schema = []string{
	// v1 — Phase 1 first batch: requirements normalized (it is the
	// ledger faces filter and sort), plan/merge slots as JSON
	// documents (each IS one document; supersede replaces it whole),
	// per-project counters, and a rev cell per namespace. rowid is
	// deliberately kept on `requirements`: it is the insertion order
	// the JSON shelf's slice order used to carry.
	`CREATE TABLE IF NOT EXISTS store_meta (
		ns TEXT NOT NULL DEFAULT '',
		k  TEXT NOT NULL,
		v  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (ns, k)
	);
	CREATE TABLE IF NOT EXISTS requirements (
		ns           TEXT NOT NULL DEFAULT '',
		project      TEXT NOT NULL,
		id           TEXT NOT NULL,
		title        TEXT NOT NULL,
		body         TEXT NOT NULL DEFAULT '',
		status       TEXT NOT NULL,
		created_by   TEXT NOT NULL DEFAULT '',
		created_ts   INTEGER NOT NULL DEFAULT 0,
		park_note    TEXT NOT NULL DEFAULT '',
		park_ts      INTEGER NOT NULL DEFAULT 0,
		review_after INTEGER NOT NULL DEFAULT 0,
		closed_ts    INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (ns, project, id)
	);
	CREATE TABLE IF NOT EXISTS req_counters (
		ns      TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL,
		next    INTEGER NOT NULL DEFAULT 1,
		PRIMARY KEY (ns, project)
	);
	CREATE TABLE IF NOT EXISTS plan_slots (
		ns      TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL,
		next    INTEGER NOT NULL DEFAULT 1,
		pending TEXT,
		PRIMARY KEY (ns, project)
	);
	CREATE TABLE IF NOT EXISTS merge_slots (
		ns      TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL,
		next    INTEGER NOT NULL DEFAULT 1,
		pending TEXT,
		PRIMARY KEY (ns, project)
	);`,
	// v2 — Phase 1 second batch: the task ledger joins the database.
	// One row per project shelf (counter + the shelf's tasks as a JSON
	// document — the shelf IS one document, plan/merge slot shape; the
	// array order is the insertion order the renumber plan walks), plus
	// the studio-wide rank registry (ranks stay studio-level under
	// Phase 3 too, so ns is permanently '' there — the column rides
	// along for schema uniformity, semantics say studio).
	`CREATE TABLE IF NOT EXISTS tasks_shelves (
		ns      TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL,
		next    INTEGER NOT NULL DEFAULT 1,
		doc     TEXT NOT NULL,
		PRIMARY KEY (ns, project)
	);
	CREATE TABLE IF NOT EXISTS task_ranks (
		ns   TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		lv   INTEGER NOT NULL,
		PRIMARY KEY (ns, name)
	);`,
	// v4 — the sqlite promotion: the requirements engine joins the
	// document-shape family (its v1 normalized rows were the last
	// second copy of a domain's semantics — convertLegacyRequirements
	// folds them into shelf documents once, after migrate), and the
	// meeting/notice ledgers move in from the JSON file rack (the
	// one-time bridge reads their old directories; the files stay as
	// the forensic copy).
	`CREATE TABLE IF NOT EXISTS req_shelves (
		ns      TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL,
		next    INTEGER NOT NULL DEFAULT 1,
		doc     TEXT NOT NULL,
		PRIMARY KEY (ns, project)
	);
	CREATE TABLE IF NOT EXISTS meeting_shelves (
		ns      TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL,
		next    INTEGER NOT NULL DEFAULT 1,
		doc     TEXT NOT NULL,
		PRIMARY KEY (ns, project)
	);
	CREATE TABLE IF NOT EXISTS notice_rows (
		ns  TEXT NOT NULL DEFAULT '',
		room TEXT NOT NULL,
		doc TEXT NOT NULL,
		PRIMARY KEY (ns, room)
	);`,
	// v3 — the identity layer: studio-level accounts (username as the
	// key, role as TEXT for human-debuggable storage, salt+hash blobs
	// per identity/credential.go) and the account↔project grant table
	// scope.go's server-side enforcement reads. Accounts are
	// studio-global: no ns column — one deployment's DB is one
	// studio's roster.
	`CREATE TABLE IF NOT EXISTS accounts (
		username     TEXT PRIMARY KEY,
		role         TEXT NOT NULL,
		display_name TEXT NOT NULL DEFAULT '',
		salt         BLOB NOT NULL,
		hash         BLOB NOT NULL,
		created_ts   INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS account_projects (
		username    TEXT NOT NULL,
		project_key TEXT NOT NULL,
		PRIMARY KEY (username, project_key)
	);`,
}

// The open-failure vocabulary boot's openLedgers branches on — the
// corruption/fallback discipline's three classes (语义对照表 §6): a
// corrupt file quarantines and reopens fresh; a migration failure or a
// newer-than-us schema falls back to the JSON shelves LOUDLY with the
// file untouched (its data is fine, this binary just can't move it
// forward); anything else is environment (the JSON fallback's original
// quiet log — no stale-snapshot risk, the DB never existed).
var (
	// ErrCorrupt: the file exists but will not read as a database
	// (garbage bytes, truncation, disk errors) — class ①, quarantine.
	ErrCorrupt = errors.New("sqlstore: 库文件损坏或不可读")
	// ErrMigrate: the file reads fine but a schema step failed —
	// class ②, loud fallback; migrate() is transactional so the file
	// stays at its previous version, untouched.
	ErrMigrate = errors.New("sqlstore: schema 迁移失败（库文件未动）")
	// ErrNewerSchema: the file's schema version is ahead of this
	// binary (an old program opening a newer DB). Refused BEFORE the
	// version stamp write — the stamp must never be downgraded, or the
	// next newer binary would replay migrations over half-known tables.
	ErrNewerSchema = errors.New("sqlstore: 库 schema 版本比本程序新（请先升级程序）")
)

// Open opens (creating if needed) the single database at path. The
// pragmas ride the DSN so they hold before the first statement:
// busy_timeout queues writers instead of erroring, WAL keeps the
// crash window one commit wide, synchronous=NORMAL is the WAL-safe
// setting (a power loss may lose the last commit, never corrupts
// history — same guarantee class as the JSON era's atomic rename).
// One connection: this is a local-first single-process studio; total
// serialization is cheaper than busy-waiting on our own writes.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("sqlstore: 库路径不能为空")
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// canary：sql.Open 是惰性的——先强制真连接并证明这文件读得成
	// 库，再让 migrate 碰它。垃圾/截断文件在这里就以 ErrCorrupt 现形
	// （对照表 §6 ①类，boot 侧走隔离重开），而不是混进迁移失败里
	// 冒充 ②类。空新库 sqlite_master 恒零行，count(*) 恒有一行可读。
	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&tables); err != nil {
		db.Close()
		return nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, path, err)
	}
	d := &DB{db: db, path: path}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlstore %s: %w", path, err)
	}
	if err := d.convertLegacyRequirements(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlstore %s: 遗留需求行表归档失败: %w", path, err)
	}
	return d, nil
}

// convertLegacyRequirements folds the v1 normalized requirements rows
// (requirements + req_counters — the deleted full-face
// implementation's tables) into req_shelves documents, once: per
// (ns, project) the rows in rowid (insertion) order become ONE shelf
// document, the counter and the highest stored id floor its
// high-water, then the old rows clear. One transaction; idempotent
// (empty old tables = no-op). A v4-fresh database never writes the
// old tables, so this runs exactly once per upgraded database.
func (d *DB) convertLegacyRequirements() error {
	var one int
	err := d.db.QueryRow(`SELECT 1 FROM requirements LIMIT 1`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return d.Tx(func(tx *TxStores) error {
		q := tx.q
		counters := map[string]int{} // "ns\x00project" → next
		crows, err := q.QueryContext(context.Background(), `SELECT ns, project, next FROM req_counters`)
		if err != nil {
			return err
		}
		for crows.Next() {
			var ns, project string
			var next int
			if err := crows.Scan(&ns, &project, &next); err != nil {
				crows.Close()
				return err
			}
			counters[ns+"\x00"+project] = next
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			return err
		}
		type acc struct {
			reqs []requirements.Req
			next int
		}
		shelves := map[string]*acc{} // "ns\x00project" → accumulator
		order := []string{}
		rrows, err := q.QueryContext(context.Background(),
			`SELECT ns, project, id, title, body, status, created_by, created_ts, park_note, park_ts, review_after, closed_ts
			 FROM requirements ORDER BY ns ASC, project ASC, rowid ASC`)
		if err != nil {
			return err
		}
		for rrows.Next() {
			var ns, project string
			var r requirements.Req
			if err := rrows.Scan(&ns, &project, &r.ID, &r.Title, &r.Body, &r.Status,
				&r.CreatedBy, &r.CreatedTS, &r.ParkNote, &r.ParkTS, &r.ReviewAfter, &r.ClosedTS); err != nil {
				rrows.Close()
				return err
			}
			r.ProjectKey = project
			k := ns + "\x00" + project
			a := shelves[k]
			if a == nil {
				a = &acc{next: 1}
				shelves[k] = a
				order = append(order, k)
			}
			a.reqs = append(a.reqs, r)
			var n int
			if _, err := fmt.Sscanf(r.ID, "r_%d", &n); err == nil && n >= a.next {
				a.next = n + 1
			}
		}
		rrows.Close()
		if err := rrows.Err(); err != nil {
			return err
		}
		for _, k := range order {
			a := shelves[k]
			parts := strings.SplitN(k, "\x00", 2)
			ns, project := parts[0], parts[1]
			if c := counters[k]; c > a.next {
				a.next = c
			}
			doc, err := json.Marshal(a.reqs)
			if err != nil {
				return err
			}
			if _, err := q.ExecContext(context.Background(),
				`INSERT INTO req_shelves (ns, project, next, doc) VALUES (?, ?, ?, ?)
				ON CONFLICT (ns, project) DO NOTHING`, ns, project, a.next, string(doc)); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(context.Background(), `DELETE FROM requirements`); err != nil {
			return err
		}
		_, err = q.ExecContext(context.Background(), `DELETE FROM req_counters`)
		return err
	})
}

func (d *DB) migrate() error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS store_meta (
		ns TEXT NOT NULL DEFAULT '',
		k  TEXT NOT NULL,
		v  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (ns, k)
	)`); err != nil {
		return err
	}
	var version int
	if err := tx.QueryRow(`SELECT v FROM store_meta WHERE ns='' AND k='schema'`).Scan(&version); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	// 版本钉：旧程序开新库在这里拒绝，而不是把版本戳降级覆写后
	// 「成功」——stamp 一旦倒退，下一个新程序会对着半知的表重放迁
	// 移（今天全是 CREATE IF NOT EXISTS 无害，加列式迁移那天就炸）。
	if version > len(schema) {
		return fmt.Errorf("%w: 库版本 %d > 程序 schema %d", ErrNewerSchema, version, len(schema))
	}
	for v := version; v < len(schema); v++ {
		if _, err := tx.Exec(schema[v]); err != nil {
			// 步进失败＝②类（canary 已证文件可读）：migrate 是事务，
			// 回滚后库停在原版本、文件未动——boot 侧响亮降级，不隔离。
			return fmt.Errorf("%w: step %d: %v", ErrMigrate, v+1, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO store_meta (ns, k, v) VALUES ('', 'schema', ?)
		ON CONFLICT (ns, k) DO UPDATE SET v = excluded.v`, len(schema)); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the underlying handle (open stores become unusable).
func (d *DB) Close() error { return d.db.Close() }

// Path is the file this database lives in (boot logs and reports).
func (d *DB) Path() string { return d.path }

// Tasks is the task-ledger view over this database (the Engine's
// persistence seam; join DB.Tx for cross-store atomicity).
func (d *DB) Tasks() *TaskStore { return &TaskStore{db: d.db} }

// TxStores bundles the ledger family's seams bound to ONE transaction
// — the handle DB.Tx hands to its callback. Every call on these views
// inside the callback joins the transaction; an error return rolls
// the WHOLE set back (the 三写中途死 proof rides on this, and the
// server's accept path lands its three legs through it — the
// verb-granularity closure the promotion's hard condition demanded).
type TxStores struct {
	q       DBTX
	reqs    requirements.ShelfStore
	plans   plan.SlotStore
	merges  merge.SlotStore
	meets   meeting.ShelfStore
	notices notice.RowStore
	tasks   *TaskStore
}

// Requirements is the tx-bound requirements shelf seam.
func (t *TxStores) Requirements() requirements.ShelfStore { return t.reqs }

// Plan is the tx-bound plan slot seam.
func (t *TxStores) Plan() plan.SlotStore { return t.plans }

// Merge is the tx-bound merge slot seam.
func (t *TxStores) Merge() merge.SlotStore { return t.merges }

// Meetings is the tx-bound meeting shelf seam.
func (t *TxStores) Meetings() meeting.ShelfStore { return t.meets }

// Notices is the tx-bound notice row seam.
func (t *TxStores) Notices() notice.RowStore { return t.notices }

// Tasks is the tx-bound tasks seam.
func (t *TxStores) Tasks() tasks.Store { return t.tasks }

// Tx runs fn with the ledger family's seams bound to a single
// transaction: either every write lands or none does. This is the
// seam the JSON era never had — the plan-acceptance flow (pop the
// plan slot, write the task shelf, flip the requirement) is one
// commit here.
func (d *DB) Tx(fn func(tx *TxStores) error) error {
	sqlTx, err := d.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	t := &TxStores{
		q:       sqlTx,
		reqs:    reqShelvesOver(sqlTx),
		plans:   planSlotsOver(sqlTx),
		merges:  mergeSlotsOver(sqlTx),
		meets:   meetingShelvesOver(sqlTx),
		notices: noticeRowsOver(sqlTx),
		tasks:   &TaskStore{db: sqlTx},
	}
	if err := fn(t); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}

// runInTx runs fn either inside the caller's transaction (q already
// is one — nested BEGIN is a SQLite error, so pass through) or in a
// fresh autocommit transaction over the DB handle.
func runInTx(q DBTX, fn func(q DBTX) error) error {
	if _, ok := q.(*sql.Tx); ok {
		return fn(q)
	}
	tx, err := q.(*sql.DB).BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
