package storetest

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/merge"
)

// MergeHarness wires one merge.Store implementation into the contract
// suite (see ReqHarness for the reopen contract).
type MergeHarness struct {
	Name string
	// MultiSubject: true = the backing namespaces per subject (the
	// single database); false = the in-memory backing, which REFUSES
	// non-empty subjects.
	MultiSubject bool
	OpenBacking  func(t *testing.T) (open func(subject string) merge.Store, reopen func(subject string) func(t *testing.T) merge.Store)
}

// TestMergeStore is the merge domain's contract suite — plan's twin
// shape (submit stamp/supersede, Take id-gate, persistence, drop) plus
// the RemapIDs face that mingles t_NN/r_NN citation sets.
func TestMergeStore(t *testing.T, h MergeHarness) {
	if h.OpenBacking == nil {
		t.Fatalf("%s: harness 无 OpenBacking", h.Name)
	}
	mk := func(t *testing.T) (merge.Store, func(t *testing.T) merge.Store) {
		open, reopen := h.OpenBacking(t)
		s := open("")
		if s == nil {
			t.Fatalf("%s: open 返回 nil store", h.Name)
		}
		return s, func(t *testing.T) merge.Store { return reopen("")(t) }
	}

	t.Run("subject", func(t *testing.T) { TestMergeSubject(t, h) })

	t.Run("submit stamps and supersedes", func(t *testing.T) {
		s, _ := mk(t)
		stored, sup := s.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-a", Into: "main"}, 100)
		if stored.ID != "m_01" || stored.SubmittedBy != "编排者" || stored.SubmittedTS != 100 {
			t.Fatalf("盖章不合预期（%s: %+v）", h.Name, stored)
		}
		if sup != nil {
			t.Fatalf("首件无被顶替者（%s）", h.Name)
		}
		stored2, sup2 := s.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-b", Into: "main"}, 200)
		if stored2.ID != "m_02" || sup2 == nil || sup2.ID != "m_01" {
			t.Fatalf("第二件应顶替 m_01（%s: %+v sup=%+v）", h.Name, stored2, sup2)
		}
		if _, sup3 := s.Submit("", "proj-y", "编排者", merge.Merge{Branch: "feat-c", Into: "main"}, 300); sup3 != nil {
			t.Fatalf("proj-y 首件无被顶替者（%s）", h.Name)
		}
	})

	t.Run("take id gate", func(t *testing.T) {
		s, _ := mk(t)
		stored, _ := s.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-a", Into: "main"}, 100)
		if _, err := s.Take("", "proj-x", "m_99"); err == nil {
			t.Fatalf("陈旧 id 不应消费到新件（%s）", h.Name)
		}
		got, err := s.Take("", "proj-x", stored.ID)
		if err != nil || got.ID != stored.ID {
			t.Fatalf("对口 id 应取到（%s: %+v %v）", h.Name, got, err)
		}
		if p := s.Pending("", "proj-x"); p != nil {
			t.Fatalf("Take 后槽应空（%s）", h.Name)
		}
		if _, err := s.Take("", "proj-x", stored.ID); err == nil {
			t.Fatalf("二次 Take 应报无待审件（%s）", h.Name)
		}
	})

	t.Run("persistence round trip", func(t *testing.T) {
		s, reopen := mk(t)
		m := merge.Merge{Branch: "feat-a", Into: "main", Req: "r_01",
			Tasks: []string{"t_01", "r_02"}, Commits: 3, Files: 5, Additions: 10, Deletions: 2}
		stored, _ := s.Submit("", "proj-x", "编排者", m, 100)
		s2 := reopen(t)
		got := s2.Pending("", "proj-x")
		if got == nil || got.ID != stored.ID || got.Branch != "feat-a" || len(got.Tasks) != 2 ||
			got.Commits != 3 || got.Files != 5 || got.Additions != 10 || got.Deletions != 2 {
			t.Fatalf("重开后待审件应原样（%s: %+v）", h.Name, got)
		}
		next, _ := s2.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-b", Into: "main"}, 200)
		if next.ID != "m_02" {
			t.Fatalf("计数应续走 m_02（%s: %s）", h.Name, next.ID)
		}
	})

	t.Run("drop resets counter", func(t *testing.T) {
		s, reopen := mk(t)
		s.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-a", Into: "main"}, 100)
		if !s.Drop("", "proj-x") {
			t.Fatalf("Drop 应报有件在等（%s）", h.Name)
		}
		if s.Drop("", "proj-x") {
			t.Fatalf("二次 Drop 应报无件（%s）", h.Name)
		}
		s2 := reopen(t)
		fresh, _ := s2.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-c", Into: "main"}, 300)
		if fresh.ID != "m_01" {
			t.Fatalf("清槽后号段从 m_01 重排（%s: %s）", h.Name, fresh.ID)
		}
	})

	t.Run("remap ids", func(t *testing.T) {
		s, _ := mk(t)
		s.Submit("", "proj-x", "编排者", merge.Merge{
			Branch: "feat-a", Into: "main", Req: "r_02", Tasks: []string{"t_02", "r_03"},
		}, 100)
		reqMap := map[string]string{"r_02": "r_01", "r_03": "r_02"}
		taskMap := map[string]string{"t_02": "t_01"}
		if !s.RemapIDs("", "proj-x", reqMap, taskMap) {
			t.Fatalf("RemapIDs 应报有改（%s）", h.Name)
		}
		got := s.Pending("", "proj-x")
		if got.Req != "r_01" {
			t.Fatalf("Req 应按 reqMap 改（%s: %s）", h.Name, got.Req)
		}
		if got.Tasks[0] != "t_01" || got.Tasks[1] != "r_02" {
			t.Fatalf("引用集应按两张映射改（%s: %v）", h.Name, got.Tasks)
		}
	})
}
