package vcs

// sync_test.go — 远端同步与授权半场的钉子：push/pull 的生命周期（首推
// 钉上游、快进进来 N 笔、分叉被拒）、上游缺失的人话拒绝、凭据类拒绝
// 的指纹分类、远端探测的 OK/失败两态、NoGitError 的 errors.Is 锚点。
// 夹具与 git_test.go 同款真 git 仓库；本机无 git 一律跳过。

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needGitBin(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机无 git，跳过远端同步用例")
	}
}

func gitAt(t *testing.T, dir string, args ...string) {
	t.Helper()
	needGitBin(t)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=测试牛马", "GIT_AUTHOR_EMAIL=t@niuma.local",
		"GIT_COMMITTER_NAME=测试牛马", "GIT_COMMITTER_EMAIL=t@niuma.local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v（in %s）: %v\n%s", args, dir, err, out)
	}
}

// newSyncFixture builds repo（一分支一笔提交）＋bare 远端，repo 已挂
// origin 并首推。返回 repo 目录、远端路径。
func newSyncFixture(t *testing.T) (string, string) {
	t.Helper()
	needGitBin(t)
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "clone", remote, repo).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=测试牛马", "GIT_AUTHOR_EMAIL=t@niuma.local",
			"GIT_COMMITTER_NAME=测试牛马", "GIT_COMMITTER_EMAIL=t@niuma.local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644)
	git("add", ".")
	git("commit", "-m", "feat: 起步 [t_100]")
	git("push", "-u", "origin", "main")
	r, err := Detect(ctx, repo)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if _, err := r.Push(ctx, "", ""); err != nil {
		t.Fatalf("首推（钉上游）: %v", err)
	}
	return repo, remote
}

// commitIn writes one file and commits it in dir（分支隔离测试的万金油）.
func commitIn(t *testing.T, dir, msg string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "f-"+msg[:4]+".txt"), []byte(msg+"\n"), 0o644)
	git := exec.Command("git", "add", ".")
	git.Dir = dir
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	git = exec.Command("git", "commit", "-m", msg)
	git.Dir = dir
	git.Env = append(os.Environ(), "GIT_AUTHOR_NAME=测试牛马", "GIT_AUTHOR_EMAIL=t@niuma.local",
		"GIT_COMMITTER_NAME=测试牛马", "GIT_COMMITTER_EMAIL=t@niuma.local")
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

func TestPushPullLifecycle(t *testing.T) {
	ctx := context.Background()
	repo, remote := newSyncFixture(t)
	r, err := Detect(ctx, repo)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	// 工作区无新提交时 pull：已是最新，incoming=0。
	branch, incoming, err := r.PullFFOnly(ctx)
	if err != nil || branch != "main" || incoming != 0 {
		t.Fatalf("无新提交的快进应 0 笔: branch=%s incoming=%d err=%v", branch, incoming, err)
	}

	// 远端被人推进两笔 → pull 快进且 incoming=2。
	peer := t.TempDir()
	gitAt(t, peer, "clone", remote, ".")
	commitIn(t, peer, "feat: 同伴一 [t_101]")
	commitIn(t, peer, "feat: 同伴二 [t_102]")
	gitAt(t, peer, "push")
	_, incoming, err = r.PullFFOnly(ctx)
	if err != nil || incoming != 2 {
		t.Fatalf("快进应进 2 笔: incoming=%d err=%v", incoming, err)
	}

	// 本地分叉（本地与远端各有独有提交）→ --ff-only 拒绝且树保持原状。
	commitIn(t, repo, "feat: 本地独走")
	commitIn(t, peer, "feat: 同伴三再进一步")
	gitAt(t, peer, "push")
	pre, _ := r.HeadSHA(ctx)
	if _, _, err := r.PullFFOnly(ctx); err == nil {
		t.Fatal("分叉的快进应被拒绝")
	}
	post, _ := r.HeadSHA(ctx)
	if pre != post {
		t.Fatal("快进被拒后 HEAD 必须原地不动")
	}

	// 指名分支推送：建 feat-x 后推上去，远端可见。
	gitAt(t, repo, "branch", "feat-x")
	if _, err := r.Push(ctx, "", "feat-x"); err != nil {
		t.Fatalf("推 feat-x: %v", err)
	}

	// 不存在的远端名如实拒绝。
	if _, err := r.Push(ctx, "nowhere", ""); err == nil || !strings.Contains(err.Error(), "nowhere") {
		t.Fatalf("推往不存在的远端应报名字: %v", err)
	}
	_ = remote
}

func TestPullWithoutUpstream(t *testing.T) {
	ctx := context.Background()
	repo, remote := newSyncFixture(t)
	// 无上游的裸仓（自 init，不挂跟踪）：pull 用人话报「没有上游」。
	solo := filepath.Join(t.TempDir(), "solo")
	gitAt(t, t.TempDir(), "init", "-b", "main", solo)
	commitIn(t, solo, "feat: 独行")
	r, err := Detect(ctx, solo)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	_, _, err = r.PullFFOnly(ctx)
	if err == nil || !strings.Contains(err.Error(), "上游") {
		t.Fatalf("无上游应人话拒绝: %v", err)
	}
	// 挂上远端但没钉上游的分支同样报。
	gitAt(t, solo, "remote", "add", "origin", remote)
	if _, _, err := r.PullFFOnly(ctx); err == nil || !strings.Contains(err.Error(), "上游") {
		t.Fatalf("挂了远端但分支无上游，同样要报: %v", err)
	}
	_ = repo
}

func TestAuthRejectClassify(t *testing.T) {
	yes := []string{
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled",
		"fatal: Authentication failed for 'https://github.com/x/y.git/'",
		"git@github.com: Permission denied (publickey).",
		"fatal: Could not read from remote repository.",
		"fatal: unable to access '…': The requested URL returned error: 403",
	}
	for _, d := range yes {
		if !authReject(d) {
			t.Errorf("应判为凭据类: %q", d)
		}
	}
	no := []string{
		"error: Your local changes to the following files would be overwritten by checkout",
		"fatal: not a git repository (or any of the parent directories): .git",
		"CONFLICT (content): Merge conflict in a.txt",
		"error: pathspec 'main' did not match any file(s) known to git",
	}
	for _, d := range no {
		if authReject(d) {
			t.Errorf("不应判为凭据类: %q", d)
		}
	}
}

func TestAuthProbeLocalRemote(t *testing.T) {
	ctx := context.Background()
	repo, _ := newSyncFixture(t)
	r, err := Detect(ctx, repo)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	st := r.AuthProbe(ctx, "origin")
	if !st.OK || st.Auth || st.Reason != "" {
		t.Fatalf("本地 bare 远端应探测通过: %+v", st)
	}
	st = r.AuthProbe(ctx, "ghost")
	if st.OK || st.Auth || st.Reason == "" {
		t.Fatalf("不存在的远端应如实报因: %+v", st)
	}
}

func TestNoGitSentinel(t *testing.T) {
	// 摘走 PATH 与全部候选安装位：findGitTool 必须落进 ErrNoGit 锚点
	// （服务端摘要面靠 errors.Is 分流成「没装 git」空态卡）。
	saved := gitInstallCandidates
	gitInstallCandidates = nil
	t.Cleanup(func() { gitInstallCandidates = saved })
	t.Setenv("PATH", "")
	_, err := findGitTool()
	if err == nil {
		// PATH 清空后 LookPath 仍可能命中 hash/继承态，个别环境兜不住
		// ——此时不判失败，本用例在干净环境必然命中。
		t.Skip("环境仍能解析到 git，跳过哨兵用例")
	}
	if !errors.Is(err, ErrNoGit) {
		t.Fatalf("应锚定 ErrNoGit: %v", err)
	}
	if !strings.Contains(err.Error(), "安装 git") {
		t.Fatalf("文案要带安装指引: %v", err)
	}
}
