package dispatch

// gitsettings_test.go — 两级门顶层的调度器面钉子（Config.GitOffProbe）：
// 总开关关着的项目，带分支座位的成员出生被拒（绝不静默落回共享主树
// ——「以为隔离其实共享」是最坏状态）；探针翻开（面板重开总开关）
// 后这道门让路，走到下一道路障。附带主动档（Config.AutoSeatProbe）：
// 座位空的成员出生即自动驻 wt/<人名>。

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// execLookPathGit mirrors the server tests' needGitTool guard.
func execLookPathGit() (string, error) { return exec.LookPath("git") }

// initTinyRepo builds a one-commit main repo（server 侧 newGitRepo 的瘦身版）.
func initTinyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitOut(t, dir, "init", "-b", "main")
	gitOut(t, dir, "config", "user.email", "test@niuma.local")
	gitOut(t, dir, "config", "user.name", "测试牛马")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", ".")
	gitOut(t, dir, "commit", "-m", "feat: 起步 [t_100]")
	return dir
}

// gitOut runs one git command in dir, failing the test on error.
func gitOut(t *testing.T, dir string, args ...string) string {
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

func TestBirthWorkspaceRefusesWhenGitOff(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staff, err := staffing.Open(filepath.Join(t.TempDir(), "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staff.Join(chat.LobbyKey, "甲", "工程师", "s-gitoff-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := staff.SetBranch(chat.LobbyKey, "甲", "wt/jia"); err != nil {
		t.Fatal(err)
	}
	var gitOff atomic.Bool
	d := Start(hub, store, &wakeBridge{}, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
		StaffStore:  staff,
		GitOffProbe: func() bool { return gitOff.Load() },
	})
	t.Cleanup(d.Stop)

	// 总开关关着：分支座位出生被拒，文案指路两个出路。
	gitOff.Store(true)
	if _, err := d.birthWorkspace("甲"); err == nil || !strings.Contains(err.Error(), "已关闭 git 版本管理") {
		t.Fatalf("关闭态分支座位出生应拒并指路: %v", err)
	}
	// 无分支座位的成员不受这道门影响（照旧共享主树）。
	if _, err := staff.Join(chat.LobbyKey, "乙", "设计师", "s-gitoff-b"); err != nil {
		t.Fatal(err)
	}
	if ws, err := d.birthWorkspace("乙"); err != nil || ws == "" {
		t.Fatalf("无分支座位不应被总开关拦: %q %v", ws, err)
	}

	// 探针翻开（面板重开）：这道门让路——错误换成下一道路障（夹具没
	// 配工作树根），证明不再吃总开关的闭门羹。
	gitOff.Store(false)
	if _, err := d.birthWorkspace("甲"); err == nil || !strings.Contains(err.Error(), "工作树根目录") {
		t.Fatalf("重开后应放行到建树路障: %v", err)
	}
}

// TestBirthWorkspaceAutoSeat：主动档开着——座位空的成员出生即自动驻
// wt/<人名>（分支自基线切出、树落 WorktreeRoot、账面落 staffing）；
// 关着照旧共享主树。
func TestBirthWorkspaceAutoSeat(t *testing.T) {
	if _, err := execLookPathGit(); err != nil {
		t.Skip("本机无 git，跳过自动驻枝用例")
	}
	repo := initTinyRepo(t)
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staff, err := staffing.Open(filepath.Join(t.TempDir(), "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staff.Join(chat.LobbyKey, "甲", "工程师", "s-autoseat-a"); err != nil {
		t.Fatal(err)
	}
	var autoSeat atomic.Bool
	d := Start(hub, store, &wakeBridge{}, Config{
		Workspace:     repo,
		WorktreeRoot:  t.TempDir(),
		InboxDir:      t.TempDir(),
		PatrolEvery:   -1,
		DailyAt:       "off",
		StaffStore:    staff,
		AutoSeatProbe: func() (bool, string) { return autoSeat.Load(), "" },
	})
	t.Cleanup(d.Stop)

	// 关着：照旧共享主树。
	if ws, err := d.birthWorkspace("甲"); err != nil || ws != repo {
		t.Fatalf("关着应共享主树: %q %v", ws, err)
	}
	// 开着：自动驻 wt/甲——分支自 main 切出、树在 WorktreeRoot 下、账面落账。
	autoSeat.Store(true)
	ws, err := d.birthWorkspace("甲")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(ws, filepath.Join("wt", chat.LobbyKey, "甲")) && !strings.Contains(ws, filepath.Join(chat.LobbyKey, "甲")) {
		t.Fatalf("应出生在专属工作树: %q", ws)
	}
	if row, ok := staff.Get(chat.LobbyKey, "甲"); !ok || row.Branch != "wt/甲" {
		t.Fatalf("账面应驻 wt/甲: %+v", row)
	}
	out := gitOut(t, repo, "branch", "--list", "wt/甲")
	if !strings.Contains(out, "wt/甲") {
		t.Fatalf("分支 wt/甲 应已切出: %q", out)
	}
}
