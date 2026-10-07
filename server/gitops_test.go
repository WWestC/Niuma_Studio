package server

// gitops_test.go — 版本管理 HTTP 面的钉子：摘要/分支/历史/需求反查的
// 读脸、切分支的护栏（脏树 409）与两腿通知（recorded [版本] 行 +
// Options.GitBranchSwitched tap）、建分支＋自动绑定、绑定校验、钩子
// 装卸与外人钩子位的 409。夹具是真 git 仓库挂在大厅项目（key=default）
// 的 workspace 上，观察者直接听大厅。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

type gitSwitchEvent struct{ key, from, to string }

// needGitTool skips when git is absent.
func needGitTool(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机无 git，跳过版本管理 HTTP 面用例")
	}
}

// gitRun runs one git command in dir, failing the test on error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v（in %s）: %v\n%s", args, dir, err, out.String())
	}
	return out.String()
}

// newGitRepo builds a fixture repo: main 带两笔合规提交，外加版本 v1
// 供绑定用例（版本挂到项目上，不进 git）。
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@niuma.local")
	gitRun(t, dir, "config", "user.name", "测试牛马")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "feat: 起步 [t_100]")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("two\n"), 0o644)
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "feat: 第二步\n\n任务: t_101")
	gitRun(t, dir, "branch", "feat-x")
	return dir
}

// startGitStudio boots a studio whose lobby project sits on a fixture
// repo, with the GitBranchSwitched tap fed into a channel.
func startGitStudio(t *testing.T) (*Server, *chat.Client, chan gitSwitchEvent) {
	t.Helper()
	needGitTool(t)
	repo := newGitRepo(t)
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	store, err := projects.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureLobby(repo); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	if _, err := store.Update("default", projects.Patch{Versions: []projects.Version{{Name: "v1"}}}); err != nil {
		t.Fatalf("补版本： %v", err)
	}
	events := make(chan gitSwitchEvent, 4)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: store,
		},
		LocalName: "房主",
		Fleet: FleetFuncs{GitBranchSwitchedFn: func(key, from, to string) error {
			events <- gitSwitchEvent{key, from, to}
			return nil
		}}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, lobby.AttachObserver(chat.LobbyKey, false, 0), events
}

// gitGetJSON fetches and decodes a git GET face.
func gitGetJSON(t *testing.T, s *Server, path string, out any) int {
	t.Helper()
	resp, err := http.Get(addrHTTP(s) + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// gitPostJSON posts and decodes; returns status + decoded body + error text.
func gitPostJSON(t *testing.T, s *Server, path string, body any, out any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(addrHTTP(s)+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var raw map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	if out != nil {
		if b2, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b2, out)
		}
	}
	if e, ok := raw["error"].(string); ok {
		return resp.StatusCode, e
	}
	return resp.StatusCode, ""
}

// drainGitFrames collects the observer's frames for one settling beat.
func drainGitFrames(c *chat.Client) []chat.Message {
	var got []chat.Message
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case m := <-c.Receive():
			got = append(got, m)
		case <-deadline:
			return got
		}
	}
}

func TestGitSummaryAndBranches(t *testing.T) {
	s, _, _ := startGitStudio(t)
	var sum struct {
		Repo *struct {
			Root     string `json:"root"`
			Worktree bool   `json:"worktree"`
		} `json:"repo"`
		Status *struct {
			Branch string `json:"branch"`
		} `json:"status"`
		Compliance *struct {
			Checked int `json:"checked"`
			Bad     int `json:"bad"`
		} `json:"compliance"`
	}
	if code := gitGetJSON(t, s, "/p/default/git", &sum); code != http.StatusOK {
		t.Fatalf("摘要应 200，得 %d", code)
	}
	if sum.Repo == nil || sum.Status == nil || sum.Status.Branch != "main" {
		t.Fatalf("摘要不合预期: %+v", sum)
	}
	if sum.Compliance == nil || sum.Compliance.Checked != 2 || sum.Compliance.Bad != 0 {
		t.Fatalf("夹具两笔合规提交，统计不合预期: %+v", sum.Compliance)
	}

	// 补一笔不合规提交（无 type 无号）→ bad=1。
	repo := sum.Repo.Root
	os.WriteFile(filepath.Join(repo, "c.txt"), []byte("x\n"), 0o644)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "随手一提交")
	gitGetJSON(t, s, "/p/default/git", &sum)
	if sum.Compliance.Bad != 1 {
		t.Fatalf("补一笔脏提交后 bad 应为 1: %+v", sum.Compliance)
	}

	var bs struct {
		Current  string `json:"current"`
		Branches []struct {
			Name    string `json:"name"`
			Current bool   `json:"current"`
			Subject string `json:"subject"`
		} `json:"branches"`
	}
	if code := gitGetJSON(t, s, "/p/default/git/branches", &bs); code != http.StatusOK || bs.Current != "main" {
		t.Fatalf("分支脸不合预期: %d %+v", code, bs)
	}
	if len(bs.Branches) != 2 { // main + feat-x（origin 还没配）
		t.Fatalf("应有 main/feat-x 两枝: %+v", bs.Branches)
	}

	// 提交历史脸：逐条 verdict，脏那笔 ok=false。
	var log struct {
		Commits []struct {
			Subject string `json:"subject"`
			Verdict struct {
				OK bool `json:"ok"`
			} `json:"verdict"`
		} `json:"commits"`
	}
	if code := gitGetJSON(t, s, "/p/default/git/log?n=5", &log); code != http.StatusOK || len(log.Commits) != 3 {
		t.Fatalf("历史脸应 3 条: %d %+v", code, log)
	}
	if log.Commits[0].Verdict.OK || log.Commits[1].Verdict.OK == false {
		t.Fatalf("最新一笔应不合规、次新应合规: %+v", log.Commits)
	}
}

func TestGitGraphFace(t *testing.T) {
	s, _, _ := startGitStudio(t)

	// 夹具上长出真分叉：feat-x 前进一笔，再 --no-ff 并回 main——
	// graph 面该有 4 笔（merge、分叉笔、两笔起步），merge 带双父。
	var sum struct {
		Repo *struct {
			Root string `json:"root"`
		} `json:"repo"`
	}
	if code := gitGetJSON(t, s, "/p/default/git", &sum); code != http.StatusOK || sum.Repo == nil {
		t.Fatalf("摘要应 200: %d %+v", code, sum)
	}
	repo := sum.Repo.Root
	gitRun(t, repo, "switch", "feat-x")
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("fork\n"), 0o644)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "feat: 分叉一笔 [t_102]")
	gitRun(t, repo, "switch", "main")
	gitRun(t, repo, "merge", "--no-ff", "feat-x", "-m", "chore: 并回 feat-x\n\n需求: r_10")

	var g struct {
		Current string `json:"current"`
		Commits []struct {
			SHA        string   `json:"sha"`
			Subject    string   `json:"subject"`
			Parents    int      `json:"parents"`
			ParentSHAs []string `json:"parent_shas"`
		} `json:"commits"`
	}
	if code := gitGetJSON(t, s, "/p/default/git/graph?n=50", &g); code != http.StatusOK {
		t.Fatalf("graph 脸应 200: %d", code)
	}
	if g.Current != "main" {
		t.Fatalf("current 应 main: %+v", g.Current)
	}
	if len(g.Commits) != 4 {
		t.Fatalf("分叉合并后应 4 笔: %+v", g.Commits)
	}
	top := g.Commits[0]
	if top.Parents != 2 || len(top.ParentSHAs) != 2 {
		t.Fatalf("最新一笔是 merge，应双父: %+v", top)
	}
	if top.ParentSHAs[0] == "" {
		t.Fatalf("父 SHA 不该为空: %+v", top)
	}
	// 拓扑序契约：每笔的窗口内父必须排在它后面（前端泳道布局的地基）
	idx := map[string]int{}
	for i, c := range g.Commits {
		idx[c.SHA] = i
	}
	for i, c := range g.Commits {
		for _, p := range c.ParentSHAs {
			if j, ok := idx[p]; ok && j <= i {
				t.Fatalf("父 %s（位 %d）应排在子（位 %d）后面：%+v", p[:7], j, i, c)
			}
		}
	}
	// 中段那笔分叉提交单父、指向 c2
	fork := g.Commits[1]
	if fork.Parents != 1 || len(fork.ParentSHAs) != 1 {
		t.Fatalf("分叉笔应单父: %+v", fork)
	}
	if idx[fork.ParentSHAs[0]] != 2 {
		t.Fatalf("分叉笔的父应是第三行（c2）: %+v", fork)
	}

	// 单支历史面不带回 parent_shas（省流量口径，同一结构体省列）
	var logRaw map[string]any
	gitGetJSON(t, s, "/p/default/git/log?n=5", &logRaw)
	rows := logRaw["commits"].([]any)
	if _, has := rows[0].(map[string]any)["parent_shas"]; has {
		t.Fatalf("log 面不该带 parent_shas: %+v", rows[0])
	}
}

func TestGitCheckoutGuardsAndNotify(t *testing.T) {
	s, obs, events := startGitStudio(t)

	// 干净树切换：200 + 房间 [版本] 行 + tap 带 from/to。
	var out struct{ From, To string }
	code, errMsg := gitPostJSON(t, s, "/p/default/git/checkout", map[string]any{"branch": "feat-x", "by": "房主"}, &out)
	if code != http.StatusOK || errMsg != "" || out.From != "main" || out.To != "feat-x" {
		t.Fatalf("切分支应成功: %d %q %+v", code, errMsg, out)
	}
	select {
	case ev := <-events:
		if ev.key != "default" || ev.from != "main" || ev.to != "feat-x" {
			t.Fatalf("tap 参数不合预期: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("GitBranchSwitched 未被 tap")
	}
	found := false
	for _, m := range drainGitFrames(obs) {
		if bytes.Contains([]byte(m.Text), []byte("切到 feat-x")) {
			found = true
		}
	}
	if !found {
		t.Fatal("房间应落 recorded [版本] 行")
	}

	// 脏树：409 拒绝，错误文案带计数。
	var repoMu struct{ Repo *struct{ Root string } }
	gitGetJSON(t, s, "/p/default/git", &repoMu)
	os.WriteFile(filepath.Join(repoMu.Repo.Root, "a.txt"), []byte("dirty\n"), 0o644)
	code, errMsg = gitPostJSON(t, s, "/p/default/git/checkout", map[string]any{"branch": "main"}, nil)
	if code != http.StatusConflict || !bytes.Contains([]byte(errMsg), []byte("1 处未提交改动")) {
		t.Fatalf("脏树应 409 且带计数: %d %q", code, errMsg)
	}

	// 建分支＋自动绑定：枝落地、link 落账、房间再落一行。
	var nb struct {
		Branch string `json:"branch"`
		Link   *struct {
			Req     string `json:"req"`
			Version string `json:"version"`
		} `json:"link"`
	}
	code, errMsg = gitPostJSON(t, s, "/p/default/git/branch",
		map[string]any{"name": "r12-login", "req": "r_12", "version": "v1", "by": "房主"}, &nb)
	if code != http.StatusOK || errMsg != "" || nb.Link == nil || nb.Link.Req != "r_12" || nb.Link.Version != "v1" {
		t.Fatalf("建分支＋绑定应一次成: %d %q %+v", code, errMsg, nb)
	}
	select {
	case <-events:
	case <-time.After(time.Second):
		t.Fatal("建分支也算切换，应 tap")
	}
}

func TestGitBindAndPolicyFaces(t *testing.T) {
	s, _, _ := startGitStudio(t)

	// 绑不存在的分支：400。
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/bind",
		map[string]any{"branch": "ghost", "req": "r_1"}, nil); code != http.StatusBadRequest ||
		!bytes.Contains([]byte(errMsg), []byte("没有")) {
		t.Fatalf("幽灵分支应 400: %d %q", code, errMsg)
	}
	// 绑真实分支：link 回带；需求号形状错的拒绝。
	var out struct {
		Link *struct {
			Req     string `json:"req"`
			Version string `json:"version"`
		} `json:"link"`
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/bind",
		map[string]any{"branch": "feat-x", "req": "r_12", "version": "v1", "by": "房主"}, &out); code != http.StatusOK || out.Link == nil {
		t.Fatalf("绑定应成功: %d %q %+v", code, errMsg, out)
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/bind",
		map[string]any{"branch": "feat-x", "version": "v9"}, nil); code != http.StatusBadRequest ||
		!bytes.Contains([]byte(errMsg), []byte("版本列表")) {
		t.Fatalf("未知版本应 400: %d %q", code, errMsg)
	}
	// 解绑幂等。
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/unbind", map[string]any{"branch": "feat-x"}, nil); code != http.StatusOK {
		t.Fatalf("解绑应成功: %d %q", code, errMsg)
	}
	if code, _ := gitPostJSON(t, s, "/p/default/git/unbind", map[string]any{"branch": "feat-x"}, nil); code != http.StatusOK {
		t.Fatalf("重复解绑应幂等: %d", code)
	}

	// 策略：设 task 档、读回、重置回默认。
	var pol struct {
		Policy struct {
			RequireRef string `json:"require_ref"`
		} `json:"policy"`
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/policy",
		map[string]any{"require_ref": "task"}, &pol); code != http.StatusOK || pol.Policy.RequireRef != "task" {
		t.Fatalf("策略应落账: %d %q %+v", code, errMsg, pol)
	}
	var sum struct {
		Policy struct {
			RequireRef string `json:"require_ref"`
		} `json:"policy"`
	}
	gitGetJSON(t, s, "/p/default/git", &sum)
	if sum.Policy.RequireRef != "task" {
		t.Fatalf("摘要应回显生效档: %+v", sum.Policy)
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/policy",
		map[string]any{"reset": true}, &pol); code != http.StatusOK || pol.Policy.RequireRef != "either" {
		t.Fatalf("重置应回默认档: %d %q %+v", code, errMsg, pol)
	}
}

func TestGitHookFaces(t *testing.T) {
	s, _, _ := startGitStudio(t)
	var sum struct {
		Repo          *struct{ Root string } `json:"repo"`
		HookPath      string                 `json:"hook_path"`
		HookInstalled bool                   `json:"hook_installed"`
	}
	gitGetJSON(t, s, "/p/default/git", &sum)
	hookPath := filepath.Join(sum.Repo.Root, ".git", "hooks", "commit-msg")

	var out struct {
		Installed bool   `json:"installed"`
		Path      string `json:"path"`
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/hook", map[string]any{"action": "install"}, &out); code != http.StatusOK || !out.Installed {
		t.Fatalf("装钩子应成功: %d %q %+v", code, errMsg, out)
	}
	if b, err := os.ReadFile(hookPath); err != nil || !bytes.Contains(b, []byte("Niuma Studio commit-msg")) {
		t.Fatalf("钩子应落盘且带标记: %v", err)
	}
	gitGetJSON(t, s, "/p/default/git", &sum)
	if !sum.HookInstalled {
		t.Fatal("摘要应回显钩子已装")
	}

	// 外人的钩子位：不覆盖、不代删。
	os.WriteFile(hookPath, []byte("#!/bin/sh\necho custom\n"), 0o755)
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/hook", map[string]any{"action": "install"}, nil); code != http.StatusConflict {
		t.Fatalf("外人钩子位装应 409: %d %q", code, errMsg)
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/hook", map[string]any{"action": "uninstall"}, nil); code != http.StatusConflict {
		t.Fatalf("外人钩子位卸应 409: %d %q", code, errMsg)
	}

	// 换回自家的再卸：干净移除。
	os.Remove(hookPath)
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/hook", map[string]any{"action": "uninstall"}, nil); code != http.StatusOK {
		t.Fatalf("空位卸载应幂等成功: %d %q", code, errMsg)
	}
}

func TestGitReqActivityFace(t *testing.T) {
	s, _, _ := startGitStudio(t)
	var sum struct {
		Repo *struct{ Root string } `json:"repo"`
	}
	gitGetJSON(t, s, "/p/default/git", &sum)
	repo := sum.Repo.Root

	// feat-x 上补一笔提及 r_12 的提交，并把 feat-x 绑给 r_12。
	gitRun(t, repo, "checkout", "feat-x")
	os.WriteFile(filepath.Join(repo, "d.txt"), []byte("d\n"), 0o644)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "feat: 需求落账\n\n需求: r_12")
	gitRun(t, repo, "checkout", "main")
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/bind",
		map[string]any{"branch": "feat-x", "req": "r_12"}, nil); code != http.StatusOK {
		t.Fatalf("绑定: %d %q", code, errMsg)
	}

	var act struct {
		Branches []string `json:"branches"`
		Commits  []struct {
			Subject string `json:"subject"`
			Verdict struct {
				OK bool `json:"ok"`
			} `json:"verdict"`
		} `json:"commits"`
	}
	if code := gitGetJSON(t, s, "/p/default/git/req-activity?req=r_12", &act); code != http.StatusOK {
		t.Fatalf("需求反查应 200: %d", code)
	}
	if len(act.Branches) != 1 || act.Branches[0] != "feat-x" {
		t.Fatalf("应反查到 feat-x: %+v", act.Branches)
	}
	if len(act.Commits) != 1 || act.Commits[0].Subject != "feat: 需求落账" || !act.Commits[0].Verdict.OK {
		t.Fatalf("应命中一笔合规提及: %+v", act.Commits)
	}
	// r_1 整词不误伤 r_12。
	if code := gitGetJSON(t, s, "/p/default/git/req-activity?req=r_1", &act); code != http.StatusOK || len(act.Commits) != 0 {
		t.Fatalf("r_1 不该命中 r_12 的提交: %+v", act.Commits)
	}
}

func TestGitNonRepoSummary(t *testing.T) {
	needGitTool(t)
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	store, _ := projects.Open("")
	if _, err := store.EnsureLobby(t.TempDir()); err != nil { // 非 git 目录
		t.Fatal(err)
	}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{ProjectStore: store}, LocalName: "房主"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	var sum struct {
		Repo   *struct{ Root string } `json:"repo"`
		Policy struct {
			RequireRef string `json:"require_ref"`
		} `json:"policy"`
	}
	if code := gitGetJSON(t, s, "/p/default/git", &sum); code != http.StatusOK {
		t.Fatalf("非仓库摘要应 200: %d", code)
	}
	if sum.Repo != nil {
		t.Fatal("非仓库应 repo=null 空态")
	}
	if sum.Policy.RequireRef != "either" {
		t.Fatal("策略默认档应照常可见")
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/fetch", map[string]any{}, nil); code != http.StatusBadRequest {
		t.Fatalf("非仓库 fetch 应 400: %d %q", code, errMsg)
	}
}

// --- v2.14 远端同步与授权：init / push / pull / auth 面 ----------------------

// startEmptyWorkspaceStudio boots a studio whose lobby workspace is an
// EMPTY directory（init 面的起点）。
func startEmptyWorkspaceStudio(t *testing.T) *Server {
	t.Helper()
	needGitTool(t)
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	store, err := projects.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureLobby(t.TempDir()); err != nil { // 空目录，非 git
		t.Fatal(err)
	}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{ProjectStore: store}, LocalName: "房主"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestGitInitPushPullAuthFaces(t *testing.T) {
	s := startEmptyWorkspaceStudio(t)

	// init：空 workspace 落成仓库（初始分支 main＋空根提交）。
	var initOut struct {
		OK        bool   `json:"ok"`
		Root      string `json:"root"`
		Workspace string `json:"workspace"`
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/init", map[string]any{}, &initOut); code != http.StatusOK {
		t.Fatalf("init 应 200: %d %q", code, errMsg)
	}
	if !initOut.OK || initOut.Root == "" {
		t.Fatalf("init 回执不合预期: %+v", initOut)
	}
	// 摘要从此 repo≠null；再 init 折成 409。
	var sum struct {
		Repo       *struct{ Root string } `json:"repo"`
		GitMissing bool                   `json:"git_missing"`
	}
	if code := gitGetJSON(t, s, "/p/default/git", &sum); code != http.StatusOK || sum.Repo == nil || sum.GitMissing {
		t.Fatalf("init 后摘要应见仓库: %d %+v", code, sum)
	}
	if code, _ := gitPostJSON(t, s, "/p/default/git/init", map[string]any{}, nil); code != http.StatusConflict {
		t.Fatalf("重复 init 应 409: %d", code)
	}

	// push：挂一个本地 bare 远端后推当前分支（InitRepo 的空根提交），
	// -u 钉上游——pull 面靠它。
	remote := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v\n%s", err, out)
	}
	ws := initOut.Workspace
	gitRun(t, ws, "remote", "add", "origin", remote)
	var pushOut struct {
		OK     bool   `json:"ok"`
		Remote string `json:"remote"`
		Branch string `json:"branch"`
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/push", map[string]any{}, &pushOut); code != http.StatusOK {
		t.Fatalf("push 应 200: %d %q", code, errMsg)
	}
	if !pushOut.OK || pushOut.Remote != "origin" || pushOut.Branch != "main" {
		t.Fatalf("push 回执不合预期: %+v", pushOut)
	}

	// auth：探测本地 bare 远端应通过。
	var authOut struct {
		Checks []struct {
			Remote string `json:"remote"`
			OK     bool   `json:"ok"`
			Auth   bool   `json:"auth"`
		} `json:"checks"`
	}
	if code := gitGetJSON(t, s, "/p/default/git/auth", &authOut); code != http.StatusOK || len(authOut.Checks) != 1 || !authOut.Checks[0].OK {
		t.Fatalf("auth 探测应通过: %d %+v", code, authOut)
	}

	// pull：同伴推进两笔 → 快进 incoming=2。
	peer := t.TempDir()
	peerGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = peer
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=同伴", "GIT_AUTHOR_EMAIL=p@niuma.local",
			"GIT_COMMITTER_NAME=同伴", "GIT_COMMITTER_EMAIL=p@niuma.local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("peer git %v: %v\n%s", args, err, out)
		}
	}
	peerGit("clone", remote, ".")
	os.WriteFile(filepath.Join(peer, "p.txt"), []byte("peer\n"), 0o644)
	peerGit("add", ".")
	peerGit("commit", "-m", "feat: 同伴一 [t_201]")
	peerGit("push")
	var pullOut struct {
		OK       bool   `json:"ok"`
		Branch   string `json:"branch"`
		Incoming int    `json:"incoming"`
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/pull", map[string]any{}, &pullOut); code != http.StatusOK {
		t.Fatalf("pull 应 200: %d %q", code, errMsg)
	}
	if !pullOut.OK || pullOut.Incoming != 1 || pullOut.Branch != "main" {
		t.Fatalf("pull 回执不合预期: %+v", pullOut)
	}

	// 脏树先行拦截（远端再进一笔也轮不到 git 说话；untracked 不算
	// 脏——护栏口径与切分支同款，tracked 的 staged/modified 才拦）。
	os.WriteFile(filepath.Join(ws, "dirty.txt"), []byte("x\n"), 0o644)
	gitRun(t, ws, "add", "dirty.txt")
	peerGit("commit", "-m", "feat: 同伴二", "--allow-empty")
	peerGit("push")
	if code, _ := gitPostJSON(t, s, "/p/default/git/pull", map[string]any{}, nil); code != http.StatusConflict {
		t.Fatalf("脏树 pull 应 409: %d", code)
	}
}

func TestGitSeatsOrphanOffboardRow(t *testing.T) {
	s, _, staff, _, wtRoot, _ := startGitSeatStudio(t, nil)
	// 小猿离编（行保留），树还在：orphan_trees 必须收进它——离编行的
	// 树是回收入口，把自己当 known 会永远藏住它。
	if _, err := staff.SetBranch("default", "小猿", "wt/小猿"); err != nil {
		t.Fatal(err)
	}
	if _, err := staff.Offboard("default", "小猿"); err != nil {
		t.Fatal(err)
	}
	treeDir := filepath.Join(wtRoot, "default", "小猿")
	if err := os.MkdirAll(treeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Orphans []struct {
			Person string `json:"person"`
		} `json:"orphan_trees"`
	}
	if code := gitGetJSON(t, s, "/p/default/git/seats", &out); code != http.StatusOK {
		t.Fatalf("seats 应 200: %d", code)
	}
	found := false
	for _, o := range out.Orphans {
		if o.Person == "小猿" {
			found = true
		}
	}
	if !found {
		t.Fatalf("离编成员的树应进 orphan_trees: %+v", out.Orphans)
	}
}
