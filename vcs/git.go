// Package vcs — 版本管理（v2.7）的 git 半场：工作室不引 go-git，一
// 切经 git CLI 子进程（util/rebuild.go 的 exec 纪律同源——PATH 优先、
// 常见安装位兜底、超时兜墙钟、拒绝文案原文透传）。项目按 Workspace
// 向上探测所属仓库（子目录工作区天然支持，rev-parse 自会向上找
// .git），不同项目各归各仓；分支/状态/提交全走机器可读格式
// （for-each-ref / porcelain / 自定义分隔符），解析绝不猜。
package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
)

// ErrNotRepo 是「探测起点不是 git 仓库」的诚实回执：上层据此渲染空
// 态卡（该工作区还能 git init 是房主的事，工作室不越权代建）。
var ErrNotRepo = errors.New("不是 git 仓库（该路径及其上层都没有 .git）")

// 三档超时兜住三类命令的墙钟：读是本地快照操作（大仓库 20 秒烧不完
// 就该怀疑仓库异常）；fetch 要走网络（含大仓库首拉，3 分钟）；切/建
// 分支动工作区文件（60 秒绰绰有余）。超时文案与 rebuild 同款直说。
const (
	readTimeout   = 20 * time.Second
	fetchTimeout  = 3 * time.Minute
	switchTimeout = time.Minute
)

// gitInstallCandidates 是 shell PATH 之外按序探测的 git 安装位——
// GUI 双击启动只拿 LaunchServices 最小 PATH（/usr/bin:/bin:…），终端
// 里装在 Homebrew 位的 git 不在其中（findGoTool 同款兜底理由）。
var gitInstallCandidates = []string{
	"/usr/bin/git",                           // macOS Xcode CLT shim（最小 PATH 里就有它）
	"/opt/homebrew/bin/git",                  // Homebrew（Apple Silicon）
	"/usr/local/bin/git",                     // Homebrew（Intel）或手动软链
	`C:\Program Files\Git\cmd\git.exe`,       // Git for Windows（64 位）
	`C:\Program Files (x86)\Git\cmd\git.exe`, // Git for Windows（32 位）
}

// findGitTool locates the git command: PATH first（终端启动一切如
// 常），then the common install spots above（双击 .app 的最小 PATH 兜
// 底）。
func findGitTool() (string, error) {
	p, lookErr := exec.LookPath("git")
	if lookErr == nil {
		return p, nil
	}
	for _, p := range gitInstallCandidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", &NoGitError{Err: lookErr}
}

// GitError 是 runGit 失败的统一回执。此前 detail 拼接用 %s 把错误链
// 切断，上层只能 grep 错误字符串分诊；现在 Op/Detail/Timeout 是字段，
// server 层据此分流（超时 504、凭据指引、其余照原文透传）——文案
// 逐字保持旧形，已有的契约测试与前端匹配不受影响。
type GitError struct {
	Op      string        // 子命令（args[0]）
	Detail  string        // git 的拒绝文案（stderr，截尾 8KB；可为空）
	Timeout bool          // 墙钟超时（仓库过大或网络卡住）
	Auth    bool          // 凭据/权限类拒绝（服务端分流 401，探测行亮「需凭据」；authReject 的判定）
	Span    time.Duration // 超时档位（Timeout 时用于文案）
	Err     error         // 底层 exec 错误（Detail 为空时是唯一原因）
}

func (e *GitError) Error() string {
	switch {
	case e.Timeout:
		return i18n.Sf("git %s 超时（%s）——仓库过大或网络卡住，稍后再试", e.Op, e.Span)
	case e.Detail == "":
		return i18n.Sf("git %s 失败: %v", e.Op, e.Err)
	default:
		return i18n.Sf("git %s 失败：%s%s", e.Op, e.Detail, e.hint())
	}
}

func (e *GitError) Unwrap() error { return e.Err }

// hint 凭据类拒绝补一句出路：GIT_TERMINAL_PROMPT=0 让缺凭据立刻失
// 败，这是 fetch 最常见的拒绝——git 自己的文案照抄，出路附在后面。
// 判定与 Auth 字段同源（authReject），两处永不各说各话。
func (e *GitError) hint() string {
	if e.Auth {
		return AuthHint()
	}
	return ""
}

// runGit executes one git command in dir and returns stdout. 拒绝时把
// git 的输出（截尾 8KB）拼进错误原文带回——切分支被脏树挡下、远端
// 需要凭据这类事，git 自己的文案比任何转述都准。
func runGit(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	git, err := findGitTool()
	if err != nil {
		return "", err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	full := append([]string{"-c", "core.quotePath=false"}, args...)
	cmd := util.HideConsole(exec.CommandContext(ctx, git, full...))
	cmd.Dir = dir
	cmd.Env = gitEnv(os.Environ(), filepath.Dir(git))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", &GitError{Op: args[0], Timeout: true, Span: timeout}
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if len(detail) > 8<<10 {
			detail = i18n.S("……（前文截断）\n") + detail[len(detail)-8<<10:]
		}
		return "", &GitError{Op: args[0], Detail: detail, Auth: authReject(detail), Err: err}
	}
	return stdout.String(), nil
}

// gitEnv shapes the subprocess environment: LC_ALL=C 钉死输出语种
// （ErrNotRepo 的判定靠英文 fatal 文案，本地化会让它失明）、
// GIT_TERMINAL_PROMPT=0 让缺凭据的 fetch 立刻失败而不是挂着等终端
// 输入（GUI 进程没有终端可给）、git 自身目录前置进 PATH（fetch 走
// ssh 时 git 的子进程还要找得到路）。
func gitEnv(base []string, gitDir string) []string {
	env := make([]string, 0, len(base)+4)
	sawPath := false
	for _, kv := range base {
		switch {
		case strings.HasPrefix(kv, "LC_ALL="), strings.HasPrefix(kv, "GIT_TERMINAL_PROMPT="):
			continue // 后写的才作数
		case gitDir != "" && strings.HasPrefix(kv, "PATH="):
			sawPath = true
			kv = "PATH=" + gitDir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
		env = append(env, kv)
	}
	env = append(env, "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	if gitDir != "" && !sawPath {
		env = append(env, "PATH="+gitDir)
	}
	return env
}

// refPattern 是分支/ref 名的准入形状：任意文字系统的字母/数字开头
// （首字符不可能是 "-"，防选项注入的防线就在这一处），身体只许常规
// 安全字符——v2.7.1 起放宽到 Unicode：成员名是中文，默认分支
// wt/小猿 必须全链路可用。Git 自己还会做 check-ref-format 级校验，
// 这里只求把明显的脏输入挡在子进程之外。
var refPattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._\-/]*$`)

// ValidRefName reports whether a branch/ref name is safe to hand to git.
func ValidRefName(name string) bool {
	return refPattern.MatchString(name) &&
		!strings.Contains(name, "..") &&
		!strings.HasSuffix(name, ".lock") &&
		!strings.HasSuffix(name, "/")
}

// RefNameError is the uniform "illegal ref name" rejection（此前 vcs /
// projects / server 各自 fmt.Errorf、措辞漂移——staffing 版是唯一带
// 规则的）。kind 是措辞位（分支名 / ref 名 / 起点 ref），规则统一教
// 给用户与成员：什么样的名字合法，错在哪。
func RefNameError(kind, name string) error {
	return errors.New(i18n.Sf("非法%s: %q（字母/数字开头，可含字母、数字与 . _ - /，不含 ..，不以 / 或 .lock 结尾）", kind, name))
}

// Worktree is one linked worktree row（`git worktree list --porcelain`
// 的一块）。Branch 为空表示游离 HEAD 的树。
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
}

// Worktrees lists every worktree of the repository（主树在前，git 自
// 己的输出顺序）。
func (r *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := runGit(ctx, r.Workspace, readTimeout, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	cur := Worktree{}
	flush := func() {
		if cur.Path != "" {
			wts = append(wts, cur)
		}
		cur = Worktree{}
	}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		switch {
		case ln == "":
			flush()
		case strings.HasPrefix(ln, "worktree "):
			flush()
			cur.Path = strings.TrimSpace(strings.TrimPrefix(ln, "worktree "))
		case strings.HasPrefix(ln, "branch "):
			ref := strings.TrimSpace(strings.TrimPrefix(ln, "branch "))
			cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case ln == "detached":
			cur.Branch = ""
		}
	}
	flush()
	return wts, nil
}

// AddWorktree creates (or adopts) the linked worktree at path——分支隔
// 离（v2.7.1 按成员工作树）的落树原语。幂等：path 已是本仓库的工作
// 树直接成功（驻在哪个分支是调用方随后 switch 的事）。branch 非空
// 时：分支已存在→挂到它；不存在→-b 自 base（空=HEAD）新建。分支被
// 他树检出时 git 原生拒绝，文案透传。大仓库首建要做一份 checkout，
// 超时与 fetch 同档。
func (r *Repo) AddWorktree(ctx context.Context, path, branch, base string) error {
	wts, err := r.Worktrees(ctx)
	if err != nil {
		return err
	}
	for _, wt := range wts {
		if samePath(wt.Path, path) {
			return nil // 已是工作树：幂等成功
		}
	}
	args := []string{"worktree", "add"}
	switch {
	case branch == "":
		args = append(args, path)
	default:
		if !ValidRefName(branch) {
			return RefNameError(i18n.S("分支名"), branch)
		}
		exists := false
		if bs, err := r.Branches(ctx); err == nil {
			for _, b := range bs {
				if b.Remote == "" && b.Name == branch {
					exists = true
					break
				}
			}
		}
		if exists {
			args = append(args, path, branch)
		} else {
			args = append(args, "-b", branch, path)
			if base != "" {
				if !ValidRefName(base) {
					return RefNameError(i18n.S("起点 ref"), base)
				}
				args = append(args, base)
			}
		}
	}
	return runErr(runGit(ctx, r.Workspace, fetchTimeout, args...))
}

// RemoveWorktree tears one linked worktree down（面板「回收」的执行
// 体）。幂等：path 已不在工作树清单＝早收过了，直接成功。树有未提
// 交改动时 git 拒绝——force 是房主二次确认后的显式推翻，文案照实透
// 传。
func (r *Repo) RemoveWorktree(ctx context.Context, path string, force bool) error {
	wts, err := r.Worktrees(ctx)
	if err != nil {
		return err
	}
	gone := true
	for _, wt := range wts {
		if samePath(wt.Path, path) {
			gone = false
			break
		}
	}
	if gone {
		return nil
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	return runErr(runGit(ctx, r.Workspace, fetchTimeout, args...))
}

// Repo is a git repository discovered at or above a workspace path.
type Repo struct {
	Workspace string `json:"workspace"`  // 探测起点（项目工作区，可能在仓库子目录）
	Root      string `json:"root"`       // 仓库根（rev-parse --show-toplevel）
	GitDir    string `json:"git_dir"`    // 本工作树的 .git（worktree 时指私有 git dir）
	CommonDir string `json:"common_dir"` // 公共 git dir（worktree 时 != GitDir）
}

// IsWorktree reports whether the workspace lives in a linked worktree
// （同一仓库多棵工作树各挂各的分支——多项目共仓的常见姿势）。
func (r *Repo) IsWorktree() bool { return r.GitDir != r.CommonDir }

// Detect resolves the repository at or above workspace. 子目录工作区
// 天然支持；--git-common-dir 相对路径按工作区目录补全（git 在仓库根
// 才会给相对的 ".git"）。
func Detect(ctx context.Context, workspace string) (*Repo, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New(i18n.S("项目 workspace 未设置——请到项目设置里补上工作区路径再试"))
	}
	if _, err := os.Stat(workspace); err != nil {
		return nil, errors.New(i18n.Sf("workspace 不可访问: %v——请到项目设置核对 workspace 路径", err))
	}
	out, err := runGit(ctx, workspace, readTimeout,
		"rev-parse", "--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return nil, ErrNotRepo
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		return nil, errors.New(i18n.Sf("git rev-parse 只回了 %d 行（预期 3 行）——git 版本可能过旧或输出被改写，请升级 git 或反馈此问题", len(lines)))
	}
	root := strings.TrimSpace(lines[0])
	gitDir := strings.TrimSpace(lines[1])
	common := strings.TrimSpace(lines[2])
	if root == "" || gitDir == "" {
		return nil, errors.New(i18n.Sf("git rev-parse 输出无法解读（%q）——请升级 git 或反馈此问题", strings.TrimSpace(out)))
	}
	if !filepath.IsAbs(common) {
		// 相对形状（".git"、"../.git"）锚在探测起点上；随后统一过
		// EvalSymlinks——--absolute-git-dir 给的是穿过软链的真身（macOS
		// 的 /var→/private/var），不归一的话同一仓库会被误报成 worktree。
		common = filepath.Join(workspace, common)
	}
	return &Repo{
		Workspace: workspace,
		Root:      root,
		GitDir:    realpath(gitDir),
		CommonDir: realpath(common),
	}, nil
}

// realpath resolves symlinks when it can (失败原样返回——路径还在但
// 临时不可达时不做戏)。
func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// samePath compares two paths through symlinks——macOS 的 /var 是
// /private/var 的软链，git 报真身、调用方常持软链形状，直接字符串比
// 较会把同一棵树当成两棵。
func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b) || realpath(a) == realpath(b)
}

// SamePath is samePath's exported face（v2.14 立项引导的红线判定要用：
// 「仓库根恰是工作区」必须穿过软链比，否则同一棵树会被误判成外层仓
// 库，自动引导整档误停）。
func SamePath(a, b string) bool { return samePath(a, b) }

// Branch is one ref row（本地或远端跟踪）。Subject 挂末次提交，是分
// 支表的「最近动静」列。
type Branch struct {
	Name    string `json:"name"` // 展示名：main（本地）或 origin/feat-x（远端）
	Remote  string `json:"remote,omitempty"`
	SHA     string `json:"sha"`
	Subject string `json:"subject,omitempty"`
	Author  string `json:"author,omitempty"`
	DateTS  int64  `json:"date_ts,omitempty"`
	Current bool   `json:"current,omitempty"` // HEAD 所指（只可能落在本地分支上）
}

// Branches lists local then remote-tracking branches（各组内按名排序；
// origin/HEAD 这类 symref 重复行跳过）。SHA 给全长——提交图要把分支
// 徽章贴到 HEAD 提交行上，与 log 的 %H 全长对账（短缩写做键会错位）。
func (r *Repo) Branches(ctx context.Context) ([]Branch, error) {
	out, err := runGit(ctx, r.Workspace, readTimeout, "for-each-ref",
		"--format="+strings.Join([]string{
			"%(refname)", "%(objectname)", "%(authorname)",
			"%(committerdate:unix)", "%(contents:subject)", "%(HEAD)",
		}, "\t"),
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var local, remote []Branch
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		f := strings.Split(ln, "\t")
		if len(f) < 6 {
			continue // 格式外的行不猜
		}
		ref, sha, author, date, subject, head := f[0], f[1], f[2], f[3], f[4], strings.TrimSpace(f[5])
		ts, _ := strconv.ParseInt(strings.TrimSpace(date), 10, 64)
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			local = append(local, Branch{
				Name: strings.TrimPrefix(ref, "refs/heads/"), SHA: sha,
				Author: author, DateTS: ts, Subject: subject, Current: head == "*",
			})
		case strings.HasPrefix(ref, "refs/remotes/"):
			name := strings.TrimPrefix(ref, "refs/remotes/")
			if strings.HasSuffix(name, "/HEAD") {
				continue // 远端默认分支指针，重复行
			}
			remoteName := name
			if i := strings.Index(name, "/"); i > 0 {
				remoteName = name[:i]
			}
			remote = append(remote, Branch{
				Name: name, Remote: remoteName, SHA: sha,
				Author: author, DateTS: ts, Subject: subject,
			})
		}
	}
	sortBranches := func(bs []Branch) {
		sort.Slice(bs, func(i, j int) bool { return bs[i].Name < bs[j].Name })
	}
	sortBranches(local)
	sortBranches(remote)
	return append(local, remote...), nil
}

// Status is the working-tree snapshot. Modified+Staged 是「被跟文件的
// 未提交改动」——切分支护栏盯的就是它（untracked 不挡 git switch，
// 单列不掺和）。
type Status struct {
	Branch    string `json:"branch"` // 当前分支；游离 HEAD 时为空
	Detached  bool   `json:"detached,omitempty"`
	Upstream  string `json:"upstream,omitempty"` // 如 origin/main
	Ahead     int    `json:"ahead,omitempty"`
	Behind    int    `json:"behind,omitempty"`
	Staged    int    `json:"staged,omitempty"`
	Modified  int    `json:"modified,omitempty"`
	Untracked int    `json:"untracked,omitempty"`
}

// TrackedDirty is the checkout guard's number: staged + unstaged
// changes to tracked files.
func (s Status) TrackedDirty() int { return s.Staged + s.Modified }

// Status reads `git status --porcelain=v1 -b`. 头行解析覆盖四种形状：
// `## main`、`## main...origin/main [ahead 2, behind 1]`、
// `## HEAD (no branch)`、`## No commits yet on main`。
func (r *Repo) Status(ctx context.Context) (*Status, error) {
	out, err := runGit(ctx, r.Workspace, readTimeout, "status", "--porcelain=v1", "-b")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(out, "\n")
	st := &Status{}
	if len(lines) > 0 {
		parseStatusHeader(strings.TrimSpace(lines[0]), st)
	}
	for _, ln := range lines[1:] {
		if len(ln) < 2 {
			continue
		}
		x, y := ln[0], ln[1]
		switch {
		case x == '?' && y == '?':
			st.Untracked++
		default:
			if x != ' ' {
				st.Staged++
			}
			if y != ' ' {
				st.Modified++
			}
		}
	}
	return st, nil
}

// parseStatusHeader decodes the `## ` branch line into st.
func parseStatusHeader(head string, st *Status) {
	if !strings.HasPrefix(head, "## ") {
		return
	}
	body := strings.TrimPrefix(head, "## ")
	if strings.HasPrefix(body, "No commits yet on ") {
		st.Branch = strings.TrimPrefix(body, "No commits yet on ")
		return
	}
	if i := strings.Index(body, "..."); i >= 0 {
		st.Branch, body = body[:i], body[i+3:]
		if j := strings.Index(body, " ["); j >= 0 {
			parseAheadBehind(body[j+2:], st)
			body = body[:j]
		}
		st.Upstream = body
		return
	}
	if strings.HasPrefix(body, "HEAD (no branch)") {
		st.Detached = true
		return
	}
	st.Branch = body
}

// parseAheadBehind decodes "ahead 2, behind 1]" tails.
func parseAheadBehind(s string, st *Status) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "]")
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "ahead "):
			st.Ahead, _ = strconv.Atoi(strings.TrimPrefix(part, "ahead "))
		case strings.HasPrefix(part, "behind "):
			st.Behind, _ = strconv.Atoi(strings.TrimPrefix(part, "behind "))
		}
	}
}

// Commit is one log row. Parents 供 merge 豁免判定（两个父提交即
// merge，commit 规范放行）。
type Commit struct {
	SHA     string `json:"sha"`
	Author  string `json:"author"`
	DateTS  int64  `json:"date_ts"`
	Subject string `json:"subject"`
	Parents int    `json:"parents,omitempty"`
}

// IsMerge reports the merge-commit exemption.
func (c Commit) IsMerge() bool { return c.Parents >= 2 }

// logFormat is the machine-readable log line: unit-separator fields
// （标题里不会出现 \x1f）。
const logFormat = "%H%x1f%an%x1f%at%x1f%s%x1f%P"

// msgLogFormat carries the FULL message (%B)：规范判定要读 trailer，
// 光标题不够。%B 自带换行，提交之间用 \x1e 记录分隔符切开。
const msgLogFormat = "%H%x1f%an%x1f%at%x1f%P%x1f%B%x1e"

// CommitMsg is one log row with its full message (subject + body).
// ParentSHAs 是父提交全名列表（提交图连线的数据源；根提交为空），
// Parents 数量口径保留——merge 豁免判定用。
type CommitMsg struct {
	SHA        string
	Author     string
	DateTS     int64
	Parents    int
	ParentSHAs []string
	Body       string // 完整提交信息（首行即标题）
}

// SubjectOf pulls the subject line out of a full commit message.
func SubjectOf(body string) string {
	s := strings.TrimSpace(body)
	if i := strings.Index(s, "\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// Log lists up to n commits reachable from ref（"" = HEAD）。解析不到
// 的行丢弃，绝不猜。
func (r *Repo) Log(ctx context.Context, ref string, n int) ([]Commit, error) {
	if n <= 0 {
		n = 30
	}
	args := []string{"log", fmt.Sprintf("-%d", n), "--format=" + logFormat}
	if ref != "" {
		if !ValidRefName(ref) {
			return nil, RefNameError(i18n.S("ref 名"), ref)
		}
		args = append(args, ref)
	}
	return parseLog(runGit(ctx, r.Workspace, readTimeout, args...))
}

// LogMessages is Log with full messages — 规范判定的数据源（面板合规
// 统计/提交历史徽章/CLI lint 共用）。
func (r *Repo) LogMessages(ctx context.Context, ref string, n int) ([]CommitMsg, error) {
	if n <= 0 {
		n = 30
	}
	args := []string{"log", fmt.Sprintf("-%d", n), "--format=" + msgLogFormat}
	if ref != "" {
		if !ValidRefName(ref) {
			return nil, RefNameError(i18n.S("ref 名"), ref)
		}
		args = append(args, ref)
	}
	return parseLogMessages(runGit(ctx, r.Workspace, readTimeout, args...))
}

// LogGrepAll 反查「提及某台账号的提交」（需求视图的数据源）：token 是
// 台账号（t_103 / r_12），整词匹配（r_12 不命中 r_123——扩展正则 +
// 非数字边界），--all 扫全部分支引用（git 按 commit 去重，同一提交挂
// 多枝只出一次）。
func (r *Repo) LogGrepAll(ctx context.Context, token string, n int) ([]CommitMsg, error) {
	if !regexp.MustCompile(`^[a-z]+_[0-9]+$`).MatchString(token) {
		return nil, errors.New(i18n.Sf("非法台账号: %q（形如 r_12 / t_103）", token))
	}
	if n <= 0 {
		n = 30
	}
	pattern := token + "([^0-9]|$)"
	return parseLogMessages(runGit(ctx, r.Workspace, readTimeout,
		"log", fmt.Sprintf("-%d", n), "--all", "--extended-regexp", "--grep="+pattern,
		"--format="+msgLogFormat))
}

// LogGraph is the commit-graph data source (GitLens 式树形提交图的
// 半场)：`--all --topo-order` 扫全部分支引用（含 HEAD），拓扑序保证
// 子前父后、同一条线聚拢不插队——前端 lane 布局依赖这个次序假设。
func (r *Repo) LogGraph(ctx context.Context, n int) ([]CommitMsg, error) {
	if n <= 0 {
		n = 50
	}
	return parseLogMessages(runGit(ctx, r.Workspace, readTimeout,
		"log", fmt.Sprintf("-%d", n), "--all", "--topo-order",
		"--format="+msgLogFormat))
}

// parseLogMessages splits msgLogFormat output into rows. Body 字段按
// \x1f 切会碎（%B 理论上可含 \x1f——荒谬但廉价防守），把第 5 列之后
// 的碎片重新拼回。
func parseLogMessages(out string, err error) ([]CommitMsg, error) {
	if err != nil {
		return nil, err
	}
	var rows []CommitMsg
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 5 {
			continue
		}
		body := f[4]
		if len(f) > 5 {
			body = strings.Join(f[4:], "\x1f")
		}
		ts, _ := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 64)
		parents := 1
		var parentSHAs []string
		if strings.TrimSpace(f[3]) == "" {
			parents = 0
		} else {
			parentSHAs = strings.Fields(f[3])
			parents = len(parentSHAs)
		}
		rows = append(rows, CommitMsg{SHA: f[0], Author: f[1], DateTS: ts,
			Parents: parents, ParentSHAs: parentSHAs, Body: body})
	}
	return rows, nil
}

func parseLog(out string, err error) ([]Commit, error) {
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		f := strings.Split(ln, "\x1f")
		if len(f) < 5 {
			continue
		}
		ts, _ := strconv.ParseInt(f[2], 10, 64)
		parents := 1
		if strings.TrimSpace(f[4]) == "" {
			parents = 0 // 根提交
		} else {
			parents = len(strings.Fields(f[4]))
		}
		commits = append(commits, Commit{
			SHA: f[0], Author: f[1], DateTS: ts, Subject: f[3], Parents: parents,
		})
	}
	return commits, nil
}

// Remote is one configured remote（取 fetch URL）。
type Remote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Remotes lists the configured remotes by name order.
func (r *Repo) Remotes(ctx context.Context) ([]Remote, error) {
	out, err := runGit(ctx, r.Workspace, readTimeout, "remote", "-v")
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	for _, ln := range strings.Split(out, "\n") {
		f := strings.SplitN(ln, "\t", 2)
		if len(f) != 2 {
			continue
		}
		name, rest := f[0], f[1]
		if strings.HasSuffix(rest, " (fetch)") && seen[name] == "" {
			seen[name] = strings.TrimSuffix(strings.TrimSpace(rest), " (fetch)")
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	out2 := make([]Remote, 0, len(names))
	for _, n := range names {
		out2 = append(out2, Remote{Name: n, URL: seen[n]})
	}
	return out2, nil
}

// Fetch runs `git fetch --all --prune`——把远端分支拉进本地视野（远端
// 跟踪引用更新，工作树纹丝不动，最安全的「拉进来」）。
func (r *Repo) Fetch(ctx context.Context) error {
	_, err := runGit(ctx, r.Workspace, fetchTimeout, "fetch", "--all", "--prune")
	return err
}

// Checkout switches the working tree to branch. 本地名走 `git switch`
// （git 的 DWIM 会在唯一远端同名分支上自动建跟踪）；带远端前缀的名
// （origin/feat-x）走 `--track` 落一个同名本地分支。脏树/冲突时 git
// 自己会拒绝，文案原样透传——服务器层还有一道 TrackedDirty 预检给
// 出更体面的提示。
func (r *Repo) Checkout(ctx context.Context, branch string) error {
	if !ValidRefName(branch) {
		return RefNameError(i18n.S("分支名"), branch)
	}
	remotes, _ := r.Remotes(ctx)
	for _, rm := range remotes {
		if strings.HasPrefix(branch, rm.Name+"/") {
			return runErr(runGit(ctx, r.Workspace, switchTimeout,
				"switch", "--track", branch))
		}
	}
	return runErr(runGit(ctx, r.Workspace, switchTimeout, "switch", branch))
}

// NewBranch creates branch at from（"" = 当前 HEAD）并切过去——与
// checkout 同一条护栏通道（脏树被 git 拒绝时文案透传）。
func (r *Repo) NewBranch(ctx context.Context, name, from string) error {
	if !ValidRefName(name) {
		return RefNameError(i18n.S("分支名"), name)
	}
	args := []string{"switch", "-c", name}
	if from != "" {
		if !ValidRefName(from) {
			return RefNameError(i18n.S("起点 ref"), from)
		}
		args = append(args, from)
	}
	return runErr(runGit(ctx, r.Workspace, switchTimeout, args...))
}

// EnsureBranch creates branch at from when missing — and does NOTHING
// else: no switch（用户在用的工作树一个文件不动）, no delete, no
// force. 幂等：本地已有同名分支＝成功不重建。from "" = 当前 HEAD；
// from 必须指向已存在的 ref（git 自己会拒，文案透传）。这是自动建枝
// （立项引导/座位分支，v2.14）的唯一原语——新增性、可重复、绝不破坏。
func (r *Repo) EnsureBranch(ctx context.Context, name, from string) (bool, error) {
	if !ValidRefName(name) {
		return false, RefNameError(i18n.S("分支名"), name)
	}
	if from != "" && !ValidRefName(from) {
		return false, RefNameError(i18n.S("起点 ref"), from)
	}
	if bs, err := r.Branches(ctx); err == nil {
		for _, b := range bs {
			if b.Remote == "" && b.Name == name {
				return false, nil // 已在：幂等成功
			}
		}
	}
	args := []string{"branch", name}
	if from != "" {
		args = append(args, from)
	}
	if _, err := runGit(ctx, r.Workspace, switchTimeout, args...); err != nil {
		return false, err
	}
	return true, nil
}

// InitRepo initializes a brand-new repository AT dir（base 为初始分支
// 名，空＝main），落一笔空根提交——分支要先真实存在，工作树与成员分
// 枝才有得切。安全红线（v2.14 立项引导）：dir 或其任何祖先已是仓库
// 一律拒绝——绝不嵌套建仓、绝不认养外层仓库（动外层仓库就是动用户
// 的别的项目）；提交自带作者身份（-c user.name/email），没配全局 git
// 身份的机器也落得下去，且不写用户的任何全局配置。这个家族没有删除
// 与强制。
func InitRepo(ctx context.Context, dir, base, commitMsg string) (*Repo, error) {
	if base == "" {
		base = "main"
	}
	if !ValidRefName(base) {
		return nil, RefNameError(i18n.S("分支名"), base)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if _, err := Detect(ctx, dir); err == nil {
		return nil, errors.New(i18n.Sf("%s 已在 git 仓库内（或其子目录）——不在别人的仓库里建仓", dir))
	} else if !errors.Is(err, ErrNotRepo) {
		return nil, err
	}
	// init 后用 symbolic-ref 定初始分支名：不依赖 2.28+ 的 -b，老 git
	// 一样成立；空提交前 HEAD 还是 unborn，正是改名的窗口。
	if _, err := runGit(ctx, dir, switchTimeout, "init"); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, dir, switchTimeout, "symbolic-ref", "HEAD", "refs/heads/"+base); err != nil {
		return nil, err
	}
	if commitMsg == "" {
		commitMsg = "chore: 项目初始化"
	}
	if _, err := runGit(ctx, dir, switchTimeout,
		"-c", "user.name=Niuma Studio", "-c", "user.email=studio@niuma.local",
		"commit", "--allow-empty", "-m", commitMsg); err != nil {
		return nil, err
	}
	return Detect(ctx, dir)
}

// runErr drops the stdout cargo (switch 家族没有要解析的输出)。
func runErr(_ string, err error) error { return err }

// WorkspaceForRoot 在候选工作区里找出「所属仓库根 == root」的那一
// 个——check-msg 钩子的执行体用它把仓库映射回项目（钩子在仓库里
// 跑，cwd 可能是任何子目录，项目注册表才是唯一的归属真相）。
func WorkspaceForRoot(ctx context.Context, root string, workspaces []string) string {
	for _, ws := range workspaces {
		if ws == "" {
			continue
		}
		if r, err := Detect(ctx, ws); err == nil && r.Root == root {
			return ws
		}
	}
	return ""
}
