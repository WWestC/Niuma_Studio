// vcs/git_test.go — 真 git 夹具用例：init/commit/branch/bare 远端全套
// 在 t.TempDir() 里现造（本机装了 git 的开发机与 CI 都能跑；没装则
// 整组跳过，绝不猜）。解析层（状态头/规范判定）另有脱离 git 的纯表
// 驱动用例钉形状。
package vcs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// needGit skips the real-repo cases when git is absent.
func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机无 git，跳过真仓库用例")
	}
}

// git runs one git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v（in %s）: %v\n%s", args, dir, err, out.String())
	}
	return out.String()
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo 造一个 main 分支带两次提交的仓库（提交信息合规，方便上层
// 用例复用）。
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.email", "test@niuma.local")
	git(t, dir, "config", "user.name", "测试牛马")
	write(t, dir, "a.txt", "one\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: 起步 [t_100]")
	write(t, dir, "b.txt", "two\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: 第二步\n\n任务: t_101")
	return dir
}

// withRemote 给仓库配一个 bare 克隆当 origin（fetch/ahead-behind 用例
// 的地基）并推 main。
func withRemote(t *testing.T, repo string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "remote.git")
	git(t, repo, "clone", "--bare", ".", bare)
	git(t, repo, "remote", "add", "origin", bare)
	git(t, repo, "push", "-u", "origin", "main")
	return bare
}

func TestDetect_NotRepo(t *testing.T) {
	needGit(t)
	if _, err := Detect(context.Background(), t.TempDir()); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("期望 ErrNotRepo，得到 %v", err)
	}
}

func TestDetect_RepoAndSubdir(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	sub := filepath.Join(repo, "web", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Detect(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	// macOS 的 /var 是 /private/var 的软链，rev-parse 给真身——比
	// 较前把夹具也解析成真身。
	real, _ := filepath.EvalSymlinks(repo)
	if filepath.Clean(r.Root) != filepath.Clean(real) {
		t.Fatalf("子目录探测的仓库根 %q != 仓库真身 %q", r.Root, real)
	}
	if r.IsWorktree() {
		t.Fatal("普通仓库不该误报 worktree")
	}
	// 仓库根本身探测同样成立。
	r2, err := Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(r2.Root) != filepath.Clean(real) {
		t.Fatalf("根探测 %q != %q", r2.Root, real)
	}
}

func TestBranches(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	git(t, repo, "branch", "feat-x")
	withRemote(t, repo)
	git(t, repo, "push", "origin", "feat-x")
	git(t, repo, "fetch", "--prune")

	r, err := Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := r.Branches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range bs {
		got = append(got, b.Remote+"#"+b.Name)
		if b.Name == "main" && !b.Current {
			t.Fatal("main 是当前分支，应带 Current 标记")
		}
	}
	// 本地在前、远端在后，各自按名排序。
	want := []string{"#feat-x", "#main", "origin#origin/feat-x", "origin#origin/main"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("分支序不合预期：\n got %v\nwant %v", got, want)
	}
}

func TestStatus(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	withRemote(t, repo)
	r, err := Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 干净树：分支/upstream 正常、零改动。
	st, err := r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || st.Upstream != "origin/main" || st.TrackedDirty() != 0 {
		t.Fatalf("干净树的状态不合预期: %+v", st)
	}

	// 改动三态：staged / modified / untracked 各一。
	write(t, repo, "a.txt", "dirty\n")
	write(t, repo, "new.txt", "?\n")
	git(t, repo, "add", "a.txt")
	st, err = r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Staged != 1 || st.Modified != 0 || st.Untracked != 1 || st.TrackedDirty() != 1 {
		t.Fatalf("三态计数不合预期: %+v", st)
	}

	// 本地新提交不上推 → ahead 1。
	git(t, repo, "commit", "-m", "feat: 本地领先 [t_102]")
	st, err = r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 1 || st.Behind != 0 {
		t.Fatalf("ahead/behind 不合预期: %+v", st)
	}

	// 游离 HEAD。
	git(t, repo, "checkout", "--detach")
	st, err = r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Detached || st.Branch != "" {
		t.Fatalf("游离 HEAD 不合预期: %+v", st)
	}
}

func TestLogAndMerge(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	git(t, repo, "checkout", "-b", "side")
	write(t, repo, "c.txt", "side\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "feat: 侧枝 [t_103]")
	git(t, repo, "checkout", "main")
	git(t, repo, "merge", "--no-ff", "side", "-m", "merge: 并入侧枝")

	r, err := Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := r.Log(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 4 {
		t.Fatalf("期望 4 条提交，得到 %d", len(cs))
	}
	if cs[0].Subject != "merge: 并入侧枝" || !cs[0].IsMerge() {
		t.Fatalf("最新提交应是 merge： %+v", cs[0])
	}
	if cs[3].Parents != 0 {
		t.Fatalf("根提交 Parents 应为 0： %+v", cs[3])
	}

	// merge 提交豁免规范判定（message 是 git 拼的，没有 type 也有号）。
	if v := CheckCommit(cs[0].Subject, Policy{}, true); !v.OK {
		t.Fatalf("merge 豁免失效: %+v", v)
	}
}

func TestLogGrepAll(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	for i, msg := range []string{
		"feat: 甲\n\n需求: r_12",
		"feat: 乙\n\n需求: r_123",
		"fix: 丙 [t_9]",
	} {
		write(t, repo, "f"+string(rune('0'+i))+".txt", "x\n")
		git(t, repo, "add", ".")
		git(t, repo, "commit", "-m", msg)
	}
	r, err := Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ms, err := r.LogGrepAll(ctx, "r_12", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || !strings.Contains(SubjectOf(ms[0].Body), "甲") {
		t.Fatalf("r_12 应整词命中且只命中一条: %+v", ms)
	}
	if v := CheckCommit(ms[0].Body, Policy{}, ms[0].Parents >= 2); !v.OK {
		t.Fatalf("带 trailer 的提交应判合规: %+v（msg=%q）", v, ms[0].Body)
	}
	ms, err = r.LogGrepAll(ctx, "t_9", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || !strings.Contains(SubjectOf(ms[0].Body), "丙") {
		t.Fatalf("t_9 应命中一条: %+v", ms)
	}
	if _, err := r.LogGrepAll(ctx, "rm -rf", 10); err == nil {
		t.Fatal("非台账号应被拒绝")
	}

	// LogMessages 全量带回完整信息（含 body 的 trailer）。
	ms, err = r.LogMessages(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 5 || !strings.Contains(ms[0].Body, "丙") {
		t.Fatalf("LogMessages 应带回 5 条且最新是丙: %d 条", len(ms))
	}
}

func TestCheckoutAndNewBranch(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	r, err := Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 建分支即切换。
	if err := r.NewBranch(ctx, "feat-y", ""); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.Status(ctx); st.Branch != "feat-y" {
		t.Fatalf("建分支后应停在 feat-y: %+v", st)
	}

	// 切回存在的分支。
	if err := r.Checkout(ctx, "main"); err != nil {
		t.Fatal(err)
	}

	// 脏树 + 分支间文件相异 → git 拒绝，错误文案透传。
	git(t, repo, "checkout", "feat-y")
	write(t, repo, "a.txt", "diverge\n")
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-m", "feat: 分歧 [t_104]")
	git(t, repo, "checkout", "main")
	write(t, repo, "a.txt", "local-edit\n")
	err = r.Checkout(ctx, "feat-y")
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("脏树切换应被拒且错误带 git 原文: %v", err)
	}
	if err := r.Checkout(ctx, "--force"); err == nil {
		t.Fatal("选项形状的分支名应被准入校验挡下")
	}
}

func TestFetch(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	bare := withRemote(t, repo)

	// 另一头克隆推进远端：main 前进一格 + 新分支 feat-z。
	peer := filepath.Join(t.TempDir(), "peer")
	git(t, repo, "clone", bare, peer)
	git(t, peer, "config", "user.email", "peer@niuma.local")
	git(t, peer, "config", "user.name", "同行")
	write(t, peer, "p.txt", "peer\n")
	git(t, peer, "add", ".")
	git(t, peer, "commit", "-m", "feat: 同行推进 [t_105]")
	git(t, peer, "push", "origin", "main")
	git(t, peer, "checkout", "-b", "feat-z")
	write(t, peer, "z.txt", "z\n")
	git(t, peer, "add", ".")
	git(t, peer, "commit", "-m", "feat: 新枝 [t_106]")
	git(t, peer, "push", "origin", "feat-z")

	r, err := Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	has := func(name string) bool {
		bs, err := r.Branches(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bs {
			if b.Name == name {
				return true
			}
		}
		return false
	}
	if has("origin/feat-z") {
		t.Fatal("fetch 前不该看见 origin/feat-z")
	}
	if err := r.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if !has("origin/feat-z") || !has("origin/main") {
		t.Fatal("fetch 后应看见远端新枝与主干")
	}
	st, err := r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Behind != 1 {
		t.Fatalf("fetch 后本地应落后 1: %+v", st)
	}
}

func TestWorkspaceForRoot(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	sub := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Detect(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if got := WorkspaceForRoot(context.Background(), r.Root, []string{t.TempDir(), sub}); got != sub {
		t.Fatalf("应按仓库根匹配到子目录工作区 %q，得到 %q", sub, got)
	}
}

func TestParseStatusHeader(t *testing.T) {
	cases := []struct {
		head             string
		branch, upstream string
		detached         bool
		ahead, behind    int
	}{
		{"## main", "main", "", false, 0, 0},
		{"## main...origin/main", "main", "origin/main", false, 0, 0},
		{"## main...origin/main [ahead 2]", "main", "origin/main", false, 2, 0},
		{"## main...origin/main [behind 3]", "main", "origin/main", false, 0, 3},
		{"## main...origin/main [ahead 2, behind 1]", "main", "origin/main", false, 2, 1},
		{"## HEAD (no branch)", "", "", true, 0, 0},
		{"## No commits yet on trunk", "trunk", "", false, 0, 0},
	}
	for _, c := range cases {
		st := &Status{}
		parseStatusHeader(c.head, st)
		if st.Branch != c.branch || st.Upstream != c.upstream || st.Detached != c.detached ||
			st.Ahead != c.ahead || st.Behind != c.behind {
			t.Fatalf("%q 解析成 %+v，期望 branch=%q upstream=%q detached=%v ahead=%d behind=%d",
				c.head, st, c.branch, c.upstream, c.detached, c.ahead, c.behind)
		}
	}
}

func TestValidRefName(t *testing.T) {
	ok := []string{"main", "feat/x-y", "release-1.2", "r12_login"}
	bad := []string{"", "-force", "--global", "a..b", "lock.lock", "feat/", "带空格 x", "a;b"}
	for _, n := range ok {
		if !ValidRefName(n) {
			t.Fatalf("%q 应合法", n)
		}
	}
	for _, n := range bad {
		if ValidRefName(n) {
			t.Fatalf("%q 应被拒", n)
		}
	}
}

func TestCheckCommit(t *testing.T) {
	cases := []struct {
		name   string
		msg    string
		policy Policy
		merge  bool
		ok     bool
	}{
		{"全合规带双号", "feat(login): 手机号登录\n\n任务: t_103\n需求: r_12", Policy{}, false, true},
		{"标题内引用也算", "fix: 修 r_12 的崩溃", Policy{}, false, true},
		{"缺 type", "随手一提交\n\n任务: t_1", Policy{}, false, false},
		{"type 不在表内", "perf: 提速\n\n任务: t_1", Policy{}, false, false},
		{"自定 type 表", "perf: 提速\n\n任务: t_1", Policy{CommitTypes: []string{"perf"}}, false, true},
		{"缺引用（either）", "feat: 新功能", Policy{}, false, false},
		{"只要任务号——需求号不算数", "feat: 新功能\n\n需求: r_1", Policy{RequireRef: RefTask}, false, false},
		{"只要任务号——命中", "feat: 新功能\n\n任务: t_1", Policy{RequireRef: RefTask}, false, true},
		{"只要需求号——任务号不算数", "feat: 新功能\n\n任务: t_1", Policy{RequireRef: RefReq}, false, false},
		{"关闭引用强制", "feat: 新功能", Policy{RequireRef: RefOff}, false, true},
		{"merge 豁免", "Merge branch 'side'", Policy{}, true, true},
		{"空信息", "", Policy{}, false, false},
		{"需求号整词命中", "feat: x\n\n需求: r_99", Policy{RequireRef: RefReq}, false, true},
	}
	for _, c := range cases {
		v := CheckCommit(c.msg, c.policy, c.merge)
		if v.OK != c.ok {
			t.Fatalf("%s：期望 ok=%v，得到 %+v（issues=%v）", c.name, c.ok, v, v.Issues)
		}
		if !c.ok && len(v.Issues) == 0 {
			t.Fatalf("%s：不合规却没给出 issues", c.name)
		}
	}
}

func TestHookScript(t *testing.T) {
	s := HookScript("/Applications/Niuma Studio.app/Contents/MacOS/niuma")
	for _, want := range []string{`"/Applications/Niuma Studio.app/Contents/MacOS/niuma" vcs check-msg "$1"`, "commit-msg"} {
		if !strings.Contains(s, want) {
			t.Fatalf("钩子脚本缺关键行 %q：\n%s", want, s)
		}
	}
}

func TestWorktrees(t *testing.T) {
	needGit(t)
	repo := newRepo(t)
	r, err := Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	base := t.TempDir()

	// 中文分支名全链路（成员名即分支名）。
	wtX := filepath.Join(base, "小猿")
	if err := r.AddWorktree(ctx, wtX, "wt/小猿", ""); err != nil {
		t.Fatalf("建中文分支工作树: %v", err)
	}
	// 已有分支复挂（不 -b）＋幂等（同路径再 add 直接成功）。
	wtY := filepath.Join(base, "小鹿")
	if err := r.AddWorktree(ctx, wtY, "wt/小鹿", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.AddWorktree(ctx, wtY, "wt/小鹿", ""); err != nil {
		t.Fatalf("重复建树应幂等: %v", err)
	}
	wts, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, wt := range wts {
		got[filepath.Base(wt.Path)] = wt.Branch
	}
	if got["小猿"] != "wt/小猿" || got["小鹿"] != "wt/小鹿" || got[filepath.Base(repo)] != "main" {
		t.Fatalf("工作树清单不合预期: %#v", got)
	}
	// 树各自独立：小猿的树上提交，主树与别的树看不见工作区改动。
	write(t, wtX, "only-x.txt", "x\n")
	st, err := r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Untracked != 0 {
		t.Fatalf("隔离树上新增文件不该漏进主树状态: %+v", st)
	}
	git(t, wtX, "config", "user.email", "x@niuma.local")
	git(t, wtX, "config", "user.name", "小猿")
	git(t, wtX, "add", ".")
	git(t, wtX, "commit", "-m", "feat: 树上开工 [t_1]")
	rX, err := Detect(ctx, wtX)
	if err != nil {
		t.Fatal(err)
	}
	if stX, _ := rX.Status(ctx); stX.Branch != "wt/小猿" {
		t.Fatalf("小猿的树应驻 wt/小猿: %+v", stX)
	}

	// 双树抢同一分支：git 原生拒绝，文案透传。
	wtZ := filepath.Join(base, "小狐")
	if err := r.AddWorktree(ctx, wtZ, "wt/小猿", ""); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("抢分支应被 git 拒: %v", err)
	}

	// 脏树回收被拒；干净树回收成功；重复回收幂等（路径已不在清单，幂
	// 等短路）。
	write(t, wtY, "dirty.txt", "d\n")
	if err := r.RemoveWorktree(ctx, wtY, false); err == nil {
		t.Fatal("脏树回收应被拒")
	}
	if err := r.RemoveWorktree(ctx, wtY, true); err != nil {
		t.Fatalf("强收应成功: %v", err)
	}
	if err := r.RemoveWorktree(ctx, wtY, false); err != nil {
		t.Fatalf("重复回收应幂等: %v", err)
	}
	wts, err = r.Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range wts {
		if wt.Path == wtY {
			t.Fatal("小鹿的树应已移除")
		}
	}
}

func TestValidRefNameUnicode(t *testing.T) {
	ok := []string{"main", "wt/小猿", "分支一", "feat/x-y", "r12_login"}
	bad := []string{"", "-force", "--global", "a..b", "lock.lock", "feat/", "带空格 x", "a;b"}
	for _, n := range ok {
		if !ValidRefName(n) {
			t.Fatalf("%q 应合法（Unicode 放宽后）", n)
		}
	}
	for _, n := range bad {
		if ValidRefName(n) {
			t.Fatalf("%q 应被拒", n)
		}
	}
}
