package sqlstore

// shelves.go — the ledger family's dumb document seams. The sqlite
// promotion deleted the full-face Store implementations this package
// used to carry (a second copy of every domain's semantics, held
// byte-equal by the parity tests): domain logic lives ONCE in each
// domain package's Engine, and what remains here is whole-document
// storage — load every shelf of one subject, atomically replace one
// shelf, remove one shelf. Each (ns, project) row holds the counter
// plus the document payload (the shelf IS one document, the
// tasks.Shelf posture); a torn shelf is never readable because every
// replace is one transaction.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
)

// docTable is the shared (ns, project, next, doc) shape behind the
// four shelf seams. decode rebuilds one shelf from the counter column
// and the document bytes; encode is its inverse (a nil doc binds SQL
// NULL — an empty string would read back as a corrupt document).
type docTable[T any] struct {
	db     DBTX
	table  string
	docCol string // "doc" for shelf tables, "pending" for the v1 slot tables
	decode func(next int, doc []byte) (*T, error)
	encode func(f *T) (next int, doc any)
}

func (d docTable[T]) open(subject string) (map[string]*T, error) {
	out := map[string]*T{}
	rows, err := d.db.QueryContext(context.Background(),
		`SELECT project, next, `+d.docCol+` FROM `+d.table+` WHERE ns=? ORDER BY project ASC`, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var project string
		var next int
		var doc []byte
		if err := rows.Scan(&project, &next, &doc); err != nil {
			return nil, err
		}
		f, err := d.decode(next, doc)
		if err != nil {
			return nil, fmt.Errorf("%s shelf %s 损坏: %w", d.table, project, err)
		}
		out[project] = f
	}
	return out, rows.Err()
}

func (d docTable[T]) save(subject, project string, f *T) error {
	next, doc := d.encode(f)
	return runInTx(d.db, func(q DBTX) error {
		_, err := q.ExecContext(context.Background(),
			`INSERT INTO `+d.table+` (ns, project, next, `+d.docCol+`) VALUES (?, ?, ?, ?)
			ON CONFLICT (ns, project) DO UPDATE SET next=excluded.next, `+d.docCol+`=excluded.`+d.docCol,
			subject, project, next, doc)
		return err
	})
}

func (d docTable[T]) delete(subject, project string) error {
	return runInTx(d.db, func(q DBTX) error {
		_, err := q.ExecContext(context.Background(),
			`DELETE FROM `+d.table+` WHERE ns=? AND project=?`, subject, project)
		return err
	})
}

// —— requirements.ShelfStore ——

type reqShelves struct{ docTable[requirements.Shelf] }

var _ requirements.ShelfStore = reqShelves{}

func (s reqShelves) OpenShelves(subject string) (map[string]*requirements.Shelf, error) {
	return s.open(subject)
}
func (s reqShelves) SaveShelf(subject, project string, f *requirements.Shelf) error {
	return s.save(subject, project, f)
}
func (s reqShelves) DeleteShelf(subject, project string) error {
	return s.delete(subject, project)
}

// ReqShelves is the requirements engine's persistence seam over this
// database (per-call namespace; the engine threads its bound subject).
func (d *DB) ReqShelves() requirements.ShelfStore { return reqShelvesOver(d.db) }

// —— plan.SlotStore ——

type planSlots struct{ docTable[plan.Slot] }

var _ plan.SlotStore = planSlots{}

func (s planSlots) OpenSlots(subject string) (map[string]*plan.Slot, error) {
	return s.open(subject)
}
func (s planSlots) SaveSlot(subject, project string, f *plan.Slot) error {
	return s.save(subject, project, f)
}
func (s planSlots) DeleteSlot(subject, project string) error {
	return s.delete(subject, project)
}

// PlanSlots is the plan engine's persistence seam (the pending
// document binds NULL when the slot is empty — exactly the JSON
// era's {"next": n} with no pending key).
func (d *DB) PlanSlots() plan.SlotStore { return planSlotsOver(d.db) }

// —— merge.SlotStore ——

type mergeSlots struct{ docTable[merge.Slot] }

var _ merge.SlotStore = mergeSlots{}

func (s mergeSlots) OpenSlots(subject string) (map[string]*merge.Slot, error) {
	return s.open(subject)
}
func (s mergeSlots) SaveSlot(subject, project string, f *merge.Slot) error {
	return s.save(subject, project, f)
}
func (s mergeSlots) DeleteSlot(subject, project string) error {
	return s.delete(subject, project)
}

// MergeSlots is the merge engine's persistence seam (plan's twin).
func (d *DB) MergeSlots() merge.SlotStore { return mergeSlotsOver(d.db) }

// —— meeting.ShelfStore ——

type meetingShelves struct{ docTable[meeting.Shelf] }

var _ meeting.ShelfStore = meetingShelves{}

func (s meetingShelves) OpenShelves(subject string) (map[string]*meeting.Shelf, error) {
	return s.open(subject)
}
func (s meetingShelves) SaveShelf(subject, project string, f *meeting.Shelf) error {
	return s.save(subject, project, f)
}
func (s meetingShelves) DeleteShelf(subject, project string) error {
	return s.delete(subject, project)
}

// MeetingShelves is the meeting engine's persistence seam (the
// finished history — live meetings are memory-only by design).
func (d *DB) MeetingShelves() meeting.ShelfStore { return meetingShelvesOver(d.db) }

// —— notice.RowStore ——

type noticeRows struct{ db DBTX }

var _ notice.RowStore = noticeRows{}

func (s noticeRows) Load(subject, room string) (notice.Notice, bool, error) {
	var doc []byte
	err := s.db.QueryRowContext(context.Background(),
		`SELECT doc FROM notice_rows WHERE ns=? AND room=?`, subject, room).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return notice.Notice{}, false, nil
	}
	if err != nil {
		return notice.Notice{}, false, err
	}
	var n notice.Notice
	if err := json.Unmarshal(doc, &n); err != nil {
		// 坏行按无公告读（notice 的读侧纪律：读不出的行就是不
		// 存在——绝不猜），写路径的拒绝语义不受影响。
		return notice.Notice{}, false, nil
	}
	return n, true, nil
}

func (s noticeRows) Save(subject, room string, n notice.Notice) error {
	return runInTx(s.db, func(q DBTX) error {
		_, err := q.ExecContext(context.Background(),
			`INSERT INTO notice_rows (ns, room, doc) VALUES (?, ?, ?)
			ON CONFLICT (ns, room) DO UPDATE SET doc=excluded.doc`,
			subject, room, mustJSON(n))
		return err
	})
}

func (s noticeRows) Remove(subject, room string) error {
	return runInTx(s.db, func(q DBTX) error {
		_, err := q.ExecContext(context.Background(),
			`DELETE FROM notice_rows WHERE ns=? AND room=?`, subject, room)
		return err
	})
}

// NoticeRows is the notice engine's row seam.
func (d *DB) NoticeRows() notice.RowStore { return noticeRowsOver(d.db) }

// mustJSON is the seams' marshal helper (the payload types are the
// wire shapes — marshal cannot fail; if it ever does, panic is the
// honest crash).
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("sqlstore: marshal shelf document: %v", err))
	}
	return b
}

// The tx-bound constructors (DB.Tx hands these, bound to the open
// transaction handle, to its callback).
func reqShelvesOver(q DBTX) requirements.ShelfStore {
	return reqShelves{docTable[requirements.Shelf]{db: q, table: "req_shelves", docCol: "doc",
		decode: func(next int, doc []byte) (*requirements.Shelf, error) {
			f := &requirements.Shelf{Next: next}
			if err := json.Unmarshal(doc, &f.Reqs); err != nil {
				return nil, err
			}
			return f, nil
		},
		encode: func(f *requirements.Shelf) (int, any) { return f.Next, mustJSON(f.Reqs) }}}
}
func planSlotsOver(q DBTX) plan.SlotStore {
	return planSlots{docTable[plan.Slot]{db: q, table: "plan_slots", docCol: "pending",
		decode: func(next int, doc []byte) (*plan.Slot, error) {
			f := &plan.Slot{Next: next}
			if len(doc) > 0 {
				p := new(plan.Plan)
				if err := json.Unmarshal(doc, p); err != nil {
					return nil, err
				}
				f.Pending = p
			}
			return f, nil
		},
		encode: func(f *plan.Slot) (int, any) {
			if f.Pending == nil {
				return f.Next, nil
			}
			return f.Next, mustJSON(*f.Pending)
		}}}
}
func mergeSlotsOver(q DBTX) merge.SlotStore {
	return mergeSlots{docTable[merge.Slot]{db: q, table: "merge_slots", docCol: "pending",
		decode: func(next int, doc []byte) (*merge.Slot, error) {
			f := &merge.Slot{Next: next}
			if len(doc) > 0 {
				m := new(merge.Merge)
				if err := json.Unmarshal(doc, m); err != nil {
					return nil, err
				}
				f.Pending = m
			}
			return f, nil
		},
		encode: func(f *merge.Slot) (int, any) {
			if f.Pending == nil {
				return f.Next, nil
			}
			return f.Next, mustJSON(*f.Pending)
		}}}
}
func meetingShelvesOver(q DBTX) meeting.ShelfStore {
	return meetingShelves{docTable[meeting.Shelf]{db: q, table: "meeting_shelves", docCol: "doc",
		decode: func(next int, doc []byte) (*meeting.Shelf, error) {
			f := &meeting.Shelf{Next: next}
			if err := json.Unmarshal(doc, &f.Hist); err != nil {
				return nil, err
			}
			return f, nil
		},
		encode: func(f *meeting.Shelf) (int, any) { return f.Next, mustJSON(f.Hist) }}}
}
func noticeRowsOver(q DBTX) notice.RowStore { return noticeRows{db: q} }
