package gitops

// gitops_handlers.go — the branch/seat HTTP family, split from
// gitops.go (zero logic edits): checkout/branch-new/bind/unbind/
// policy/settings/hook/seats/seat/seat-cleanup + the tree-dir helpers.

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// handleGitCheckout serves POST /p/{key}/git/checkout {branch, by}:
// 切工作区分支。脏树 409 拒绝（先提交或 stash——面板不做强切，丢改
// 动的事不替房主按）；成功后房间落 recorded [版本] 行并异步 tap
// Options.GitBranchSwitched（在岗成员的会话告知）。
func (g *Face) HandleGitCheckout(w http.ResponseWriter, r *http.Request) {
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
		Branch string `json:"branch"`
		By     string `json:"by"`
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
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库"))
		return
	}
	lock := g.gitLockKey(key)
	lock.Lock()
	defer lock.Unlock()
	ctx := context.Background()
	st, err := repo.Status(ctx)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if dirty := st.TrackedDirty(); dirty > 0 {
		httputil.WriteJSONErr(w, http.StatusConflict,
			i18n.Sf("工作区有 %d 处未提交改动（暂存 %d、修改 %d）——先提交或 stash 再切分支，面板不做强切", dirty, st.Staged, st.Modified))
		return
	}
	from := st.Branch
	if err := repo.Checkout(ctx, body.Branch); err != nil {
		writeGitErr(w, err)
		return
	}
	g.notifyBranchSwitched(key, from, body.Branch)
	httputil.WriteJSON(w, map[string]any{"from": from, "to": body.Branch})
}

// handleGitBranchNew serves POST /p/{key}/git/branch {name, from, req,
// version, by}: 建分支即切换（git switch -c）。from 空 = 自当前
// HEAD（脏树无险——工作树文件一个不动）；from 指定他枝时按切分支同
// 款护栏预检。req/version 给了就顺手落绑定（绑定失败不回滚建枝，作
// 为 bind_error 字段如实带回）。
func (g *Face) HandleGitBranchNew(w http.ResponseWriter, r *http.Request) {
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
		Name    string `json:"name"`
		From    string `json:"from"`
		Req     string `json:"req"`
		Version string `json:"version"`
		By      string `json:"by"`
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
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库"))
		return
	}
	lock := g.gitLockKey(key)
	lock.Lock()
	defer lock.Unlock()
	ctx := context.Background()
	from := ""
	if body.From != "" {
		st, err := repo.Status(ctx)
		if err != nil {
			writeGitErr(w, err)
			return
		}
		if dirty := st.TrackedDirty(); dirty > 0 {
			httputil.WriteJSONErr(w, http.StatusConflict,
				i18n.Sf("自他枝建分支要先过树（当前 %d 处未提交改动）——先提交或 stash，或把起点留空自当前 HEAD", dirty))
			return
		}
		from = st.Branch
	} else if st, err := repo.Status(ctx); err == nil {
		from = st.Branch
	}
	if err := repo.NewBranch(ctx, body.Name, body.From); err != nil {
		writeGitErr(w, err)
		return
	}
	g.notifyBranchSwitched(key, from, body.Name)
	out := map[string]any{"branch": body.Name, "from": from}
	if body.Req != "" || body.Version != "" {
		if !g.bindReqBelongs(key, body.Req) {
			out["bind_error"] = i18n.Sf("需求 %s 不在项目 %s 的需求架上——号段分项目后绑定只认本房号", body.Req, key)
		} else {
			p, err := g.Stores.ProjectStore.BindBranch(key, body.Name, projects.BranchLink{
				Req: body.Req, Version: body.Version, BoundBy: body.By,
			})
			if err != nil {
				out["bind_error"] = err.Error() // 枝已建、绑定没落成——如实带回归因
			} else {
				out["link"] = p.Link(body.Name)
			}
		}
	}
	httputil.WriteJSON(w, out)
}

// bindReqBelongs gates a branch binding's r_NN to the binding project's
// own shelf (v2.10): the token only means something on that shelf, so
// a foreign or unknown number refuses (the nil-store discipline keeps
// the check off when the requirements face isn't wired).
func (g *Face) bindReqBelongs(key, req string) bool {
	if req == "" || g.Stores.Requirements == nil {
		return true
	}
	_, ok := g.Stores.Requirements.GetIn(g.Subject, key, req)
	return ok
}

// handleGitBind serves POST /p/{key}/git/bind {branch, req, version,
// note, by}——整条替换语义（解绑单侧＝传空字段）。分支须真实存在
// （防手滑绑错名）。
func (g *Face) HandleGitBind(w http.ResponseWriter, r *http.Request) {
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
		Branch  string `json:"branch"`
		Req     string `json:"req"`
		Version string `json:"version"`
		Note    string `json:"note"`
		By      string `json:"by"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	_, repo, err := g.ProjectGit(key)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库，无从绑定"))
		return
	}
	bs, err := repo.Branches(context.Background())
	if err != nil {
		writeGitErr(w, err)
		return
	}
	known := false
	for _, b := range bs {
		if b.Name == body.Branch {
			known = true
			break
		}
	}
	if !known {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("分支 %q 不在该仓库里（本地与远端都没有）", body.Branch))
		return
	}
	if !g.bindReqBelongs(key, body.Req) {
		httputil.WriteJSONErr(w, http.StatusBadRequest,
			i18n.Sf("需求 %s 不在项目 %s 的需求架上——号段分项目后绑定只认本房号", body.Req, key))
		return
	}
	p, err := g.Stores.ProjectStore.BindBranch(key, body.Branch, projects.BranchLink{
		Req: body.Req, Version: body.Version, Note: body.Note, BoundBy: body.By,
	})
	if err != nil {
		writeGitErr(w, err)
		return
	}
	httputil.WriteJSON(w, map[string]any{"branch": body.Branch, "link": p.Link(body.Branch)})
}

// handleGitUnbind serves POST /p/{key}/git/unbind {branch}——幂等。
func (g *Face) HandleGitUnbind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if g.refuseIfGitDisabled(w, key) {
		return
	}
	var body struct {
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	if _, err := g.Stores.ProjectStore.UnbindBranch(key, body.Branch); err != nil {
		writeGitErr(w, err)
		return
	}
	httputil.WriteJSON(w, map[string]any{"branch": body.Branch})
}

// handleGitPolicy serves POST /p/{key}/git/policy {commit_types?,
// require_ref?, reset?}——reset=true 一键回默认档。
func (g *Face) HandleGitPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if g.refuseIfGitDisabled(w, key) {
		return
	}
	var body struct {
		CommitTypes []string `json:"commit_types"`
		RequireRef  string   `json:"require_ref"`
		Reset       bool     `json:"reset"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	var policy *vcs.Policy
	if !body.Reset {
		policy = &vcs.Policy{CommitTypes: body.CommitTypes, RequireRef: body.RequireRef}
	}
	p, err := g.Stores.ProjectStore.SetGitPolicy(key, policy)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	httputil.WriteJSON(w, map[string]any{"policy": effectivePolicy(p)})
}

// handleGitSettings serves POST /p/{key}/git/settings {disabled,
// base_branch, auto_seat_branch, auto_seat}——两级门的写面：顶层总开关
// ＋基线/成员分支自动新建（被动档）/成员自动驻分支（主动档：出生即
// 各驻 wt/<人名>，开启时存量无分支成员立即补驻并迁移）。这正是 v2.14
// 缺口的补口：GitPlan 此前立项时一次写死，功能落地前立项的老项目没
// 有任何途径补开。回执带生效值（基线空＝main，与 BaseOrDefault 同口
// 径）与补驻名单，前端回执权威。
func (g *Face) HandleGitSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	prev, hasPrev := g.Stores.ProjectStore.Get(key)
	var body struct {
		Disabled      bool   `json:"disabled"`
		BaseBranch    string `json:"base_branch"`
		AutoSeat      bool   `json:"auto_seat_branch"`
		AutoSeatAtBir bool   `json:"auto_seat"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	// 主动档红线：自动驻枝要在出生时切枝落树，工作区必须是本项目自有
	// 的 git 仓库（外层仓库/非仓库一概拒——红线①同款）。
	if body.AutoSeatAtBir && !body.Disabled {
		repo, derr := vcs.Detect(context.Background(), prev.Workspace)
		if derr != nil || !repoOwnedByWorkspace(repo, prev.Workspace) {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("成员自动驻分支需要工作区是本项目自有的 git 仓库——外层仓库或非仓库工作区不支持（先 git init 或换工作区）"))
			return
		}
	}
	p, err := g.Stores.ProjectStore.SetGitSettings(key, body.Disabled, body.BaseBranch, body.AutoSeat, body.AutoSeatAtBir)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	// 补驻：主动档从关到开（或总开关重开）时，存量无分支在编成员立即
	// 各驻 wt/<人名>——用户开这开关要的就是「现在就自动开分支」。
	var backfill []string
	if body.AutoSeatAtBir && !body.Disabled {
		wasOff := !hasPrev || prev.GitPlan == nil || !prev.GitPlan.AutoSeat || prev.GitDisabled
		if wasOff {
			backfill = g.autoSeatBackfill(key)
		}
	}
	if hub := g.RoomHub(key); hub != nil {
		if body.Disabled {
			hub.System(i18n.S("【版本】git 版本管理已关闭——面板只读，分支隔离/合并流/提交播报全停（重新打开即恢复）"))
		} else {
			autoTxt := i18n.S("开")
			if !body.AutoSeat {
				autoTxt = i18n.S("关")
			}
			hub.System(i18n.Sf("【版本】git 版本管理已开启（基线 %s，成员分支自动新建：%s）", p.GitPlan.BaseOrDefault(), autoTxt))
			if body.AutoSeatAtBir {
				if len(backfill) > 0 {
					hub.SystemRecorded(i18n.Sf("【版本】成员自动驻分支已开——存量成员 %s 已补驻专属枝（wt/ 前缀，自基线 %s 切出；在岗者会话已迁至专属工作树，历史/任务无损续接）；此后新成员出生即自动驻枝", strings.Join(backfill, "、"), p.GitPlan.BaseOrDefault()))
				} else {
					hub.System(i18n.Sf("【版本】成员自动驻分支已开——暂无待补驻的成员；此后新成员出生即自动驻枝（wt/<人名>，自基线 %s 切出）", p.GitPlan.BaseOrDefault()))
				}
			}
		}
	}
	httputil.WriteJSON(w, map[string]any{
		"disabled":         p.GitDisabled,
		"base_branch":      p.GitPlan.BaseOrDefault(),
		"auto_seat_branch": p.GitPlan != nil && p.GitPlan.AutoSeatBranch,
		"auto_seat":        p.GitPlan != nil && p.GitPlan.AutoSeat,
		"backfilled":       backfill,
	})
}

// autoSeatBackfill seats every active branch-less member of the project on
// their own wt/<人名> branch（自基线切出，只建不删）。走 Fleet.GitSeat
// （有调度器时含会话迁移 Rebirth；与手动设分支同一执行体），无调度器
// 的退化形态只落账+切枝——树在下次出生时自然就位。单成员失败不炸整
// 批：跳过并记日志，名单如实回（失败者不在册）。
func (g *Face) autoSeatBackfill(key string) []string {
	if g.Stores.StaffStore == nil || g.Stores.ProjectStore == nil {
		return nil
	}
	p, ok := g.Stores.ProjectStore.Get(key)
	if !ok {
		return nil
	}
	repo, err := vcs.Detect(context.Background(), p.Workspace)
	if err != nil || !repoOwnedByWorkspace(repo, p.Workspace) {
		return nil // 红线形态：写面已拦，这里双保险
	}
	base := p.GitPlan.BaseOrDefault()
	ctx := context.Background()
	var done []string
	for _, row := range g.Stores.StaffStore.ListByProject(key) {
		if !row.Occupying() || row.Branch != "" {
			continue
		}
		branch := "wt/" + row.Person
		lock := g.gitLockKey(key)
		lock.Lock()
		_, berr := repo.EnsureBranch(ctx, base, "")
		if berr == nil {
			_, berr = repo.EnsureBranch(ctx, branch, base)
		}
		var merr error
		if berr == nil {
			if g.Fleet != nil {
				merr = g.Fleet.GitSeat(key, row.Person, branch)
			} else {
				_, merr = g.Stores.StaffStore.SetBranch(key, row.Person, branch)
			}
		}
		lock.Unlock()
		if err := pickErr(berr, merr); err != nil {
			log.Printf("[版本] %s: 自动驻枝补驻 %s 失败（跳过）：%v", key, row.Person, err)
			continue
		}
		done = append(done, row.Person)
	}
	return done
}

// pickErr returns the first non-nil（补驻失败日志的小工具）。
func pickErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// writeGitHooks drops the studio's own commit-msg + post-commit hooks
// into the repo's common hooks dir（handleGitHook 的 install 腿与 gitboot
// 的新仓直装共用）。CALLER owns the occupancy check：面板路径先查 foreign
// 位再调；gitboot 只在全新自建仓上直调（自家地界，无外来钩子可言）。
// 只写自家两枚，不删不改别人的东西。
func writeGitHooks(repo *vcs.Repo) (hookPath, postPath string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	hookPath = filepath.Join(repo.CommonDir, "hooks", "commit-msg")
	postPath = filepath.Join(repo.CommonDir, "hooks", "post-commit")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(hookPath, []byte(vcs.HookScript(exe)), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(postPath, []byte(vcs.PostHookScript(exe)), 0o755); err != nil {
		return "", "", err
	}
	return hookPath, postPath, nil
}

// handleGitHook serves POST /p/{key}/git/hook {action: install|
// uninstall}: 把 vcs.HookScript/vcs.PostHookScript（本程序绝对路径）
// 写进仓库公共 git dir 的 hooks/commit-msg 与 hooks/post-commit——前者
// 拦规范（提交前），后者播提交（落库后，v2.8 gitflow）。只认自己的
// 钩子位：被未知脚本占用时 409 如实报，绝不覆盖别人的东西；卸载同
// 理。两枚钩子独立成对（一枚被占不连坐另一枚）。
func (g *Face) HandleGitHook(w http.ResponseWriter, r *http.Request) {
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
		Action string `json:"action"`
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
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库"))
		return
	}
	hookPath, ours, foreign := hookState(repo)
	postPath, postOurs, postForeign := postHookState(repo)
	switch body.Action {
	case "install":
		if foreign && !ours {
			httputil.WriteJSONErr(w, http.StatusConflict,
				i18n.Sf("commit-msg 钩子位被未知脚本占用（%s）——先人工确认处置，工作室不覆盖别人的钩子", hookPath))
			return
		}
		if postForeign && !postOurs {
			httputil.WriteJSONErr(w, http.StatusConflict,
				i18n.Sf("post-commit 钩子位被未知脚本占用（%s）——先人工确认处置，工作室不覆盖别人的钩子", postPath))
			return
		}
		hp, pp, werr := writeGitHooks(repo)
		if werr != nil {
			httputil.WriteJSONErr(w, http.StatusInternalServerError, i18n.Sf("写钩子失败: %s", werr))
			return
		}
		httputil.WriteJSON(w, map[string]any{"installed": true, "path": hp, "post_path": pp})
	case "uninstall":
		if foreign && !ours {
			httputil.WriteJSONErr(w, http.StatusConflict,
				i18n.Sf("commit-msg 钩子位是未知脚本（%s），不敢替你删——请人工确认", hookPath))
			return
		}
		if postForeign && !postOurs {
			httputil.WriteJSONErr(w, http.StatusConflict,
				i18n.Sf("post-commit 钩子位是未知脚本（%s），不敢替你删——请人工确认", postPath))
			return
		}
		if foreign {
			if err := os.Remove(hookPath); err != nil {
				httputil.WriteJSONErr(w, http.StatusInternalServerError, i18n.Sf("卸 commit-msg 钩子失败: %s", err))
				return
			}
		}
		if postForeign {
			if err := os.Remove(postPath); err != nil {
				httputil.WriteJSONErr(w, http.StatusInternalServerError, i18n.Sf("卸 post-commit 钩子失败: %s", err))
				return
			}
		}
		httputil.WriteJSON(w, map[string]any{"installed": false, "path": hookPath, "post_path": postPath})
	default:
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("未知 action：%s（install | uninstall）", body.Action))
	}
}

// notifyBranchSwitched is the checkout's room leg (notifyEstablish-
// mentAdded 同款两腿)：范围房间先落一条 recorded [版本] 行（面板写
// 是房主私有知识，房间要看得见），再异步敲 Options.GitBranchSwitched
// （main 路由到 fleet，在岗成员的会话收到告知）。tap 失败只记日志——
// git 已经切完，通知不到是提醒的事，绝不是回滚的理由。
func (g *Face) notifyBranchSwitched(projectKey, from, to string) {
	hub := g.Hub
	if projectKey != chat.LobbyKey && g.Registry != nil {
		hub = g.Registry.Rooms()[projectKey]
	}
	if hub != nil {
		hub.SystemRecorded(branchSwitchedLine(from, to))
	}
	if g.Fleet == nil {
		return
	}
	go util.Guard("server: fleet notify", func() {
		if err := g.Fleet.GitBranchSwitched(projectKey, from, to); err != nil {
			log.Printf("[版本] %s 切分支后告知成员失败：%v", projectKey, err)
		}
	})
}

// branchSwitchedLine renders the room's recorded [版本] trace.
func branchSwitchedLine(from, to string) string {
	return i18n.Sf("【版本】房主把工作区分支切到 %s（%s → %s）——手头有未提交改动的成员请先报备核对", to, branchLabel(from), branchLabel(to))
}

// branchLabel renders a branch name for room lines（游离 HEAD 如实说）。
func branchLabel(b string) string {
	if b == "" {
		return i18n.S("游离 HEAD")
	}
	return b
}

// --- 分支隔离（v2.7.1：按成员的 git worktree）的座位面 ------------------------

// wtRoot is the worktree root's resolution: Options.GitWorktreeRoot first
// （tests 注入临时根，不碰真实 ~/.niuma）, the ~/.niuma/wt convention
// otherwise（与 dispatch.Fleet 同源）.
func (g *Face) WTRoot() string {
	if g.WorktreeRoot != "" {
		return g.WorktreeRoot
	}
	root, err := projects.RootDir()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "wt")
}

// memberTreeDir is the convention: the member's isolated worktree lives at
// <wt-root>/<project>/<person>.
func (g *Face) MemberTreeDir(key, person string) string {
	root := g.WTRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, key, person)
}

// memberTreeDirs lists the project's EXISTING member worktrees——trace/file
// 的放行根随之扩容（隔离成员引用的本地图片在自己树上）。
func (g *Face) MemberTreeDirs(key string) []string {
	dir := filepath.Join(g.WTRoot(), key)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// treeStatusOut is one member worktree's snapshot（缺树如实 null——座位
// 设了分支但人还没出生/树还没建，是合法中间态）。
type treeStatusOut struct {
	Branch string `json:"branch"`
	Dirty  int    `json:"dirty"` // staged + modified（tracked）
	Ahead  int    `json:"ahead,omitempty"`
	Behind int    `json:"behind,omitempty"`
}

func treeStatusAt(ctx context.Context, dir string) *treeStatusOut {
	repo, err := vcs.Detect(ctx, dir)
	if err != nil {
		return nil
	}
	st, err := repo.Status(ctx)
	if err != nil {
		return nil
	}
	return &treeStatusOut{Branch: st.Branch, Dirty: st.TrackedDirty(), Ahead: st.Ahead, Behind: st.Behind}
}

// handleGitSeats serves GET /p/{key}/git/seats: 成员×分支矩阵——在编制
// 行（branch 槽）＋每人树状态；末尾附「不在编制的残留树」行（离职/
// 迁出后的孤儿，面板给回收入口）。
func (g *Face) HandleGitSeats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	key := r.PathValue("key")
	if g.Stores.ProjectStore == nil || g.Stores.StaffStore == nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := g.Stores.ProjectStore.Get(key); !ok {
		writeGitErr(w, errProjectNotFound(key))
		return
	}
	if g.refuseIfGitDisabled(w, key) {
		return
	}
	ctx := context.Background()
	rows := g.Stores.StaffStore.ListByProject(key)
	known := map[string]bool{}
	type seatOut struct {
		Person     string         `json:"person"`
		Branch     string         `json:"branch,omitempty"` // 空=共享主工作树
		State      string         `json:"state"`
		Worktree   string         `json:"worktree,omitempty"` // 约定位（树未必已建）
		TreeStatus *treeStatusOut `json:"tree_status"`
	}
	out := []seatOut{}
	for _, e := range rows {
		// known 只认在编（Occupying）：离编行的树是孤儿（面板的回收
		// 入口），把自己当 known 会让「行已离编」的树永远进不了
		// orphan_trees——注释说的「行已离编」与实际判定从此一致。
		if e.Occupying() {
			known[e.Person] = true
		}
		wt := ""
		var ts *treeStatusOut
		if e.Branch != "" {
			wt = g.MemberTreeDir(key, e.Person)
			ts = treeStatusAt(ctx, wt)
		}
		out = append(out, seatOut{Person: e.Person, Branch: e.Branch, State: e.State, Worktree: wt, TreeStatus: ts})
	}
	// 残留树：树目录在、人不在编制（或行已离编）。
	orphans := []seatOut{}
	for _, dir := range g.MemberTreeDirs(key) {
		person := filepath.Base(dir)
		if known[person] {
			continue
		}
		orphans = append(orphans, seatOut{Person: person, Worktree: dir, TreeStatus: treeStatusAt(ctx, dir)})
	}
	httputil.WriteJSON(w, map[string]any{"project": key, "seats": out, "orphan_trees": orphans})
}

// handleGitSeat serves POST /p/{key}/git/seat {person, branch}（branch 空
// =回共享主树）：有调度器时全套经办（落账＋树内换枝/跨树重生＋告知，
// 失败自动回滚落账）；无调度器（调度未开/未开张）退化成 staffing 直写
// ——树与人都在，等出生时自然落位。
func (g *Face) HandleGitSeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil || g.Stores.StaffStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	var body struct {
		Person string `json:"person"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	if body.Person == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("person 必填"))
		return
	}
	row, ok := g.Stores.StaffStore.Get(key, body.Person)
	if !ok || !row.Occupying() {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("%s 不在项目 %s 的编制里——先在编制表把 TA 加进该项目", body.Person, key))
		return
	}
	_, repo, err := g.ProjectGit(key)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库——分支隔离无从谈起"))
		return
	}
	autoCreated := false // 缺失分支被自动切出时（GitPlan.AutoSeatBranch）房间留痕要说明
	if body.Branch != "" {
		if !vcs.ValidRefName(body.Branch) {
			httputil.WriteJSONErr(w, http.StatusBadRequest, vcs.RefNameError("分支名", body.Branch).Error())
			return
		}
		// 指名分支须真实存在（本地或远端）——手滑绑错名当场拦。例外：
		// 立项时开了「成员分支自动新建」（GitPlan.AutoSeatBranch，v2.14）
		// 的项目，缺失分支从基线自动切出（只建不删；外层仓库一概不动，
		// 见 gitboot.go 的红线）。
		bs, err := repo.Branches(context.Background())
		if err != nil {
			writeGitErr(w, err)
			return
		}
		known := false
		for _, b := range bs {
			if b.Name == body.Branch {
				known = true
				break
			}
		}
		if !known {
			created, cerr := g.ensureSeatBranch(key, repo, body.Branch)
			if cerr != nil {
				writeGitErr(w, cerr)
				return
			}
			autoCreated = created
		}
	}
	moved := false
	if g.Fleet != nil {
		lock := g.gitLockKey(key)
		lock.Lock()
		err := g.Fleet.GitSeat(key, body.Person, body.Branch)
		lock.Unlock()
		if err != nil {
			writeGitErr(w, err)
			return
		}
		moved = true
	} else {
		// 无调度器的退化直写与 DispatcherSeat 路径同一把锁——两条路
		// 都在改座位与动树，fetch/checkout 的互斥纪律不该因调度未开
		// 而豁免。
		lock := g.gitLockKey(key)
		lock.Lock()
		_, err := g.Stores.StaffStore.SetBranch(key, body.Person, body.Branch)
		lock.Unlock()
		if err != nil {
			writeGitErr(w, err)
			return
		}
	}
	// 房间留痕（面板写是房主私有知识，房间看得见）。
	if hub := g.RoomHub(key); hub != nil {
		if body.Branch != "" {
			line := i18n.Sf("【版本】%s 已驻分支 %s（专属工作树，与其他成员互不踩脚）", body.Person, body.Branch)
			if autoCreated {
				line = i18n.Sf("【版本】%s 已驻分支 %s（分支原不存在，已按立项计划自基线自动新建；专属工作树，与其他成员互不踩脚）", body.Person, body.Branch)
			}
			hub.SystemRecorded(line)
		} else {
			hub.SystemRecorded(i18n.Sf("【版本】%s 已回共享主工作区（原工作树保留）", body.Person))
		}
	}
	httputil.WriteJSON(w, map[string]any{"person": body.Person, "branch": body.Branch, "moved": moved, "branch_created": autoCreated})
}

// roomHub resolves the scope's hub (notifyBranchSwitched 的房间解析同
// 款).

// handleGitSeatCleanup serves POST /p/{key}/git/seat-cleanup {person,
// force?}: 回收不在编制（或已迁出该分支）的残留工作树。在驻分支上的
// 树不许收——先设回主树。脏树由 git 拒绝（force 是显式推翻）。
func (g *Face) HandleGitSeatCleanup(w http.ResponseWriter, r *http.Request) {
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
		Person string `json:"person"`
		Force  bool   `json:"force"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	if body.Person == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("person 必填"))
		return
	}
	// 在驻分支的树不收（人还工作在那）。
	if g.Stores.StaffStore != nil {
		if row, ok := g.Stores.StaffStore.Get(key, body.Person); ok && row.Occupying() && row.Branch != "" {
			httputil.WriteJSONErr(w, http.StatusConflict, i18n.Sf("%s 还驻在分支 %s 上——先「设分支」回主树再回收", body.Person, row.Branch))
			return
		}
	}
	_, repo, err := g.ProjectGit(key)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("该工作区不是 git 仓库"))
		return
	}
	wt := g.MemberTreeDir(key, body.Person)
	if wt == "" {
		httputil.WriteJSONErr(w, http.StatusInternalServerError, i18n.S("工作树根目录不可用"))
		return
	}
	// v2.8 gitflow：树的分支还有提交没并回主线时不收——「未合并工作
	// 是数据」从注释升级成护栏（先走合并提案收口；force 是显式推翻，
	// 连同未提交改动一起放弃）。已并入（或读不出分支）照旧由 git 的
	// 脏树拒绝兜底。
	if !body.Force {
		if ts := treeStatusAt(r.Context(), wt); ts != nil && ts.Branch != "" {
			if st, serr := repo.Status(r.Context()); serr == nil && st.Branch != "" && st.Branch != ts.Branch {
				if merged, merr := repo.MergedInto(r.Context(), ts.Branch, st.Branch); merr == nil && !merged {
					if ahead, aerr := repo.AheadCount(r.Context(), st.Branch, ts.Branch); aerr == nil && ahead > 0 {
						httputil.WriteJSONErr(w, http.StatusConflict, i18n.Sf(
							"%s 的分支 %s 还有 %d 提交未并入主线 %s——先走合并提案收口再回收（force=true 显式放弃）",
							body.Person, ts.Branch, ahead, st.Branch))
						return
					}
				}
			}
		}
	}
	lock := g.gitLockKey(key)
	lock.Lock()
	defer lock.Unlock()
	if err := repo.RemoveWorktree(context.Background(), wt, body.Force); err != nil {
		writeGitErr(w, err)
		return
	}
	httputil.WriteJSON(w, map[string]any{"person": body.Person, "removed": true})
}
