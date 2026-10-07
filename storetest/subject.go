package storetest

// subject.go — the SUBJECT half of the storage contract: the Store
// interfaces' first parameter is the storage namespace ("" = the
// single-user historical shape, non-empty = a multi-user studio
// subject). The suite branches on the harness's MultiSubject flag:
//
//   - true  (the single database): two subjects over one backing
//     behave as two ledgers — same ids, same counters, no cross-talk,
//     and the default ("") namespace sees neither;
//   - false (the in-memory backing): a non-empty subject is REFUSED,
//     byte-pinned to the domain package's ErrSingleSubject
//     constructor — the memory shape has one engine, not one per
//     namespace, so it fails loud instead of silently sharing one
//     subject's data with another.

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
)

func subjectRefusal(t *testing.T, name string, err error, want error, op string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s：非空主体应被拒绝（%s）（%s）", name, op, op)
	}
	if err.Error() != want.Error() {
		t.Fatalf("%s：%s 拒绝文案应逐字节钉构造器：%q != %q", name, op, err.Error(), want.Error())
	}
}

// TestReqSubject is the requirements ledger's subject contract.
func TestReqSubject(t *testing.T, h ReqHarness) {
	open, _ := h.OpenBacking(t)
	if h.MultiSubject {
		a, b := open("alice"), open("bob")
		if _, err := a.Create("alice", "demo", "甲的 r_01", "", "alice"); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Create("bob", "demo", "乙的 r_01", "", "bob"); err != nil {
			t.Fatal(err)
		}
		if x, ok := a.GetIn("alice", "demo", "r_01"); !ok || x.CreatedBy != "alice" {
			t.Fatalf("alice 主体只读自家的行（%s: %+v）", h.Name, x)
		}
		if got := a.List("alice"); len(got) != 1 {
			t.Fatalf("alice 主体列表只含自家的行（%s: %d）", h.Name, len(got))
		}
		e := open("")
		if got := e.List(""); len(got) != 0 {
			t.Fatalf("默认主体不应看到具名主体的行（%s: %d）", h.Name, len(got))
		}
		if id, _ := b.Create("bob", "demo", "乙的第二条", "", "bob"); id.ID != "r_02" {
			t.Fatalf("bob 主体计数独立推进（%s: %s）", h.Name, id.ID)
		}
	} else {
		s := open("")
		_, err := s.Create("alice", "demo", "标题", "", "alice")
		subjectRefusal(t, h.Name, err, requirements.ErrSingleSubject("alice"), "Create")
		if _, ok := s.GetIn("alice", "demo", "r_01"); ok {
			t.Fatalf("%s：拒绝主体下的 GetIn 不应命中", h.Name)
		}
	}
}

// TestPlanSubject is the plan domain's subject contract.
func TestPlanSubject(t *testing.T, h PlanHarness) {
	open, _ := h.OpenBacking(t)
	if h.MultiSubject {
		a, b := open("alice"), open("bob")
		if stored, _ := a.Submit("alice", "demo", "甲", plan.Plan{Title: "甲的提案"}, 1); stored == nil {
			t.Fatalf("alice 主体提交应落（%s）", h.Name)
		}
		if stored, _ := b.Submit("bob", "demo", "乙", plan.Plan{Title: "乙的提案"}, 1); stored == nil {
			t.Fatalf("bob 主体提交应落（%s）", h.Name)
		}
		if p := a.Pending("alice", "demo"); p == nil || p.SubmittedBy != "甲" {
			t.Fatalf("alice 主体只见自家的待审（%s: %+v）", h.Name, p)
		}
		if got := b.PendingIDs("bob"); len(got) != 1 || got[0] != "demo" {
			t.Fatalf("bob 主体待审清单（%s: %v）", h.Name, got)
		}
		e := open("")
		if got := e.PendingIDs(""); len(got) != 0 {
			t.Fatalf("默认主体不应看到具名主体的待审（%s: %v）", h.Name, got)
		}
	} else {
		s := open("")
		s.Submit("alice", "demo", "甲", plan.Plan{Title: "甲的提案"}, 1)
		if p := s.Pending("alice", "demo"); p != nil {
			t.Fatalf("%s：拒绝主体下不应有提交落地", h.Name)
		}
	}
}

// TestMergeSubject is the merge domain's subject contract.
func TestMergeSubject(t *testing.T, h MergeHarness) {
	open, _ := h.OpenBacking(t)
	if h.MultiSubject {
		a, b := open("alice"), open("bob")
		if stored, _ := a.Submit("alice", "demo", "甲", merge.Merge{Branch: "feat-a"}, 1); stored == nil {
			t.Fatalf("alice 主体提交应落（%s）", h.Name)
		}
		if stored, _ := b.Submit("bob", "demo", "乙", merge.Merge{Branch: "feat-b"}, 1); stored == nil {
			t.Fatalf("bob 主体提交应落（%s）", h.Name)
		}
		if m := a.Pending("alice", "demo"); m == nil || m.SubmittedBy != "甲" {
			t.Fatalf("alice 主体只见自家的待审（%s: %+v）", h.Name, m)
		}
		e := open("")
		if got := e.PendingIDs(""); len(got) != 0 {
			t.Fatalf("默认主体不应看到具名主体的待审（%s: %v）", h.Name, got)
		}
	} else {
		s := open("")
		s.Submit("alice", "demo", "甲", merge.Merge{Branch: "feat-a"}, 1)
		if m := s.Pending("alice", "demo"); m != nil {
			t.Fatalf("%s：拒绝主体下不应有提交落地", h.Name)
		}
	}
}

// TestTaskSubject is the task engine's subject contract (engine-level:
// the engine binds one subject at open; the sqlite backing isolates by
// it, the memory backing has only the "" engine).
func TestTaskSubject(t *testing.T, h TaskHarness) {
	open, _ := h.OpenBacking(t)
	e := open("")
	e.SetProjectKeys(func() []string { return []string{"book", "demo"} }) // 套件合法项目集
	if h.MultiSubject {
		if o := e.Create("demo", "alice", "甲的活", "", "alice"); o.Denied {
			t.Fatalf("主体内创建（%s）: %s", h.Name, o.Reason)
		}
		if got := e.Count(); got != 1 {
			t.Fatalf("主体内应见自家的任务（%s: %d）", h.Name, got)
		}
	} else {
		// 单用户形态：引擎绑定 ""，内存后端没有别的主体可开——由域
		//包自身的拒绝路径钉（ErrSingleSubject）。此处钉引擎绑定形状。
		if e.Subject() != "" {
			t.Fatalf("单用户引擎应绑定空主体（%s: %q）", h.Name, e.Subject())
		}
	}
}
