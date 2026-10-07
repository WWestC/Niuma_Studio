package cli

// vcs.go — 版本管理（v2.7）的成员只读面＋自查面：status/branches/log
// 直接在本地仓库上跑（vcs 包 shell-out，不经服务器——成员会话的 cwd
// 就是自家工作区，开箱即用）；lint 是 commit 规范自查（面板同一套判
// 定）；check-msg 是 commit-msg 钩子的执行体（面板「装提交钩子」写进
// 仓库的脚本调的就是它，读消息文件判规范，非零退出即拦）。绑定与策
// 略从 ~/.niuma/projects.json 反查（仓库根 ↔ 项目工作区匹配）；台账
// 不可达时静默按默认档——查询动作不该被注册表的状态拖垮。

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// runVCS is the vcs family's dispatcher.
func runVCS(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma vcs <status|branches|log|lint|check-msg|post-commit> [--path D] [-n N] [-ref R]")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		return runVCSStatus(rest)
	case "branches":
		return runVCSBranches(rest)
	case "log":
		return runVCSLog(rest)
	case "lint":
		return runVCSLint(rest)
	case "check-msg":
		return runVCSCheckMsg(rest)
	case "post-commit":
		return runVCSPostCommit(rest)
	default:
		fmt.Fprintf(os.Stderr, "vcs: unknown topic %q (status|branches|log|lint|check-msg|post-commit)\n", sub)
		return 2
	}
}

// openRepoAt resolves the repo at path（缺省 cwd）.
func openRepoAt(path string) (*vcs.Repo, error) {
	if path == "" {
		path, _ = os.Getwd()
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return vcs.Detect(context.Background(), abs)
}

// projectContext 反查「仓库根 ↔ 项目」：绑定列与策略的来源。找不到项
// 目（仓库不在工作室台账里）时零值返回，查询照常。
func projectContext(repo *vcs.Repo) (projects.Project, bool) {
	path, err := projects.DefaultPath()
	if err != nil {
		return projects.Project{}, false
	}
	return projectContextAt(path, repo)
}

// projectContextAt is projectContext against an explicit store path（测试
// 注入临时台账，不碰真实 ~/.niuma）。
func projectContextAt(storePath string, repo *vcs.Repo) (projects.Project, bool) {
	store, err := projects.Open(storePath)
	if err != nil {
		return projects.Project{}, false
	}
	for _, p := range store.List() {
		if p.Workspace == "" {
			continue
		}
		if r, err := vcs.Detect(context.Background(), p.Workspace); err == nil && r.Root == repo.Root {
			return p, true
		}
	}
	return projects.Project{}, false
}

func policyOfProject(p projects.Project, ok bool) vcs.Policy {
	if ok && p.GitPolicy != nil {
		return *p.GitPolicy
	}
	return vcs.Policy{}
}

// runVCSStatus prints the workspace's git snapshot（分支/上下游/脏净 +
// 当前绑定 + 生效规范一行）。
func runVCSStatus(args []string) int {
	fs := flag.NewFlagSet("vcs status", flag.ContinueOnError)
	path := fs.String("path", "", "探测起点（缺省当前目录）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, err := openRepoAt(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vcs status: %v\n", err)
		return 1
	}
	ctx := context.Background()
	st, err := repo.Status(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vcs status: %v\n", err)
		return 1
	}
	p, hasProj := projectContext(repo)
	branch := st.Branch
	if branch == "" {
		branch = "（游离 HEAD）"
	}
	fmt.Printf("仓库：%s\n", repo.Root)
	if repo.IsWorktree() {
		fmt.Println("形态：linked worktree（同一仓库的另一棵工作树）")
	}
	fmt.Printf("分支：%s", branch)
	if st.Upstream != "" {
		ab := []string{}
		if st.Ahead > 0 {
			ab = append(ab, fmt.Sprintf("领先 %d", st.Ahead))
		}
		if st.Behind > 0 {
			ab = append(ab, fmt.Sprintf("落后 %d", st.Behind))
		}
		if len(ab) > 0 {
			fmt.Printf("（%s，上游 %s）", strings.Join(ab, "，"), st.Upstream)
		} else {
			fmt.Printf("（与 %s 同步）", st.Upstream)
		}
	}
	fmt.Println()
	fmt.Printf("改动：暂存 %d · 修改 %d · 未跟踪 %d", st.Staged, st.Modified, st.Untracked)
	if st.TrackedDirty() > 0 {
		fmt.Print("（切分支前先提交或 stash）")
	}
	fmt.Println()
	if hasProj {
		if st.Branch != "" {
			if link := p.Link(st.Branch); link != nil {
				bits := []string{}
				if link.Req != "" {
					bits = append(bits, "需求 "+link.Req)
				}
				if link.Version != "" {
					bits = append(bits, "版本 "+link.Version)
				}
				fmt.Printf("绑定：%s\n", strings.Join(bits, " · "))
			}
		}
		pol := policyOfProject(p, hasProj)
		fmt.Printf("规范：type(scope): 摘要 · 引用 %s · 可用 type：%s\n",
			refPolicyLabel(pol), strings.Join(pol.Types(), "|"))
	}
	return 0
}

func refPolicyLabel(p vcs.Policy) string {
	switch p.RequireRefEffective() {
	case vcs.RefTask:
		return "须带任务号 t_NN"
	case vcs.RefReq:
		return "须带需求号 r_NN"
	case vcs.RefOff:
		return "不强制"
	default:
		return "t_NN 或 r_NN 任一"
	}
}

// runVCSBranches lists local then remote branches with bindings.
func runVCSBranches(args []string) int {
	fs := flag.NewFlagSet("vcs branches", flag.ContinueOnError)
	path := fs.String("path", "", "探测起点（缺省当前目录）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, err := openRepoAt(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vcs branches: %v\n", err)
		return 1
	}
	bs, err := repo.Branches(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "vcs branches: %v\n", err)
		return 1
	}
	p, hasProj := projectContext(repo)
	fmt.Printf("%-4s %-28s %-10s %s\n", "", "分支", "归属", "末次提交 / 绑定")
	for _, b := range bs {
		kind := "本地"
		if b.Remote != "" {
			kind = "远端"
		}
		cur := ""
		if b.Current {
			cur = "●"
		}
		last := b.Subject
		if b.Author != "" {
			last += "（" + b.Author
			if b.DateTS > 0 {
				last += " · " + ageOf(b.DateTS)
			}
			last += "）"
		}
		if hasProj {
			if link := p.Link(b.Name); link != nil {
				bits := []string{}
				if link.Req != "" {
					bits = append(bits, link.Req)
				}
				if link.Version != "" {
					bits = append(bits, "版本 "+link.Version)
				}
				last += " ［" + strings.Join(bits, "·") + "］"
			}
		}
		fmt.Printf("%-4s %-28s %-10s %s\n", cur, clip(b.Name, 28), kind, last)
	}
	return 0
}

// runVCSLog prints one ref's history with convention verdicts.
func runVCSLog(args []string) int {
	fs := flag.NewFlagSet("vcs log", flag.ContinueOnError)
	ref := fs.String("ref", "", "分支/ref（缺省 HEAD）")
	n := fs.Int("n", 15, "条数")
	path := fs.String("path", "", "探测起点（缺省当前目录）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, err := openRepoAt(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vcs log: %v\n", err)
		return 1
	}
	msgs, err := repo.LogMessages(context.Background(), *ref, *n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vcs log: %v\n", err)
		return 1
	}
	p, hasProj := projectContext(repo)
	pol := policyOfProject(p, hasProj)
	for _, m := range msgs {
		v := vcs.CheckCommit(m.Body, pol, m.Parents >= 2)
		mark := "✓"
		if !v.OK {
			mark = "✗ " + strings.Join(v.Issues, "；")
		}
		fmt.Printf("%s  %s  %s（%s · %s）\n", m.SHA[:min(7, len(m.SHA))], mark,
			vcs.SubjectOf(m.Body), m.Author, ageOf(m.DateTS))
	}
	return 0
}

// runVCSLint self-checks the current branch's recent commits（成员自查）。
func runVCSLint(args []string) int {
	fs := flag.NewFlagSet("vcs lint", flag.ContinueOnError)
	n := fs.Int("n", 15, "检查最近 N 条")
	path := fs.String("path", "", "探测起点（缺省当前目录）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, err := openRepoAt(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vcs lint: %v\n", err)
		return 1
	}
	msgs, err := repo.LogMessages(context.Background(), "", *n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vcs lint: %v\n", err)
		return 1
	}
	p, hasProj := projectContext(repo)
	pol := policyOfProject(p, hasProj)
	bad := 0
	for _, m := range msgs {
		v := vcs.CheckCommit(m.Body, pol, m.Parents >= 2)
		if v.OK {
			continue
		}
		bad++
		fmt.Printf("✗ %s %s\n  %s\n", m.SHA[:min(7, len(m.SHA))], vcs.SubjectOf(m.Body),
			strings.Join(v.Issues, "；"))
	}
	if bad == 0 {
		fmt.Printf("近 %d 条提交全合规（引用 %s）\n", len(msgs), refPolicyLabel(pol))
		return 0
	}
	fmt.Printf("共 %d/%d 条不合规范——补提交时按 type(scope): 摘要 带上 t_NN/r_NN\n", bad, len(msgs))
	return 1
}

// runVCSCheckMsg is the commit-msg hook's body: 读消息文件判规范，非零
// 退出即拦（钩子脚本 exec 的就是这一句）。merge 豁免（v2.8 gitflow 修
// 正）：MERGE_HEAD 可解析＝合并进行中，message 是 git 拼的——放行；
// 否则装了钩子的仓库里 git merge 会被「缺 type 前缀」误拦。
func runVCSCheckMsg(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: niuma vcs check-msg <file>（commit-msg 钩子的执行体）")
		return 2
	}
	body, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "vcs check-msg: %v\n", err)
		return 1
	}
	wd, _ := os.Getwd()
	repo, rerr := vcs.Detect(context.Background(), wd)
	var pol vcs.Policy
	isMerge := false
	if rerr == nil {
		p, hasProj := projectContext(repo)
		pol = policyOfProject(p, hasProj)
		isMerge = repo.MergeHeadLive(context.Background())
	}
	v := vcs.CheckCommit(string(body), pol, isMerge)
	if v.OK {
		return 0
	}
	fmt.Fprintln(os.Stderr, "提交信息不合工作室规范：")
	for _, issue := range v.Issues {
		fmt.Fprintf(os.Stderr, "  · %s\n", issue)
	}
	fmt.Fprintln(os.Stderr, "模板：type(scope): 一句话摘要 —— 正文附 任务: t_NN 或 需求: r_NN")
	return 1
}

// runVCSPostCommit is the post-commit hook's body（提交播报，v2.8
// gitflow）: 把刚落库的这笔提交报给工作室——POST /internal/git/commit，
// 房间对话流出一张提交卡。post-commit 的退出码 git 本就不看，这里对
// 一切失败也静默退 0（工作室没开/仓库不在台账/端口探不到——播报是锦
// 上添花，绝不给提交添堵）；成功同样一字不出（钩子的 stdout 会混进
// git commit 自己的输出）。
func runVCSPostCommit(args []string) int {
	wd, _ := os.Getwd()
	repo, err := openRepoAt(wd)
	if err != nil {
		return 0
	}
	sha, err := repo.HeadSHA(context.Background())
	if err != nil {
		return 0
	}
	port, err := server.DiscoverPort()
	if err != nil {
		return 0
	}
	body, _ := json.Marshal(map[string]string{"sha": sha, "root": repo.CommonDir})
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/internal/git/commit", port),
		"application/json", bytes.NewReader(body))
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return 0
}

func ageOf(ts int64) string {
	d := time.Since(time.Unix(ts, 0)).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
