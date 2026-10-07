// Package merge is the gitflow merge-proposal domain (版本管理 v2.8 的
// 合并评审门)：分支汇回主线的单槽待审件。形状照抄 plan 包（每项目一
// 文件 ~/.niuma/merge/<key>.json、单槽 pending、新提案顶替旧件、Take
// 的一次性 id 门），但语义是「并回主线」：编排者（或房主）把一根领先
// 分支连同它的提交清单/红绿量/引用的 t_NN 提交进槽，房主 accept 后
// 工作室代执行 git merge --no-ff。The orchestrator never merges
// directly — merge_submit is the only inbound road, 镜像 plan_submit。
//
// 卡片数据（Commits/Files/Additions/Deletions/Tasks）在提交时快照进槽：
// 审的是提交那一刻的形状；accept 前置检查会重新对账（分支仍在、仍领
// 先、未并入），世界变了就拒收不猜。
package merge

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/projects"
)

// Field limits（plan 同源的房风帽）。
const (
	MaxBranch  = 120 // 分支/ref 名帽（git 自有 check-ref-format，这是入口夹）
	maxByRunes = 24
)

// Merge is one merge proposal: 把 branch 并入 into（主工作区当时的集
// 成分支）。Tasks 是提交信息里提到的 t_NN/r_NN 集（accept 的合并消息
// 带上它们，反查链不断）；Commits/Files/Additions/Deletions 是提交时
// 的差异快照（房主审卡不开终端）。
type Merge struct {
	ID          string   `json:"id,omitempty"` // m_NN, server-stamped
	ProjectKey  string   `json:"project_key,omitempty"`
	Branch      string   `json:"branch"`
	Into        string   `json:"into"`
	Req         string   `json:"req,omitempty"`
	Tasks       []string `json:"tasks,omitempty"`
	Commits     int      `json:"commits,omitempty"`
	Files       int      `json:"files,omitempty"`
	Additions   int      `json:"additions,omitempty"`
	Deletions   int      `json:"deletions,omitempty"`
	SubmittedBy string   `json:"submitted_by,omitempty"`
	SubmittedTS int64    `json:"submitted_ts,omitempty"`
}

// DefaultDir returns the conventional location: ~/.niuma/merge.
func DefaultDir() (string, error) {
	root, err := projects.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "merge"), nil
}

// Engine is the merge domain's engine — the ONLY home of the slot's
// semantics (m_NN counter, supersede, the one-shot Take gate, id
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
}

// Slot is one project's persisted envelope: the id counter plus the
// single pending merge (nil = nothing awaiting review). The JSON tags
// are the bridge's import shape, byte-stable.
type Slot struct {
	Next    int    `json:"next"`
	Pending *Merge `json:"pending,omitempty"`
}

// OpenMemory builds the in-memory engine (single-user subject "").
func OpenMemory() *Engine {
	return &Engine{slots: map[string]*Slot{}}
}

// OpenStore loads the engine over a persistence seam, bound to one
// storage subject. A corrupt slot document is an error (the caller's
// corrupt-reset path decides what happens next); an empty backing is
// the fresh-install state.
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

// Submit installs m as projectKey's pending merge, stamping ID /
// Submitter / SubmittedTS. Returns the stored proposal and the one it
// SUPERSEDED (nil when the slot was empty).
func (s *Engine) Submit(subject, projectKey, submitter string, m Merge, now int64) (stored, superseded *Merge) {
	if s.gate(subject) != nil {
		return nil, nil
	}
	m.Branch = strings.TrimSpace(m.Branch)
	m.Into = strings.TrimSpace(m.Into)
	m.SubmittedBy = SanitizeBy(submitter)
	m.SubmittedTS = now
	s.mu.Lock()
	defer s.mu.Unlock()
	sl := s.slotForLocked(projectKey)
	m.ID = fmt.Sprintf("m_%02d", sl.Next)
	sl.Next++
	if sl.Pending != nil {
		old := *sl.Pending
		superseded = &old
	}
	storedM := m
	sl.Pending = &storedM
	s.saveLocked(projectKey)
	return &storedM, superseded
}

// Pending returns a copy of projectKey's awaiting merge (nil = none).
func (s *Engine) Pending(subject, projectKey string) *Merge {
	if s.gate(subject) != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok || sl.Pending == nil {
		return nil
	}
	m := *sl.Pending
	return &m
}

// PendingIDs lists the projects holding an awaiting merge, sorted.
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

// Take pops projectKey's pending merge when its id matches mergeID
// （accept/reject 的一次性门：陈旧回执吃不到新提案）。
func (s *Engine) Take(subject, projectKey, mergeID string) (*Merge, error) {
	if err := s.gate(subject); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok || sl.Pending == nil {
		return nil, ErrNoPending(projectKey)
	}
	if sl.Pending.ID != mergeID {
		return nil, ErrStaleMerge(mergeID, sl.Pending.ID)
	}
	m := *sl.Pending
	sl.Pending = nil
	s.saveLocked(projectKey)
	return &m, nil
}

// Drop deletes projectKey's slot outright — the project reset's
// pending-merge half（待审合并提案不能活得过它引用的分支与任务架）.
// The m_NN counter goes with it; a rebuilt project numbers its first
// proposal m_01. Reports whether a merge was awaiting review.
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
			log.Printf("drop merge slot (%s): %v — 重启会复活该槽，请检查单库", projectKey, err)
		}
	}
	return had
}

// RemapIDs rewrites the pending proposal's Req per reqMap and its
// Tasks citation set per BOTH maps (the set mingles t_NN and r_NN
// tokens — the renumber migrations' mappings, v2.11 号段完全重排;
// the v2.10 sweep missed this store's Req entirely). Returns whether
// anything changed.
func (s *Engine) RemapIDs(subject, projectKey string, reqMap, taskMap map[string]string) bool {
	if s.gate(subject) != nil {
		return false
	}
	if len(reqMap) == 0 && len(taskMap) == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok || sl.Pending == nil {
		return false
	}
	m := sl.Pending
	if nn, ok := reqMap[m.Req]; ok {
		m.Req = nn
	}
	lookup := func(id string) (string, bool) {
		if nn, ok := taskMap[id]; ok {
			return nn, true
		}
		nn, ok := reqMap[id]
		return nn, ok
	}
	for i, id := range m.Tasks {
		if nn, ok := lookup(id); ok {
			m.Tasks[i] = nn
		}
	}
	s.saveLocked(projectKey)
	return true
}

func (s *Engine) slotForLocked(projectKey string) *Slot {
	if sl, ok := s.slots[projectKey]; ok {
		return sl
	}
	sl := &Slot{Next: 1}
	s.slots[projectKey] = sl
	return sl
}

// saveLocked rewrites the project's slot through the seam. A nil seam
// is the in-memory engine; a persistence failure logs but never aborts
// the in-memory mutation (memory stays ahead until the next save).
// Callers hold s.mu.
func (s *Engine) saveLocked(projectKey string) {
	if s.seam == nil {
		return
	}
	if err := s.seam.SaveSlot(s.subject, projectKey, s.slots[projectKey]); err != nil {
		log.Printf("save merge slot (%s): %v — 内存态领先，下次保存追平", projectKey, err)
	}
}

// gate is the bound-subject door (plan.Engine 的同款：绑定的主体放
// 行，单用户引擎拒绝一切非空主体，工作室引擎拒绝外来主体).
func (s *Engine) gate(subject string) error {
	if subject == s.subject {
		return nil
	}
	if s.subject == "" {
		return ErrSingleSubject(subject)
	}
	return ErrSubjectMismatch(s.subject, subject)
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

// ErrNoPending / ErrStaleMerge — the Take gate's refusals, shared by
// every Store implementation (plan 同款单一来源纪律).
func ErrNoPending(projectKey string) error {
	return errors.New(i18n.Sf("项目 %s 没有待审合并提案", projectKey))
}

func ErrStaleMerge(mergeID, current string) error {
	return errors.New(i18n.Sf("合并提案 %s 已不是当前待审件（现为 %s）", mergeID, current))
}

// ErrSingleSubject is the single-user engine's honest refusal (same
// contract note as requirements.ErrSingleSubject).
func ErrSingleSubject(subject string) error {
	return errors.New(i18n.Sf("合并提案槽是单用户形态（收到主体 %q）——多用户命名空间需 sqlite 单库", subject))
}

// ErrSubjectMismatch is the bound engine's refusal for a foreign
// subject (a wiring bug, never a runtime shape).
func ErrSubjectMismatch(bound, got string) error {
	return errors.New(i18n.Sf("合并提案槽引擎绑定主体 %q（收到 %q）——主体不匹配", bound, got))
}
