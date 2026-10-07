package dispatch

// gitflow_patrol_test.go — 版本管理流程（v2.8 gitflow）调度器半场的钉
// 子：巡检的「版本收口」段（未合并分支点名＋在途任务口径＋已并入即
// 静默＋共享主树脏净）、PatrolNow 注入把该段带给编排者、MergeLanded
// 的背景告知（分支主人收到、无座分支如实报错）。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// newPatrolGitDispatcher boots a staffing+tasks dispatcher on a git repo
// fixture（版本收口段的公共夹具：编制表＋任务引擎都在）。
func newPatrolGitDispatcher(t *testing.T) (*Dispatcher, *wsBridge, *staffing.Store, *tasks.Engine, string) {
	t.Helper()
	needGitDispatch(t)
	repo := gitRepoFixture(t)
	hub := chat.NewHub()
	agentsStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staffStore, err := staffing.Open("")
	if err != nil {
		t.Fatal(err)
	}
	eng := tasks.MustOpenMemory("房主")
	eng.SetProjectKeys(func() []string { return []string{"default"} })
	wb := &wsBridge{}
	d := Start(hub, agentsStore, wb, Config{
		ProjectKey:   "default",
		Workspace:    repo,
		WorktreeRoot: t.TempDir(),
		StaffStore:   staffStore,
		Tasks:        eng,
		InboxDir:     t.TempDir(),
		PatrolEvery:  -1,
		DailyAt:      "off",
	})
	t.Cleanup(d.Stop)
	return d, wb, staffStore, eng, repo
}

// patrolGitRun runs one git command in repo, failing the test on error.
func patrolGitRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// patrolBranchAhead makes branch exist and hold one commit beyond main.
func patrolBranchAhead(t *testing.T, repo, branch string) {
	t.Helper()
	patrolGitRun(t, repo, "switch", "-c", branch)
	if err := os.WriteFile(filepath.Join(repo, "fx.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patrolGitRun(t, repo, "add", ".")
	patrolGitRun(t, repo, "commit", "-m", "feat(x): 领先一步 [t_9]")
	patrolGitRun(t, repo, "switch", "main")
}

func TestPatrolGitLines(t *testing.T) {
	d, _, staff, eng, repo := newPatrolGitDispatcher(t)
	if _, err := staff.Join("default", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	patrolBranchAhead(t, repo, "feat-x")
	if _, err := staff.SetBranch("default", "小猿", "feat-x"); err != nil {
		t.Fatal(err)
	}

	// 无在途任务 → 点名「该收口」。
	got := d.patrolGitLines()
	if !strings.Contains(got, "小猿 的分支 feat-x 领先 main 1 提交未并入") ||
		!strings.Contains(got, "该收口") {
		t.Fatalf("无在途任务应点名收口：%q", got)
	}

	// 有在途任务 → 口径换成「尚有在途任务」（点名但不催收）。
	out := eng.Create("", "房主", "在做的事", "", "小猿")
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if u := eng.Update("", "房主", out.Task.ID, tasksPatchDoing()); u.Denied {
		t.Fatal(u.Reason)
	}
	got = d.patrolGitLines()
	if !strings.Contains(got, "尚有在途任务") || strings.Contains(got, "该收口") {
		t.Fatalf("在途口径应换挡：%q", got)
	}

	// 共享主树脏 → 附加一条。
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = d.patrolGitLines()
	if !strings.Contains(got, "共享主树（main）有 1 处未提交改动") {
		t.Fatalf("主树脏应点名：%q", got)
	}
	patrolGitRun(t, repo, "checkout", "--", "a.txt")

	// 已并入 → 整段静默（一行不多话）。
	patrolGitRun(t, repo, "merge", "--no-ff", "-m", "merge: feat-x 收口", "feat-x")
	if got := d.patrolGitLines(); got != "" {
		t.Fatalf("已并入后应静默：%q", got)
	}
}

func TestPatrolNowCarriesGitLines(t *testing.T) {
	d, wb, staff, _, repo := newPatrolGitDispatcher(t)
	if err := d.attach("编排者", agents.OrchestratorRole, "s-patrol-1"); err != nil {
		t.Fatal(err)
	}
	patrolBranchAhead(t, repo, "feat-x")
	if _, err := staff.Join("default", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := staff.SetBranch("default", "小猿", "feat-x"); err != nil {
		t.Fatal(err)
	}
	d.PatrolNow()
	s := wb.lastSend()
	if !strings.Contains(s, "【巡检】") || !strings.Contains(s, "版本收口") || !strings.Contains(s, "feat-x") {
		t.Fatalf("巡检注入应带版本收口段：%q", s)
	}
}

func TestMergeLandedBackgroundNote(t *testing.T) {
	d, _, staff, _, _ := newPatrolGitDispatcher(t)
	if err := d.attach("小猿", "前端开发", "s-ml-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := staff.Join("default", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := staff.SetBranch("default", "小猿", "feat-x"); err != nil {
		t.Fatal(err)
	}
	if err := d.MergeLanded("feat-x", "main"); err != nil {
		t.Fatalf("分支主人应收到告知：%v", err)
	}
	d.mu.Lock()
	m := d.members["小猿"]
	note := ""
	if m != nil && len(m.lanes.inject) > 0 {
		note = m.lanes.inject[len(m.lanes.inject)-1].text
	}
	d.mu.Unlock()
	if !strings.Contains(note, "【版本】你的分支 feat-x 已并入主线 main") || !strings.Contains(note, "无需回应") {
		t.Fatalf("告知应落背景车道且带纪律：%q", note)
	}
	// 没有座位驻在这根分支 → 如实报错。
	if err := d.MergeLanded("no-such-branch", "main"); err == nil {
		t.Fatal("无座分支应报错")
	}
}

// tasksPatchDoing is the doing-flip patch（在途口径用例的小便利）。
func tasksPatchDoing() tasks.Patch {
	return tasks.Patch{Status: tasks.StatusDoing}
}
