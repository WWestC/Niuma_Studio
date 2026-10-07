// Package requirements is the v2 requirement ledger (PRD §4.2): light
// r_NN entries that mother task trees. Storage is one file per project
// under ~/.niuma/requirements/ (v2.10 project isolation: every project
// owns its own shelf AND its own r_NN counter — book's r_01 and the
// lobby's r_01 are different entries resolved through their project's
// face; the plan package's per-project slot files are the pattern). A
// requirement deliberately carries no assignee and no negotiation flow
// — those are task semantics; splitting flows through plan proposals
// (P4), whose acceptance flips the entry to split. The trace chain
// requirement → plan → tasks runs over plan.req / task.plan_id /
// task.req, none of which this package needs to know about.
package requirements

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/util"
)

// Requirement lifecycle states. open awaits splitting; split has its
// task tree in the ledger (the tree no longer tracks the requirement's
// later life); closed is done or withdrawn.
const (
	StatusOpen    = "open"
	StatusParking = "parking"
	StatusSplit   = "split"
	StatusClosed  = "closed"
)

// Field limits, aligned with the tasks ledger's title/desc caps.
const (
	MaxTitle = 120
	MaxBody  = 2000
)

const maxByRunes = 24 // created_by: the member-name cap, house style

// Req is one requirement entry. IDs are per-project r_NN counters
// (v2.10): unique inside one project's shelf only — every read face
// resolves ids through a project context (the room, the page, the
// plan's project_key), so cross-project faces must qualify. ProjectKey
// is the owning shelf's key (a foreign key into projects). ClosedTS is
// the completion stamp: written once by MarkClosed (closed = done or
// withdrawn — the requirement's one terminal moment), zero while
// open/split.
type Req struct {
	ID         string `json:"id"`
	ProjectKey string `json:"project_key"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	Status     string `json:"status"`
	CreatedBy  string `json:"created_by,omitempty"`
	CreatedTS  int64  `json:"created_ts,omitempty"`
	// r_31 parking：暂缓原因（可核对的等待条件，非观点——三不准校验
	// 在 CLI/服务面；store 只存不判）、拍章、复查期（unix ts，巡检提
	// 醒用——不做自动解禁，条件判断是编排者职责）
	ParkNote    string `json:"park_note,omitempty"`
	ParkTS      int64  `json:"park_ts,omitempty"`
	ReviewAfter int64  `json:"review_after,omitempty"`
	ClosedTS    int64  `json:"closed_ts,omitempty"`
}

// Engine is the requirement ledger's domain engine — the ONLY home of
// the ledger's semantics (state machine, per-project counters,
// renumber). Memory is the truth: every mutation rewrites the touched
// shelf through the injected ShelfStore seam (the JSON era kept a
// second copy of these semantics in sqlstore; the sqlite promotion
// deleted it — the seam is deliberately whole-shelf shaped, so a
// backend swap changes WHERE the document lives, never how the domain
// reads or numbers it). A nil seam is the in-memory engine (tests,
// home-less boot); a save failure logs and leaves memory ahead (the
// A-族 discipline the file era defined). The engine binds ONE storage
// subject at open; a face call under any other subject is refused.
type Engine struct {
	mu      sync.Mutex
	seam    ShelfStore
	subject string
	slots   map[string]*Shelf
	rev     uint64
	stage   *acceptStage // the accept path's staged MarkSplit (nil outside one)
}

// Shelf is one project's persisted envelope: that shelf's own id
// high-water (a deleted r_NN is never re-issued after a restart) plus
// its requirement list, in insertion order. The JSON tags are the
// bridge's import shape, byte-stable.
type Shelf struct {
	Next int   `json:"next"`
	Reqs []Req `json:"reqs"`
}

// OpenMemory builds the in-memory engine (single-user subject "").
func OpenMemory() *Engine {
	return &Engine{slots: map[string]*Shelf{}}
}

// OpenStore loads the engine over a persistence seam, bound to one
// storage subject ("" = the single-user historical shape, non-empty =
// the multi-user studio owner account). A corrupt shelf document is an
// error (the caller's corrupt-reset path decides what happens next);
// an empty backing is the fresh-install state.
func OpenStore(seam ShelfStore, subject string) (*Engine, error) {
	if seam == nil && subject != "" {
		return nil, ErrSingleSubject(subject)
	}
	e := &Engine{seam: seam, subject: subject, slots: map[string]*Shelf{}}
	shelves, err := seam.OpenShelves(subject)
	if err != nil {
		return nil, err
	}
	for key, f := range shelves {
		if f.Next < 1 {
			f.Next = 1
		}
		// 读侧兜底：envelope next 落后于存量行号时抬到行号＋1（手改架
		// 文件的年代留下的纪律，桥与缝共用）。
		for _, r := range f.Reqs {
			var n int
			if _, err := fmt.Sscanf(r.ID, "r_%d", &n); err == nil && n >= f.Next {
				f.Next = n + 1
			}
		}
		e.slots[key] = f
	}
	return e, nil
}

// DefaultDir returns the conventional v2.10 location:
// ~/.niuma/requirements (one <key>.json per project inside).
func DefaultDir() (string, error) {
	root, err := projects.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "requirements"), nil
}

// LegacyPath returns the pre-isolation single file:
// ~/.niuma/requirements.json. SplitLegacyFile moves it into DefaultDir;
// the path stays known for that migration and the boot log.
func LegacyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma", "requirements.json"), nil
}

// SplitLegacyFile performs the one-time storage isolation move: the
// pre-v2.10 single ledger (bare array or {next, reqs} envelope —
// read dual-shape like the old Open) splits into one file per
// project_key under dir, then the old file is renamed to
// <old>.migrated (kept for forensics, never deleted). Rows with an
// empty project_key (defensive — the single-file era always stamped
// one) land on the lobby shelf "default". Idempotent: a missing old
// file is a no-op; a project whose target file already exists keeps
// that file (a partial earlier run is never clobbered). Returns the
// number of requirements moved.
func SplitLegacyFile(oldPath, dir string) (int, error) {
	b, err := os.ReadFile(oldPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var list []Req
	globalNext := 1
	if err := json.Unmarshal(b, &list); err != nil {
		var f fileShape
		if err2 := json.Unmarshal(b, &f); err2 != nil {
			return 0, fmt.Errorf("parse %s: %w", oldPath, err)
		}
		list = f.Reqs
		if f.Next > globalNext {
			globalNext = f.Next
		}
	}
	bySlot := map[string]*Shelf{}
	for _, r := range list {
		key := r.ProjectKey
		if key == "" {
			key = projects.LobbyKey
		}
		sl, ok := bySlot[key]
		if !ok {
			sl = &Shelf{Next: 1}
			bySlot[key] = sl
		}
		sl.Reqs = append(sl.Reqs, r)
		var n int
		if _, err := fmt.Sscanf(r.ID, "r_%d", &n); err == nil && n >= sl.Next {
			sl.Next = n + 1
		}
	}
	// The legacy envelope's global high-water floors EVERY shelf: the
	// single-file era issued numbers from one counter, so any number
	// below it may have been issued to this project and deleted — a
	// stale in-flight reference must never meet a freshly minted twin.
	for _, sl := range bySlot {
		if sl.Next < globalNext {
			sl.Next = globalNext
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	moved := 0
	for key, sl := range bySlot {
		target := filepath.Join(dir, key+".json")
		if _, err := os.Stat(target); err == nil {
			continue // existing shelf file wins — idempotent re-runs
		}
		out, err := json.MarshalIndent(sl, "", "  ")
		if err != nil {
			return moved, err
		}
		if err := persist.Save(target, out, 0o644); err != nil {
			return moved, err
		}
		moved += len(sl.Reqs)
	}
	if err := os.Rename(oldPath, oldPath+".migrated"); err != nil {
		return moved, fmt.Errorf("archive legacy requirements file: %w", err)
	}
	return moved, nil
}

// Create appends a requirement in open on projectKey's shelf. The
// store stamps ID (that project's own counter), Status and CreatedTS.
// Title is required; prose fields clamp (house style), the project key
// validates or rejects.
func (s *Engine) Create(subject, projectKey, title, body, createdBy string) (Req, error) {
	if err := s.gate(subject); err != nil {
		return Req{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	title, body, createdBy = SanitizeCreate(title, body, createdBy)
	if err := ValidateCreate(projectKey, title); err != nil {
		return Req{}, err
	}
	sl := s.slotForLocked(projectKey)
	r := Req{
		ID:         fmt.Sprintf("r_%02d", sl.Next),
		ProjectKey: projectKey,
		Title:      title,
		Body:       body,
		Status:     StatusOpen,
		CreatedBy:  createdBy,
		CreatedTS:  util.Now(),
	}
	sl.Next++
	sl.Reqs = append(sl.Reqs, r)
	s.rev++
	s.saveLocked(projectKey)
	return r, nil
}

// MarkSplit flips open→split — the plan-acceptance write-back ("已拆
// 解入库"; the task tree no longer tracks the requirement). A declined
// or superseded plan leaves the status alone.
func (s *Engine) MarkSplit(subject, projectKey, id string) (Req, error) {
	if err := s.gate(subject); err != nil {
		return Req{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionLocked(projectKey, id, StatusOpen, StatusSplit,
		i18n.S("仅 open 需求可标记拆解"))
}

// Park freezes an open requirement (r_31): the stocked-but-frozen
// shelf — 拆解钟/会议钟不可见、水位合计可见。park 只从 open 可达
// （split 已有任务树、closed 是终局留档——双向孤岛，状态图最简）。
// note 是可核对的等待条件（三不准在服务面校验）；reviewAfter>0 是
// 复查期（巡检提醒用，不自动解禁）。
func (s *Engine) Park(subject, projectKey, id, note string, reviewAfter int64) (Req, error) {
	if err := s.gate(subject); err != nil {
		return Req{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, i, err := s.findLocked(projectKey, id)
	if err != nil {
		return Req{}, err
	}
	e := sl.Reqs[i]
	if e.Status != StatusOpen {
		return Req{}, ErrParkOnlyOpen(id, e.Status)
	}
	e.Status = StatusParking
	e.ParkNote = note
	e.ParkTS = util.Now()
	e.ReviewAfter = reviewAfter
	sl.Reqs[i] = e
	s.rev++
	s.saveLocked(projectKey)
	return e, nil
}

// Unpark thaws a parked requirement back to open (r_31): the park
// reason is preserved on the row (编排决策可追溯——unpark 不抹历史，
// 再 park 会覆盖）。
func (s *Engine) Unpark(subject, projectKey, id string) (Req, error) {
	if err := s.gate(subject); err != nil {
		return Req{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionLocked(projectKey, id, StatusParking, StatusOpen,
		i18n.S("仅 parking 需求可解暂缓"))
}

// MarkClosed closes the requirement (open|split→closed) and stamps
// ClosedTS — the completion time the ledger sorts by.
func (s *Engine) MarkClosed(subject, projectKey, id string) (Req, error) {
	if err := s.gate(subject); err != nil {
		return Req{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, i, err := s.findLocked(projectKey, id)
	if err != nil {
		return Req{}, err
	}
	e := sl.Reqs[i]
	if e.Status == StatusClosed {
		return Req{}, ErrAlreadyClosed(id)
	}
	if e.Status == StatusParking {
		// r_31：蓄着的需求先 unpark 再终局（双向孤岛——parking 只与
		// open 互通，状态图最简、台账不脱钩）
		return Req{}, ErrCloseWhileParking(id)
	}
	e.Status = StatusClosed
	e.ClosedTS = util.Now()
	sl.Reqs[i] = e
	s.rev++
	s.saveLocked(projectKey)
	return e, nil
}

func (s *Engine) transitionLocked(projectKey, id, from, to, refusal string) (Req, error) {
	sl, i, err := s.findLocked(projectKey, id)
	if err != nil {
		return Req{}, err
	}
	e := sl.Reqs[i]
	if e.Status != from {
		return Req{}, ErrWrongState(refusal, id, e.Status)
	}
	e.Status = to
	sl.Reqs[i] = e
	s.rev++
	s.saveLocked(projectKey)
	return e, nil
}

// Delete removes an open requirement — the mistaken-entry cleanup
// path. Only open may leave: split mothers a task tree (the trace
// chain requirement → plan → tasks must not lose its head) and closed
// is the terminal record the ledger keeps ("done or withdrawn" 的终局
// 留档). The returned Req is the removed snapshot (the broadcast face
// speaks it); the per-project id high-water persists in the shelf
// file's envelope, so a deleted r_NN is never re-issued after a
// restart.
func (s *Engine) Delete(subject, projectKey, id string) (Req, error) {
	if err := s.gate(subject); err != nil {
		return Req{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, i, err := s.findLocked(projectKey, id)
	if err != nil {
		return Req{}, err
	}
	e := sl.Reqs[i]
	if e.Status != StatusOpen && e.Status != StatusParking {
		// r_31：parking 可删——暂缓是库存态不是保护态（不值得做走
		// DELETE 或当面说，park 起来晾着是变相否决）
		return Req{}, ErrDeleteOnly(id, e.Status)
	}
	sl.Reqs = append(sl.Reqs[:i], sl.Reqs[i+1:]...)
	s.rev++
	s.saveLocked(projectKey)
	return e, nil
}

// DropProject wipes one project's requirement shelf outright — the
// project reset's half. Delete is the single-entry cleanup bound by
// status rules; a reset owes no lineage anything, so entries of every
// status and the r_NN counter all go. Numbering restarts from r_01.
// Returns how many entries went.
func (s *Engine) DropProject(subject, projectKey string) int {
	if s.gate(subject) != nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok {
		return 0
	}
	n := len(sl.Reqs)
	delete(s.slots, projectKey)
	s.rev++
	if s.seam != nil {
		if err := s.seam.DeleteShelf(s.subject, projectKey); err != nil {
			log.Printf("drop requirements shelf (%s): %v — 重启会复活该架，请检查单库", projectKey, err)
		}
	}
	return n
}

// GetIn returns one requirement by exact id inside projectKey's shelf
// — the project-resolved read every scoped face should use (ids are
// only unique within a project since v2.10).
func (s *Engine) GetIn(subject, projectKey, id string) (Req, bool) {
	if s.gate(subject) != nil {
		return Req{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sl, i, err := s.findLocked(projectKey, id); err == nil {
		return sl.Reqs[i], true
	}
	return Req{}, false
}

// FindAll returns every shelf's entry carrying id, oldest slot key
// first — the cross-project host faces' read (the legacy migration,
// the member-career titler). A single hit is unambiguous; more means
// two projects minted the same number and the caller must qualify.
func (s *Engine) FindAll(subject, id string) []Req {
	if s.gate(subject) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findAllLocked(id)
}

func (s *Engine) findAllLocked(id string) []Req {
	out := make([]Req, 0, 1)
	for _, key := range s.sortedKeysLocked() {
		sl := s.slots[key]
		for _, r := range sl.Reqs {
			if r.ID == id {
				out = append(out, r)
			}
		}
	}
	return out
}

// List returns a copy of every requirement across shelves (all states
// — the raw store view), slot keys in stable sorted order so repeated
// calls agree.
func (s *Engine) List(subject string) []Req {
	if s.gate(subject) != nil {
		return []Req{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Req, 0, 8)
	for _, key := range s.sortedKeysLocked() {
		out = append(out, s.slots[key].Reqs...)
	}
	return out
}

// ListByProject returns one project's requirements, all states.
func (s *Engine) ListByProject(subject, projectKey string) []Req {
	if s.gate(subject) != nil {
		return []Req{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok {
		return []Req{}
	}
	return append([]Req(nil), sl.Reqs...)
}

// Rev returns the mutation counter; callers use it to detect changes
// without comparing entry bodies.
func (s *Engine) Rev(subject string) uint64 {
	if s.gate(subject) != nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

// RenumberPlan computes projectKey's compact renumber map (old id →
// new id) with NO mutation: entries renumber r_01.. in insertion
// order, each already-compact entry keeping its slot. The caller may
// guard entries out of the returned map (a number referenced somewhere
// unwritable keeps its old id) — a guarded-out entry leaves a hole in
// the new sequence rather than pushing later entries down; the shelf's
// counter resumes past the post-apply maximum either way. Empty map =
// nothing to do.
func (s *Engine) RenumberPlan(subject, projectKey string) map[string]string {
	if s.gate(subject) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok {
		return nil
	}
	next := 1
	plan := map[string]string{}
	for _, r := range sl.Reqs {
		var n int
		if _, err := fmt.Sscanf(r.ID, "r_%d", &n); err != nil {
			continue // hand-edited non r_NN id: not ours to renumber
		}
		if n == next {
			next++
			continue
		}
		plan[r.ID] = fmt.Sprintf("r_%02d", next)
		next++
	}
	return plan
}

// RenumberApply rewrites ids on projectKey's shelf per mapping and
// resets that shelf's counter past the new maximum (the compact
// sequence continues from its own tail). Callers pass a filtered
// RenumberPlan — entries absent from the mapping keep their numbers.
func (s *Engine) RenumberApply(subject, projectKey string, mapping map[string]string) error {
	if err := s.gate(subject); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok {
		return ErrNoShelfRenumber(projectKey)
	}
	max := 0
	for i, r := range sl.Reqs {
		if nn, ok := mapping[r.ID]; ok {
			r.ID = nn
			sl.Reqs[i] = r
		}
		var n int
		if _, err := fmt.Sscanf(r.ID, "r_%d", &n); err == nil && n > max {
			max = n
		}
	}
	sl.Next = max + 1
	s.rev++
	s.saveLocked(projectKey)
	return nil
}

// RemapReq rewrites foreign-key references per mapping — the v2.10
// migration's companion face on the OTHER ledgers (plans, tasks,
// meetings, branch links); the requirements shelf itself has no
// outgoing r_NN references.

func (s *Engine) slotForLocked(projectKey string) *Shelf {
	sl, ok := s.slots[projectKey]
	if !ok {
		sl = &Shelf{Next: 1}
		s.slots[projectKey] = sl
	}
	return sl
}

func (s *Engine) findLocked(projectKey, id string) (*Shelf, int, error) {
	sl, ok := s.slots[projectKey]
	if !ok {
		return nil, -1, ErrNoShelf(id, projectKey)
	}
	for i, r := range sl.Reqs {
		if r.ID == id {
			return sl, i, nil
		}
	}
	return nil, -1, ErrMissing(id, projectKey)
}

func (s *Engine) sortedKeysLocked() []string {
	keys := make([]string, 0, len(s.slots))
	for k := range s.slots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fileShape is the legacy single-file envelope (dual-shape read in
// SplitLegacyFile; kept for that migration's parse only). Every shelf
// document persists the same {next, reqs} shape per project.
type fileShape struct {
	Next int   `json:"next"`
	Reqs []Req `json:"reqs"`
}

// saveLocked rewrites one project's shelf through the seam (the JSON
// era's atomic whole-file rewrite, translated whole-document). A nil
// seam is the in-memory engine; a persistence failure logs but does
// not abort the in-memory mutation (memory stays ahead until the next
// save). Callers hold s.mu.
func (s *Engine) saveLocked(projectKey string) {
	if s.seam == nil {
		return
	}
	sl := s.slotForLocked(projectKey)
	if err := s.seam.SaveShelf(s.subject, projectKey, sl); err != nil {
		log.Printf("save requirements shelf (%s): %v — 内存态领先，下次保存追平", projectKey, err)
	}
}

// gate is the bound-subject door: the engine serves exactly the
// subject it opened under. The single-user engine ("") refuses any
// other subject with ErrSingleSubject — the historical contract; an
// engine bound to a studio subject refuses a foreign subject outright
// (a wiring bug, never a runtime shape).
func (s *Engine) gate(subject string) error {
	if subject == s.subject {
		return nil
	}
	if s.subject == "" {
		return ErrSingleSubject(subject)
	}
	return ErrSubjectMismatch(s.subject, subject)
}

// —— 接受路径的暂存面（公开面 additive 扩展，语义对照表 §1.6 的
// 「动词粒度收口」硬条件）——
//
// plan_accept 的产品形状要三处写在 ONE 事务里（弹槽＋任务架整写＋
// 需求标拆解）。Engine 是内存真源，事务在缝上做，所以 MarkSplit 拆成
// 四步：AcceptBegin 锁引擎并验证状态机（不改动内存），AcceptWrite 把
// 后状态架文档写进调用方的事务缝，AcceptCommit 把内存推到后状态并解
// 锁，AcceptAbort 弃置并解锁。锁从 Begin 持到 Commit/Abort——中途不
// 可有同架的交错写。见 server.acceptPlanCore 的编排。

type acceptStage struct {
	project string
	idx     int
	after   Req
}

// AcceptBegin locks the engine and validates a MarkSplit WITHOUT
// mutating (open→split, the plan-acceptance write-back). A refusal
// (wrong state, missing row, foreign subject) is the caller's
// "skip the requirement leg" signal — the accept path treats the
// linkage as best-effort, exactly like MarkSplit's log-only failure.
func (s *Engine) AcceptBegin(subject, projectKey, id string) error {
	if err := s.gate(subject); err != nil {
		return err
	}
	s.mu.Lock()
	if s.stage != nil {
		s.mu.Unlock()
		return errors.New("requirements: 上一个接受暂存未收尾")
	}
	sl, i, err := s.findLocked(projectKey, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if sl.Reqs[i].Status != StatusOpen {
		err := ErrWrongState(i18n.S("仅 open 需求可标记拆解"), id, sl.Reqs[i].Status)
		s.mu.Unlock()
		return err
	}
	e := sl.Reqs[i]
	e.Status = StatusSplit
	s.stage = &acceptStage{project: projectKey, idx: i, after: e}
	return nil
}

// AcceptWrite persists the post-state shelf document through w (the
// transaction-bound seam the accept path hands in; nil = the in-memory
// engine, nothing to write). The engine's memory is NOT yet moved —
// that is AcceptCommit's job, so a failed transaction leaves both
// sides untouched.
func (s *Engine) AcceptWrite(w ShelfStore) error {
	if s.stage == nil || w == nil {
		return nil
	}
	after := *s.slots[s.stage.project]
	after.Reqs = append([]Req(nil), s.slots[s.stage.project].Reqs...)
	after.Reqs[s.stage.idx] = s.stage.after
	return w.SaveShelf(s.subject, s.stage.project, &after)
}

// AcceptCommit moves memory to the post-state and unlocks. Cannot
// fail (the lock was held the whole time — nothing interleaved).
func (s *Engine) AcceptCommit() {
	if s.stage == nil {
		return
	}
	sl := s.slots[s.stage.project]
	sl.Reqs[s.stage.idx] = s.stage.after
	s.rev++
	s.stage = nil
	s.mu.Unlock()
}

// AcceptAbort discards the staged write and unlocks.
func (s *Engine) AcceptAbort() {
	if s.stage == nil {
		return
	}
	s.stage = nil
	s.mu.Unlock()
}

// sanitizeBy clamps a provenance name (created_by): single line,
// trimmed, ≤24 runes — the member-name cap, house style.
func sanitizeBy(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		out = append(out, r)
	}
	s := strings.TrimSpace(string(out))
	if r := []rune(s); len(r) > maxByRunes {
		s = string(r[:maxByRunes])
	}
	return s
}

func clampRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}
