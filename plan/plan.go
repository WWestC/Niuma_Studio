// Package plan is the v2 scheduling-proposal domain (PRD §4.2, P4-b /
// orchestration §5.2): the orchestrator's plan_submit payload and the
// per-project SINGLE pending-review slot, persisted agents.Store style
// (one mutex, atomic rewrite, empty dir = in-memory) as one file per
// project under ~/.niuma/plan/<key>.json. A new proposal in a
// project SUPERSEDES the slot's previous one (★Q8, per-project scope);
// acceptance pops the slot and hands its task entries to the task
// engine — the orchestrator never creates tasks directly, plan_submit
// is the only inbound road for proposal tasks.
//
// The wire shape (Plan/PlanTask) lives here rather than chat/wire.go
// because plan has no import cycle with chat (the tasks.Task
// precedent); chat.Message references it directly.
package plan

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/projects"
)

// Field limits, aligned with the task/requirement caps (house style).
const (
	MaxTitle   = 120 // plan title and per-task titles
	MaxNeed    = 2000
	MaxDesc    = 2000
	MaxTasks   = 64 // a plan is ≤7±2 packages by guidance; the cap is misfire insurance
	maxByRunes = 24
)

// PlanTask is one proposed task inside a Plan. Deps carries PLAN-
// INTERNAL 1-based entry indices (the orchestrator cannot know t_NN ids
// before acceptance) and may only reference EARLIER entries — the
// proposal reads in topological order; start/end are Unix seconds (the
// O6 protocol-wide time 口径).
type PlanTask struct {
	Title     string `json:"title"`
	Desc      string `json:"desc,omitempty"`
	Assignee  string `json:"assignee,omitempty"` // empty = task pool
	StartTS   int64  `json:"start,omitempty"`    // Unix seconds
	EndTS     int64  `json:"end,omitempty"`      // Unix seconds
	Deps      []int  `json:"deps,omitempty"`     // 1-based indices into plan.tasks
	Milestone bool   `json:"milestone,omitempty"`
}

// Plan is one scheduling proposal (orchestration §5.2's frame payload).
// The server stamps ID (a per-project p_NN counter), Submitter and
// SubmittedTS at submit; everything else is the orchestrator's (or, on
// plan_accept.revised, the host's edited) content.
type Plan struct {
	ID          string     `json:"id,omitempty"` // p_NN, server-stamped
	Req         string     `json:"req,omitempty"`
	Title       string     `json:"title"`
	Need        string     `json:"need,omitempty"`
	ProjectKey  string     `json:"project_key,omitempty"`
	Version     string     `json:"version,omitempty"`
	Tasks       []PlanTask `json:"tasks"`
	SubmittedBy string     `json:"submitted_by,omitempty"`
	SubmittedTS int64      `json:"submitted_ts,omitempty"`
}

// DefaultDir returns the conventional v2 location:
// ~/.niuma/plan (one <key>.json per project inside).
func DefaultDir() (string, error) {
	root, err := projects.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "plan"), nil
}

// Engine is the plan domain's engine — the ONLY home of the slot's
// semantics (p_NN counter, supersede, the one-shot Take gate, prose
// remap). Memory is the truth: every mutation rewrites the touched
// slot through the injected SlotStore seam (the JSON era kept a
// second copy of these semantics in sqlstore; the sqlite promotion
// deleted it). A nil seam is the in-memory engine (tests, home-less
// boot); a save failure logs and leaves memory ahead (the A-族
// discipline). The engine binds ONE storage subject at open; a face
// call under any other subject is refused.
type Engine struct {
	mu      sync.Mutex
	seam    SlotStore
	subject string
	slots   map[string]*Slot
	stage   *acceptStage // the accept path's staged Take (nil outside one)
}

// Slot is one project's persisted envelope: the id counter plus the
// single pending plan (nil = no proposal awaiting review). The JSON
// tags are the bridge's import shape, byte-stable.
type Slot struct {
	Next    int   `json:"next"`
	Pending *Plan `json:"pending,omitempty"`
}

// OpenMemory builds the in-memory engine (single-user subject "").
func OpenMemory() *Engine {
	return &Engine{slots: map[string]*Slot{}}
}

// OpenStore loads the engine over a persistence seam, bound to one
// storage subject ("" = the single-user historical shape, non-empty =
// the multi-user studio owner account). A corrupt slot document is an
// error (the caller's corrupt-reset path decides what happens next);
// an empty backing is the fresh-install state.
func OpenStore(seam SlotStore, subject string) (*Engine, error) {
	if seam == nil && subject != "" {
		return nil, ErrSingleSubject(subject)
	}
	e := &Engine{seam: seam, subject: subject, slots: map[string]*Slot{}}
	slots, err := seam.OpenSlots(subject)
	if err != nil {
		return nil, err
	}
	for key, sl := range slots {
		if sl.Next < 1 {
			sl.Next = 1
		}
		e.slots[key] = sl
	}
	return e, nil
}

// Submit installs p as projectKey's pending plan, stamping ID /
// Submitter / SubmittedTS. It returns the stored plan and the plan it
// SUPERSEDED (nil when the slot was empty) — the caller broadcasts the
// superseded event before the submitted one.
func (s *Engine) Submit(subject, projectKey, submitter string, p Plan, now int64) (stored, superseded *Plan) {
	if s.gate(subject) != nil {
		return nil, nil
	}
	p = p.sanitize()
	p.SubmittedBy = SanitizeBy(submitter)
	p.SubmittedTS = now
	s.mu.Lock()
	defer s.mu.Unlock()
	sl := s.slotForLocked(projectKey)
	p.ID = fmt.Sprintf("p_%02d", sl.Next)
	sl.Next++
	if sl.Pending != nil {
		old := *sl.Pending
		superseded = &old
	}
	storedPlan := p
	sl.Pending = &storedPlan
	s.saveLocked(projectKey)
	return &storedPlan, superseded
}

// Pending returns a copy of projectKey's awaiting plan (nil = none).
func (s *Engine) Pending(subject, projectKey string) *Plan {
	if s.gate(subject) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok || sl.Pending == nil {
		return nil
	}
	p := *sl.Pending
	return &p
}

// PendingIDs lists the projects holding an awaiting plan, sorted —
// the patrol prompt's 提案滞留 check and ops surfaces read it.
func (s *Engine) PendingIDs(subject string) []string {
	if s.gate(subject) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for k, sl := range s.slots {
		if sl.Pending != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Take pops projectKey's pending plan when its id matches planID (the
// accept/reject gate: one shot, id-checked — a stale accept after a
// supersede cannot consume the newer proposal). Errors name the
// mismatch so the denial reads.
func (s *Engine) Take(subject, projectKey, planID string) (*Plan, error) {
	if err := s.gate(subject); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok || sl.Pending == nil {
		return nil, ErrNoPending(projectKey)
	}
	if sl.Pending.ID != planID {
		return nil, ErrStalePlan(planID, sl.Pending.ID)
	}
	p := *sl.Pending
	sl.Pending = nil
	s.saveLocked(projectKey)
	return &p, nil
}

// Drop deletes projectKey's slot outright — the project reset's
// pending-proposal half (an awaiting plan cannot outlive the task and
// requirement shelves it cites). The p_NN counter goes with it, so a
// rebuilt project numbers its first proposal p_01, the first-open
// state. Reports whether a plan was awaiting review.
func (s *Engine) Drop(subject, projectKey string) bool {
	if s.gate(subject) != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok {
		return false
	}
	had := sl.Pending != nil
	delete(s.slots, projectKey)
	if s.seam != nil {
		if err := s.seam.DeleteSlot(s.subject, projectKey); err != nil {
			log.Printf("drop plan slot (%s): %v — 重启会复活该槽，请检查单库", projectKey, err)
		}
	}
	return had
}

// idTokenRE matches a whole r_NN/t_NN token in free text — the
// renumber mappings' single-pass swap. One pass, never chained
// ReplaceAll: a renumber map can hold a value that is also a key
// (t_02→t_01 with t_03→t_02), and chained replaces would re-replace
// a just-written token.
var idTokenRE = regexp.MustCompile(`[rt]_\d{1,4}`)

// swapIDTokens rewrites every r_NN/t_NN token of str that rides in
// mapping, in one pass.
func SwapIDTokens(str string, mapping map[string]string) string {
	if str == "" || len(mapping) == 0 {
		return str
	}
	return idTokenRE.ReplaceAllStringFunc(str, func(tok string) string {
		if nn, ok := mapping[tok]; ok {
			return nn
		}
		return tok
	})
}

// RemapReq rewrites the pending plan's Req field and its prose's
// r_NN tokens per mapping — the renumber migrations' companion face
// (the requirement ids changed underneath a proposal that cites
// them). Exact-id field swap plus single-pass token replace in the
// prose fields; returns whether anything changed.
func (s *Engine) RemapReq(subject, projectKey string, mapping map[string]string) bool {
	if s.gate(subject) != nil {
		return false
	}
	if len(mapping) == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok || sl.Pending == nil {
		return false
	}
	p := sl.Pending
	if nn, ok := mapping[p.Req]; ok {
		p.Req = nn
	}
	p.Need = SwapIDTokens(p.Need, mapping)
	for i := range p.Tasks {
		p.Tasks[i].Desc = SwapIDTokens(p.Tasks[i].Desc, mapping)
	}
	s.saveLocked(projectKey)
	return true
}

// RemapTasks rewrites the pending plan's prose t_NN tokens per
// mapping — the v2.11 task renumber's companion face. Tasks carry no
// structural id in a plan (PlanTask.Deps are proposal-local ordinals
// translated to fresh ids on accept), so prose is the whole surface.
func (s *Engine) RemapTasks(subject, projectKey string, mapping map[string]string) bool {
	if s.gate(subject) != nil {
		return false
	}
	return s.RemapReq(subject, projectKey, mapping)
}

// slotForLocked reads/creates the project's slot. Callers hold s.mu.
func (s *Engine) slotForLocked(projectKey string) *Slot {
	if sl, ok := s.slots[projectKey]; ok {
		return sl
	}
	sl := &Slot{Next: 1}
	s.slots[projectKey] = sl
	return sl
}

// saveLocked rewrites the project's slot through the seam (the JSON
// era's atomic whole-file rewrite, translated whole-document). A nil
// seam is the in-memory engine; a persistence failure logs but never
// aborts the in-memory mutation (memory stays ahead until the next
// save). Callers hold s.mu.
func (s *Engine) saveLocked(projectKey string) {
	if s.seam == nil {
		return
	}
	if err := s.seam.SaveSlot(s.subject, projectKey, s.slots[projectKey]); err != nil {
		log.Printf("save plan slot (%s): %v — 内存态领先，下次保存追平", projectKey, err)
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
// acceptPlanCore 把 Take 拆成四步：AcceptBegin 锁引擎并过一次性 id
// 门（不改动内存），AcceptWrite 把清空后的槽文档写进调用方的事务
// 缝，AcceptCommit 把内存推到后状态并解锁，AcceptAbort 弃置并解
// 锁。锁从 Begin 持到 Commit/Abort——中途不可有 Submit 顶替交错。

type acceptStage struct {
	project string
}

// AcceptBegin locks the engine and validates the take gate WITHOUT
// mutating: the pending plan must exist and carry planID (one shot,
// id-checked — a raced supersede between peek and take refuses with
// the slot untouched). Returns the plan the accept path will land.
func (s *Engine) AcceptBegin(subject, projectKey, planID string) (*Plan, error) {
	if err := s.gate(subject); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.stage != nil {
		s.mu.Unlock()
		return nil, errors.New("plan: 上一个接受暂存未收尾")
	}
	sl, ok := s.slots[projectKey]
	if !ok || sl.Pending == nil {
		s.mu.Unlock()
		return nil, ErrNoPending(projectKey)
	}
	if sl.Pending.ID != planID {
		err := ErrStalePlan(planID, sl.Pending.ID)
		s.mu.Unlock()
		return nil, err
	}
	p := *sl.Pending
	s.stage = &acceptStage{project: projectKey}
	return &p, nil
}

// AcceptWrite persists the post-state slot document (pending cleared)
// through w (the transaction-bound seam; nil = the in-memory engine,
// nothing to write). Memory is NOT yet moved — a failed transaction
// leaves both sides untouched.
func (s *Engine) AcceptWrite(w SlotStore) error {
	if s.stage == nil || w == nil {
		return nil
	}
	return w.SaveSlot(s.subject, s.stage.project, &Slot{Next: s.slots[s.stage.project].Next})
}

// AcceptCommit moves memory to the post-state (slot emptied) and
// unlocks. Cannot fail (the lock was held the whole time).
func (s *Engine) AcceptCommit() {
	if s.stage == nil {
		return
	}
	s.slots[s.stage.project].Pending = nil
	s.stage = nil
	s.mu.Unlock()
}

// AcceptAbort discards the staged take and unlocks (slot intact).
func (s *Engine) AcceptAbort() {
	if s.stage == nil {
		return
	}
	s.stage = nil
	s.mu.Unlock()
}

// sanitize clamps the prose fields. Identifiers (req, project_key,
// version) are validated or rejected by Validate — never rewritten.
func (p *Plan) sanitize() Plan {
	out := *p
	out.Title = clampRunes(strings.TrimSpace(out.Title), MaxTitle)
	out.Need = clampRunes(strings.TrimSpace(out.Need), MaxNeed)
	for i := range out.Tasks {
		out.Tasks[i].Title = clampRunes(strings.TrimSpace(out.Tasks[i].Title), MaxTitle)
		out.Tasks[i].Desc = clampRunes(strings.TrimSpace(out.Tasks[i].Desc), MaxDesc)
	}
	return out
}

// Sanitized is sanitize's exported copy — the server's plan_accept
// merge runs a host-supplied revised draft through the same clamps the
// store's Submit applies (an untrusted edit never bypasses the limits).
func (p Plan) Sanitized() Plan { return p.sanitize() }

// Validate is the pure content gate both submit and accept run
// (orchestration §5.2): non-empty title, ≥1 and ≤MaxTasks entries with
// non-empty titles, start ≤ end, and deps that are legal 1-based
// indices referencing ONLY EARLIER entries (so acceptance can create
// in order and translate indices to fresh t_NN ids as it goes — a
// forward reference would name an id that does not exist yet).
func (p Plan) Validate() error {
	if strings.TrimSpace(p.Title) == "" {
		return errors.New(i18n.S("提案标题不能为空"))
	}
	if len(p.Tasks) == 0 {
		return errors.New(i18n.S("提案至少需要一条任务"))
	}
	if len(p.Tasks) > MaxTasks {
		return errors.New(i18n.Sf("提案任务数超上限（%d 条）: %d", MaxTasks, len(p.Tasks)))
	}
	for i, t := range p.Tasks {
		if strings.TrimSpace(t.Title) == "" {
			return errors.New(i18n.Sf("第 %d 条任务标题不能为空", i+1))
		}
		if t.StartTS > 0 && t.EndTS > 0 && t.StartTS > t.EndTS {
			return errors.New(i18n.Sf("第 %d 条任务（%s）的排期起点晚于终点", i+1, t.Title))
		}
		for _, d := range t.Deps {
			if d < 1 || d > i {
				return errors.New(i18n.Sf("第 %d 条任务（%s）的依赖序号 %d 非法（只能引用更早条目，1–%d）",
					i+1, t.Title, d, i))
			}
		}
	}
	return nil
}

// TranslateDeps maps a plan entry's in-plan deps indices to the created
// tasks' real ids. ids holds the accepted entries' t_NN in plan order
// (index 0 = entry 1); Validate has already proven every index legal.
func TranslateDeps(deps []int, ids []string) []string {
	if len(deps) == 0 {
		return nil
	}
	out := make([]string, 0, len(deps))
	for _, d := range deps {
		out = append(out, ids[d-1])
	}
	return out
}

// sanitizeBy clamps a provenance name — the member-name cap, house style.
func SanitizeBy(name string) string {
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

// ErrNoPending / ErrStalePlan — the Take gate's refusals, shared by
// every caller (the storetest contract suite pins the bytes).
func ErrNoPending(projectKey string) error {
	return errors.New(i18n.Sf("项目 %s 没有待审提案", projectKey))
}

func ErrStalePlan(planID, current string) error {
	return errors.New(i18n.Sf("提案 %s 已不是当前待审件（现为 %s）", planID, current))
}

// ErrSingleSubject is the single-user engine's honest refusal (same
// contract note as requirements.ErrSingleSubject): the in-memory
// shape (and any backend bound to "") has no counterpart for a
// non-empty storage subject.
func ErrSingleSubject(subject string) error {
	return errors.New(i18n.Sf("提案槽是单用户形态（收到主体 %q）——多用户命名空间需 sqlite 单库", subject))
}

// ErrSubjectMismatch is the bound engine's refusal for a foreign
// subject (a wiring bug, never a runtime shape).
func ErrSubjectMismatch(bound, got string) error {
	return errors.New(i18n.Sf("提案槽引擎绑定主体 %q（收到 %q）——主体不匹配", bound, got))
}
