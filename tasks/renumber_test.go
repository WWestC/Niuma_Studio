// renumber_test.go — v2.11 号段完全重排的任务面钉：tasks.RenumberPlan 按架内
// 插入序紧凑发号（已紧凑的保留原位、非 t_NN 手编 id 不动）、
// tasks.RenumberApply 连带改写同架 Parent/Deps/挂起提案 Patch 的 deps 文本与
// prose（标题/描述/进度/日志行）——单遍整词换，值与键同在一张映射里
// 也不链式踩踏、架计数器复位到新最大号+1、幂等重跑零计划。

package tasks_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// openShelf opens an engine over a fresh single database plus its
// restart simulation; seed plants old-id tasks by writing the shelf
// DOCUMENT (Create mints fresh ids — the renumber migration's whole
// point is the stock numbers).
func openRenum(t *testing.T) (*sqlstore.DB, func() *tasks.Engine, func(t *testing.T) *tasks.Engine) {
	t.Helper()
	db, err := sqlstore.Open(filepath.Join(t.TempDir(), "studio.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mk := func() *tasks.Engine {
		e, err := tasks.OpenStoreNS(db.Tasks(), "", "房主")
		if err != nil {
			t.Fatalf("open engine: %v", err)
		}
		e.SetProjectKeys(func() []string { return []string{"default", "book"} })
		return e
	}
	return db, mk, func(t *testing.T) *tasks.Engine { return mk() }
}

func seed(t *testing.T, db *sqlstore.DB, ts ...*tasks.Task) {
	t.Helper()
	shelves, _, err := db.Tasks().OpenShelves("")
	if err != nil {
		t.Fatal(err)
	}
	get := func(key string) *tasks.Shelf {
		if f, ok := shelves[key]; ok {
			return f
		}
		f := &tasks.Shelf{Next: 1}
		shelves[key] = f
		return f
	}
	for _, x := range ts {
		f := get(tasks.SlotKey(x.ProjectKey))
		f.Tasks = append(f.Tasks, x)
		var n int
		if _, err := fmt.Sscanf(x.ID, "t_%d", &n); err == nil && n >= f.Next {
			f.Next = n + 1
		}
	}
	for key, f := range shelves {
		if err := db.Tasks().SaveShelf("", key, f); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTaskRenumberPlanCompactOrder(t *testing.T) {
	// 架上存量：t_01 紧凑、t_05/t_06 有洞、t_99 大号——插入序紧凑后
	// t_05→t_02、t_06→t_03、t_99→t_04
	db, open, _ := openRenum(t)
	seed(t, db,
		&tasks.Task{ID: "t_01", Title: "独苗", ProjectKey: "book", Status: tasks.StatusTodo},
		&tasks.Task{ID: "t_05", Title: "洞后", ProjectKey: "book", Status: tasks.StatusTodo},
		&tasks.Task{ID: "t_06", Title: "再洞", ProjectKey: "book", Status: tasks.StatusTodo},
		&tasks.Task{ID: "t_99", Title: "大号", ProjectKey: "book", Status: tasks.StatusTodo})
	e := open()

	plan := e.RenumberPlan("book")
	want := map[string]string{"t_05": "t_02", "t_06": "t_03", "t_99": "t_04"}
	if len(plan) != len(want) {
		t.Fatalf("计划应为 %v：得 %v", want, plan)
	}
	for old, nn := range want {
		if plan[old] != nn {
			t.Fatalf("计划 %s 应排 %s：得 %v", old, nn, plan)
		}
	}
	// 别架不掺和
	if len(e.RenumberPlan("default")) != 0 {
		t.Fatal("Niuma_Studio 架空空如也，不应有计划")
	}
}

func TestTaskRenumberApplyRewritesReferences(t *testing.T) {
	db, open, reopen := openRenum(t)
	seed(t, db,
		&tasks.Task{ID: "t_01", Title: "根", ProjectKey: "book", Status: tasks.StatusTodo},
		&tasks.Task{ID: "t_03", Title: "先做 t_03 前置", ProjectKey: "book", Status: tasks.StatusTodo,
			Parent: "t_01", Deps: []string{"t_01", "t_04"}, Req: "r_09",
			Pending: &tasks.Proposal{By: "甲", Patch: tasks.Patch{Deps: "t_01,t_04", Note: "等 t_04 收尾"},
				Need: []string{"乙"}, TS: 1},
			Log: []tasks.LogEntry{{TS: 1, By: "甲", Note: "依赖 +t_04"}}},
		&tasks.Task{ID: "t_04", Title: "在改", ProjectKey: "book", Status: tasks.StatusTodo})
	e := open()

	plan := e.RenumberPlan("book") // t_03→t_02、t_04→t_03
	if err := e.RenumberApply("book", plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, ok := e.GetIn("book", "t_02")
	if !ok {
		t.Fatal("t_03 应已重排为 t_02")
	}
	if got.Parent != "t_01" {
		t.Fatalf("Parent 应保持 t_01：得 %s", got.Parent)
	}
	if len(got.Deps) != 2 || got.Deps[0] != "t_01" || got.Deps[1] != "t_03" {
		t.Fatalf("Deps 应换到 t_03：得 %v", got.Deps)
	}
	if got.Title != "先做 t_02 前置" {
		t.Fatalf("标题 prose 应跟上：得 %q", got.Title)
	}
	if got.Req != "r_09" {
		t.Fatalf("需求外键不归任务重排管：得 %s", got.Req)
	}
	if got.Pending.Patch.Deps != "t_01,t_03" {
		t.Fatalf("提案 deps 文本应换：得 %q", got.Pending.Patch.Deps)
	}
	if got.Pending.Patch.Note != "等 t_03 收尾" {
		t.Fatalf("提案 note 应换：得 %q", got.Pending.Patch.Note)
	}
	if got.Log[0].Note != "依赖 +t_03" {
		t.Fatalf("日志行应换：得 %q", got.Log[0].Note)
	}
	if _, ok := e.GetIn("book", "t_03"); !ok {
		t.Fatal("t_04 应已重排为 t_03")
	}

	// 计数器越过新最大号
	next := e.CreatePlanned("房主", tasks.Task{Title: "新单", ProjectKey: "book"})
	if next.Task.ID != "t_04" {
		t.Fatalf("重排后新号应 t_04：得 %s", next.Task.ID)
	}

	// 幂等：紧凑后重跑零计划零变化
	if plan2 := e.RenumberPlan("book"); len(plan2) != 0 {
		t.Fatalf("紧凑架重跑应零计划：得 %v", plan2)
	}

	// 落盘往返：改写后的引用不回卷
	e2 := reopen(t)
	if g, ok := e2.GetIn("book", "t_02"); !ok || g.Parent != "t_01" || g.Deps[1] != "t_03" {
		t.Fatalf("重开后引用应保持改写后口径：%+v %v", g, ok)
	}
}

func TestTaskRenumberSinglePassNoChaining(t *testing.T) {
	// 值与键同在一张映射（t_03→t_02、t_02→t_01）：单遍整词换不得
	// 把刚换出的 t_02 再踩成 t_01
	db, open, _ := openRenum(t)
	seed(t, db,
		&tasks.Task{ID: "t_02", Title: "旧二", ProjectKey: "book", Status: tasks.StatusTodo},
		&tasks.Task{ID: "t_03", Title: "看 t_03 与 t_02", ProjectKey: "book", Status: tasks.StatusTodo})
	e := open()
	plan := e.RenumberPlan("book") // t_02→t_01、t_03→t_02
	if err := e.RenumberApply("book", plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := e.GetIn("book", "t_02")
	if got.Title != "看 t_02 与 t_01" {
		t.Fatalf("单遍整词换应各归各位：得 %q", got.Title)
	}
}

func TestTaskRenumberSkipsHandEditedIDs(t *testing.T) {
	db, open, _ := openRenum(t)
	seed(t, db,
		&tasks.Task{ID: "手工-单", Title: "手编", ProjectKey: "book", Status: tasks.StatusTodo},
		&tasks.Task{ID: "t_07", Title: "正册", ProjectKey: "book", Status: tasks.StatusTodo})
	e := open()
	plan := e.RenumberPlan("book") // 手编 id 不占号：t_07→t_01
	if len(plan) != 1 || plan["t_07"] != "t_01" {
		t.Fatalf("手编 id 不应入册：得 %v", plan)
	}
	if err := e.RenumberApply("book", plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, ok := e.GetIn("book", "手工-单"); !ok {
		t.Fatal("手编 id 原样保留")
	}
}
