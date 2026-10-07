// isolate_test.go — v2.10/v2.11 分项目隔离契约：每项目各自 t_NN 号段
//（空库一律 t_01 起——v2.11 起大厅空库不再种子 100，v1 号段已被完全
// 重排洗掉）、GetIn 按架解析与裸号 Get 的歧义纪律、信封高水位不回卷、
// 旧单文件 SplitLegacyFile（裸数组、空 key 盖章 default、幂等、
// .migrated 留档——拆出的架由单向桥读走）、跨项目 deps 拒绝。

package tasks_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// openShelf opens an engine over a fresh single database plus its
// restart simulation (reopen over the same bytes).
func openShelf(t *testing.T) (*tasks.Engine, func(t *testing.T) *tasks.Engine, *sqlstore.DB) {
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
	return mk(), func(t *testing.T) *tasks.Engine { return mk() }, db
}

func TestPerProjectTaskNumbering(t *testing.T) {
	e, reopen, _ := openShelf(t)

	// 池任务（Create 不带项目）落大厅架：空库从 t_01 起（v2.11——
	// 种子 100 退役，v1 号段由完全重排洗掉）
	pool := e.Create("", "房主", "池单", "", "")
	if pool.Task.ID != "t_01" {
		t.Fatalf("Niuma_Studio 空库首号应从 t_01 起：得 %s", pool.Task.ID)
	}
	// 计划落地的 book 任务从自己的 t_01 起
	b1 := e.CreatePlanned("房主", tasks.Task{Title: "书一", ProjectKey: "book"})
	if b1.Task.ID != "t_01" {
		t.Fatalf("book 首号应从 t_01 起：得 %s", b1.Task.ID)
	}
	b2 := e.CreatePlanned("房主", tasks.Task{Title: "书二", ProjectKey: "book"})
	if b2.Task.ID != "t_02" {
		t.Fatalf("book 续号：得 %s 想 t_02", b2.Task.ID)
	}
	// 板上直建（Create 带项目）同样铸本架号＋一步挂靠
	b3 := e.Create("book", "房主", "书板建", "", "")
	if b3.Denied {
		t.Fatalf("板建被拒：%s", b3.Reason)
	}
	if b3.Task.ID != "t_03" || b3.Task.ProjectKey != "book" {
		t.Fatalf("板建应铸 book 架 t_03 并挂靠：得 %s %s", b3.Task.ID, b3.Task.ProjectKey)
	}

	// 落盘往返后各架续号互不干扰（板建的 t_03 也在架上，续 t_04）
	e2 := reopen(t)
	next := e2.CreatePlanned("房主", tasks.Task{Title: "书三", ProjectKey: "book"})
	if next.Task.ID != "t_04" {
		t.Fatalf("book 重开续号：得 %s 想 t_04", next.Task.ID)
	}
	lobby := e2.Create("", "房主", "又一池单", "", "")
	if lobby.Task.ID != "t_02" {
		t.Fatalf("Niuma_Studio 重开续号：得 %s 想 t_02", lobby.Task.ID)
	}
}

func TestGetInResolvesShelves(t *testing.T) {
	_, _, db := openShelf(t)
	// 手工种两个架子：default 与 book 各有一个 t_01（同号异架是合法态）
	seed := map[string][]*tasks.Task{
		"default": {{ID: "t_01", Title: "Niuma_Studio 一号", ProjectKey: "default", Status: tasks.StatusTodo}},
		"book":    {{ID: "t_01", Title: "书一号", ProjectKey: "book", Status: tasks.StatusTodo}},
	}
	for key, list := range seed {
		if err := db.Tasks().SaveShelf("", key, &tasks.Shelf{Next: 2, Tasks: list}); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	e2, _, _ := openShelfOver(t, db)

	// 裸号 Get 是宿主面：同号两架＝歧义＝查无（不得静默取第一）
	if _, ok := e2.Get("t_01"); ok {
		t.Fatal("同号两架时裸号 Get 应落空（歧义不是随便挑一个）")
	}
	if got, ok := e2.GetIn("book", "t_01"); !ok || got.Title != "书一号" {
		t.Fatalf("GetIn(book, t_01) 解析错误：%+v %v", got, ok)
	}
	if got, ok := e2.GetIn("default", "t_01"); !ok || got.Title != "Niuma_Studio 一号" {
		t.Fatalf("GetIn(default, t_01) 解析错误：%+v %v", got, ok)
	}
	if got, ok := e2.GetIn("", "t_01"); !ok || got.Title != "Niuma_Studio 一号" {
		t.Fatalf("空串应读作 Niuma_Studio 架：%+v %v", got, ok)
	}
	if _, ok := e2.GetIn("book", "t_99"); ok {
		t.Fatal("book 架上没有 t_99")
	}
}

// openShelfOver opens a fresh engine over an EXISTING database handle
// (the seeded-shape tests' opener).
func openShelfOver(t *testing.T, db *sqlstore.DB) (*tasks.Engine, func(t *testing.T) *tasks.Engine, *sqlstore.DB) {
	t.Helper()
	mk := func() *tasks.Engine {
		e, err := tasks.OpenStoreNS(db.Tasks(), "", "房主")
		if err != nil {
			t.Fatalf("open engine: %v", err)
		}
		e.SetProjectKeys(func() []string { return []string{"default", "book"} })
		return e
	}
	return mk(), func(t *testing.T) *tasks.Engine { return mk() }, db
}

func TestShelfEnvelopeHighWaterNoRollback(t *testing.T) {
	// 手工种一个 next 超过存量最大号的架子：信封高水位说了算
	_, _, db := openShelf(t)
	list := []*tasks.Task{{ID: "t_01", Title: "独苗", ProjectKey: "book", Status: tasks.StatusTodo}}
	if err := db.Tasks().SaveShelf("", "book", &tasks.Shelf{Next: 5, Tasks: list}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, _, _ := openShelfOver(t, db)
	out := e.CreatePlanned("房主", tasks.Task{Title: "新书", ProjectKey: "book"})
	if out.Task.ID != "t_05" {
		t.Fatalf("信封高水位应挡住 t_02–t_04 复用：得 %s 想 t_05", out.Task.ID)
	}
}

func TestSplitLegacyTasksFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tasks")
	old := filepath.Join(root, ".niuma_tasks.json")
	legacy := `[
		{"id":"t_101","title":"Niuma_Studio 单","project_key":"default","status":"done"},
		{"id":"t_102","title":"池单空key","project_key":"","status":"todo"},
		{"id":"t_103","title":"书单","project_key":"book","status":"todo"}
	]`
	if err := os.WriteFile(old, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	moved, err := tasks.SplitLegacyFile(old, dir)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if moved != 3 {
		t.Fatalf("应搬 3 张：得 %d", moved)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("旧文件应已改名离开原位")
	}
	if _, err := os.Stat(old + ".migrated"); err != nil {
		t.Fatalf("旧文件应留档 .migrated：%v", err)
	}

	// 拆出的架由单向桥读进单库，引擎开在库上
	db, err := sqlstore.Open(filepath.Join(t.TempDir(), "studio.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ImportTasksLedger(dir, ""); err != nil {
		t.Fatalf("bridge: %v", err)
	}
	e, _, _ := openShelfOver(t, db)
	// 空 key 行收敛盖章 default（双拼法从此一个架）
	if got, ok := e.GetIn("default", "t_102"); !ok || got.Title != "池单空key" {
		t.Fatalf("空 key 行应归 Niuma_Studio 架：%+v %v", got, ok)
	}
	if got, ok := e.GetIn("book", "t_103"); !ok || got.Title != "书单" {
		t.Fatalf("book 架应照原号：%+v %v", got, ok)
	}
	// 各架续号从本架存量最大号推导（裸数组旧档无信封高水位）
	a := e.Create("", "房主", "Niuma_Studio 新", "", "")
	if a.Task.ID != "t_103" {
		t.Fatalf("Niuma_Studio 续号：得 %s", a.Task.ID)
	}
	b := e.CreatePlanned("房主", tasks.Task{Title: "书新", ProjectKey: "book"})
	if b.Task.ID != "t_104" {
		t.Fatalf("book 续号：得 %s", b.Task.ID)
	}

	// 幂等：旧文件已不在，再跑零移动
	if moved, err := tasks.SplitLegacyFile(old, dir); err != nil || moved != 0 {
		t.Fatalf("重跑应零移动：%d %v", moved, err)
	}
}

func TestCrossProjectDepsRefused(t *testing.T) {
	// 单架内结构校验看不到别架任务：跨项目依赖要么「不存在」要么明拒
	set := []tasks.Task{
		{ID: "t_01", Title: "Niuma_Studio 件", ProjectKey: "default", Status: tasks.StatusTodo},
		{ID: "t_05", Title: "书件", ProjectKey: "book", Status: tasks.StatusTodo, Deps: []string{"t_99"}},
	}
	// book 的 t_05 deps 指向本架没有的 t_99 —— 明确报不存在
	if err := tasks.CheckStructure(set, []string{"default", "book"}); err == nil {
		t.Fatal("悬空依赖应拒绝")
	}

	// 大厅内合法依赖照旧
	ok := []tasks.Task{
		{ID: "t_01", Title: "Niuma_Studio 件", ProjectKey: "default", Status: tasks.StatusTodo},
		{ID: "t_02", Title: "Niuma_Studio 后件", ProjectKey: "default", Status: tasks.StatusTodo, Deps: []string{"t_01"}},
	}
	if err := tasks.CheckStructure(ok, []string{"default", "book"}); err != nil {
		t.Fatalf("同项目依赖不应拒绝：%v", err)
	}
}
