package vcs

// flow_test.go — v2.8 gitflow 新原语的真 git 钉子：Show（消息＋逐文件
// numstat）、AheadCount/MergedInto/LogRange 的三方对账、MergeNoFF 的
// 落地与冲突还原（半合并状态绝不外泄）、AdditiveOnly 的纯新增判定、
// HeadSHA/MergeHeadLive、PostHookScript 渲染。git.go 的夹具（needGit/
// git/write）全复用。

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// flowRepo 造一棵带分叉的树：main 上 C1，feat 分支上 C2（改 a.txt＋新
// 增 c.txt），main 上再 C3（改 a.txt 另一处）—— Ahead=1/冲突可控。
func flowRepo(t *testing.T) string {
	t.Helper()
	needGit(t)
	dir := newRepo(t) // main: 两笔提交（t_100/t_101），已建 feat-x
	git(t, dir, "switch", "-c", "feat-flow")
	write(t, dir, "a.txt", "one\nflow 改动\n")
	write(t, dir, "c.txt", "新增文件\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat(flow): 分支上的一步\n\n任务: t_102")
	git(t, dir, "switch", "main")
	write(t, dir, "b.txt", "two\nmain 又动了 b\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: main 自己的演进\n\n任务: t_103")
	return dir
}

func TestFlowShowReadsMessageAndFiles(t *testing.T) {
	needGit(t)
	dir := newRepo(t)
	r, err := Detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := r.HeadSHA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sha) != 40 {
		t.Fatalf("HeadSHA 应给全长：%q", sha)
	}
	msg, files, err := r.Show(context.Background(), sha)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Body, "t_101") {
		t.Fatalf("Show 应带完整消息：%q", msg.Body)
	}
	if SubjectOf(msg.Body) != "feat: 第二步" {
		t.Fatalf("标题不合预期：%q", SubjectOf(msg.Body))
	}
	found := false
	for _, f := range files {
		if f.Path == "b.txt" && f.Added == 1 && f.Removed == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("b.txt 的 +1 行应在 numstat 里：%+v", files)
	}
	if _, _, err := r.Show(context.Background(), "not-a-sha!!"); err == nil {
		t.Fatal("脏 sha 应被拒")
	}
}

func TestFlowAheadMergedRange(t *testing.T) {
	dir := flowRepo(t)
	r, err := Detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if n, err := r.AheadCount(ctx, "main", "feat-flow"); err != nil || n != 1 {
		t.Fatalf("feat-flow 领先 main 1 笔，得 %d err=%v", n, err)
	}
	if n, err := r.AheadCount(ctx, "feat-flow", "main"); err != nil || n != 1 {
		t.Fatalf("main 也领先 feat-flow 1 笔（分叉），得 %d err=%v", n, err)
	}
	if m, err := r.MergedInto(ctx, "feat-flow", "main"); err != nil || m {
		t.Fatalf("feat-flow 未并入 main，得 %v err=%v", m, err)
	}
	msgs, err := r.LogRange(ctx, "main", "feat-flow", 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("范围 main..feat-flow 应恰 1 笔，得 %d err=%v", len(msgs), err)
	} else if !strings.Contains(msgs[0].Body, "t_102") {
		t.Fatalf("范围里应是 t_102 那笔：%q", msgs[0].Body)
	}
}

func TestFlowMergeNoFFAndAdditive(t *testing.T) {
	dir := flowRepo(t)
	r, err := Detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// feat-flow 改了 a.txt（main 侧没再动 a）→ 非纯新增
	if add, err := r.AdditiveOnly(ctx, "main", "feat-flow"); err != nil || add {
		t.Fatalf("改已有文件的分支不算纯新增，得 %v err=%v", add, err)
	}
	if err := r.MergeNoFF(ctx, "feat-flow", "merge: feat-flow → main（m_01，t_102）"); err != nil {
		t.Fatalf("合并不应失败：%v", err)
	}
	if m, err := r.MergedInto(ctx, "feat-flow", "main"); err != nil || !m {
		t.Fatalf("合并后 feat-flow 应已并入 main，得 %v err=%v", m, err)
	}
	out := git(t, dir, "log", "-1", "--format=%P%n%s")
	if !strings.Contains(out, " ") {
		t.Fatalf("应是 merge 节点（两枚父提交）：%q", out)
	}
	if !strings.Contains(out, "m_01") {
		t.Fatalf("合并消息应带提案号：%q", out)
	}
	if r.MergeHeadLive(ctx) {
		t.Fatal("完成的 merge 后 MERGE_HEAD 应已清（判定为 false）")
	}
}

func TestFlowMergeConflictAbortsClean(t *testing.T) {
	needGit(t)
	dir := newRepo(t)
	ctx := context.Background()
	// 两边改同一行 → 冲突
	git(t, dir, "switch", "-c", "feat-conflict")
	write(t, dir, "a.txt", "branch side\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: 分支侧同文件\n\n任务: t_110")
	git(t, dir, "switch", "main")
	write(t, dir, "a.txt", "main side\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: 主线侧同文件\n\n任务: t_111")
	r, err := Detect(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	err = r.MergeNoFF(ctx, "feat-conflict", "merge: 必冲突")
	if err == nil || !strings.Contains(err.Error(), "已还原现场") {
		t.Fatalf("冲突应如实报错并还原，得 %v", err)
	}
	// 树必须干净且无合并进行中（半合并状态绝不外泄）
	if _, serr := r.Status(ctx); serr != nil {
		t.Fatalf("还原后 status 应可读：%v", serr)
	}
	if r.MergeHeadLive(ctx) {
		t.Fatal("abort 后 MERGE_HEAD 不应残留")
	}
	st := git(t, dir, "status", "--porcelain")
	if strings.TrimSpace(st) != "" {
		t.Fatalf("还原后树应干净，得 %q", st)
	}
	if m, _ := r.MergedInto(ctx, "feat-conflict", "main"); m {
		t.Fatal("冲突分支不应被并入")
	}
}

func TestFlowAdditiveOnlyPureAdds(t *testing.T) {
	needGit(t)
	dir := newRepo(t)
	ctx := context.Background()
	git(t, dir, "switch", "-c", "feat-add")
	write(t, dir, "new1.txt", "纯新增\n")
	write(t, dir, "new2.txt", "也是新增\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: 只加新文件\n\n任务: t_120")
	git(t, dir, "switch", "main")
	r, err := Detect(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if add, err := r.AdditiveOnly(ctx, "main", "feat-add"); err != nil || !add {
		t.Fatalf("纯新增分支应判 true，得 %v err=%v", add, err)
	}
	// 无差异的分支不算「纯新增」（无事可并）
	if add, err := r.AdditiveOnly(ctx, "main", "main"); err != nil || add {
		t.Fatalf("无差异不算纯新增，得 %v err=%v", add, err)
	}
}

func TestFlowDiffNumStatThreeDot(t *testing.T) {
	dir := flowRepo(t)
	r, err := Detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := r.DiffNumStat(context.Background(), "main", "feat-flow")
	if err != nil {
		t.Fatal(err)
	}
	var add, del int
	sawC := false
	for _, f := range rows {
		add += f.Added
		del += f.Removed
		if f.Path == "c.txt" {
			sawC = true
		}
	}
	if !sawC || add == 0 {
		t.Fatalf("三点差异应含分支新增的 c.txt 与行数：%+v", rows)
	}
	_ = del
	if _, err := r.DiffNumStat(context.Background(), "main", "bad..ref"); err == nil {
		t.Fatal("脏 ref 应被拒（注入防线）")
	}
}

func TestFlowMergeHeadLiveDuringMerge(t *testing.T) {
	needGit(t)
	dir := newRepo(t)
	ctx := context.Background()
	// 两边改同一行，merge 卡在冲突里——MERGE_HEAD 存在的正是这个窗口
	// （commit-msg 钩子的 merge 豁免就吃这个判定）。
	git(t, dir, "switch", "-c", "feat-mh")
	write(t, dir, "a.txt", "branch\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: 分支侧\n\n任务: t_130")
	git(t, dir, "switch", "main")
	write(t, dir, "a.txt", "main\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: 主线侧\n\n任务: t_131")
	r, err := Detect(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if out := gitExpectFail(t, dir, "merge", "--no-ff", "-m", "merge: 必冲突", "feat-mh"); !strings.Contains(out, "CONFLICT") {
		t.Fatalf("本用例需要一次真冲突，得 %q", out)
	}
	if !r.MergeHeadLive(ctx) {
		t.Fatal("冲突卡住的 merge 进行中，MERGE_HEAD 应可解析")
	}
	git(t, dir, "merge", "--abort")
	if r.MergeHeadLive(ctx) {
		t.Fatal("abort 后 MERGE_HEAD 不应残留")
	}
}

// gitExpectFail runs one git command that MUST fail, returning its output
// （冲突这类「失败是剧情」的场景——git 夹具的失败即Fatal 版反面）。
func gitExpectFail(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err == nil {
		t.Fatalf("git %v（in %s）应失败却成功：%s", args, dir, out.String())
	}
	return out.String()
}

func TestPostHookScriptRenders(t *testing.T) {
	s := PostHookScript("/opt/bin/niuma")
	if !strings.Contains(s, "/opt/bin/niuma") || !strings.Contains(s, "vcs post-commit") {
		t.Fatalf("钩子脚本应 exec 本程序 vcs post-commit：%q", s)
	}
	if !strings.Contains(s, "Niuma Studio post-commit") {
		t.Fatal("钩子须带自家标识（占用位判定靠它）")
	}
}
