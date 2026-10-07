package server

// /kb/tasks/{id}/brief 的钉子：装配面（需求原文/父单/依赖/纪要/点名
// 文档/分支/工作区都上台面）、节预算（超限截断有标注）、号段作用域
// （?project= 换架即 404，与 {id} 同门）。CLI 的 task brief 是这条
// 端点的薄拉取，不另设测。

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// startBriefStudio boots a server with one project (proj-a) holding a
// requirement line (r_1) split into a parent task, two prerequisites
// (one done, one open), the target task, review minutes on the
// project's meetings shelf, a doc the task text names, and a branch
// bound to the requirement — every brief section has its fixture.
func startBriefStudio(t *testing.T) (*Server, string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	projStore, err := projects.Open(filepath.Join(root, "projects.json"))
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.Create(projects.Project{Key: "proj-a", Name: "项目甲",
		Workspace: filepath.Join(root, "ws")}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-a"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if _, err := projStore.Create(projects.Project{Key: "proj-b", Name: "项目乙",
		Workspace: filepath.Join(root, "ws2")}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	eng := tasks.MustOpenMemory("房主")
	eng.SetProjectKeys(func() []string { return []string{"proj-a", "proj-b", chat.LobbyKey} })
	reqs := requirements.OpenMemory()
	docs, err := kb.OpenDocs(filepath.Join(root, "kb"))
	if err != nil {
		t.Fatalf("kb.OpenDocs: %v", err)
	}

	rq, err := reqs.Create("", "proj-a", "登录页改版", "口径见评审；验收：回归全绿、无障碍走查通过", "编排者")
	if err != nil {
		t.Fatalf("reqs.Create: %v", err)
	}
	if _, err := reqs.MarkSplit("", "proj-a", rq.ID); err != nil {
		t.Fatalf("MarkSplit: %v", err)
	}

	// minutes on the project shelf (v2.9 跟房走)
	mkey := kb.ProjectDocKey("proj-a", strings.TrimPrefix(meeting.MinutesKey(rq.ID, 1), "ops/"))
	if _, err := docs.Write(mkey, meeting.MinutesTitle(meeting.Meeting{ReqID: rq.ID, ReqTitle: rq.Title}),
		"结论：拆三包，登录页先行；风险：旧会话态迁移。", "会议归档"); err != nil {
		t.Fatalf("minutes Write: %v", err)
	}
	if _, err := docs.Write("design/login-spec", "登录规格", "布局与文案定稿", "房主"); err != nil {
		t.Fatalf("doc Write: %v", err)
	}
	if _, err := projStore.BindBranch("proj-a", "r1-login", projects.BranchLink{Req: rq.ID}); err != nil {
		t.Fatalf("BindBranch: %v", err)
	}

	mustCreate := func(draft tasks.Task) string {
		t.Helper()
		out := eng.CreatePlanned("房主", draft)
		if out.Denied {
			t.Fatalf("CreatePlanned %q: %s", draft.Title, out.Reason)
		}
		return out.Task.ID
	}
	// 同需求兄弟单的面（Parent 字段当前无写入方——计划提案只产 deps，
	// 树节留待该字段启用）
	mustCreate(tasks.Task{ProjectKey: "proj-a", Title: "登录改版总包", Req: rq.ID, Assignee: "小牛"})
	bID := mustCreate(tasks.Task{ProjectKey: "proj-a", Title: "前置：建分支", Req: rq.ID})
	dID := mustCreate(tasks.Task{ProjectKey: "proj-a", Title: "前置：设计稿"})
	if out := eng.Update("proj-a", "房主", bID, tasks.Patch{Status: tasks.StatusDone}); out.Denied {
		t.Fatalf("Update done: %s", out.Reason)
	}
	cID := mustCreate(tasks.Task{ProjectKey: "proj-a", Title: "登录页实现",
		Desc:     "按 design/login-spec 实现，提交走规范",
		Req:      rq.ID,
		Deps:     []string{bID, dID},
		Assignee: "小马",
		Version:  "v1.2",
	})

	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			Engine:       eng,
			Docs:         docs,
			Requirements: reqs,
			ProjectStore: projStore,
		},
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, cID, rq.ID
}

func getBrief(t *testing.T, s *Server, path string) (string, int) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", s.Port, path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode
}

func TestTaskBriefAssemblesEverySection(t *testing.T) {
	s, cID, reqID := startBriefStudio(t)
	body, code := getBrief(t, s, "/kb/tasks/"+cID+"/brief")
	if code != http.StatusOK {
		t.Fatalf("brief -> %d: %s", code, body)
	}
	for _, want := range []string{
		"【任务简报｜" + cID,
		"项目 proj-a（项目甲）",
		"负责人 小马",
		"登录页实现",
		"按 design/login-spec 实现",
		"来源需求 " + reqID + "「登录页改版」（split·已拆解）",
		"回归全绿",
		"「登录改版总包」[待处理·小牛]", // 同需求兄弟单
		"「前置：建分支」[已完成]",
		"「前置：设计稿」[待处理] ⚠ 前置未完",
		"评审纪要",
		"p/proj-a/meetings/" + strings.ToLower(reqID) + "-1",
		"拆三包，登录页先行",
		"文中点名的黑板文档",
		"design/login-spec（登录规格）",
		"绑定分支：r1-login",
		"工作区 ",
		"按需自取",
		"task show " + cID,
		"版本 v1.2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("brief 缺 %q——装配面不全\n----\n%s", want, body)
		}
	}
	// 纪要只出现在自己的节里：相关文档扫描应跳过它（正常恰好一次，
	// 被双列则两次）
	if n := strings.Count(body, mkeyOf(reqID)); n != 1 {
		t.Errorf("纪要键应只出现一次（相关文档须跳过），got %d:\n%s", n, body)
	}
}

func mkeyOf(reqID string) string { return "p/proj-a/meetings/" + strings.ToLower(reqID) + "-1" }

func TestTaskBriefScopedShelfAndUnknown404(t *testing.T) {
	s, cID, _ := startBriefStudio(t)
	if _, code := getBrief(t, s, "/kb/tasks/"+cID+"/brief?project=proj-a"); code != http.StatusOK {
		t.Errorf("本架读取应 200，got %d", code)
	}
	// 号段分家（v2.10）：proj-b 的架上没有这个号——404 是「此架无此单」
	if _, code := getBrief(t, s, "/kb/tasks/"+cID+"/brief?project=proj-b"); code != http.StatusNotFound {
		t.Errorf("跨架应 404，got %d", code)
	}
	if _, code := getBrief(t, s, "/kb/tasks/t_999/brief"); code != http.StatusNotFound {
		t.Errorf("未知号应 404，got %d", code)
	}
}

func TestTaskBriefClipsOverBudgetFields(t *testing.T) {
	s, _, _ := startBriefStudio(t)
	eng := s.opts.Stores.Engine
	long := strings.Repeat("长", 2000) // MaxDesc 允许的满额描述
	out := eng.CreatePlanned("房主", tasks.Task{ProjectKey: "proj-a", Title: "满额描述单", Desc: long})
	if out.Denied {
		t.Fatalf("CreatePlanned: %s", out.Reason)
	}
	body, code := getBrief(t, s, "/kb/tasks/"+out.Task.ID+"/brief")
	if code != http.StatusOK {
		t.Fatalf("brief -> %d: %s", code, body)
	}
	if !strings.Contains(body, "（截断——原文按尾节指针自取）") {
		t.Errorf("超预算字段应带截断标注:\n%s", body[:200])
	}
	if n := strings.Count(body, "长"); n != 1600 {
		t.Errorf("节预算应截到 1600 rune，got %d", n)
	}
}

// TestTaskBriefArtifactsSection — 工件与提交节：git 工作区里按
// t_NN/r_NN 反查的提交与工作区状态上台面（工件即状态——续做的人先
// 看已经落了什么）。非仓库工作区整节静默（startBriefStudio 的其余
// 用例即证：正文里没有「工件与提交」字样）。
func TestTaskBriefArtifactsSection(t *testing.T) {
	needGitTool(t)
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	projStore, err := projects.Open(filepath.Join(root, "projects.json"))
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.Create(projects.Project{Key: "proj-a", Name: "项目甲", Workspace: ws}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-a"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	eng := tasks.MustOpenMemory("房主")
	eng.SetProjectKeys(func() []string { return []string{"proj-a", chat.LobbyKey} })
	reqs := requirements.OpenMemory()
	rq, err := reqs.Create("", "proj-a", "登录页改版", "验收：回归全绿", "编排者")
	if err != nil {
		t.Fatalf("reqs.Create: %v", err)
	}
	out := eng.CreatePlanned("房主", tasks.Task{ProjectKey: "proj-a", Title: "登录页实现", Req: rq.ID})
	if out.Denied {
		t.Fatalf("CreatePlanned: %s", out.Reason)
	}
	cID := out.Task.ID

	// 工作区成仓库：一笔提交带任务号、一笔带需求号，再留一个未跟踪文件
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	gitRun(t, ws, "init", "-b", "main")
	gitRun(t, ws, "config", "user.email", "test@niuma.local")
	gitRun(t, ws, "config", "user.name", "测试牛马")
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("one\n"), 0o644)
	gitRun(t, ws, "add", ".")
	gitRun(t, ws, "commit", "-m", "feat: 登录页骨架\n\n任务: "+cID)
	os.WriteFile(filepath.Join(ws, "b.txt"), []byte("two\n"), 0o644)
	gitRun(t, ws, "add", ".")
	gitRun(t, ws, "commit", "-m", "fix: 顺带修 "+rq.ID+" 的细节")
	os.WriteFile(filepath.Join(ws, "dirty.txt"), []byte("wip\n"), 0o644) // 未跟踪——脏净如实报

	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			Engine:       eng,
			Requirements: reqs,
			ProjectStore: projStore,
		},
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)

	body, code := getBrief(t, s, "/kb/tasks/"+cID+"/brief")
	if code != http.StatusOK {
		t.Fatalf("brief -> %d: %s", code, body)
	}
	for _, want := range []string{
		"工件与提交：",
		"工作区分支 main——有未提交改动（暂存 0·修改 0·未跟踪 1）",
		"登录页骨架", // 按任务号反查的提交
		"的细节",   // 按需求号反查的提交
	} {
		if !strings.Contains(body, want) {
			t.Errorf("工件节缺 %q:\n%s", want, body)
		}
	}
}
