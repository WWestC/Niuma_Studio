// requirements_test.go — 台账契约：完成时刻（ClosedTS）只由 MarkClosed
// 盖一次——创建/拆解不碰它，二次关闭拒绝且不重盖；落盘往返带戳（JSON
// 键 closed_ts）。删除契约：仅 open 可删（split 任务树/closed 留档不随
// 删）、id 高水位落盘不回卷。分项目隔离契约：每项目各自 r_NN 从 01
// 起、GetIn 按架解析、FindAll 跨架检索、旧单文件档 SplitLegacyFile 拆
// 架（裸数组/信封双形状、空 key 归大厅、幂等、留档 .migrated）——拆
// 出的架由单向桥（sqlstore.ImportLocal）读走，那是一次性导入的最后
// 读方。排序语义在前端（web/project/reqs.test.js），这里钉住数据源头
// 的不变量。

package requirements_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/storetest"
)

func mkStore(t *testing.T) *requirements.Engine {
	t.Helper()
	return requirements.OpenMemory()
}

// mkPersist opens the engine over a fresh single database plus its
// restart simulation.
func mkPersist(t *testing.T) (*requirements.Engine, func(t *testing.T) *requirements.Engine) {
	t.Helper()
	open, reopen := storetest.ReqSQLiteBacking(t)
	s := open("").(*requirements.Engine)
	return s, func(t *testing.T) *requirements.Engine { return reopen("")(t).(*requirements.Engine) }
}

func TestClosedTSContract(t *testing.T) {
	s := mkStore(t)
	a, err := s.Create("", "demo", "需求甲", "", "小牛")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.ClosedTS != 0 {
		t.Fatalf("创建不应盖完成时刻：%d", a.ClosedTS)
	}
	b, err := s.MarkSplit("", "demo", a.ID)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if b.ClosedTS != 0 {
		t.Fatalf("拆解不应盖完成时刻：%d", b.ClosedTS)
	}
	c, err := s.MarkClosed("", "demo", a.ID)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if c.ClosedTS == 0 {
		t.Fatal("关闭应盖完成时刻")
	}
	if _, err := s.MarkClosed("", "demo", a.ID); err == nil {
		t.Fatal("二次关闭应拒绝")
	}
}

func TestClosedTSPersistsThroughFile(t *testing.T) {
	s, reopen := mkPersist(t)
	if _, err := s.Create("", "demo", "需求乙", "", "小牛"); err != nil {
		t.Fatalf("create: %v", err)
	}
	closed, err := s.MarkClosed("", "demo", "r_01")
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	// 重开同一落盘：closed_ts 随文档往返存活（前端完成档排序的源头）
	s2 := reopen(t)
	got, ok := s2.GetIn("", "demo", "r_01")
	if !ok {
		t.Fatal("重开后 r_01 不见了")
	}
	if got.Status != requirements.StatusClosed || got.ClosedTS != closed.ClosedTS {
		t.Fatalf("落盘往返丢完成时刻：status=%s closed=%d want=%d",
			got.Status, got.ClosedTS, closed.ClosedTS)
	}
}

func TestDeleteOpenOnly(t *testing.T) {
	s := mkStore(t)
	mistake, err := s.Create("", "demo", "误录条目", "", "小牛")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	split, err := s.Create("", "demo", "已拆需求", "", "小牛")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.MarkSplit("", "demo", split.ID); err != nil {
		t.Fatalf("split: %v", err)
	}
	closed, err := s.Create("", "demo", "已关需求", "", "小牛")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.MarkClosed("", "demo", closed.ID); err != nil {
		t.Fatalf("close: %v", err)
	}

	// split 拒绝：任务树之母不随删
	if _, err := s.Delete("", "demo", split.ID); err == nil {
		t.Fatal("split 需求删除应拒绝")
	}
	// closed 拒绝：终局留档不随删
	if _, err := s.Delete("", "demo", closed.ID); err == nil {
		t.Fatal("closed 需求删除应拒绝")
	}
	// 拒绝路径不动台账
	if got := len(s.List("")); got != 3 {
		t.Fatalf("拒绝删除不应动台账：len=%d", got)
	}

	// open 可删：返回被删快照，台账少一条，GetIn 落空
	rev := s.Rev("")
	gone, err := s.Delete("", "demo", mistake.ID)
	if err != nil {
		t.Fatalf("delete open: %v", err)
	}
	if gone.ID != mistake.ID || gone.Title != "误录条目" {
		t.Fatalf("应返回被删快照：%+v", gone)
	}
	if _, ok := s.GetIn("", "demo", mistake.ID); ok {
		t.Fatal("删除后 GetIn 应落空")
	}
	rest := s.List("")
	if len(rest) != 2 || rest[0].ID != split.ID || rest[1].ID != closed.ID {
		t.Fatalf("删除后台账不符：%+v", rest)
	}
	if s.Rev("") != rev+1 {
		t.Fatalf("删除应推进 rev：%d → %d", rev, s.Rev(""))
	}

	// 不存在的 id：报错不 panic
	if _, err := s.Delete("", "demo", "r_99"); err == nil {
		t.Fatal("删不存在的需求应报错")
	}
}

func TestDeleteKeepsIDHighWaterThroughFile(t *testing.T) {
	s, reopen := mkPersist(t)
	if _, err := s.Create("", "demo", "需求一", "", "小牛"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("", "demo", "需求二", "", "小牛"); err != nil {
		t.Fatal(err)
	}
	// 删掉 r_02——它的号已被消费，重启不得重发
	if _, err := s.Delete("", "demo", "r_02"); err != nil {
		t.Fatalf("delete r_02: %v", err)
	}
	s2 := reopen(t)
	if got := s2.ListByProject("", "demo"); len(got) != 1 || got[0].ID != "r_01" {
		t.Fatalf("重开后应只剩 r_01：%+v", got)
	}
	next, err := s2.Create("", "demo", "需求三", "", "小牛")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if next.ID != "r_03" {
		t.Fatalf("高水位应过重启（r_03）：%s", next.ID)
	}
}

func TestPerProjectCountersAndResolution(t *testing.T) {
	s, reopen := mkPersist(t)
	// 两个项目各自从 r_01 起——book 的 r_01 与大厅的 r_01 是两条需求
	lobbyA, _ := s.Create("", "default", "Niuma_Studio 一号", "", "房主")
	lobbyB, _ := s.Create("", "default", "Niuma_Studio 二号", "", "房主")
	bookA, _ := s.Create("", "book", "书一号", "", "小苗")
	if lobbyA.ID != "r_01" || lobbyB.ID != "r_02" {
		t.Fatalf("Niuma_Studio 各自计数：得 %s %s", lobbyA.ID, lobbyB.ID)
	}
	if bookA.ID != "r_01" {
		t.Fatalf("book 应有自己的 r_01：得 %s", bookA.ID)
	}

	// GetIn 按架解析：同号不同架互不串门
	if got, ok := s.GetIn("", "book", "r_01"); !ok || got.Title != "书一号" {
		t.Fatalf("GetIn(book) 解析错误：%+v %v", got, ok)
	}
	if got, ok := s.GetIn("", "default", "r_01"); !ok || got.Title != "Niuma_Studio 一号" {
		t.Fatalf("GetIn(default) 解析错误：%+v %v", got, ok)
	}
	if _, ok := s.GetIn("", "book", "r_02"); ok {
		t.Fatal("book 架上没有 r_02，GetIn 应落空")
	}

	// FindAll 跨架检索：r_01 命中两架，r_02 只大厅
	if got := s.FindAll("", "r_01"); len(got) != 2 {
		t.Fatalf("FindAll(r_01) 应命中两架：得 %d", len(got))
	}
	if got := s.FindAll("", "r_02"); len(got) != 1 || got[0].ProjectKey != "default" {
		t.Fatalf("FindAll(r_02) 应只 Niuma_Studio：%+v", got)
	}

	// 状态翻转走架定位：book 的 r_01 拆解不碰大厅的 r_01
	if _, err := s.MarkSplit("", "book", "r_01"); err != nil {
		t.Fatalf("split book r_01: %v", err)
	}
	if got, _ := s.GetIn("", "default", "r_01"); got.Status != requirements.StatusOpen {
		t.Fatalf("Niuma_Studio r_01 不应被 book 的拆解波及：%s", got.Status)
	}

	// 落盘往返后同号两架仍各自解析
	s2 := reopen(t)
	if got, ok := s2.GetIn("", "book", "r_01"); !ok || got.Status != requirements.StatusSplit {
		t.Fatalf("重开后 book r_01 应为 split：%+v %v", got, ok)
	}
	if got, ok := s2.GetIn("", "default", "r_01"); !ok || got.Status != requirements.StatusOpen {
		t.Fatalf("重开后 Niuma_Studio r_01 应为 open：%+v %v", got, ok)
	}
	next, _ := s2.Create("", "book", "书二号", "", "小苗")
	if next.ID != "r_02" {
		t.Fatalf("book 续号应从自己架推导：得 %s 想 r_02", next.ID)
	}
}

// bridgeOverSplit runs the one-time bridge (ImportLocal) over a split
// rack and opens the engine on the imported shelves.
func bridgeOverSplit(t *testing.T, root string) *requirements.Engine {
	t.Helper()
	db, err := sqlstore.Open(filepath.Join(t.TempDir(), "studio.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() }) // Windows 删不动打开中的库——TempDir 清理依赖关库
	if _, err := db.ImportLocal(root); err != nil {
		t.Fatalf("bridge: %v", err)
	}
	s, err := requirements.OpenStore(db.ReqShelves(), "")
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	return s
}

func TestSplitLegacyFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "requirements")
	old := filepath.Join(root, "requirements.json")

	// 信封形状的旧单文件档：两个项目混住，含一个空 key 行（归大厅）
	legacy := `{"next": 5, "reqs": [
		{"id":"r_01","project_key":"default","title":"Niuma_Studio 旧档","status":"split","created_ts":1},
		{"id":"r_03","project_key":"","title":"空 key 行","status":"open","created_ts":2},
		{"id":"r_04","project_key":"book","title":"书旧档","status":"open","created_ts":3}
	]}`
	if err := os.WriteFile(old, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	moved, err := requirements.SplitLegacyFile(old, dir)
	if err != nil {
		t.Fatalf("split legacy: %v", err)
	}
	if moved != 3 {
		t.Fatalf("应搬 3 条：得 %d", moved)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("旧文件应已改名离开原位")
	}
	if _, err := os.Stat(old + ".migrated"); err != nil {
		t.Fatalf("旧文件应留档 .migrated：%v", err)
	}

	s := bridgeOverSplit(t, root)
	if got, ok := s.GetIn("", "default", "r_03"); !ok || got.Title != "空 key 行" {
		t.Fatalf("空 key 行应归 Niuma_Studio 架：%+v %v", got, ok)
	}
	if got, ok := s.GetIn("", "book", "r_04"); !ok || got.Title != "书旧档" {
		t.Fatalf("book 架应照原号：%+v %v", got, ok)
	}
	// 各架高水位从本架最大号推导：大厅 r_05、book r_05
	a, _ := s.Create("", "default", "Niuma_Studio 新", "", "房主")
	b, _ := s.Create("", "book", "书新", "", "小苗")
	if a.ID != "r_05" || b.ID != "r_05" {
		t.Fatalf("各架续号：得 %s %s 想 r_05 r_05", a.ID, b.ID)
	}

	// 幂等：旧文件已不在，再跑零移动；已有架文件不被覆写
	if moved, err := requirements.SplitLegacyFile(old, dir); err != nil || moved != 0 {
		t.Fatalf("重跑应零移动：%d %v", moved, err)
	}
	if got, _ := s.GetIn("", "default", "r_01"); got.Title != "Niuma_Studio 旧档" {
		t.Fatalf("重跑不应覆写已有架：%+v", got)
	}
}

func TestSplitLegacyFileBareArray(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "requirements")
	old := filepath.Join(root, "requirements.json")
	legacy := `[{"id":"r_07","project_key":"demo","title":"旧档","status":"closed","created_ts":1,"closed_ts":2}]`
	if err := os.WriteFile(old, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if _, err := requirements.SplitLegacyFile(old, dir); err != nil {
		t.Fatalf("裸数组旧档应照拆: %v", err)
	}
	s := bridgeOverSplit(t, root)
	if got, ok := s.GetIn("", "demo", "r_07"); !ok || got.Status != requirements.StatusClosed {
		t.Fatalf("旧档内容不符：%+v %v", got, ok)
	}
	// 旧档没有高水位：next 仍由本架最大 r_NN 推导（r_08 接续）
	fresh, err := s.Create("", "demo", "新需求", "", "小牛")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if fresh.ID != "r_08" {
		t.Fatalf("旧档续号应接最大号：得 %s 想 r_08", fresh.ID)
	}
}
