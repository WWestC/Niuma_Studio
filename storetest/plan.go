package storetest

import (
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/plan"
)

// PlanHarness wires one plan.Store implementation into the contract
// suite (see ReqHarness for the reopen contract).
type PlanHarness struct {
	Name string
	// MultiSubject: true = the backing namespaces per subject (the
	// single database); false = the in-memory backing, which REFUSES
	// non-empty subjects.
	MultiSubject bool
	OpenBacking  func(t *testing.T) (open func(subject string) plan.Store, reopen func(subject string) func(t *testing.T) plan.Store)
}

// TestPlanStore is the plan domain's contract suite: submit stamping
// and supersede, the Take id-gate, persistence round-trips, drop
// semantics, prose remap faces. Ported from the plan/merge twin tests'
// expectations (merge_test.go) plus plan.go's documented semantics.
func TestPlanStore(t *testing.T, h PlanHarness) {
	if h.OpenBacking == nil {
		t.Fatalf("%s: harness 无 OpenBacking", h.Name)
	}
	mk := func(t *testing.T) (plan.Store, func(t *testing.T) plan.Store) {
		open, reopen := h.OpenBacking(t)
		s := open("")
		if s == nil {
			t.Fatalf("%s: open 返回 nil store", h.Name)
		}
		return s, func(t *testing.T) plan.Store { return reopen("")(t) }
	}

	t.Run("subject", func(t *testing.T) { TestPlanSubject(t, h) })

	t.Run("submit stamps and supersedes", func(t *testing.T) {
		s, _ := mk(t)
		stored, sup := s.Submit("", "proj-x", "编排者", plan.Plan{Title: "提案甲", Tasks: []plan.PlanTask{{Title: "活一"}}}, 100)
		if stored.ID != "p_01" || stored.SubmittedBy != "编排者" || stored.SubmittedTS != 100 {
			t.Fatalf("盖章不合预期（%s: %+v）", h.Name, stored)
		}
		if sup != nil {
			t.Fatalf("首件无被顶替者（%s）", h.Name)
		}
		stored2, sup2 := s.Submit("", "proj-x", "编排者", plan.Plan{Title: "提案乙", Tasks: []plan.PlanTask{{Title: "活二"}}}, 200)
		if stored2.ID != "p_02" || sup2 == nil || sup2.ID != "p_01" {
			t.Fatalf("第二件应顶替 p_01（%s: %+v sup=%+v）", h.Name, stored2, sup2)
		}
		if _, sup3 := s.Submit("", "proj-y", "编排者", plan.Plan{Title: "别家的", Tasks: []plan.PlanTask{{Title: "活"}}}, 300); sup3 != nil {
			t.Fatalf("proj-y 首件无被顶替者（%s）", h.Name)
		}
		if got := s.Pending("", "proj-x"); got == nil || got.ID != "p_02" {
			t.Fatalf("槽内应持最新件（%s: %+v）", h.Name, got)
		}
		if ids := s.PendingIDs(""); len(ids) != 2 || ids[0] != "proj-x" || ids[1] != "proj-y" {
			t.Fatalf("PendingIDs 有序列出（%s: %v）", h.Name, ids)
		}
	})

	t.Run("take id gate", func(t *testing.T) {
		s, _ := mk(t)
		stored, _ := s.Submit("", "proj-x", "编排者", plan.Plan{Title: "提案", Tasks: []plan.PlanTask{{Title: "活"}}}, 100)
		if _, err := s.Take("", "proj-x", "p_99"); err == nil {
			t.Fatalf("陈旧 id 不应消费到新件（%s）", h.Name)
		}
		got, err := s.Take("", "proj-x", stored.ID)
		if err != nil || got.ID != stored.ID {
			t.Fatalf("对口 id 应取到（%s: %+v %v）", h.Name, got, err)
		}
		if p := s.Pending("", "proj-x"); p != nil {
			t.Fatalf("Take 后槽应空（%s: %+v）", h.Name, p)
		}
		if _, err := s.Take("", "proj-x", stored.ID); err == nil {
			t.Fatalf("二次 Take 应报无待审件（%s）", h.Name)
		}
	})

	t.Run("persistence round trip", func(t *testing.T) {
		s, reopen := mk(t)
		p := plan.Plan{Title: "重启存活", Req: "r_01", Need: "引 r_01 干活",
			Tasks: []plan.PlanTask{{Title: "活", Desc: "引 r_02 与 t_03"}}}
		stored, _ := s.Submit("", "proj-x", "编排者", p, 100)
		s2 := reopen(t)
		got := s2.Pending("", "proj-x")
		if got == nil || got.ID != stored.ID || got.Title != "重启存活" || got.Req != "r_01" {
			t.Fatalf("重开后待审件应原样（%s: %+v）", h.Name, got)
		}
		next, _ := s2.Submit("", "proj-x", "编排者", plan.Plan{Title: "接续", Tasks: []plan.PlanTask{{Title: "活"}}}, 200)
		if next.ID != "p_02" {
			t.Fatalf("计数应续走 p_02（%s: %s）", h.Name, next.ID)
		}
	})

	t.Run("drop resets counter", func(t *testing.T) {
		s, reopen := mk(t)
		s.Submit("", "proj-x", "编排者", plan.Plan{Title: "待清", Tasks: []plan.PlanTask{{Title: "活"}}}, 100)
		s.Submit("", "proj-x", "编排者", plan.Plan{Title: "再顶一件", Tasks: []plan.PlanTask{{Title: "活"}}}, 200)
		if !s.Drop("", "proj-x") {
			t.Fatalf("Drop 应报有件在等（%s）", h.Name)
		}
		if s.Drop("", "proj-x") {
			t.Fatalf("二次 Drop 应报无件（%s）", h.Name)
		}
		s2 := reopen(t)
		fresh, _ := s2.Submit("", "proj-x", "编排者", plan.Plan{Title: "重建第一件", Tasks: []plan.PlanTask{{Title: "活"}}}, 300)
		if fresh.ID != "p_01" {
			t.Fatalf("清槽后号段从 p_01 重排（%s: %s）", h.Name, fresh.ID)
		}
	})

	t.Run("remap", func(t *testing.T) {
		s, _ := mk(t)
		s.Submit("", "proj-x", "编排者", plan.Plan{
			Title: "改号", Req: "r_02", Need: "引 r_02 与 r_03 干活",
			Tasks: []plan.PlanTask{{Title: "活", Desc: "依赖 t_02"}},
		}, 100)
		mapping := map[string]string{"r_02": "r_01", "r_03": "r_02", "t_02": "t_01"}
		if !s.RemapReq("", "proj-x", mapping) {
			t.Fatalf("RemapReq 应报有改（%s）", h.Name)
		}
		got := s.Pending("", "proj-x")
		if got.Req != "r_01" || !strings.Contains(got.Need, "r_01") || !strings.Contains(got.Need, "r_02") {
			t.Fatalf("RemapReq 应改 Req 字段与 Need 语料（%s: %+v）", h.Name, got)
		}
		// 链式替换防线：r_03→r_02 不得再被 r_02→r_01 二次改写
		if strings.Count(got.Need, "r_01") != 1 {
			t.Fatalf("单趟替换不得链式（%s: %q）", h.Name, got.Need)
		}
		if !s.RemapTasks("", "proj-x", mapping) {
			t.Fatalf("RemapTasks 应报有改（%s）", h.Name)
		}
		if got := s.Pending("", "proj-x"); got.Tasks[0].Desc != "依赖 t_01" {
			t.Fatalf("RemapTasks 应改任务语料（%s: %q）", h.Name, got.Tasks[0].Desc)
		}
	})

	t.Run("submit clamps prose", func(t *testing.T) {
		s, _ := mk(t)
		long := strings.Repeat("长", plan.MaxTitle+10)
		stored, _ := s.Submit("", "proj-x", "编排者", plan.Plan{Title: long, Tasks: []plan.PlanTask{{Title: long}}}, 100)
		if rc := len([]rune(stored.Title)); rc != plan.MaxTitle {
			t.Fatalf("标题应钳到帽（%s: %d）", h.Name, rc)
		}
		if rc := len([]rune(stored.Tasks[0].Title)); rc != plan.MaxTitle {
			t.Fatalf("任务标题应钳到帽（%s: %d）", h.Name, rc)
		}
	})
}
