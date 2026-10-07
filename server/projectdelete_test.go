package server

// projectdelete_test.go — 项目删除面（POST /p/{key}/delete）的端到端
// 契约：删除是除名——成员档案（含 ZCode 深清）、全部台账架（架子文件
// 真删，防下次启动复活）、项目文档（编制表不重建——没有项目可重建
// 了）、房间持久档案（历史/三台账/终端/座位快照/回放日）一并消失，
// 项目册行除名（同 key 之后可再立）；active 项目拒绝（409，先归档）、
// 大厅拒绝、口令不符 400 不动任何存储；工作区目录永远原样保留——
// 删除动的是登记表，不是用户的文件。

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/projects"
)

// postLifecycleRaw POSTs a raw body to the lifecycle endpoint (the
// archive leg of the delete tests) and returns the status code.
func postLifecycleRaw(t *testing.T, s *Server, path, body string) int {
	t.Helper()
	resp, err := http.Post(addrHTTP(s)+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestProjectDeleteArchivedSweepsEverything(t *testing.T) {
	r := startPurgeRig(t)
	// 自带已知工作区（用户文件红线的验证锚点）
	workspace := filepath.Join(t.TempDir(), "ws-x")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "用户的文件.txt"), []byte("别动我"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.proj.Create(projects.Project{Key: "proj-x", Name: "被删项目",
		Workspace: workspace}); err != nil {
		t.Fatalf("Create proj-x: %v", err)
	}
	if _, err := r.proj.Activate("proj-x"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if _, err := r.registry.Hub("proj-x"); err != nil {
		t.Fatalf("Hub: %v", err)
	}
	if !r.proj.Exists("keep") {
		if _, err := r.proj.Create(projects.Project{Key: "keep", Name: "邻项目",
			Workspace: t.TempDir()}); err != nil {
			t.Fatalf("Create keep: %v", err)
		}
	}
	if _, err := r.staff.Join("proj-x", "小牛", "后端", "sess-x"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	r.agent.Upsert("小牛", "后端", "", "提示词")
	r.staff.SetAutoRecall("proj-x", false)
	if out := r.engine.Create("proj-x", "房主", "旧任务", "", "小牛"); out.Denied {
		t.Fatalf("Create task: %s", out.Reason)
	}
	if _, err := r.reqs.Create("", "proj-x", "旧需求", "", "房主"); err != nil {
		t.Fatalf("req Create: %v", err)
	}
	if _, err := r.docs.Write("p/proj-x/room-log", "离岗台账", "旧台账一行", "房主"); err != nil {
		t.Fatalf("docs Write: %v", err)
	}
	hub, _ := r.registry.Hub("proj-x")
	hub.SystemRecorded("删除前的旧消息")
	replayDir := filepath.Join(r.root, "replay", "proj-x")
	if err := os.MkdirAll(replayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replayDir, "20260101.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 走生命周期端点归档（退房离编）——删除面的正规前站
	if status := postLifecycleRaw(t, r.s, "/p/proj-x/lifecycle", `{"action":"archive"}`); status != http.StatusOK {
		t.Fatalf("前置失败：归档应 200，得 %d", status)
	}

	if status, rep := postPurgeHTTP(t, r.s, "/p/proj-x/delete", "DELETE"); status != http.StatusOK || !rep.OK {
		t.Fatalf("归档项目删除应 200 ok，得 %d %+v", status, rep)
	}
	// 项目册除名；邻项目毫发无伤
	if r.proj.Exists("proj-x") {
		t.Fatal("删除后项目册不应再有该行")
	}
	if !r.proj.Exists("keep") {
		t.Fatal("邻项目不许被误伤")
	}
	// 人事：编制行、档案、ZCode 深清
	if got := len(r.staff.ListByProject("proj-x")); got != 0 {
		t.Fatalf("编制行应清空，剩 %d 行", got)
	}
	if _, ok := r.agent.Get("小牛"); ok {
		t.Fatal("成员全局档案应已删除")
	}
	if len(*r.purged) == 0 {
		t.Fatal("ZCode 会话深清应已执行")
	}
	// 台账架：内存与架子文件都真删（空文件会在下次启动复活）
	if got := len(r.engine.ListFiltered("", "", "proj-x", "")); got != 0 {
		t.Fatalf("任务应清空，剩 %d 条", got)
	}
	if _, err := os.Stat(filepath.Join(r.root, "tasks", "proj-x.json")); !os.IsNotExist(err) {
		t.Fatal("任务架文件应删除")
	}
	if got := len(r.reqs.ListByProject("", "proj-x")); got != 0 {
		t.Fatalf("需求应清空，剩 %d 条", got)
	}
	// 文档：删干净且不重建编制表（没有项目可重建了）
	if _, err := r.docs.Get("p/proj-x/room-log", 0); err == nil {
		t.Fatal("旧项目文档应已删除")
	}
	if _, err := r.docs.Get("p/proj-x/establishment", 0); err == nil {
		t.Fatal("删除面不许重建编制表")
	}
	// 房间档案：历史 jsonl、座位快照、回放日全消失
	if _, err := os.Stat(r.registry.HistoryPath("proj-x")); !os.IsNotExist(err) {
		t.Fatal("历史文件应删除")
	}
	if _, err := os.Stat(r.registry.SnapshotPath("proj-x")); !os.IsNotExist(err) {
		t.Fatal("座位快照应删除")
	}
	if _, err := os.Stat(replayDir); !os.IsNotExist(err) {
		t.Fatal("回放日目录应删除")
	}
	// 房间经生命周期归档已卸载，注册表不再持有它
	if _, loaded := r.registry.Rooms()["proj-x"]; loaded {
		t.Fatal("已删除项目的房间不应仍在注册表里")
	}
	// 工作区与用户文件永不动：目录和里面的文件原样在磁盘上
	if _, err := os.Stat(filepath.Join(workspace, "用户的文件.txt")); err != nil {
		t.Fatalf("工作区文件不许动: %v", err)
	}
	// 除名后同 key 可再立（key 是 slug 不是墓碑；工作区已随归档释放）
	if _, err := r.proj.Create(projects.Project{Key: "proj-x", Name: "再生",
		Workspace: workspace}); err != nil {
		t.Fatalf("除名后同 key 应可再立: %v", err)
	}
}

func TestProjectDeleteDraftSweepsQuietly(t *testing.T) {
	r := startPurgeRig(t)
	if _, err := r.proj.Create(projects.Project{Key: "draft-y", Name: "丁草稿",
		Workspace: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if status, rep := postPurgeHTTP(t, r.s, "/p/draft-y/delete", "DELETE"); status != http.StatusOK || !rep.OK {
		t.Fatalf("草稿删除应 200 ok，得 %d %+v", status, rep)
	}
	if r.proj.Exists("draft-y") {
		t.Fatal("草稿删除后项目册不应再有该行")
	}
}

func TestProjectDeleteGuards(t *testing.T) {
	r := startPurgeRig(t)
	r.newPurgeProject(t, "proj-a")
	if _, err := r.staff.Join("proj-a", "小石", "测试", "sess-a"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	// 大厅、未知 key、口令、active
	if status, _ := postPurgeHTTP(t, r.s, "/p/default/delete", "DELETE"); status != http.StatusBadRequest {
		t.Fatalf("Niuma_Studio 删除应 400，得 %d", status)
	}
	if status, _ := postPurgeHTTP(t, r.s, "/p/nope/delete", "DELETE"); status != http.StatusNotFound {
		t.Fatalf("未知项目应 404，得 %d", status)
	}
	if status, _ := postPurgeHTTP(t, r.s, "/p/proj-a/delete", "WRONG"); status != http.StatusBadRequest {
		t.Fatalf("口令不符应 400，得 %d", status)
	}
	if !r.proj.Exists("proj-a") {
		t.Fatal("拒绝路径不许除名")
	}
	if status, rep := postPurgeHTTP(t, r.s, "/p/proj-a/delete", "DELETE"); status != http.StatusConflict || rep.OK {
		t.Fatalf("开张中项目删除应 409（先归档），得 %d %+v", status, rep)
	}
	if !r.proj.Exists("proj-a") {
		t.Fatal("active 拒绝路径不许除名")
	}
	// 残局：绕过生命周期归档（房还挂在注册表）——删除面先清活房再扫文件
	if _, err := r.proj.Archive("proj-a"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, loaded := r.registry.Rooms()["proj-a"]; !loaded {
		t.Fatal("前置失败：直调 store 的归档不退房——残房应还挂在注册表")
	}
	if status, rep := postPurgeHTTP(t, r.s, "/p/proj-a/delete", "DELETE"); status != http.StatusOK || !rep.OK {
		t.Fatalf("残局归档项目删除应 200 ok，得 %d %+v", status, rep)
	}
	if _, loaded := r.registry.Rooms()["proj-a"]; loaded {
		t.Fatal("残局删除后房间应已卸载")
	}
	if _, err := os.Stat(r.registry.HistoryPath("proj-a")); !os.IsNotExist(err) {
		t.Fatal("残局删除后历史文件应消失")
	}
	if r.proj.Exists("proj-a") {
		t.Fatal("残局删除后项目册不应再有该行")
	}
}
