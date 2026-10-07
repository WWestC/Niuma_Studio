package dispatch

// gitlink_test.go — 版本管理调度器半场的钉子：任务发布尾行带「工作区
// 分支·版本·提交规范」（git 项目带、非 git 项目一行不多话）、切分支
// 告知落进每位成员的背景车道（不唤醒回合）、空房如实回 false。

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/vcs"
	"github.com/WWestC/Niuma_Studio/zcode"
)

func needGitDispatch(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机无 git，跳过 gitlink 用例")
	}
}

// gitLinkBridge 记录注入文本，永不失败。
type gitLinkBridge struct {
	baseBridge
	mu    sync.Mutex
	sends []string
}

func (f *gitLinkBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	f.sends = append(f.sends, content)
	f.mu.Unlock()
	return nil, nil
}

func (f *gitLinkBridge) joined() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.sends, "\n---\n")
}

// newGitLinkDispatcher boots a dispatcher on the given workspace.
func newGitLinkDispatcher(t *testing.T, workspace string) (*Dispatcher, *gitLinkBridge) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	gb := &gitLinkBridge{}
	d := Start(hub, store, gb, Config{
		Workspace:   workspace,
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	return d, gb
}

// gitRepoFixture 造一笔提交的 main 仓库。
func gitRepoFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@niuma.local"},
		{"config", "user.name", "测试牛马"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "feat: 起步 [t_1]"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out.String())
		}
	}
	return dir
}

func TestPlanLandedCarriesBranchLine(t *testing.T) {
	needGitDispatch(t)
	repo := gitRepoFixture(t)
	d, gb := newGitLinkDispatcher(t, repo)
	if err := d.attach("小明", "工程师", "s-git-1"); err != nil {
		t.Fatal(err)
	}
	d.PlanLanded("p_1", "登录改造", "编排者", []*tasks.Task{
		{ID: "t_9", Title: "手机号登录", Assignee: "小明", Version: "v1"},
	})
	waitFor(t, func() bool {
		j := gb.joined()
		return strings.Contains(j, "【任务发布") &&
			strings.Contains(j, "工作区分支：main") &&
			strings.Contains(j, "版本 v1") &&
			strings.Contains(j, "t_NN/r_NN")
	}, "发布注入应带工作区分支·版本·规范尾行")
}

func TestPlanLandedNonGitStaysSilent(t *testing.T) {
	d, gb := newGitLinkDispatcher(t, t.TempDir()) // 非 git 目录
	if err := d.attach("小明", "工程师", "s-git-2"); err != nil {
		t.Fatal(err)
	}
	d.PlanLanded("p_2", "无仓项目", "编排者", []*tasks.Task{
		{ID: "t_10", Title: "随便干点", Assignee: "小明"},
	})
	waitFor(t, func() bool { return strings.Contains(gb.joined(), "【任务发布") }, "发布注入应送达")
	if strings.Contains(gb.joined(), "工作区分支") {
		t.Fatal("非 git 项目不该带分支尾行")
	}
}

func TestBranchSwitchedBackground(t *testing.T) {
	d, _ := newGitLinkDispatcher(t, t.TempDir())
	if d.BranchSwitched("main", "feat-x") {
		t.Fatal("空房应回 false")
	}
	if err := d.attach("小红", "工程师", "s-git-3"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("小绿", "工程师", "s-git-4"); err != nil {
		t.Fatal(err)
	}
	if !d.BranchSwitched("main", "feat-x") {
		t.Fatal("有成员应回 true")
	}
	// 告知落背景车道（不唤醒回合）：两位成员各有一条，内容带去向与
	// 「无需回应」纪律。
	d.mu.Lock()
	for _, name := range []string{"小红", "小绿"} {
		m := d.members[name]
		if m == nil || len(m.lanes.inject) != 1 {
			d.mu.Unlock()
			t.Fatalf("%s 的背景车道应恰有一条告知: %+v", name, m)
		}
		if !strings.Contains(m.lanes.inject[0].text, "main 切到 feat-x") || !strings.Contains(m.lanes.inject[0].text, "无需回应") {
			d.mu.Unlock()
			t.Fatalf("告知内容不合预期: %q", m.lanes.inject[0].text)
		}
	}
	d.mu.Unlock()
}

// --- 分支隔离（v2.7.1）------------------------------------------------------

// wsBridge 记录注入文本与每次 CreateSession 的 workspace 参数——隔离
// 语义的核心断言就是「会话生在哪棵树上」。
type wsBridge struct {
	baseBridge
	mu        sync.Mutex
	sends     []string
	wsCreated []string
	seq       int
}

func (f *wsBridge) CreateSession(ctx context.Context, workspace, mode string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.wsCreated = append(f.wsCreated, workspace)
	return fmt.Sprintf("s-ws-%d", f.seq), nil
}

func (f *wsBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	f.sends = append(f.sends, content)
	f.mu.Unlock()
	return nil, nil
}

func (f *wsBridge) workspaces() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.wsCreated...)
}
func (f *wsBridge) lastSend() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		return ""
	}
	return f.sends[len(f.sends)-1]
}

// newIsolatedDispatcher boots a staffing-backed dispatcher on a git repo
// fixture with a worktree root.
func newIsolatedDispatcher(t *testing.T) (*Dispatcher, *wsBridge, *staffing.Store, string, string) {
	t.Helper()
	needGitDispatch(t)
	repo := gitRepoFixture(t)
	wtRoot := t.TempDir()
	hub := chat.NewHub()
	agentsStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staffStore, err := staffing.Open("")
	if err != nil {
		t.Fatal(err)
	}
	wb := &wsBridge{}
	d := Start(hub, agentsStore, wb, Config{
		ProjectKey:   "default",
		Workspace:    repo,
		WorktreeRoot: wtRoot,
		StaffStore:   staffStore,
		InboxDir:     t.TempDir(),
		PatrolEvery:  -1,
		DailyAt:      "off",
	})
	t.Cleanup(d.Stop)
	return d, wb, staffStore, repo, wtRoot
}

func TestBirthInIsolatedWorktree(t *testing.T) {
	d, wb, staff, repo, wtRoot := newIsolatedDispatcher(t)
	if _, err := staff.Join("default", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := staff.SetBranch("default", "小猿", "wt/小猿"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Birth("小猿", "前端开发", "", "", ""); err != nil {
		t.Fatalf("隔离出生应成功: %v", err)
	}
	want := filepath.Join(wtRoot, "default", "小猿")
	ws := wb.workspaces()
	if len(ws) != 1 || ws[0] != want {
		t.Fatalf("会话应生在专属工作树 %q，实际 %v", want, ws)
	}
	// 树真的驻在该分支。
	r, err := vcs.Detect(t.Context(), want)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := r.Status(t.Context()); st.Branch != "wt/小猿" {
		t.Fatalf("树应驻 wt/小猿: %+v", st)
	}
	// 主树仍在 main（隔离不动主树）。
	rm, _ := vcs.Detect(t.Context(), repo)
	if st, _ := rm.Status(t.Context()); st.Branch != "main" {
		t.Fatalf("主树应仍在 main: %+v", st)
	}
	// 出生注入带工作树说明。
	if s := wb.lastSend(); !strings.Contains(s, "【工作树】") || !strings.Contains(s, "wt/小猿") {
		t.Fatalf("出生注入应带工作树说明: %q", s)
	}
}

func TestBirthIsolatedNonGitRefuses(t *testing.T) {
	needGitDispatch(t)
	hub := chat.NewHub()
	agentsStore, _ := agents.Open("")
	staffStore, _ := staffing.Open("")
	if _, err := staffStore.Join("default", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := staffStore.SetBranch("default", "小猿", "wt/小猿"); err != nil {
		t.Fatal(err)
	}
	wb := &wsBridge{}
	d := Start(hub, agentsStore, wb, Config{
		ProjectKey: "default", Workspace: t.TempDir(), // 非 git 目录
		WorktreeRoot: t.TempDir(), StaffStore: staffStore,
		InboxDir: t.TempDir(), PatrolEvery: -1, DailyAt: "off",
	})
	t.Cleanup(d.Stop)
	if _, err := d.Birth("小猿", "前端开发", "", "", ""); err == nil ||
		!strings.Contains(err.Error(), "不是 git 仓库") {
		t.Fatalf("非 git 工作区的隔离出生应如实报错: %v", err)
	}
	if len(wb.workspaces()) != 0 {
		t.Fatal("报错的出生不该创建任何会话")
	}
}

func TestSeatBranchMigrationAndInTreeSwitch(t *testing.T) {
	d, wb, staff, repo, wtRoot := newIsolatedDispatcher(t)
	if _, err := staff.Join("default", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	// 先共享出生（主树）。
	if _, err := d.Birth("小猿", "前端开发", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if ws := wb.workspaces(); len(ws) != 1 || ws[0] != repo {
		t.Fatalf("无分支座位应生在主树: %v", ws)
	}
	// 备一棵目标分支。
	gitRun2(t, repo, "branch", "feat-y")

	// 跨树迁移：SetBranch ＋ 重生到树上。
	if err := d.SeatBranch("小猿", "wt/小猿"); err != nil {
		t.Fatalf("迁移到专属树: %v", err)
	}
	ws := wb.workspaces()
	want := filepath.Join(wtRoot, "default", "小猿")
	if len(ws) != 2 || ws[1] != want {
		t.Fatalf("迁移应重生成会话在树上: %v", ws)
	}
	if row, _ := staff.Get("default", "小猿"); row.Branch != "wt/小猿" {
		t.Fatalf("座位分支未落账: %+v", row)
	}
	if s := wb.lastSend(); !strings.Contains(s, "【迁移】") {
		t.Fatalf("迁移告知应送达: %q", s)
	}

	// 树内换枝：不重生成会话（wsCreated 不变），只切树＋背景告知。
	if err := d.SeatBranch("小猿", "feat-y"); err != nil {
		t.Fatalf("树内换枝: %v", err)
	}
	if ws = wb.workspaces(); len(ws) != 2 {
		t.Fatalf("树内换枝不该重生成会话: %v", ws)
	}
	if row, _ := staff.Get("default", "小猿"); row.Branch != "feat-y" {
		t.Fatalf("换枝未落账: %+v", row)
	}
	r, _ := vcs.Detect(t.Context(), want)
	if st, _ := r.Status(t.Context()); st.Branch != "feat-y" {
		t.Fatalf("树应驻 feat-y: %+v", st)
	}

	// 回主树：再重生一次，落回主工作区；树保留。
	if err := d.SeatBranch("小猿", ""); err != nil {
		t.Fatalf("回主树: %v", err)
	}
	if ws = wb.workspaces(); len(ws) != 3 || ws[2] != repo {
		t.Fatalf("回主树应重生成会话在主树: %v", ws)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("旧工作树应保留未动: %v", err)
	}
}

// gitRun2 is the local git runner (named to avoid clashing with the
// vcs package's test helpers — same package file).
func gitRun2(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v（in %s）: %v\n%s", args, dir, err, out.String())
	}
}
