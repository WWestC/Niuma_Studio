package storetest

import (
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/requirements"
)

// ReqHarness wires one requirements.Store implementation into the
// contract suite. Open returns a fresh empty store and a reopen bound
// to the same durable backing (restart simulation). Name rides failure
// messages so a red suite names the implementation under test.
type ReqHarness struct {
	Name string
	// MultiSubject: true = the backing namespaces per subject (the
	// single database); false = the in-memory backing, which REFUSES
	// non-empty subjects.
	MultiSubject bool
	// OpenBacking creates one durable backing; open yields one engine
	// bound to one subject over it, reopen reconstructs over the same
	// backing and subject.
	OpenBacking func(t *testing.T) (open func(subject string) requirements.Store, reopen func(subject string) func(t *testing.T) requirements.Store)
}

// TestReqStore is the requirements ledger's contract suite. Every case
// is ported from the LocalStore tests that encoded the shelf semantics
// (requirements_test.go / parking_test.go) — the expectations are the
// product's, not the file backend's.
func TestReqStore(t *testing.T, h ReqHarness) {
	if h.OpenBacking == nil {
		t.Fatalf("%s: harness 无 OpenBacking", h.Name)
	}
	mk := func(t *testing.T) (requirements.Store, func(t *testing.T) requirements.Store) {
		open, reopen := h.OpenBacking(t)
		s := open("")
		if s == nil {
			t.Fatalf("%s: open 返回 nil store", h.Name)
		}
		return s, func(t *testing.T) requirements.Store { return reopen("")(t) }
	}

	t.Run("subject", func(t *testing.T) { TestReqSubject(t, h) })

	t.Run("lifecycle", func(t *testing.T) {
		s, _ := mk(t)
		a, err := s.Create("", "demo", "需求甲", "", "小牛")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if a.ID != "r_01" || a.Status != requirements.StatusOpen || a.CreatedTS == 0 || a.ProjectKey != "demo" {
			t.Fatalf("create 应盖 ID/Status/CreatedTS/ProjectKey（%s: %+v）", h.Name, a)
		}
		if _, err := s.Create("", "demo", "  ", "", "小牛"); err == nil {
			t.Fatalf("空标题应拒绝（%s）", h.Name)
		}
		if _, err := s.Create("", "bad key!", "标题", "", "小牛"); err == nil {
			t.Fatalf("非法项目 key 应拒绝（%s）", h.Name)
		}

		if _, err := s.MarkSplit("", "demo", a.ID); err != nil {
			t.Fatalf("split: %v", err)
		}
		if got, _ := s.GetIn("", "demo", a.ID); got.Status != requirements.StatusSplit {
			t.Fatalf("split 后状态应落（%s: %s）", h.Name, got.Status)
		}
		closed, err := s.MarkClosed("", "demo", a.ID)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		if closed.ClosedTS == 0 || closed.ClosedTS < closed.CreatedTS {
			t.Fatalf("MarkClosed 应盖不早于创建的完成时刻（%s: %+v）", h.Name, closed)
		}
		first := closed.ClosedTS
		if _, err := s.MarkClosed("", "demo", a.ID); err == nil {
			t.Fatalf("二次关闭应拒绝（%s）", h.Name)
		}
		if got, _ := s.GetIn("", "demo", a.ID); got.ClosedTS != first {
			t.Fatalf("拒绝路径不应改戳（%s: %d → %d）", h.Name, first, got.ClosedTS)
		}
	})

	t.Run("delete rules", func(t *testing.T) {
		s, _ := mk(t)
		mistake, _ := s.Create("", "demo", "误录条目", "", "小牛")
		split, _ := s.Create("", "demo", "已拆需求", "", "小牛")
		if _, err := s.MarkSplit("", "demo", split.ID); err != nil {
			t.Fatal(err)
		}
		closed, _ := s.Create("", "demo", "已关需求", "", "小牛")
		if _, err := s.MarkClosed("", "demo", closed.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Delete("", "demo", split.ID); err == nil {
			t.Fatalf("split 删除应拒绝（%s）", h.Name)
		}
		if _, err := s.Delete("", "demo", closed.ID); err == nil {
			t.Fatalf("closed 删除应拒绝（%s）", h.Name)
		}
		if got := len(s.List("")); got != 3 {
			t.Fatalf("拒绝删除不应动台账（%s: %d）", h.Name, got)
		}
		rev := s.Rev("")
		gone, err := s.Delete("", "demo", mistake.ID)
		if err != nil || gone.ID != mistake.ID {
			t.Fatalf("open 应可删并返回快照（%s: %+v %v）", h.Name, gone, err)
		}
		if _, ok := s.GetIn("", "demo", mistake.ID); ok {
			t.Fatalf("删除后 GetIn 应落空（%s）", h.Name)
		}
		if s.Rev("") != rev+1 {
			t.Fatalf("删除应推进 rev（%s: %d → %d）", h.Name, rev, s.Rev(""))
		}
		if _, err := s.Delete("", "demo", "r_99"); err == nil {
			t.Fatalf("删不存在的 id 应报错（%s）", h.Name)
		}
	})

	t.Run("deleted id never reissued", func(t *testing.T) {
		s, reopen := mk(t)
		if _, err := s.Create("", "demo", "留档", "", "小牛"); err != nil { // r_01
			t.Fatal(err)
		}
		if _, err := s.Create("", "demo", "误录", "", "小牛"); err != nil { // r_02 最大号
			t.Fatal(err)
		}
		if _, err := s.Delete("", "demo", "r_02"); err != nil {
			t.Fatal(err)
		}
		s2 := reopen(t)
		fresh, err := s2.Create("", "demo", "新需求", "", "小牛")
		if err != nil {
			t.Fatal(err)
		}
		if fresh.ID != "r_03" {
			t.Fatalf("重启后 id 高水位必须挡住已删号复用（%s: 得 %s 想 r_03）", h.Name, fresh.ID)
		}
	})

	t.Run("per-project shelves", func(t *testing.T) {
		s, reopen := mk(t)
		lobbyA, _ := s.Create("", "default", "大厅一号", "", "房主")
		lobbyB, _ := s.Create("", "default", "大厅二号", "", "房主")
		bookA, _ := s.Create("", "book", "书一号", "", "小苗")
		if lobbyA.ID != "r_01" || lobbyB.ID != "r_02" || bookA.ID != "r_01" {
			t.Fatalf("每项目各自计数（%s: %s %s %s）", h.Name, lobbyA.ID, lobbyB.ID, bookA.ID)
		}
		if got, ok := s.GetIn("", "book", "r_01"); !ok || got.Title != "书一号" {
			t.Fatalf("GetIn 按架解析（%s: %+v %v）", h.Name, got, ok)
		}
		if _, ok := s.GetIn("", "book", "r_02"); ok {
			t.Fatalf("book 架没有 r_02（%s）", h.Name)
		}
		if got := s.FindAll("", "r_01"); len(got) != 2 {
			t.Fatalf("FindAll 跨架检索应命中两架（%s: %d）", h.Name, len(got))
		}
		if _, err := s.MarkSplit("", "book", "r_01"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.GetIn("", "default", "r_01"); got.Status != requirements.StatusOpen {
			t.Fatalf("book 的拆解不应波及大厅同号（%s: %s）", h.Name, got.Status)
		}
		if got := s.ListByProject("", "default"); len(got) != 2 {
			t.Fatalf("ListByProject 按架列出（%s: %d）", h.Name, len(got))
		}
		s2 := reopen(t)
		if got, ok := s2.GetIn("", "book", "r_01"); !ok || got.Status != requirements.StatusSplit {
			t.Fatalf("重启后按架状态各自存活（%s: %+v %v）", h.Name, got, ok)
		}
		next, _ := s2.Create("", "book", "书二号", "", "小苗")
		if next.ID != "r_02" {
			t.Fatalf("book 续号从自己架推导（%s: %s）", h.Name, next.ID)
		}
	})

	t.Run("parking", func(t *testing.T) {
		s, reopen := mk(t)
		id := func() string {
			r, err := s.Create("", "demo", "多项目房实战", "", "房主")
			if err != nil {
				t.Fatal(err)
			}
			return r.ID
		}()
		r, err := s.Park("", "demo", id, "等房主建第二项目", 0)
		if err != nil || r.Status != requirements.StatusParking || r.ParkNote != "等房主建第二项目" || r.ParkTS == 0 {
			t.Fatalf("park 后字段应齐（%s: %+v %v）", h.Name, r, err)
		}
		if _, err := s.MarkSplit("", "demo", id); err == nil || !strings.Contains(err.Error(), "仅 open") {
			t.Fatalf("parking 态 MarkSplit 必拒（%s: %v）", h.Name, err)
		}
		if _, err := s.MarkClosed("", "demo", id); err == nil || !strings.Contains(err.Error(), "unpark") {
			t.Fatalf("parking 态 MarkClosed 必拒并指路 unpark（%s: %v）", h.Name, err)
		}
		r, err = s.Unpark("", "demo", id)
		if err != nil || r.Status != requirements.StatusOpen || r.ParkNote != "等房主建第二项目" {
			t.Fatalf("unpark 回 open 且不抹原因（%s: %+v %v）", h.Name, r, err)
		}
		if _, err := s.MarkSplit("", "demo", id); err != nil {
			t.Fatalf("unpark 后恢复可拆解（%s: %v）", h.Name, err)
		}
		// 复查期与再 park 覆盖原因
		id2v, _ := s.Create("", "demo", "带复查期", "", "房主")
		id2 := id2v.ID
		after := int64(1791000000)
		if _, err := s.Park("", "demo", id2, "等发布窗口", after); err != nil {
			t.Fatal(err)
		}
		s2 := reopen(t)
		if got, ok := s2.GetIn("", "demo", id2); !ok || got.ReviewAfter != after || got.ParkNote != "等发布窗口" {
			t.Fatalf("复查期与原因应随重启存活（%s: %+v %v）", h.Name, got, ok)
		}
		if _, err := s2.Unpark("", "demo", id2); err != nil {
			t.Fatal(err)
		}
		if _, err := s2.Park("", "demo", id2, "第二版原因", 0); err != nil {
			t.Fatal(err)
		}
		if got, _ := s2.GetIn("", "demo", id2); got.ParkNote != "第二版原因" {
			t.Fatalf("再 park 覆盖原因（%s: %q）", h.Name, got.ParkNote)
		}
		// parking 可删（库存态非保护态）
		if _, err := s2.Delete("", "demo", id2); err != nil {
			t.Fatalf("parking 应可删（%s: %v）", h.Name, err)
		}
	})

	t.Run("renumber", func(t *testing.T) {
		s, reopen := mk(t)
		for _, title := range []string{"甲", "乙", "丙"} {
			if _, err := s.Create("", "demo", title, "", "小牛"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Delete("", "demo", "r_01"); err != nil {
			t.Fatal(err)
		}
		// 现存 r_02 r_03 → 紧凑计划 r_02→r_01、r_03→r_02
		plan := s.RenumberPlan("", "demo")
		if len(plan) != 2 || plan["r_02"] != "r_01" || plan["r_03"] != "r_02" {
			t.Fatalf("RenumberPlan 紧凑重排计划（%s: %v）", h.Name, plan)
		}
		if err := s.RenumberApply("", "demo", plan); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.GetIn("", "demo", "r_01"); !ok {
			t.Fatalf("重排后 r_01 应在架（%s）", h.Name)
		}
		s2 := reopen(t)
		fresh, _ := s2.Create("", "demo", "丁", "", "小牛")
		if fresh.ID != "r_03" {
			t.Fatalf("重排后计数器越过新最大号（%s: %s）", h.Name, fresh.ID)
		}
	})

	t.Run("drop project", func(t *testing.T) {
		s, reopen := mk(t)
		for _, title := range []string{"甲", "乙"} {
			if _, err := s.Create("", "demo", title, "", "小牛"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Create("", "keep", "别家的", "", "小牛"); err != nil {
			t.Fatal(err)
		}
		if n := s.DropProject("", "demo"); n != 2 {
			t.Fatalf("DropProject 返回清除条数（%s: %d）", h.Name, n)
		}
		if got := s.ListByProject("", "demo"); len(got) != 0 {
			t.Fatalf("清空后台账应空（%s: %d）", h.Name, len(got))
		}
		if _, ok := s.GetIn("", "keep", "r_01"); !ok {
			t.Fatalf("别架不受波及（%s）", h.Name)
		}
		s2 := reopen(t)
		fresh, err := s2.Create("", "demo", "重建的第一条", "", "小牛")
		if err != nil || fresh.ID != "r_01" {
			t.Fatalf("清架后号段从 r_01 重排（%s: %s %v）", h.Name, fresh.ID, err)
		}
	})
}
