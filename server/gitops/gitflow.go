package gitops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// taskRefTokenRe picks the 台账号 a commit message cites（t_NN / r_NN，
// 整词——提交卡的任务关联与合并提案的引用集共用）。
var taskRefTokenRe = regexp.MustCompile(`\b([tr]_[0-9]+)\b`)

// refTokens lists the distinct cited ids, capped at a dozen（播报不是
// 台账，够指路即可）。
func refTokens(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range taskRefTokenRe.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
		if len(out) >= 12 {
			break
		}
	}
	return out
}

// --- L1 关单验提交 ---------------------------------------------------------

// doneEvidence is the close-out proof: the newest commit citing the task
// id on any branch（"" SHA = 无证据）。
type doneEvidence struct {
	SHA     string
	Subject string
}

// doneGate checks one would-be done flip. ok=false 意为拦下（文案在
// reason）；ok=true 时 evidence 可能仍为空（不适用或房主越过——此时
// 关单后不落对账行）。projectKey is the shelf the bare id resolved in
// （v2.10：房间自己的架）。判定链逐级放行：非 done 补丁、房主本人、任务
// 不存在/已结束（引擎自会按原口径拒）、未挂项目或项目非 git、读库失
// 败（宁漏卡不误伤）。
func (g *Face) DoneGate(actor, projectKey, taskID string, p tasks.Patch) (ev doneEvidence, ok bool, reason string) {
	if p.Status != tasks.StatusDone {
		return doneEvidence{}, true, ""
	}
	if g.Stores.Engine == nil {
		return doneEvidence{}, true, ""
	}
	if actor == g.Local {
		return doneEvidence{}, true, "" // 房主越过：机制挡的是「忘了提交」，不是房主的判断
	}
	t, found := g.Stores.Engine.GetIn(projectKey, taskID)
	if !found || t.Status == tasks.StatusDone {
		return doneEvidence{}, true, "" // 引擎按原口径处理（不存在/已结束）
	}
	// 读侧口径：空 project_key 读作大厅（PRD §4.2）；大厅在本工作室
	// 就是一等项目（有自己的工作区与调度器），一样验——卡的是「忘了
	// 提交」，跟任务住在哪个房间无关。
	key := t.ProjectKey
	if key == "" {
		key = chat.LobbyKey
	}
	if g.Stores.ProjectStore == nil {
		return doneEvidence{}, true, ""
	}
	p1, ok := g.Stores.ProjectStore.Get(key)
	if !ok || p1.Workspace == "" {
		return doneEvidence{}, true, ""
	}
	if p1.GitDisabled {
		return doneEvidence{}, true, "" // 总开关关闭＝非 git 项目同口径：不验提交
	}
	repo, err := vcs.Detect(context.Background(), p1.Workspace)
	if err != nil {
		return doneEvidence{}, true, "" // 非 git 项目不惩罚
	}
	msgs, err := repo.LogGrepAll(context.Background(), taskID, 50)
	if err != nil {
		return doneEvidence{}, true, "" // 反查失败不挡业务
	}
	if len(msgs) == 0 {
		return doneEvidence{}, false, i18n.Sf(
			"关单须先落提交：项目 %s 的全部分支上找不到引用 %s 的提交——请先在你的工作树提交（type(scope): 摘要，正文带「任务: %s」），或请房主直接改单放行",
			key, taskID, taskID)
	}
	return doneEvidence{SHA: msgs[0].SHA, Subject: vcs.SubjectOf(msgs[0].Body)}, true, ""
}

// taskUpdateGateOp runs the done-gated task_update: 拦下时私拒（taskOp
// 的 denied 契约），放行时照常引擎落账；done 落地且有证据时补对账
// （台账 SystemNote ＋房间 recorded 行）。改派/挪项目先过指派范围卡
// （v2.9 项目隔离），再进 done 证据卡。v2.10 号段分家：裸号先过「任务
// 归属＝房间项目」卡——别家房间的单，本房摸不到。
func (g *Face) TaskUpdateGateOp(h *chat.Hub, c *chat.Client, actor, taskID string, p tasks.Patch) {
	room := g.HubProject(h)
	if t, found := g.TaskPeekIn(room, taskID); found {
		// 项目任务只能改派给本项目在册（房主/在座/编制在册）；把带
		// 在办人的任务挂到别家项目同样拒。池单（无项目）不查——池
		// 本就是全室面。
		if p.Assignee != "" && t.ProjectKey != "" && !g.ProjectRosterHas(t.ProjectKey, p.Assignee) {
			g.Taps.TaskOp(h, c, actor, tasks.Outcome{Denied: true,
				Reason: i18n.Sf("改派对象「%s」不在项目 %s 的在册名单——项目隔离不认跨房指派；要用人先把人招进本项目", p.Assignee, t.ProjectKey)})
			return
		}
		if p.Project != "" && p.Project != t.ProjectKey && t.Assignee != "" && !g.ProjectRosterHas(p.Project, t.Assignee) {
			g.Taps.TaskOp(h, c, actor, tasks.Outcome{Denied: true,
				Reason: i18n.Sf("任务在办人「%s」不在目标项目 %s 的在册名单——先改派本项目在册者再挪项目，或先清指派", t.Assignee, p.Project)})
			return
		}
	} else if g.CrossRoomTask(room, taskID) {
		g.Taps.TaskOp(h, c, actor, tasks.Outcome{Denied: true,
			Reason: i18n.Sf("任务 %s 不属于项目 %s——号段分项目后裸号只在本房生效，别家房间的单请到那个房间（或房主带 project 槽）操作", taskID, room)})
		return
	}
	ev, ok, reason := g.DoneGate(actor, room, taskID, p)
	if !ok {
		g.Taps.TaskOp(h, c, actor, tasks.Outcome{Denied: true, Reason: reason})
		return
	}
	out := g.Taps.EngineOp(func(e *tasks.Engine) tasks.Outcome { return e.Update(room, actor, taskID, p) })
	g.Taps.TaskOp(h, c, actor, out)
	if !out.Denied && out.Event == "updated" && out.Task.Status == tasks.StatusDone {
		g.reconcileDone(h, out.Task, ev)
	}
}

// taskConfirmGateOp is taskUpdateGateOp's confirm twin: the pending patch
// may carry the done flip——确认放行前同一道卡。仲裁（房主动词）不设
// 卡：仲裁本身就是越过机制的那个出口。
func (g *Face) TaskConfirmGateOp(h *chat.Hub, c *chat.Client, actor, taskID string) {
	room := g.HubProject(h)
	if g.CrossRoomTask(room, taskID) {
		g.Taps.TaskOp(h, c, actor, tasks.Outcome{Denied: true,
			Reason: i18n.Sf("任务 %s 不属于项目 %s——号段分项目后裸号只在本房生效，别家房间的单请到那个房间（或房主带 project 槽）操作", taskID, room)})
		return
	}
	ev := doneEvidence{}
	if t, found := g.TaskPeekIn(room, taskID); found && t.Pending != nil {
		var ok bool
		var reason string
		ev, ok, reason = g.DoneGate(actor, room, taskID, t.Pending.Patch)
		if !ok {
			g.Taps.TaskOp(h, c, actor, tasks.Outcome{Denied: true, Reason: reason})
			return
		}
	}
	out := g.Taps.EngineOp(func(e *tasks.Engine) tasks.Outcome { return e.Confirm(room, actor, taskID) })
	g.Taps.TaskOp(h, c, actor, out)
	if !out.Denied && out.Event == "confirmed" && out.Task.Status == tasks.StatusDone {
		g.reconcileDone(h, out.Task, ev)
	}
}

// crossRoomTask reports whether a bare id that missed the room's shelf
// lives on ANOTHER project's shelf (uniquely) — the isolation refusal's
// evidence. Zero hits anywhere is a plain unknown id (the engine's own
// 「任务不存在」 speaks that); two hits is cross-shelf ambiguity, also
// not this room's task.
func (g *Face) CrossRoomTask(room, taskID string) bool {
	if g.Stores.Engine == nil {
		return false
	}
	t, ok := g.Stores.Engine.Get(taskID) // host face: unique-or-not-found
	return ok && tasks.SlotKey(t.ProjectKey) != tasks.SlotKey(room)
}

// taskPeekIn is Engine.GetIn through the server's nil-safe wrapper
// （engineOp 的读侧姊妹——nil 引擎返回未找到）。
func (g *Face) TaskPeekIn(projectKey, taskID string) (tasks.Task, bool) {
	if g.Stores.Engine == nil {
		return tasks.Task{}, false
	}
	return g.Stores.Engine.GetIn(projectKey, taskID)
}

// reconcileDone lands the close-out proof where eyes and ledgers read
// it: the task's own log（系统注记，供 task show 与审计）and one
// recorded room line（关单不再是一句话——它对着一笔真实提交）.
func (g *Face) reconcileDone(h *chat.Hub, t tasks.Task, ev doneEvidence) {
	if ev.SHA == "" {
		return
	}
	if g.Stores.Engine != nil {
		g.Stores.Engine.SystemNote(t.ProjectKey, t.ID, i18n.Sf("关单提交对账：%s", vcs.ShortSHA(ev.SHA)))
	}
	if h != nil {
		h.SystemRecorded(i18n.Sf("【版本】%s 关单对账：提交 %s「%s」已在库", t.ID, vcs.ShortSHA(ev.SHA), clipRunesLine(ev.Subject, 40)))
	}
}

// clipRunesLine clips one line to n runes for room notices.
func clipRunesLine(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// --- L2 提交播报 -----------------------------------------------------------

// handleGitCommitNotify serves POST /internal/git/commit {sha, root}——
// post-commit 钩子（CLI `niuma vcs post-commit`）的落地腿。root 是提交
// 发生树的公共 git dir（跨树身份）；按它把仓库映射回项目，读出这笔提
// 交的形状，广播 "git"{commit} 帧到项目房间。钩子侧对失败静默，这里
// 的 4xx/5xx 只是日志与回执，绝不影响提交本身。
func (g *Face) HandleGitCommitNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		SHA  string `json:"sha"`
		Root string `json:"root"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	body.SHA, body.Root = strings.TrimSpace(body.SHA), strings.TrimSpace(body.Root)
	if body.SHA == "" || body.Root == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("sha 与 root 必填"))
		return
	}
	key, ok := g.projectByCommonDir(body.Root)
	if !ok {
		httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("该仓库不在工作室台账里（root=%s）——请确认提交发生在已登记项目的 workspace 内", body.Root))
		return
	}
	_, repo, err := g.ProjectGit(key)
	if err != nil {
		writeGitErr(w, err) // 总开关关闭＝409 指路文案；探测失败如实回
		return
	}
	if repo == nil {
		httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("项目 %s 的工作区已不是 git 仓库", key))
		return
	}
	msg, files, err := repo.Show(context.Background(), body.SHA)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	gc := &chat.GitCommit{
		SHA: msg.SHA, Author: msg.Author, Subject: vcs.SubjectOf(msg.Body),
		Body: msg.Body, DateTS: msg.DateTS, Refs: refTokens(msg.Body),
	}
	for _, f := range files {
		gc.Files = append(gc.Files, chat.GitFile{Path: f.Path, Added: f.Added, Removed: f.Removed, Binary: f.Binary})
		gc.Additions += f.Added
		gc.Deletions += f.Removed
	}
	gc.Subject = clipRunesLine(gc.Subject, 120)
	frame := chat.Message{Type: chat.MsgGit, Event: "commit",
		From: msg.Author, TS: msg.DateTS, Git: gc,
		Text: i18n.Sf("%s 提交 %s：%s（%d 文件 +%d/-%d）",
			msg.Author, vcs.ShortSHA(msg.SHA), gc.Subject, len(gc.Files), gc.Additions, gc.Deletions)}
	if hub := g.RoomHub(key); hub != nil {
		hub.Broadcast(frame)
	}
	httputil.WriteJSON(w, map[string]any{"project": key, "sha": msg.SHA})
}

// projectByCommonDir maps a repo's COMMON git dir back to its project:
// worktree 与主树同仓不同径（Root 各是各的树根），CommonDir 才是跨树
// 身份。Detect 对两侧都过了 realpath，直接比对。
func (g *Face) projectByCommonDir(root string) (string, bool) {
	ctx := context.Background()
	root = filepath.Clean(root)
	for _, p := range g.Stores.ProjectStore.List() {
		if p.Workspace == "" || p.Status == projects.StatusArchived {
			continue
		}
		if r, err := vcs.Detect(ctx, p.Workspace); err == nil && r.CommonDir == root {
			return p.Key, true
		}
	}
	return "", false
}

// --- L3 合并评审门 ---------------------------------------------------------

// mergeDenied builds the private refusal frame（planDenied 的合并姊妹）。
func mergeDenied(actor, reason string) chat.Message {
	return chat.Message{Type: chat.MsgMerge, Event: "denied",
		From: actor, Text: reason, TS: time.Now().Unix()}
}

// mergeSubmitAs files a merge proposal into the submitting room's slot:
// orchestrator-only channel（房主可代提——卡的是 accept，那才是房主专
// 属关口）。卡片数据（提交数/文件红绿/引用集）在提交时快照；accept
// 的前置对账会重读世界，过期自会拒。
func (g *Face) MergeSubmitAs(hub *chat.Hub, c *chat.Client, actor string, m *merge.Merge) chat.Message {
	if g.Stores.MergeStore == nil {
		return mergeDenied(actor, i18n.S("合并提案系统未启用"))
	}
	if m == nil {
		return mergeDenied(actor, i18n.S("提案载荷缺失"))
	}
	if !g.IsOrchestrator(actor) {
		return mergeDenied(actor, i18n.S("合并提案只能由编排者（岗位「排期编排」）提交——分支何时收口是编排判断"))
	}
	room := g.RoomKey(hub)
	if m.ProjectKey == "" {
		m.ProjectKey = room
	}
	if m.ProjectKey != room {
		return mergeDenied(actor, i18n.Sf("合并提案只能提交给本办公室项目（当前办公室 %s，提案写的是 %s）", room, m.ProjectKey))
	}
	branch := strings.TrimSpace(m.Branch)
	if branch == "" || !vcs.ValidRefName(branch) {
		return mergeDenied(actor, i18n.S("branch 必填且须是合法分支名"))
	}
	_, repo, err := g.ProjectGit(room)
	if err != nil {
		return mergeDenied(actor, err.Error())
	}
	if repo == nil {
		return mergeDenied(actor, i18n.S("项目工作区不是 git 仓库——合并无从谈起"))
	}
	ctx := context.Background()
	bs, err := repo.Branches(ctx)
	if err != nil {
		return mergeDenied(actor, err.Error())
	}
	known := false
	for _, b := range bs {
		if b.Remote == "" && b.Name == branch {
			known = true
			break
		}
	}
	if !known {
		return mergeDenied(actor, i18n.Sf("分支 %q 不在该仓库的本地分支里（远端分支请先 fetch 并落地本地）", branch))
	}
	st, err := repo.Status(ctx)
	if err != nil {
		return mergeDenied(actor, err.Error())
	}
	into := strings.TrimSpace(m.Into)
	if into == "" {
		into = st.Branch // 缺省：主工作区此刻的集成分支（提交时快照）
	}
	if into == "" {
		return mergeDenied(actor, i18n.S("主工作区处于游离 HEAD——提案须显式写 into（目标分支）"))
	}
	if !vcs.ValidRefName(into) {
		return mergeDenied(actor, vcs.RefNameError("目标分支名", into).Error())
	}
	if branch == into {
		return mergeDenied(actor, i18n.Sf("分支 %s 不能并进自己", branch))
	}
	ahead, err := repo.AheadCount(ctx, into, branch)
	if err != nil {
		return mergeDenied(actor, err.Error())
	}
	if ahead <= 0 {
		return mergeDenied(actor, i18n.Sf("分支 %s 没有领先 %s 的提交——无事可并", branch, into))
	}
	if merged, _ := repo.MergedInto(ctx, branch, into); merged {
		return mergeDenied(actor, i18n.Sf("分支 %s 的提交已全部在 %s 里——无需再并", branch, into))
	}
	payload := merge.Merge{
		ProjectKey: room, Branch: branch, Into: into, Req: strings.TrimSpace(m.Req),
		Commits: ahead,
	}
	if rows, err := repo.DiffNumStat(ctx, into, branch); err == nil {
		payload.Files = len(rows)
		for _, f := range rows {
			payload.Additions += f.Added
			payload.Deletions += f.Removed
		}
	}
	if msgs, err := repo.LogRange(ctx, into, branch, 20); err == nil {
		for _, cm := range msgs {
			payload.Tasks = appendUnique(payload.Tasks, refTokens(cm.Body)...)
		}
	}
	stored, superseded := g.Stores.MergeStore.Submit(g.Subject, room, actor, payload, time.Now().Unix())
	// 提交失败形状（前置审计 A：Submit 无 error 签名，事务失败返回
	// (nil, nil)）——私回 denied，不解引用（同 planSubmitAs 的守卫）。
	if stored == nil {
		return mergeDenied(actor, i18n.S("合并提案未落库（存储写入失败），槽内状态未变——请稍后重试"))
	}
	if superseded != nil {
		hub.Broadcast(chat.Message{Type: chat.MsgMerge, Event: "superseded",
			Merge: superseded.Wire(), From: actor,
			Text: i18n.Sf("合并提案 %s（%s → %s）被新提案 %s 取代（未审作废）",
				superseded.ID, superseded.Branch, superseded.Into, stored.ID),
			TS: time.Now().Unix()})
	}
	msg := chat.Message{Type: chat.MsgMerge, Event: "submitted",
		Merge: stored.Wire(), From: actor,
		Text: i18n.Sf("%s 提交了合并提案 %s：%s → %s（%d 提交，%d 文件 +%d/-%d%s）@房主",
			actor, stored.ID, stored.Branch, stored.Into, stored.Commits, stored.Files,
			stored.Additions, stored.Deletions, mergeRefsSuffix(stored.Tasks)),
		TS: time.Now().Unix()}
	hub.Broadcast(msg)
	return msg
}

// mergeRefsSuffix renders the cited t_NN/r_NN tail（涉及 …）。Broadcast
// faces go through i18n so en rooms read "involving"; the one commit-
// message use (acceptMergeCore) inherits the switch — the message is
// machine-written on the user's behalf either way.
func mergeRefsSuffix(refs []string) string {
	if len(refs) == 0 {
		return ""
	}
	return i18n.Sf("，涉及 %s", strings.Join(refs, " "))
}

// appendUnique merges token lists without duplicates.
func appendUnique(dst []string, src ...string) []string {
	seen := map[string]bool{}
	for _, x := range dst {
		seen[x] = true
	}
	for _, x := range src {
		if !seen[x] {
			seen[x] = true
			dst = append(dst, x)
		}
	}
	return dst
}

// mergeAcceptAs is the host's confirmation gate（planAcceptAs 的合并
// 姊妹：host-only，落腿在 acceptMergeCore）。
func (g *Face) MergeAcceptAs(hub *chat.Hub, c *chat.Client, actor, mergeID, project string) chat.Message {
	if g.Stores.MergeStore == nil {
		return mergeDenied(actor, i18n.S("合并提案系统未启用"))
	}
	if actor != g.Local {
		return mergeDenied(actor, i18n.S("合并的接受/拒绝是房主专属关口"))
	}
	return g.AcceptMergeCore(hub, c, actor, mergeID, project, "")
}

// acceptMergeCore is the landing leg AFTER the host gate: 前置对账（主
// 工作区仍驻目标分支、树净、分支仍领先未并入）→ Take 一次性消费 →
// git merge --no-ff --no-verify（评审门已过，格式钩子放行自家的合
// 并；冲突立刻 abort 还原）→ 广播 merged ＋编年史一行 ＋分支主人背
// 景告知。对账失败拒收不消费槽件；合并执行失败槽件如实已消费（plan
// 病态中拒的先例），文案指路重提。autopilot（纯新增分支满宽限）以
// actor 自动驾驶 直入此处——c nil 无回执连接。
func (g *Face) AcceptMergeCore(hub *chat.Hub, c *chat.Client, actor, mergeID, project, note string) chat.Message {
	if g.Stores.MergeStore == nil {
		return mergeDenied(actor, i18n.S("合并提案系统未启用"))
	}
	key := project
	if key == "" {
		key = g.RoomKey(hub)
	}
	cur := g.Stores.MergeStore.Pending(g.Subject, key)
	if cur == nil {
		return mergeDenied(actor, i18n.Sf("项目 %s 没有待审合并提案", key))
	}
	if cur.ID != mergeID {
		return mergeDenied(actor, i18n.Sf("合并提案 %s 已不是当前待审件（现为 %s）", mergeID, cur.ID))
	}
	_, repo, err := g.ProjectGit(key)
	if err != nil {
		return mergeDenied(actor, err.Error())
	}
	if repo == nil {
		return mergeDenied(actor, i18n.S("项目工作区不是 git 仓库"))
	}
	lock := g.gitLockKey(key)
	lock.Lock()
	defer lock.Unlock()
	ctx := context.Background()
	st, err := repo.Status(ctx)
	if err != nil {
		return mergeDenied(actor, err.Error())
	}
	if st.Branch != cur.Into {
		return mergeDenied(actor, i18n.Sf("主工作区现在驻在 %s，提案要并进 %s——先切回去（版本管理面板）或驳回重提", branchLabel(st.Branch), cur.Into))
	}
	if dirty := st.TrackedDirty(); dirty > 0 {
		return mergeDenied(actor, i18n.Sf("主工作区有 %d 处未提交改动（暂存 %d、修改 %d）——先提交或 stash 再合并", dirty, st.Staged, st.Modified))
	}
	ahead, err := repo.AheadCount(ctx, cur.Into, cur.Branch)
	if err != nil {
		return mergeDenied(actor, err.Error())
	}
	if ahead <= 0 {
		return mergeDenied(actor, i18n.Sf("分支 %s 已无领先 %s 的提交（可能已被并入）——请驳回这份过期提案", cur.Branch, cur.Into))
	}
	if _, err := g.Stores.MergeStore.Take(g.Subject, key, mergeID); err != nil {
		return mergeDenied(actor, err.Error())
	}
	commitMsg := fmt.Sprintf("merge: %s → %s（%s%s）", cur.Branch, cur.Into, cur.ID, mergeRefsSuffix(cur.Tasks))
	if err := repo.MergeNoFF(ctx, cur.Branch, commitMsg); err != nil {
		log.Printf("[版本] %s: 合并提案 %s 执行失败（槽件已消费）：%v", key, cur.ID, err)
		if dst := g.PlanHub(key); dst != nil {
			dst.SystemRecorded(i18n.Sf("【版本】合并提案 %s（%s → %s）执行失败（提案已消费，请修复后重提，git 原文见日志）：%s",
				cur.ID, cur.Branch, cur.Into, clipRunesLine(err.Error(), 200)))
		}
		return mergeDenied(actor, err.Error())
	}
	sha, _ := repo.HeadSHA(ctx)
	dst := g.PlanHub(key)
	msg := chat.Message{Type: chat.MsgMerge, Event: "merged",
		Merge: cur.Wire(), From: actor,
		Text: i18n.Sf("%s 把 %s 并入 %s（%s：%d 提交 → %s）%s%s",
			actor, cur.Branch, cur.Into, cur.ID, cur.Commits, vcs.ShortSHA(sha), mergeRefsSuffix(cur.Tasks), note),
		TS: time.Now().Unix()}
	dst.Broadcast(msg)
	verbs.PlanReceipt(dst, hub, c, msg)
	if g.Chronicle != nil {
		g.Chronicle.OnMerged(cur, actor)
	}
	g.mergeLandedNotify(key, cur)
	return msg
}

// mergeLandedNotify taps Options.MergeLanded（PlanLanded 的合并姊妹：
// 广播帧不唤醒任何人，这条腿把「你的分支已并入」送进分支主人的背景
// 车道）。失败只记日志——合并不是通知的事。
func (g *Face) mergeLandedNotify(key string, cur *merge.Merge) {
	if g.Fleet == nil {
		return
	}
	go util.Guard("server: fleet notify", func() {
		if err := g.Fleet.MergeLanded(key, cur.Branch, cur.Into); err != nil {
			log.Printf("[版本] %s: 合并落地后告知分支主人失败：%v", key, err)
		}
	})
}

// mergeRejectAs is the host's refusal: pops the slot, broadcasts
// rejected to the project room（planRejectAs 的合并姊妹）。
func (g *Face) MergeRejectAs(hub *chat.Hub, c *chat.Client, actor, mergeID, project string) chat.Message {
	if g.Stores.MergeStore == nil {
		return mergeDenied(actor, i18n.S("合并提案系统未启用"))
	}
	if actor != g.Local {
		return mergeDenied(actor, i18n.S("合并的接受/拒绝是房主专属关口"))
	}
	key := project
	if key == "" {
		key = g.RoomKey(hub)
	}
	m, err := g.Stores.MergeStore.Take(g.Subject, key, mergeID)
	if err != nil {
		return mergeDenied(actor, err.Error())
	}
	msg := chat.Message{Type: chat.MsgMerge, Event: "rejected",
		Merge: m.Wire(), From: actor,
		Text: i18n.Sf("%s 驳回了合并提案 %s（%s → %s）——分支保留原样", actor, m.ID, m.Branch, m.Into),
		TS:   time.Now().Unix()}
	dst := g.PlanHub(key)
	dst.Broadcast(msg)
	verbs.PlanReceipt(dst, hub, c, msg)
	return msg
}

// handleProjectMerge serves the pending-merge read (GET /p/{key}/merge,
// handleProjectPlan 的合并姊妹)。
func (g *Face) HandleProjectMerge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	key := r.PathValue("key")
	if g.Stores.MergeStore == nil || g.Stores.ProjectStore == nil || !g.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	if g.refuseIfGitDisabled(w, key) { // 合并流随总开关整面停——待办单留着但不给读面
		return
	}
	httputil.WriteJSON(w, map[string]any{
		"project": key,
		"merge":   g.Stores.MergeStore.Pending(g.Subject, key),
	})
}
