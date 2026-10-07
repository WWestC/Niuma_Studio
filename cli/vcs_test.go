package cli

// vcs_test.go — 成员只读面的钉子：真 git 夹具上跑 status/branches/log/
// lint 的退出码，check-msg（钩子执行体）的拦与放，以及仓库根↔项目的
// 台账反查（临时 store 注入，不碰真实 ~/.niuma）。

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// TestMain is the helper-process dispatcher for the commit-msg hook's
// end-to-end case: 钩子脚本 exec 的是「本程序绝对路径」，测试里没有
// 发行二进制——就用测试二进制自己顶上（钩子 argv 形如 [bin, vcs,
// check-msg, FILE]，剥掉前两段正好是 runVCS 的入参形状；环境哨兵在
// 这里分流，不进测试主循环）。
func TestMain(m *testing.M) {
	if os.Getenv("NIUMA_VCS_HOOK_HELPER") == "1" {
		os.Exit(runVCS(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func needGitCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机无 git，跳过 vcs CLI 用例")
	}
}

func cliGit(t *testing.T, dir string, args ...string) {
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

// newCLIRepo builds a fixture repo: main 带一笔合规、一笔不合规提交。
func newCLIRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cliGit(t, dir, "init", "-b", "main")
	cliGit(t, dir, "config", "user.email", "test@niuma.local")
	cliGit(t, dir, "config", "user.name", "测试牛马")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n"), 0o644)
	cliGit(t, dir, "add", ".")
	cliGit(t, dir, "commit", "-m", "feat: 起步 [t_100]")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("2\n"), 0o644)
	cliGit(t, dir, "add", ".")
	cliGit(t, dir, "commit", "-m", "随手又一笔")
	cliGit(t, dir, "branch", "feat-x")
	return dir
}

func TestVCSStatusBranchesLogLint(t *testing.T) {
	needGitCLI(t)
	repo := newCLIRepo(t)
	t.Chdir(repo) // vcs 家族缺省探测 cwd

	if code := runVCS([]string{"status"}); code != 0 {
		t.Fatalf("status 应 0，得 %d", code)
	}
	if code := runVCS([]string{"branches"}); code != 0 {
		t.Fatalf("branches 应 0，得 %d", code)
	}
	if code := runVCS([]string{"log", "-n", "5"}); code != 0 {
		t.Fatalf("log 应 0，得 %d", code)
	}
	// 夹具里有一笔不合规提交：lint 非零退出（成员自查的诚实回执）。
	if code := runVCS([]string{"lint", "-n", "5"}); code != 1 {
		t.Fatalf("lint 应 1（有不合规提交），得 %d", code)
	}
	if code := runVCS([]string{"nope"}); code != 2 {
		t.Fatalf("未知子命令应 2，得 %d", code)
	}
}

func TestVCSCheckMsg(t *testing.T) {
	needGitCLI(t)
	repo := newCLIRepo(t)
	t.Chdir(repo)

	writeMsg := func(body string) string {
		p := filepath.Join(t.TempDir(), "MSG")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if code := runVCS([]string{"check-msg", writeMsg("feat: 手机号登录\n\n任务: t_103")}); code != 0 {
		t.Fatalf("合规信息应放行，得 %d", code)
	}
	if code := runVCS([]string{"check-msg", writeMsg("随手一提交")}); code != 1 {
		t.Fatalf("不合规信息应拦下（非零退出），得 %d", code)
	}
	if code := runVCS([]string{"check-msg"}); code != 2 {
		t.Fatalf("缺参数应 2，得 %d", code)
	}
}

func TestProjectContextAt(t *testing.T) {
	needGitCLI(t)
	repo := newCLIRepo(t)

	// 落盘台账：alpha 的工作区就是夹具仓库，feat-x 绑了 r_12。
	diskPath := filepath.Join(t.TempDir(), "projects.json")
	disk, err := projects.Open(diskPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disk.Create(projects.Project{
		Key: "alpha", Name: "甲项目", Workspace: repo,
		Versions: []projects.Version{{Name: "v1"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := disk.BindBranch("alpha", "feat-x", projects.BranchLink{Req: "r_12"}); err != nil {
		t.Fatal(err)
	}

	r, err := vcs.Detect(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := projectContextAt(diskPath, r)
	if !ok || p.Key != "alpha" {
		t.Fatalf("同根仓库应命中 alpha: ok=%v key=%q", ok, p.Key)
	}
	if link := p.Link("feat-x"); link == nil || link.Req != "r_12" {
		t.Fatalf("命中后应带回绑定: %+v", link)
	}

	// 子目录工作区同样按仓库根命中（成员会话常在仓库深处）。
	sub := filepath.Join(repo, "web", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rSub, err := vcs.Detect(t.Context(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := projectContextAt(diskPath, rSub); !ok || p.Key != "alpha" {
		t.Fatalf("子目录探测也应命中 alpha: ok=%v key=%q", ok, p.Key)
	}
}

// TestCommitMsgHookEndToEnd pins 钩子的真实拦截力：把 vcs.HookScript（
// 面板「装提交钩子」落盘的同款脚本，$EXE 换成测试二进制）写进夹具仓
// 库的 .git/hooks/commit-msg，然后真跑 git commit——坏消息被 git 当场
// 拒绝且输出带原因，合规消息照常落库。
func TestCommitMsgHookEndToEnd(t *testing.T) {
	needGitCLI(t)
	repo := newCLIRepo(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "commit-msg"), []byte(vcs.HookScript(exe)), 0o755); err != nil {
		t.Fatal(err)
	}

	commit := func(n int, msg string) (bool, string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "f"+string(rune('0'+n))+".txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cliGit(t, repo, "add", ".")
		cmd := exec.Command("git", "commit", "-m", msg)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "NIUMA_VCS_HOOK_HELPER=1")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		return cmd.Run() == nil, out.String()
	}

	ok, out := commit(1, "随手一提交")
	if ok {
		t.Fatal("不合规范的提交应被钩子拦下")
	}
	if !strings.Contains(out, "提交信息不合工作室规范") {
		t.Fatalf("拦截输出应带原因：\n%s", out)
	}
	// 拦截之后没有新提交落地。
	if got := cliGitOut(t, repo, "rev-list", "--count", "HEAD"); strings.TrimSpace(got) != "2" {
		t.Fatalf("拦截后提交数应仍为 2，得到 %s", got)
	}

	ok, out = commit(2, "feat: 合规落地 [t_9]")
	if !ok {
		t.Fatalf("合规提交应放行：\n%s", out)
	}
	if got := cliGitOut(t, repo, "rev-list", "--count", "HEAD"); strings.TrimSpace(got) != "3" {
		t.Fatalf("放行后提交数应为 3，得到 %s", got)
	}
}

// cliGitOut is cliGit with the output back.
func cliGitOut(t *testing.T, dir string, args ...string) string {
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
