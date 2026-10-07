package tasks

// store.go — the Engine's persistence seam (Phase 1 second batch): the
// Engine keeps ALL domain logic (arbitration, proposals, structure
// checks, the rank registry) over an in-memory truth; this interface is
// the pluggable durable side it reads at boot and rewrites on every
// mutation. The translation baseline is the Phase 1 store-semantics
// decision — the seam is deliberately
// whole-shelf shaped (one project's tasks + that shelf's id counter are
// ONE document, exactly the JSON envelope's shape), so a backend swap
// changes WHERE the document lives, never how the domain reads or
// numbers it. LocalStore (below) is the file-backed default; a second
// implementation must pass storetest.TestTaskStore before it may carry
// production data.

import (
	"errors"
	"sort"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
)

// Shelf is one project's persisted envelope: that shelf's tasks (an
// ORDERED array — insertion order is RenumberPlan's renumber basis, so
// backends must preserve it) plus its own id high-water (a deleted
// max-number task never gets its number re-issued after a restart).
// The JSON tags are the LocalStore file format, byte-stable.
type Shelf struct {
	Next  int     `json:"next"`
	Tasks []*Task `json:"tasks"`
}

// Store is the Engine's durable side: load every shelf plus the rank
// registry at boot, atomically replace one shelf (or the ranks) per
// mutation, remove a shelf outright on project wipe. Empty-path
// implementations (the in-memory engine) no-op every method and load
// nothing — same shape LocalStore gives for "" paths.
//
// The SUBJECT parameter (first on every method) is the storage
// namespace the call acts in ("" = the single-user historical shape,
// non-empty = the multi-user studio owner account); see
// requirements.Store for the full contract note. The Engine binds ONE
// subject at open (OpenStoreNS) and threads it through — the studio's
// shared ledger is one namespace. The file-backed LocalStore refuses
// non-empty subjects (ErrSingleSubject).
type Store interface {
	// OpenShelves loads every persisted shelf (slot key → envelope) and
	// the rank registry. A parse failure is an error (the caller's
	// corrupt-reset path decides what happens next); a missing backing
	// is the fresh-install empty state, not an error.
	OpenShelves(subject string) (shelves map[string]*Shelf, ranks map[string]int, err error)
	// SaveShelf atomically replaces one project's shelf (rows and
	// counter together — a torn shelf must never be readable).
	SaveShelf(subject, slot string, f *Shelf) error
	// DeleteShelf removes one project's shelf and its counter outright;
	// a missing shelf is success (idempotent wipe).
	DeleteShelf(subject, slot string) error
	// SaveRanks atomically replaces the whole rank registry.
	SaveRanks(subject string, ranks map[string]int) error
}

// MemStore is the Store over nothing: the in-memory engine's backing.
// Every method no-ops (loads nothing, saves nothing); a non-empty
// subject is refused — the memory shape has one engine, not one per
// namespace. The JSON era's file-backed LocalStore was deleted by the
// sqlite promotion (the one-time bridge in sqlstore/import.go is the
// layout's last reader).
type MemStore struct{}

// NewMemStore builds the in-memory engine's backing.
func NewMemStore() MemStore { return MemStore{} }

// OpenShelves loads nothing (the fresh-install empty state).
func (MemStore) OpenShelves(subject string) (map[string]*Shelf, map[string]int, error) {
	if err := gateSubject(subject); err != nil {
		return nil, nil, err
	}
	return map[string]*Shelf{}, map[string]int{}, nil
}

// SaveShelf is a no-op (memory IS the truth here).
func (MemStore) SaveShelf(subject, slot string, f *Shelf) error {
	return gateSubject(subject)
}

// DeleteShelf is a no-op.
func (MemStore) DeleteShelf(subject, slot string) error {
	return gateSubject(subject)
}

// SaveRanks is a no-op.
func (MemStore) SaveRanks(subject string, ranks map[string]int) error {
	return gateSubject(subject)
}

// compile-time: the memory backing must satisfy the seam.
var _ Store = (*MemStore)(nil)

// sortedShelfKeys keeps shelf assembly deterministic: the JSON era
// walked ReadDir's sorted entries, and e.tasks' order (shelf-major,
// insertion order within) is RenumberPlan's basis — a map-iteration
// order would renumber differently run to run.
func sortedShelfKeys(shelves map[string]*Shelf) []string {
	keys := make([]string, 0, len(shelves))
	for k := range shelves {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ErrSingleSubject is the memory backing's honest refusal (same
// contract note as requirements.ErrSingleSubject): the in-memory
// shape has one engine, not one per namespace.
func ErrSingleSubject(subject string) error {
	return errors.New(i18n.Sf("任务台账的内存形态是单用户主体（收到 %q）——多用户命名空间需 sqlite 单库", subject))
}

// gateSubject is MemStore's one-subject door.
func gateSubject(subject string) error {
	if subject == "" {
		return nil
	}
	return ErrSingleSubject(subject)
}

// —— 接受路径的暂存面（公开面 additive 扩展，语义对照表 §1.6 的
// 「动词粒度收口」硬条件）——
//
// acceptPlanCore 把「每条 CreatePlanned 各自落盘」拆成四步：
// AcceptBegin 开一场暂存，AcceptCreatePlanned 逐条入内存（锁分片、
// 不落盘），AcceptWrite 把被碰过的分片整写进调用方的事务缝，
// AcceptCommit 记世代并解锁 / AcceptAbort 回滚内存并解锁。被碰分片
// 的 mu 从首次触碰持到 Commit/Abort——中途同分片的任务动词不可交
// 错。恰有一场接受在跑（plan 引擎的锁从头持到尾），所以 e.accept
// 不需要自己的锁。

// acceptShard is one touched shard's pre-state (abort's restore source).
type acceptShard struct {
	ch    *shard
	tasks []*Task // the pre-append slice (task pointers are only appended, never mutated in place here)
	next  int
	gen   uint64
}

// acceptCtx is one staged accept's state.
type acceptCtx struct {
	shards   map[string]*acceptShard
	reserved int // caps reserved so far (abort releases them)
}

// AcceptBegin opens a staged accept. Must be paired with Commit/Abort.
func (e *Engine) AcceptBegin() {
	e.accept = &acceptCtx{shards: map[string]*acceptShard{}}
}

// AcceptCreatePlanned is CreatePlanned's staged twin: same gates, same
// semantics, but the shard's persistence is deferred to AcceptWrite
// (inside the caller's transaction) — memory is NOT yet durable, and
// an Abort removes the task again as if it never was.
func (e *Engine) AcceptCreatePlanned(by string, draft Task) Outcome {
	if e.accept == nil {
		return denied(i18n.S("接受暂存未开启"))
	}
	by = sanitizeName(by)
	draft.Title = clampRunes(strings.TrimSpace(draft.Title), MaxTitle)
	draft.Desc = verifyHint(draft.Title, clampRunes(strings.TrimSpace(draft.Desc), MaxDesc))
	draft.Assignee = sanitizeName(draft.Assignee)
	if draft.Title == "" {
		return denied(i18n.S("标题不能为空"))
	}
	if !e.reserveCap() {
		return denied(i18n.Sf("任务数已达上限 %d 条", MaxTasks))
	}
	e.accept.reserved++
	ch := e.shardFor(SlotKey(draft.ProjectKey))
	if _, ok := e.accept.shards[ch.key]; !ok {
		ch.mu.Lock()
		e.accept.shards[ch.key] = &acceptShard{ch: ch,
			tasks: append([]*Task(nil), ch.tasks...), next: ch.next, gen: ch.gen}
	}
	out := e.createPlannedLocked(by, draft, util.Now(), ch)
	if out.Denied {
		e.accept.reserved--
		e.releaseCap()
	}
	return out
}

// AcceptWrite persists every touched shard's document through w (the
// transaction-bound seam; nil = the in-memory engine, nothing to
// write). The shards' locks are still held — memory cannot move under
// the write. An error aborts the whole staged accept.
func (e *Engine) AcceptWrite(w Store) error {
	if e.accept == nil || w == nil {
		return nil
	}
	for _, key := range sortedShelfKeysOf(e.accept.shards) {
		as := e.accept.shards[key]
		doc := as.ch.shelfDocLocked()
		if err := w.SaveShelf(e.subject, key, doc); err != nil {
			return err
		}
	}
	return nil
}

// AcceptCommit marks the written generations persisted and unlocks the
// touched shards. Cannot fail (their locks were held the whole time).
func (e *Engine) AcceptCommit() {
	if e.accept == nil {
		return
	}
	for _, as := range e.accept.shards {
		as.ch.saveMu.Lock()
		if as.ch.gen > as.ch.saved {
			as.ch.saved = as.ch.gen
		}
		as.ch.saveMu.Unlock()
		as.ch.mu.Unlock()
	}
	e.accept = nil
}

// AcceptAbort restores every touched shard's pre-state, releases the
// reserved caps and unlocks — the staged tasks never were.
func (e *Engine) AcceptAbort() {
	if e.accept == nil {
		return
	}
	for _, as := range e.accept.shards {
		as.ch.tasks = as.tasks
		as.ch.next = as.next
		as.ch.gen = as.gen
		as.ch.mu.Unlock()
	}
	if n := e.accept.reserved; n > 0 {
		e.total.Add(-int64(n))
	}
	e.accept = nil
}

// sortedShardKeysOf keeps the staged writes deterministic.
func sortedShelfKeysOf(m map[string]*acceptShard) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MustOpenMemory is OpenMemory for test wiring (an in-memory engine
// cannot fail to open; a panic here is a bug, not a runtime shape).
func MustOpenMemory(localName string) *Engine {
	e, err := OpenMemory(localName)
	if err != nil {
		panic(err)
	}
	return e
}
