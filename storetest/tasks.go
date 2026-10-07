package storetest

// tasks.go — the fourth contract domain: the task ledger + rank
// registry. Unlike the first three domains (a Store whose whole face
// is the contract), tasks keeps its domain logic in the Engine — the
// seam under test is the Engine's persistence (tasks.Store, one
// whole-shelf document per project plus the global ranks). The suite
// therefore drives ENGINE verbs and pins what must hold over ANY
// backend: the per-project id high-water (a spent number is never
// re-issued across restart), shelf isolation, the rank registry
// round-trip, the full status flow (including the proposal family and
// the structured work clock trial B made load-bearing), project wipe
// and renumber. The expectations are the product's, ported from the
// Engine's own tests — not either backend's.

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/tasks"
)

// TaskHarness wires one tasks.Store implementation (behind an Engine)
// into the contract suite. Open returns a fresh empty engine over a
// fresh empty backing plus a reopen bound to the same durable bytes
// (restart simulation). localName pins the host seat ("房主" — rank
// 99, never persisted); the suite attaches shelves onto the known
// project set {"book", "demo"} through SetProjectKeys.
type TaskHarness struct {
	Name string
	// MultiSubject: true = the backing namespaces per subject (the
	// single database; store-level isolation is pinned in sqlstore's
	// own tests); false = the in-memory backing, whose engines bind ""
	// only.
	MultiSubject bool
	OpenBacking  func(t *testing.T) (open func(subject string) *tasks.Engine, reopen func(subject string) func(t *testing.T) *tasks.Engine)
}

// TestTaskStore is the task ledger's contract suite.
func TestTaskStore(t *testing.T, h TaskHarness) {
	if h.OpenBacking == nil {
		t.Fatalf("%s: harness 无 OpenBacking", h.Name)
	}
	mk := func(t *testing.T) (*tasks.Engine, func(t *testing.T) *tasks.Engine) {
		open, reopen := h.OpenBacking(t)
		e := open("")
		if e == nil {
			t.Fatalf("%s: open 返回 nil engine", h.Name)
		}
		wire := func(e *tasks.Engine) *tasks.Engine {
			// 结构校验的合法项目集：套件只挂 book/demo 两架。
			e.SetProjectKeys(func() []string { return []string{"book", "demo"} })
			return e
		}
		return wire(e), func(t *testing.T) *tasks.Engine { return wire(reopen("")(t)) }
	}
	host := "房主"

	t.Run("subject", func(t *testing.T) { TestTaskSubject(t, h) })

	t.Run("numbering and high-water", func(t *testing.T) {
		e, reopen := mk(t)
		for _, title := range []string{"甲", "乙", "丙"} {
			if out := e.Create("demo", host, title, "", ""); out.Denied {
				t.Fatalf("create 被拒（%s: %s）", h.Name, out.Reason)
			}
		}
		if got := e.Create("demo", host, "丁", "", ""); got.Denied || got.Task.ID != "t_04" {
			t.Fatalf("续号应 t_04（%s: %+v）", h.Name, got)
		}
		e2 := reopen(t)
		if got := e2.Create("demo", host, "重启后的戊", "", ""); got.Denied || got.Task.ID != "t_05" {
			t.Fatalf("重启后计数行必须挡住重发（%s: 得 %s 想 t_05）", h.Name, got.Task.ID)
		}
	})

	t.Run("per-project isolation", func(t *testing.T) {
		e, reopen := mk(t)
		lobbyA := e.Create("", host, "大厅一号", "", "")
		bookA := e.Create("book", host, "书一号", "", "小明")
		if lobbyA.Denied || lobbyA.Task.ID != "t_01" || bookA.Denied || bookA.Task.ID != "t_01" {
			t.Fatalf("两架各自从 t_01 起（%s: %s %s）", h.Name, lobbyA.Task.ID, bookA.Task.ID)
		}
		if out := e.Update("book", host, "t_01", tasks.Patch{Status: tasks.StatusDoing}); out.Denied {
			t.Fatalf("book 的 t_01 应可流转（%s: %s）", h.Name, out.Reason)
		}
		if got, ok := e.GetIn("", "t_01"); !ok || got.Status != tasks.StatusTodo {
			t.Fatalf("book 的流转不应波及大厅同号（%s: %s）", h.Name, got.Status)
		}
		if _, ok := e.GetIn("demo", "t_01"); ok {
			t.Fatalf("demo 架空无 t_01（%s）", h.Name)
		}
		e2 := reopen(t)
		if got, ok := e2.GetIn("book", "t_01"); !ok || got.Status != tasks.StatusDoing || got.Assignee != "小明" {
			t.Fatalf("重启后按架状态存活（%s: %+v %v）", h.Name, got, ok)
		}
		next := e2.Create("book", host, "书二号", "", "")
		if next.Denied || next.Task.ID != "t_02" {
			t.Fatalf("book 续号从自己架推导（%s: %s）", h.Name, next.Task.ID)
		}
	})

	t.Run("status flow and work clock", func(t *testing.T) {
		e, reopen := mk(t)
		out := e.Create("demo", host, "流转全套", "", "小明")
		id := out.Task.ID
		for _, st := range []string{tasks.StatusDoing, tasks.StatusDone} {
			if u := e.Update("demo", host, id, tasks.Patch{Status: st, Note: "到 " + st}); u.Denied {
				t.Fatalf("流转到 %s 被拒（%s: %s）", st, h.Name, u.Reason)
			}
		}
		if u := e.Update("demo", host, id, tasks.Patch{Note: "还想改"}); !u.Denied {
			t.Fatalf("已完结任务应拒更新（%s: %+v）", h.Name, u)
		}
		e2 := reopen(t)
		got, ok := e2.GetIn("demo", id)
		if !ok || got.Status != tasks.StatusDone || got.DoingTS == 0 || got.DoneTS == 0 {
			t.Fatalf("工作钟应随重启存活（%s: %+v %v）", h.Name, got, ok)
		}
		if len(got.Log) < 3 {
			t.Fatalf("变更日志应随重启存活（%s: %d 行）", h.Name, len(got.Log))
		}
	})

	t.Run("proposal family", func(t *testing.T) {
		e, reopen := mk(t)
		// 同级指派 → 提名待确认（小明与小红同级，提名小红接收）
		out := e.Create("demo", "小明", "提名单", "", "小红")
		if out.Denied || out.Task.Pending == nil || out.Waiting != "小红" {
			t.Fatalf("同级指派应成提名（%s: %+v）", h.Name, out)
		}
		id := out.Task.ID
		// 旁人不可确认；本人确认后任务落地指派
		if c := e.Confirm("demo", "小黑", id); !c.Denied {
			t.Fatalf("非需确认人应拒（%s: %+v）", h.Name, c)
		}
		if c := e.Confirm("demo", "小红", id); c.Denied || c.Task.Assignee != "小红" || c.Task.Pending != nil {
			t.Fatalf("本人确认应落地指派（%s: %+v）", h.Name, c)
		}
		// 变更提案：同级的任务，同级改要走确认；房主仲裁可直批
		out2 := e.Create("demo", host, "变更单", "", "小明")
		id2 := out2.Task.ID
		if p := e.Update("demo", "小红", id2, tasks.Patch{Assignee: "小红"}); p.Denied || p.Event != "proposal" {
			t.Fatalf("同级改派应成提案（%s: %+v）", h.Name, p)
		}
		if a := e.Arbitrate("demo", id2, true); a.Denied || a.Task.Assignee != "小红" {
			t.Fatalf("房主仲裁应直批（%s: %+v）", h.Name, a)
		}
		// 提名被拒 → 任务取消（assign 提案的死路）
		out3 := e.Create("demo", "小明", "会被拒的单", "", "小红")
		id3 := out3.Task.ID
		if d := e.Decline("demo", "小红", id3); d.Denied || d.Task.Status != tasks.StatusCancelled {
			t.Fatalf("拒绝接收应取消任务（%s: %+v）", h.Name, d)
		}
		// 挂起中的提案跨重启存活（Need/Got 原样）
		out4 := e.Create("demo", "小明", "跨重启的提案", "", "小红")
		id4 := out4.Task.ID
		e2 := reopen(t)
		got, ok := e2.GetIn("demo", id4)
		if !ok || got.Pending == nil || len(got.Pending.Need) != 1 || got.Pending.Need[0] != "小红" {
			t.Fatalf("挂起提案应随重启存活（%s: %+v %v）", h.Name, got, ok)
		}
		if c := e2.Confirm("demo", "小红", id4); c.Denied || c.Task.Assignee != "小红" {
			t.Fatalf("重启后确认照常生效（%s: %+v）", h.Name, c)
		}
		_ = id
	})

	t.Run("rank registry", func(t *testing.T) {
		e, reopen := mk(t)
		if txt := e.SetRank("小明", 5); txt == "" {
			t.Fatalf("SetRank 应回广播行（%s）", h.Name)
		}
		if e.Rank("小明") != 5 || e.Rank("陌生人") != 1 || e.Rank(host) != tasks.RankHost {
			t.Fatalf("等级读取：在册 5/缺省 1/房主 99（%s: %d %d %d）",
				h.Name, e.Rank("小明"), e.Rank("陌生人"), e.Rank(host))
		}
		if !e.RemoveRank("小明") || e.RemoveRank("小明") {
			t.Fatalf("RemoveRank 应报告存在性且幂等（%s）", h.Name)
		}
		e.SetRank("小红", 3)
		e2 := reopen(t)
		if e2.Rank("小红") != 3 || e2.Rank("小明") != 1 {
			t.Fatalf("等级应随重启存活且删除不复活（%s: %d %d）", h.Name, e2.Rank("小红"), e2.Rank("小明"))
		}
		if _, isHost := e2.Ranks()[host]; isHost {
			t.Fatalf("房主 rank 永不落盘（%s）", h.Name)
		}
	})

	t.Run("delete project", func(t *testing.T) {
		e, reopen := mk(t)
		e.Create("demo", host, "甲", "", "")
		e.Create("demo", host, "乙", "", "")
		e.Create("book", host, "别家的", "", "")
		if _, err := e.DeleteProject(tasks.LobbyKey); err == nil {
			t.Fatalf("大厅架应拒整架清除（%s）", h.Name)
		}
		n, err := e.DeleteProject("demo")
		if err != nil || n != 2 {
			t.Fatalf("DeleteProject 清两单（%s: %d %v）", h.Name, n, err)
		}
		if _, ok := e.GetIn("demo", "t_01"); ok {
			t.Fatalf("清除后 demo 架应空（%s）", h.Name)
		}
		if _, ok := e.GetIn("book", "t_01"); !ok {
			t.Fatalf("别架不受波及（%s）", h.Name)
		}
		e2 := reopen(t)
		if got := e2.Create("demo", host, "重建的第一单", "", ""); got.Denied || got.Task.ID != "t_01" {
			t.Fatalf("清架后号段从 t_01 重排（%s: %s）", h.Name, got.Task.ID)
		}
		if got, ok := e2.GetIn("book", "t_01"); !ok {
			t.Fatalf("重启后别架仍在（%s: %+v）", h.Name, got)
		}
	})

	t.Run("renumber", func(t *testing.T) {
		e, reopen := mk(t)
		for _, title := range []string{"甲", "乙", "丙"} {
			if out := e.Create("demo", host, title, "", ""); out.Denied {
				t.Fatalf("建单被拒（%s: %s）", h.Name, out.Reason)
			}
		}
		// 已紧凑的架：无计划、恒等 Apply 是 no-op（两后端同形）。
		// 洞号的改写面由域测试钉（renumber_test.go 的 plant 夹具——
		// 契约套件只有公开面，造不出存量洞，不硬造）；这里钉的是
		// 重排面跨后端的持久语义：Apply 落盘后重启，号段与计数器原样。
		if got := e.RenumberPlan("demo"); len(got) != 0 {
			t.Fatalf("紧凑架应无重排计划（%s: %v）", h.Name, got)
		}
		if err := e.RenumberApply("demo", map[string]string{}); err != nil {
			t.Fatalf("空计划 Apply 应 no-op（%s: %v）", h.Name, err)
		}
		if err := e.RenumberApply("nosuch", nil); err != nil {
			t.Fatalf("空架 Apply 应 no-op（%s: %v）", h.Name, err)
		}
		e2 := reopen(t)
		for i, id := range []string{"t_01", "t_02", "t_03"} {
			if _, ok := e2.GetIn("demo", id); !ok {
				t.Fatalf("重启后 %s 应在架（%s）", id, h.Name)
			}
			_ = i
		}
		if got := e2.Create("demo", host, "重排后续号", "", ""); got.Denied || got.Task.ID != "t_04" {
			t.Fatalf("重启后续号越过最大号（%s: %s）", h.Name, got.Task.ID)
		}
	})
}
