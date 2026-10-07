package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// Face is the git domain's whole world: the seat (stores/fleet/
// routing/taps — cross-domain), the per-project git write locks (was
// the shell's Server state), the worktree-root override, the chronicle
// hook and the plan-room resolver (night-tap wiring stays shell's).
// Split from the shell's gitops.go/gitflow.go/gitboot.go.
type Face struct {
	*verbs.Seat
	Locks        sync.Map // per-project git write serialization
	WorktreeRoot string   // override; "" = the ~/.niuma/wt convention
	Chronicle    Chronicle
	PlanHub      func(projectKey string) *chat.Hub
}

// Chronicle is the 工作室志's narrow face this domain taps.
type Chronicle interface {
	OnMerged(m *merge.Merge, by string)
}

// FaceOf adapts a seat plus the shell's chronicle/plan-hub seams.
func FaceOf(st *verbs.Seat, chron Chronicle, planHub func(string) *chat.Hub, worktreeRoot string) *Face {
	return &Face{Seat: st, Chronicle: chron, PlanHub: planHub, WorktreeRoot: worktreeRoot}
}

// complianceWindow 是当前分支的规范检查窗口（近 N 条提交）——摘要统
// 计与提交历史的默认深度，够看出风气又不至于把历史陈账全翻出来。
const complianceWindow = 30

// gitLockKey serializes one project's git WRITE moves against each
// other (fetch/checkout/branch 互斥；读不锁——慢读最多看到半秒前的世
// 界，git 的引用更新自己原子)。
func (g *Face) gitLockKey(key string) *sync.Mutex {
	mu, _ := g.Locks.LoadOrStore(key, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// projectGit resolves the project and its repository. repo == nil 是合
// 法态（工作区不在任何 git 仓库里——前端渲染空态卡，不是错误）。
// GitDisabled（两级门顶层）在此一刀：总开关关着的项目，整个 git 面
// （读写皆然）统一 errGitDisabled → writeGitErr 409——分支/日志/图/
// fetch/push/pull/checkout/建枝/绑定/座位/合并流全部经此咽喉。
func (g *Face) ProjectGit(key string) (projects.Project, *vcs.Repo, error) {
	p, ok := g.Stores.ProjectStore.Get(key)
	if !ok {
		return projects.Project{}, nil, errProjectNotFound(key)
	}
	if p.GitDisabled {
		return p, nil, errGitDisabled(key)
	}
	repo, err := vcs.Detect(context.Background(), p.Workspace)
	if err != nil {
		if errors.Is(err, vcs.ErrNotRepo) {
			return p, nil, nil
		}
		return p, nil, err
	}
	return p, repo, nil
}

// gitDisabledError carries the key for the master-switch refusal（同
// projectMissingError 的带参纪律：动态串在出口拼）。
type gitDisabledError struct{ key string }

func (e *gitDisabledError) Error() string { return "git 版本管理已关闭: " + e.key }

func errGitDisabled(key string) error { return &gitDisabledError{key} }

// refuseIfGitDisabled patches the faces that bypass projectGit（不探测
// 仓库、直写工作室侧账本或直读矩阵：unbind/policy/seats/merge 清单/
// 提交播报）：总开关关着就 409 拒绝并回 true。store 未配或项目不在
// 册不在此报——各自的既有路径会报得更准。
func (g *Face) refuseIfGitDisabled(w http.ResponseWriter, key string) bool {
	if g.Stores.ProjectStore == nil {
		return false
	}
	if p, ok := g.Stores.ProjectStore.Get(key); ok && p.GitDisabled {
		writeGitErr(w, errGitDisabled(key))
		return true
	}
	return false
}

// projectMissingError carries the key so writeGitErr can Sf-translate
// the reason（动态串不过 i18n.S 的咽喉，得带参在出口拼）。
type projectMissingError struct{ key string }

func (e *projectMissingError) Error() string { return "项目不存在: " + e.key }

func errProjectNotFound(key string) error {
	return &projectMissingError{key: key}
}

// writeGitErr is the gitops face's writeJSONErr triage: 同一句错误不再
// 一律 400——项目不存在 404（语义如实）、git 超时 504（网关墙钟，
// 不是载荷错）、凭据/权限类拒绝 401（授权问题，不是载荷错——前端据
// 此知道该去配凭据而不是重试）、其余 400 带 git 原文（git 自己的拒绝
// 文案比任何转述都准）。此前全文件 30+ 处 httputil.WriteJSONErr(w, 400,
// err.Error()) 裸透传，状态码把超时也说成载荷错。
func writeGitErr(w http.ResponseWriter, err error) {
	var pm *projectMissingError
	var gd *gitDisabledError
	var ge *vcs.GitError
	switch {
	case errors.As(err, &pm):
		httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("项目不存在: %s", pm.key))
	case errors.As(err, &gd):
		httputil.WriteJSONErr(w, http.StatusConflict, i18n.Sf("项目 %s 已关闭 git 版本管理——到设置中心·项目页打开总开关后再试", gd.key))
	case errors.As(err, &ge) && ge.Timeout:
		httputil.WriteJSONErr(w, http.StatusGatewayTimeout, err.Error())
	case errors.As(err, &ge) && ge.Auth:
		httputil.WriteJSONErr(w, http.StatusUnauthorized, err.Error())
	default:
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
	}
}

// policyOf returns the project's effective commit policy（nil 存档 =
// vcs 默认档）。
func policyOf(p projects.Project) vcs.Policy {
	if p.GitPolicy != nil {
		return *p.GitPolicy
	}
	return vcs.Policy{}
}

// gitPolicyOut is the policy's EFFECTIVE face: type 表与引用档都已解析
// 成默认补全后的值——前端与 CLI 不再各自实现一遍默认逻辑。
type gitPolicyOut struct {
	CommitTypes []string `json:"commit_types"`
	RequireRef  string   `json:"require_ref"`
}

func effectivePolicy(p projects.Project) gitPolicyOut {
	pol := policyOf(p)
	return gitPolicyOut{CommitTypes: pol.Types(), RequireRef: pol.RequireRefEffective()}
}

// gitRepoOut is the summary's repo block.
type gitRepoOut struct {
	Workspace string       `json:"workspace"`
	Root      string       `json:"root"`
	Subdir    bool         `json:"subdir,omitempty"` // 工作区在仓库子目录（面板提示用）
	Worktree  bool         `json:"worktree,omitempty"`
	Remotes   []vcs.Remote `json:"remotes,omitempty"`
}

// gitComplianceOut is the current branch's lint tally.
type gitComplianceOut struct {
	Checked int      `json:"checked"`
	Bad     int      `json:"bad"`
	BadSHAs []string `json:"bad_shas,omitempty"`
}

// gitCommitOut is one log row with its convention verdict. ParentSHAs
// 供提交图连线（graph 面给；单支历史面省流量不给也行，同一结构体）。
type gitCommitOut struct {
	SHA        string      `json:"sha"`
	Author     string      `json:"author"`
	DateTS     int64       `json:"date_ts"`
	Subject    string      `json:"subject"`
	Parents    int         `json:"parents,omitempty"`
	ParentSHAs []string    `json:"parent_shas,omitempty"`
	Verdict    vcs.Verdict `json:"verdict"`
}

// commitRows converts full-message log rows into verdict-tagged output.
// withParents 打开父提交 SHA 列（提交图要连线；别的读脸省这份流量）。
func commitRows(msgs []vcs.CommitMsg, policy vcs.Policy, withParents bool) []gitCommitOut {
	rows := make([]gitCommitOut, 0, len(msgs))
	for _, m := range msgs {
		row := gitCommitOut{
			SHA: m.SHA, Author: m.Author, DateTS: m.DateTS,
			Subject: vcs.SubjectOf(m.Body), Parents: m.Parents,
			Verdict: vcs.CheckCommit(m.Body, policy, m.Parents >= 2),
		}
		if withParents {
			row.ParentSHAs = m.ParentSHAs
		}
		rows = append(rows, row)
	}
	return rows
}

// hookState reports the repo's commit-msg hook occupancy: ours / foreign
// / absent.
func hookState(repo *vcs.Repo) (path string, ours, foreign bool) {
	p := filepath.Join(repo.CommonDir, "hooks", "commit-msg")
	b, err := os.ReadFile(p)
	if err != nil {
		return p, false, false
	}
	return p, strings.Contains(string(b), "Niuma Studio commit-msg"), true
}

// postHookState reports the post-commit hook's occupancy（提交播报，
// v2.8 gitflow）: ours / foreign / absent。
func postHookState(repo *vcs.Repo) (path string, ours, foreign bool) {
	p := filepath.Join(repo.CommonDir, "hooks", "post-commit")
	b, err := os.ReadFile(p)
	if err != nil {
		return p, false, false
	}
	return p, strings.Contains(string(b), "Niuma Studio post-commit"), true
}

// handleGitSummary serves GET /p/{key}/git: repo 检测 + 状态 + 策略 +
// 当前分支绑定 + 合规统计 + 钩子占用。非仓库时 repo=null（其余字段照
// 给——策略与绑定是工作室侧账本，不依赖 git 存在）。
func (g *Face) HandleGitSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	p, ok := g.Stores.ProjectStore.Get(key)
	if !ok {
		writeGitErr(w, errProjectNotFound(key))
		return
	}
	policy := policyOf(p)
	out := struct {
		Repo          *gitRepoOut          `json:"repo"`
		Status        *vcs.Status          `json:"status,omitempty"`
		Policy        gitPolicyOut         `json:"policy"`
		Link          *projects.BranchLink `json:"link,omitempty"`
		Compliance    *gitComplianceOut    `json:"compliance,omitempty"`
		HookPath      string               `json:"hook_path,omitempty"`
		HookInstalled bool                 `json:"hook_installed,omitempty"`
		HookForeign   bool                 `json:"hook_foreign,omitempty"`
		// PostHookInstalled（v2.8 gitflow）：提交播报钩子（post-commit）
		// 是否在位——在位时每笔提交都会进房间对话流。
		PostHookInstalled bool `json:"post_hook_installed,omitempty"`
		// GitMissing（v2.14）：repo=null 的两种成因要分开——本机没有
		// git 命令（整页 git 都跑不了，空态卡给安装指引）与工作区不是
		// 仓库（空态卡给 init 入口）。false 缺省 = 不是仓库。
		GitMissing       bool   `json:"git_missing,omitempty"`
		GitMissingReason string `json:"git_missing_reason,omitempty"`
		// Enabled（两级门顶层）：false＝总开关关闭——前端最优先看它，
		// 渲染「已关闭」卡（不再看 repo/status 空态）。恒给不走
		// omitempty：缺席误判成开是这边最坏的方向。
		Enabled bool `json:"enabled"`
		// BaseBranch/AutoSeatBranch/AutoSeat：GitPlan 三个运行期设置的
		// 生效值（基线空补 main）——设置表单的预填数据源，恒给。
		BaseBranch     string `json:"base_branch"`
		AutoSeatBranch bool   `json:"auto_seat_branch"`
		AutoSeat       bool   `json:"auto_seat"`
	}{Policy: effectivePolicy(p), Enabled: !p.GitDisabled, BaseBranch: p.GitPlan.BaseOrDefault(),
		AutoSeatBranch: p.GitPlan != nil && p.GitPlan.AutoSeatBranch,
		AutoSeat:       p.GitPlan != nil && p.GitPlan.AutoSeat}
	repo, err := vcs.Detect(context.Background(), p.Workspace)
	if err != nil {
		switch {
		case errors.Is(err, vcs.ErrNoGit):
			out.GitMissing = true
			out.GitMissingReason = err.Error()
			httputil.WriteJSON(w, out)
		case errors.Is(err, vcs.ErrNotRepo):
			httputil.WriteJSON(w, out) // repo=null：空态卡
		default:
			writeGitErr(w, err)
		}
		return
	}
	ctx := context.Background()
	st, err := repo.Status(ctx)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	remotes, rerr := repo.Remotes(ctx)
	if rerr != nil {
		log.Printf("[版本] %s 读远端列表失败（摘要照常，仅远端列空）：%v", key, rerr)
	}
	out.Repo = &gitRepoOut{
		Workspace: p.Workspace, Root: repo.Root,
		Subdir:   filepath.Clean(repo.Root) != filepath.Clean(p.Workspace),
		Worktree: repo.IsWorktree(), Remotes: remotes,
	}
	out.Status = st
	if st.Branch != "" {
		out.Link = p.Link(st.Branch)
	}
	msgs, err := repo.LogMessages(ctx, "", complianceWindow)
	if err != nil {
		log.Printf("[版本] %s 读提交历史失败（合规统计缺位）：%v", key, err)
	} else {
		c := &gitComplianceOut{Checked: len(msgs)}
		for _, m := range msgs {
			if !vcs.CheckCommit(m.Body, policy, m.Parents >= 2).OK {
				c.Bad++
				c.BadSHAs = append(c.BadSHAs, m.SHA[:min(7, len(m.SHA))])
			}
		}
		out.Compliance = c
	}
	out.HookPath, out.HookInstalled, out.HookForeign = hookState(repo)
	if _, postOurs, _ := postHookState(repo); postOurs {
		out.PostHookInstalled = true
	}
	httputil.WriteJSON(w, out)
}

// handleGitBranches serves GET /p/{key}/git/branches: 本地＋远端全分
// 支，行挂工作室侧绑定。
func (g *Face) HandleGitBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	p, repo, err := g.ProjectGit(r.PathValue("key"))
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库——先在终端 git init 或核对项目 workspace"))
		return
	}
	ctx := context.Background()
	bs, err := repo.Branches(ctx)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	type rowOut struct {
		vcs.Branch
		Link *projects.BranchLink `json:"link,omitempty"`
	}
	rows := make([]rowOut, 0, len(bs))
	current := ""
	for _, b := range bs {
		link := p.Link(b.Name)
		if b.Current {
			current = b.Name
		}
		rows = append(rows, rowOut{Branch: b, Link: link})
	}
	httputil.WriteJSON(w, struct {
		Current  string   `json:"current"`
		Branches []rowOut `json:"branches"`
	}{Current: current, Branches: rows})
}

// handleGitLog serves GET /p/{key}/git/log?branch=&n=（branch 空 =
// HEAD；n 默认 30、上限 100）。每条带规范 verdict。
func (g *Face) HandleGitLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	n := complianceWindow
	if v := strings.TrimSpace(r.URL.Query().Get("n")); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			n = min(i, 100)
		}
	}
	p, repo, err := g.ProjectGit(r.PathValue("key"))
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库"))
		return
	}
	msgs, err := repo.LogMessages(context.Background(), branch, n)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	httputil.WriteJSON(w, struct {
		Commits []gitCommitOut `json:"commits"`
	}{Commits: commitRows(msgs, policyOf(p), false)})
}

// handleGitGraph serves GET /p/{key}/git/graph?n=：提交图数据（GitLens
// 式树形视图的半场）——`--all --topo-order` 全分支拓扑，逐条带父提交
// SHA 与规范 verdict；current 是 HEAD 所在分支名（前端把分支徽章贴
// 到对应提交行）。n 默认 50、上限 200。
func (g *Face) HandleGitGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	n := 50
	if v := strings.TrimSpace(r.URL.Query().Get("n")); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			n = min(i, 200)
		}
	}
	p, repo, err := g.ProjectGit(r.PathValue("key"))
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库"))
		return
	}
	msgs, err := repo.LogGraph(context.Background(), n)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	current := ""
	if st, serr := repo.Status(context.Background()); serr == nil {
		current = st.Branch
	}
	httputil.WriteJSON(w, struct {
		Current string         `json:"current"`
		Commits []gitCommitOut `json:"commits"`
	}{Current: current, Commits: commitRows(msgs, policyOf(p), true)})
}

// handleGitReqActivity serves GET /p/{key}/git/req-activity?req=r_12:
// 需求视图的数据源——绑定的本地分支 ＋ 全分支范围内提及该号的提交
// （LogGrepAll 整词反查）。非仓库返回空集（需求行的小签本就可能为
// 零）。
func (g *Face) HandleGitReqActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	req := strings.TrimSpace(r.URL.Query().Get("req"))
	out := struct {
		Req      string         `json:"req"`
		Branches []string       `json:"branches"`
		Commits  []gitCommitOut `json:"commits"`
	}{Req: req, Branches: []string{}, Commits: []gitCommitOut{}}
	if req == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("req 参数必填（形如 r_12）"))
		return
	}
	p, repo, err := g.ProjectGit(r.PathValue("key"))
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSON(w, out)
		return
	}
	ctx := context.Background()
	bs, err := repo.Branches(ctx)
	if err == nil {
		for _, b := range bs {
			if b.Remote != "" {
				continue
			}
			if link := p.Link(b.Name); link != nil && link.Req == req {
				out.Branches = append(out.Branches, b.Name)
			}
		}
	} else {
		log.Printf("[版本] %s 需求反查读分支失败：%v", r.PathValue("key"), err)
	}
	msgs, err := repo.LogGrepAll(ctx, req, 50)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	out.Commits = commitRows(msgs, policyOf(p), false)
	httputil.WriteJSON(w, out)
}

// handleGitFetch serves POST /p/{key}/git/fetch: git fetch --all
// --prune——把远端分支拉进本地视野，工作树纹丝不动。
func (g *Face) HandleGitFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	_, repo, err := g.ProjectGit(r.PathValue("key"))
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库"))
		return
	}
	key := r.PathValue("key")
	lock := g.gitLockKey(key)
	lock.Lock()
	defer lock.Unlock()
	if err := repo.Fetch(context.Background()); err != nil {
		writeGitErr(w, err)
		return
	}
	bs, err := repo.Branches(context.Background())
	if err != nil {
		httputil.WriteJSON(w, map[string]any{"ok": true})
		return
	}
	httputil.WriteJSON(w, map[string]any{"ok": true, "branches": len(bs)})
}

// handleGitInit serves POST /p/{key}/git/init: 房主显式把项目工作区
// 初始化成 git 仓库——空 workspace 的自愈路（工作室仍不自动代建：
// 这个端点只回应房主的点击，与 checkout 同一信任档）。执行体是
// vcs.InitRepo（init＋symbolic-ref 钉 main＋一笔空根提交；已在任何
// 仓库内一律拒绝），前置预检把「已是仓库」折成 409；git 缺失时
// Detect 的 NoGitError 带着安装指引走 400。
func (g *Face) HandleGitInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	p, ok := g.Stores.ProjectStore.Get(key)
	if !ok {
		writeGitErr(w, errProjectNotFound(key))
		return
	}
	if p.GitDisabled {
		writeGitErr(w, errGitDisabled(key))
		return
	}
	if p.Workspace == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("项目 workspace 未设置——先到项目设置里补上工作区路径"))
		return
	}
	lock := g.gitLockKey(key)
	lock.Lock()
	defer lock.Unlock()
	if _, err := vcs.Detect(r.Context(), p.Workspace); err == nil {
		httputil.WriteJSONErr(w, http.StatusConflict, i18n.S("这里已经是 git 仓库——无需初始化"))
		return
	} else if !errors.Is(err, vcs.ErrNotRepo) {
		writeGitErr(w, err) // git 缺失＝安装指引；其他探测失败如实回
		return
	}
	repo, err := vcs.InitRepo(r.Context(), p.Workspace, "main", "")
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if hub := g.RoomHub(key); hub != nil {
		hub.SystemRecorded(i18n.Sf("【版本】房主把项目工作区初始化成了 git 仓库（%s，初始分支 main，一笔空根提交）——可以建分支了", p.Workspace))
	}
	httputil.WriteJSON(w, map[string]any{"ok": true, "root": repo.Root, "workspace": p.Workspace})
}

// handleGitPush serves POST /p/{key}/git/push {remote?, branch?}: 把当
// 前分支（或指名分支）推去远端——推不动工作树一个文件，不设脏树护
// 栏；分叉被远端拒、凭据不认（401 分流）文案原文透传。成功后房间落
// recorded [版本] 行（发布是房间可见的事实）。
func (g *Face) HandleGitPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	var body struct {
		Remote string `json:"remote"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	_, repo, err := g.ProjectGit(key)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库——无从推送"))
		return
	}
	lock := g.gitLockKey(key)
	lock.Lock()
	defer lock.Unlock()
	remote, err := repo.Push(r.Context(), body.Remote, body.Branch)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	branch := body.Branch
	if branch == "" {
		if st, serr := repo.Status(r.Context()); serr == nil {
			branch = st.Branch
		}
	}
	if hub := g.RoomHub(key); hub != nil {
		hub.SystemRecorded(i18n.Sf("【版本】房主把分支 %s 推去了 %s——远端从此可见这份工作", branchLabel(branch), remote))
	}
	httputil.WriteJSON(w, map[string]any{"ok": true, "remote": remote, "branch": branch})
}

// handleGitPull serves POST /p/{key}/git/pull: 把工作区当前分支快进到
// 上游（--ff-only，分叉即拒绝、绝不制造合并提交——分叉的收拾是人的
// 判断）。快进动的就是共享主树的文件——脏树 409 与切分支同款护栏；
// 确有新提交进来时房间落 recorded [版本] 行＋Options.GitUpdated 异步
// tap（主树成员脚下文件变了，同切分支的事实通报；tap 失败只记日志
// 绝不回滚——快进已经落定）。
func (g *Face) HandleGitPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	_, repo, err := g.ProjectGit(key)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库——无从拉取更新"))
		return
	}
	lock := g.gitLockKey(key)
	lock.Lock()
	defer lock.Unlock()
	ctx := r.Context()
	if st, err := repo.Status(ctx); err != nil {
		writeGitErr(w, err)
		return
	} else if dirty := st.TrackedDirty(); dirty > 0 {
		httputil.WriteJSONErr(w, http.StatusConflict,
			i18n.Sf("工作区有 %d 处未提交改动（暂存 %d、修改 %d）——先提交或 stash 再拉取更新", dirty, st.Staged, st.Modified))
		return
	}
	branch, incoming, err := repo.PullFFOnly(ctx)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if incoming > 0 {
		if hub := g.RoomHub(key); hub != nil {
			hub.SystemRecorded(i18n.Sf("【版本】房主把工作区在 %s 上快进到上游最新（%d 个提交进来）", branch, incoming))
		}
		if g.Fleet != nil {
			go util.Guard("server: fleet notify", func() {
				if err := g.Fleet.GitUpdated(key, branch, incoming); err != nil {
					log.Printf("[版本] %s 拉取更新后告知成员失败：%v", key, err)
				}
			})
		}
	}
	httputil.WriteJSON(w, map[string]any{"ok": true, "branch": branch, "incoming": incoming})
}

// handleGitAuth serves GET /p/{key}/git/auth: 逐远端授权探测（git
// ls-remote 与 fetch 走同一条凭据链）——「这根远端认不认我」的体检
// 面。探测只问 refs（30s 一档），串行、至多 4 根（面板点一下的动
// 作，不该变成一次小扫描）；无远端返回空表。
func (g *Face) HandleGitAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	_, repo, err := g.ProjectGit(r.PathValue("key"))
	if err != nil {
		writeGitErr(w, err)
		return
	}
	checks := []vcs.AuthState{}
	if repo != nil {
		remotes, err := repo.Remotes(r.Context())
		if err != nil {
			writeGitErr(w, err)
			return
		}
		if len(remotes) > 4 {
			remotes = remotes[:4]
		}
		for _, rm := range remotes {
			checks = append(checks, repo.AuthProbe(r.Context(), rm.Name))
		}
	}
	httputil.WriteJSON(w, map[string]any{"checks": checks})
}
