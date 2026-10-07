package server

// RenumberIsolatedLedgers（v2.11 号段完全重排）的钉：所有项目（大厅在
// 内）的需求/会议/任务紧凑重排为各自 r_01/m_01/t_01 起，账内引用（待
// 审提案的 req 字段与文案、任务 req/parent/deps、会议 ReqID、纪要键、
// 待审合并提案的 req/tasks）、知识库正文（按文档归属项目选映射）、聊
// 天历史全量改写；守卫退役——文档引用不再是保号理由，跟着换；幂等重
// 跑零移动。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/tasks"
)

func writeIsolateFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// isolateStage bundles the OLD-WORLD stores the sweep renumbers. The
// lobby spent r_01–02 globally and carries a post-isolation hole
// (r_05) plus a seeded-era task (t_100); book holds the global tail
// r_33–35 / m_33 / t_200, a pending plan citing r_33 (prose cites
// r_34/r_35/t_200), a pending merge citing r_33+t_200+r_34, a refiled
// minutes leaf r33-1, a chronicle body citing r_34/t_200, a lobby ops
// chronicle citing r_05/t_100, and one chat-history frame per room.
type isolateStage struct {
	root      string
	docs      *kb.DocsStore
	reqs      requirements.Store
	meets     meeting.Store
	engine    *tasks.Engine
	projStore *projects.Store
	plans     *plan.Engine
	merges    *merge.Engine
}

func (st isolateStage) run() []string {
	return RenumberIsolatedLedgers(st.docs, st.reqs, st.meets, st.engine,
		st.projStore, st.plans, st.merges, filepath.Join(st.root, "history"), "")
}

func seedIsolateStage(t *testing.T) isolateStage {
	t.Helper()
	root := t.TempDir()
	var st isolateStage
	st.root = root
	var err error

	writeIsolateFile(t, filepath.Join(root, "requirements", "default.json"),
		`{"next": 6, "reqs": [
			{"id":"r_01","project_key":"default","title":"Niuma_Studio 一号","status":"split","created_ts":1},
			{"id":"r_02","project_key":"default","title":"Niuma_Studio 二号","status":"open","created_ts":2},
			{"id":"r_05","project_key":"default","title":"Niuma_Studio 新线","status":"open","created_ts":6}
		]}`)
	writeIsolateFile(t, filepath.Join(root, "requirements", "book.json"),
		`{"next": 36, "reqs": [
			{"id":"r_33","project_key":"book","title":"书启动包","status":"open","created_ts":3},
			{"id":"r_34","project_key":"book","title":"书产线","status":"open","created_ts":4},
			{"id":"r_35","project_key":"book","title":"书护栏","status":"open","created_ts":5}
		]}`)

	writeIsolateFile(t, filepath.Join(root, "meetings", "book.json"),
		`{"next": 34, "hist": [
			{"id":"m_33","project_key":"book","req":"r_33","chair":"小苗","status":"done","start_ts":1,"end_ts":2}
		]}`)

	writeIsolateFile(t, filepath.Join(root, "tasks", "default.json"),
		`{"next": 101, "tasks": [
			{"id":"t_100","project_key":"default","title":"Niuma_Studio 池单","status":"todo","created_ts":1}
		]}`)
	writeIsolateFile(t, filepath.Join(root, "tasks", "book.json"),
		`{"next": 201, "tasks": [
			{"id":"t_200","project_key":"book","req":"r_33","title":"书稿一（先于书稿二 t_201 打样）","status":"done","created_ts":1,"updated_ts":2}
		]}`)

	writeIsolateFile(t, filepath.Join(root, "plan", "book.json"),
		`{"next": 2, "pending": {
			"id":"p_01","project_key":"book","req":"r_33","title":"书启动",
			"need":"r_33 定稿后接 r_34/r_35 的产线，t_200 打样",
			"tasks":[{"title":"写设定","desc":"依 r_33 口径，样式照 t_200"}]
		}}`)

	writeIsolateFile(t, filepath.Join(root, "merge", "book.json"),
		`{"next": 2, "pending": {
			"id":"m_01","branch":"feat/shu","into":"main","req":"r_33",
			"tasks":["t_200","r_34"],"submitted_by":"小苗"
		}}`)

	// 种好的 JSON 时代由单向桥读进单库，五域引擎开在库上（转正后
	// 的正装路径：文件架已退役，桥是它的最后一个读者）。
	db, err := sqlstore.Open(filepath.Join(t.TempDir(), "studio.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() }) // Windows 删不动打开中的库——TempDir 清理依赖关库
	if _, err := db.ImportLocal(root); err != nil {
		t.Fatalf("bridge: %v", err)
	}
	if _, err := db.ImportTasksLedger(filepath.Join(root, "tasks"), ""); err != nil {
		t.Fatalf("bridge tasks: %v", err)
	}
	if st.reqs, err = requirements.OpenStore(db.ReqShelves(), ""); err != nil {
		t.Fatalf("requirements: %v", err)
	}
	if st.meets, err = meeting.OpenStore(db.MeetingShelves(), ""); err != nil {
		t.Fatalf("meetings: %v", err)
	}
	if st.engine, err = tasks.OpenStoreNS(db.Tasks(), "", "房主"); err != nil {
		t.Fatalf("tasks: %v", err)
	}
	st.engine.SetProjectKeys(func() []string { return []string{"default", "book"} })
	if st.plans, err = plan.OpenStore(db.PlanSlots(), ""); err != nil {
		t.Fatalf("plans: %v", err)
	}
	if st.merges, err = merge.OpenStore(db.MergeSlots(), ""); err != nil {
		t.Fatalf("merges: %v", err)
	}

	st.projStore, err = projects.Open("")
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if _, err := st.projStore.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	if _, err := st.projStore.Create(projects.Project{Key: "book", Name: "书", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("create book: %v", err)
	}
	if _, err := st.projStore.Activate("book"); err != nil {
		t.Fatalf("activate book: %v", err)
	}

	st.docs, err = kb.OpenDocs(filepath.Join(root, "kb"))
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	if _, err := st.docs.Write("p/book/meetings/r33-1", "内部 · 评审纪要", "书的评审（r_34 立项）", "会议归档"); err != nil {
		t.Fatalf("seed minutes: %v", err)
	}
	if _, err := st.docs.Write("p/book/chronicle", "书志", "书产线（r_34）立项，t_200 打样", "小苗"); err != nil {
		t.Fatalf("seed chronicle: %v", err)
	}
	if _, err := st.docs.Write("ops/chronicle", "工作室志", "Niuma_Studio 新线（r_05）开出，池单 t_100 跟进", "房主"); err != nil {
		t.Fatalf("seed ops chronicle: %v", err)
	}

	writeIsolateFile(t, filepath.Join(root, "history", "book.jsonl"),
		`{"type":"say","from":"小苗","text":"t_200 干完了，等 r_34 评审","ts":100,"seq":1}`+"\n")
	writeIsolateFile(t, filepath.Join(root, "history", "default.jsonl"),
		`{"type":"say","from":"房主","text":"t_100 挂到 r_05 上","ts":100,"seq":1}`+"\n")
	return st
}

func TestRenumberIsolatedLedgersFullSweep(t *testing.T) {
	st := seedIsolateStage(t)

	report := st.run()
	joined := strings.Join(report, "；")
	for _, want := range []string{
		"default: 需求 r_05→r_03；任务 t_100→t_01",
		"book: 需求 r_33→r_01、r_34→r_02、r_35→r_03；会议 m_33→m_01；任务 t_200→t_01",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("重排报告缺 %q：%v", want, report)
		}
	}

	// book 架紧凑 01–03；大厅 01–02 原号（已紧凑），r_05 补洞为 r_03
	for id, want := range map[string]string{"r_01": "书启动包", "r_02": "书产线", "r_03": "书护栏"} {
		if got, ok := st.reqs.GetIn("", "book", id); !ok || got.Title != want {
			t.Fatalf("book %s 应为 %q：%+v %v", id, want, got, ok)
		}
	}
	for _, id := range []string{"r_33", "r_34", "r_35"} {
		if _, ok := st.reqs.GetIn("", "book", id); ok {
			t.Fatalf("book 旧号 %s 应已重排", id)
		}
	}
	if got, ok := st.reqs.GetIn("", "default", "r_03"); !ok || got.Title != "Niuma_Studio 新线" {
		t.Fatalf("Niuma_Studio r_05 应重排为 r_03：%+v %v", got, ok)
	}

	// 任务：两架各排各的 t_01，跨架同号是合法态
	for key, want := range map[string]string{"book": "书稿一（先于书稿二 t_201 打样）", "default": "Niuma_Studio 池单"} {
		got, ok := st.engine.GetIn(key, "t_01")
		if !ok || got.Title != want {
			t.Fatalf("%s t_01 应为 %q：%+v %v", key, want, got, ok)
		}
	}
	if got, _ := st.engine.GetIn("book", "t_01"); got.Req != "r_01" {
		t.Fatalf("任务 req 外键应改写：%q", got.Req)
	}
	// book 任务标题里引用了不存在的 t_201——不在映射里，原样保留
	if !strings.Contains(mustTaskTitle(t, st.engine, "book", "t_01"), "t_201") {
		t.Fatal("映射外的 t_201 不应被动")
	}

	// 待审提案：req 字段与文案 token（含任务号）同步改写
	p := st.plans.Pending("", "book")
	if p == nil {
		t.Fatal("book 待审提案不应被吃掉")
	}
	if p.Req != "r_01" {
		t.Fatalf("提案 req 应改写为 r_01：%q", p.Req)
	}
	if p.Need != "r_01 定稿后接 r_02/r_03 的产线，t_01 打样" {
		t.Fatalf("提案文案 token 应改写：%q", p.Need)
	}
	if p.Tasks[0].Desc != "依 r_01 口径，样式照 t_01" {
		t.Fatalf("提案任务 desc token 应改写：%q", p.Tasks[0].Desc)
	}

	// 待审合并提案：req 与 tasks 引用集跟随
	m := st.merges.Pending("", "book")
	if m == nil {
		t.Fatal("book 待审合并不应被吃掉")
	}
	if m.Req != "r_01" || len(m.Tasks) != 2 || m.Tasks[0] != "t_01" || m.Tasks[1] != "r_02" {
		t.Fatalf("合并提案引用集应改写：req=%q tasks=%v", m.Req, m.Tasks)
	}

	// 会议：号重排＋ReqID 跟随
	if h := st.meets.History("book", 1); len(h) != 1 || h[0].ID != "m_01" || h[0].ReqID != "r_01" {
		t.Fatalf("book 会议应重排 m_01 且 ReqID=r_01：%+v", h)
	}

	// 纪要键 refile＋正文 token 跟上
	min, err := st.docs.Get("p/book/meetings/r01-1", 0)
	if err != nil {
		t.Fatalf("纪要应 refile 到 r01-1: %v", err)
	}
	if min.Body != "书的评审（r_02 立项）" {
		t.Fatalf("纪要正文 token 应改写：%q", min.Body)
	}
	if _, err := st.docs.Get("p/book/meetings/r33-1", 0); err == nil {
		t.Fatal("旧纪要键应已删除")
	}

	// 知识库正文：项目文档用项目映射、大厅文档（ops/）用大厅映射
	cron, err := st.docs.Get("p/book/chronicle", 0)
	if err != nil || cron.Body != "书产线（r_02）立项，t_01 打样" {
		t.Fatalf("书志正文应改写：%q %v", cron.Body, err)
	}
	ops, err := st.docs.Get("ops/chronicle", 0)
	if err != nil || ops.Body != "Niuma_Studio 新线（r_03）开出，池单 t_01 跟进" {
		t.Fatalf("工作室志正文应按 Niuma_Studio 映射改写：%q %v", ops.Body, err)
	}

	// 聊天历史：各房 jsonl 按各房映射换
	bookLine := readIsolateHistory(t, filepath.Join(st.root, "history", "book.jsonl"))
	if !strings.Contains(bookLine, "t_01 干完了，等 r_02 评审") {
		t.Fatalf("book 聊天历史应改写：%s", bookLine)
	}
	lobbyLine := readIsolateHistory(t, filepath.Join(st.root, "history", "default.jsonl"))
	if !strings.Contains(lobbyLine, "t_01 挂到 r_03 上") {
		t.Fatalf("Niuma_Studio 聊天历史应改写：%s", lobbyLine)
	}

	// 幂等：重跑零移动
	if again := st.run(); again != nil {
		t.Fatalf("重跑应零报告：%v", again)
	}
	// 续号从新序列尾起
	fresh, _ := st.reqs.Create("", "book", "书新线", "", "小苗")
	if fresh.ID != "r_04" {
		t.Fatalf("book 续号应从 r_04：%s", fresh.ID)
	}
	nt := st.engine.CreatePlanned("房主", tasks.Task{Title: "书新单", ProjectKey: "book"})
	if nt.Task.ID != "t_02" {
		t.Fatalf("book 任务续号应从 t_02：%s", nt.Task.ID)
	}
}

// TestRenumberRewritesDocBodiesNotGuards:守卫退役——旧号写进文档正文不
// 再是保号理由，正文跟着映射换。
func TestRenumberRewritesDocBodiesNotGuards(t *testing.T) {
	st := seedIsolateStage(t)

	report := st.run()
	if len(report) != 2 {
		t.Fatalf("两间房都应重排：%v", report)
	}
	// v2.10 守卫会把 r_34 保号；现在 r_34 照排 r_02，正文引用跟上
	if got, ok := st.reqs.GetIn("", "book", "r_02"); !ok || got.Title != "书产线" {
		t.Fatalf("被正文引用的 r_34 应照排 r_02：%+v %v", got, ok)
	}
	cron, err := st.docs.Get("p/book/chronicle", 0)
	if err != nil || !strings.Contains(cron.Body, "r_02") || !strings.Contains(cron.Body, "t_01") {
		t.Fatalf("正文应跟随新号：%q %v", cron.Body, err)
	}
}

func readIsolateHistory(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	return string(b)
}

func mustTaskTitle(t *testing.T, e *tasks.Engine, projectKey, id string) string {
	t.Helper()
	got, ok := e.GetIn(projectKey, id)
	if !ok {
		t.Fatalf("%s %s 不在架上", projectKey, id)
	}
	return got.Title
}
