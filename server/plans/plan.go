package plans

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/server/achv"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// Face is the plan/requirement/meeting review domain's world: the
// seat, the chronicle hook, the plan-room resolver (night-tap wiring
// stays the shell's), and the achievements observer. Split from the
// shell's plan.go/reqs.go/meetings.go.
type Face struct {
	*verbs.Seat
	Chronicle      Chronicle
	PlanHub        func(projectKey string) *chat.Hub
	ObserveAchieve func(hub interface{ System(string) }, ev Event)
	WireNightTap   func(h *chat.Hub)
}

// Chronicle is the 工作室志's narrow face this domain taps.
type Chronicle interface {
	OnReqSplit(projectKey, reqID, reqTitle, by string)
	OnReqPark(projectKey, reqID, reqTitle, note, by string)
	OnReqUnpark(projectKey, reqID, reqTitle, by string)
}

// Event mirrors the achievements observer's event shape (the achv
// domain's own type; kept structural so the seam stays one-way).
type Event = achv.Event

// FaceOf adapts a seat plus the shell's chronicle/plan-hub seams.
func FaceOf(st *verbs.Seat, chron Chronicle, planHub func(string) *chat.Hub) *Face {
	return &Face{Seat: st, Chronicle: chron, PlanHub: planHub}
}

// roomKey resolves a routed hub back to its project key (the lobby
// reads as chat.LobbyKey — the registry never holds the server's own
// hub, and a registry-less embed is single-room by definition).
func (pn *Face) roomKey(hub *chat.Hub) string { return pn.RoomKey(hub) }

// planHub resolves the plan's room through the shell-wired seam (the
// night-tap wiring rides it — shell state).
func (pn *Face) planHub(projectKey string) *chat.Hub { return pn.PlanHub(projectKey) }

// wireNightTap delegates to the shell-wired night-gate seam.
func (pn *Face) wireNightTap(h *chat.Hub) { pn.WireNightTap(h) }

// planDenied builds the private refusal frame (the task/kb contract).
func planDenied(actor, reason string) chat.Message {
	return chat.Message{Type: chat.MsgPlan, Event: "denied",
		From: actor, Text: reason, TS: time.Now().Unix()}
}

// isOrchestrator reports whether actor carries the orchestrator role
// marker (or is the host — the host may file a proposal on anyone's
// behalf; the gate that matters is acceptance, which is host-only).
func (pn *Face) isOrchestrator(actor string) bool { return pn.IsOrchestrator(actor) }

func planReceipt(broadcastHub, requesterHub *chat.Hub, c *chat.Client, msg chat.Message) {
	verbs.PlanReceipt(broadcastHub, requesterHub, c, msg)
}

// planSubmitAs files a proposal into the submitting room's slot:
// orchestrator-only channel, project must be the submitting room's
// own, content gated by plan.Validate plus version/req existence. A
// pending proposal for a DIFFERENT requirement refuses the submit
// (the cross-req supersede guard — displacement is the same-req
// revision path alone, never a way to discard a colleague
// requirement's awaiting verdict). On success it broadcasts
// superseded (when a proposal was displaced) and submitted, and
// returns the submitted frame (the seatless channel writes it back
// as the receipt).
func (pn *Face) PlanSubmitAs(hub *chat.Hub, c *chat.Client, actor string, p *plan.Plan) chat.Message {
	if pn.Stores.PlanStore == nil {
		return planDenied(actor, i18n.S("提案系统未启用"))
	}
	if p == nil {
		return planDenied(actor, i18n.S("提案载荷缺失"))
	}
	if !pn.IsOrchestrator(actor) {
		return planDenied(actor, i18n.Sf("提案只能由编排者（岗位「%s」）提交", agents.OrchestratorRole))
	}
	room := pn.RoomKey(hub)
	if p.ProjectKey == "" {
		p.ProjectKey = room // convenience: the room the submitter sits in
	}
	if p.ProjectKey != room {
		return planDenied(actor, i18n.Sf("提案只能提交给本办公室项目（当前办公室 %s，提案写的是 %s）", room, p.ProjectKey))
	}
	if err := pn.CheckPlanRefs(p); err != nil {
		return planDenied(actor, err.Error())
	}
	if err := p.Validate(); err != nil {
		return planDenied(actor, err.Error())
	}
	// 跨需求顶替护栏：提案槽一房一件，槽里已有的待审件挂的是别的
	// 需求时，新提案必须等房主先批/驳那一份——supersede 只服务同需求
	// 的修订重提（一场会内 p_52→p_55 的收口弧线），不能把上一场评审
	// 等待房主的结论静默作废。同需求（含同空 req 的旧形状）照旧可顶。
	if cur := pn.Stores.PlanStore.Pending(pn.Subject, room); cur != nil && cur.Req != p.Req {
		return planDenied(actor, i18n.Sf("提案 %s「%s」%s 还在等房主审批——提案槽一房一件，新提案会把它顶成未审作废；先等房主批准或驳回（同需求的修订重提不受限）", cur.ID, cur.Title, ReqSuffix(cur.Req)))
	}

	stored, superseded := pn.Stores.PlanStore.Submit(pn.Subject, room, actor, *p, time.Now().Unix())
	// 提交失败形状（前置审计 A §2.8）：无 error 签名的 Submit 在事务失败时返回 (nil, nil)——
	// LocalStore 恒非 nil，这是消费方没见过的形状。消化成私回 denied，
	// 绝不解引用（server 无 recover，panic 即全进程宕机）。
	if stored == nil {
		return planDenied(actor, i18n.S("提案未落库（存储写入失败），槽内状态未变——请稍后重试"))
	}
	if superseded != nil {
		hub.Broadcast(chat.Message{Type: chat.MsgPlan, Event: "superseded",
			Plan: superseded.Wire(), From: actor,
			Text: i18n.Sf("提案 %s「%s」被新提案 %s 取代（未审作废）", superseded.ID, superseded.Title, stored.ID),
			TS:   time.Now().Unix()})
	}
	msg := chat.Message{Type: chat.MsgPlan, Event: "submitted",
		Plan: stored.Wire(), From: actor,
		Text: i18n.Sf("%s 提交了提案 %s「%s」：%d 条任务待审 @房主%s",
			actor, stored.ID, stored.Title, len(stored.Tasks), ReqSuffix(stored.Req)),
		TS: time.Now().Unix()}
	hub.Broadcast(msg)
	return msg
}

// checkPlanRefs validates the proposal's references against the
// project and requirement stores: a named version must exist on the
// project, a named requirement must exist, belong to this project and
// not be closed. Stores left unwired (nil) skip their check — the
// linkage then simply cannot be verified.
func (pn *Face) CheckPlanRefs(p *plan.Plan) error {
	if p.Version != "" && pn.Stores.ProjectStore != nil {
		proj, ok := pn.Stores.ProjectStore.Get(p.ProjectKey)
		if !ok {
			return errors.New(i18n.Sf("挂靠项目不存在: %s", p.ProjectKey))
		}
		found := false
		for _, v := range proj.Versions {
			if v.Name == p.Version {
				found = true
				break
			}
		}
		if !found {
			return errors.New(i18n.Sf("项目 %s 没有版本 %q", p.ProjectKey, p.Version))
		}
	}
	if p.Req != "" && pn.Stores.Requirements != nil {
		r, ok := pn.Stores.Requirements.GetIn(pn.Subject, p.ProjectKey, p.Req)
		if !ok {
			return errors.New(i18n.Sf("需求 %s 不存在", p.Req))
		}
		if r.ProjectKey != p.ProjectKey {
			return errors.New(i18n.Sf("需求 %s 属于项目 %s，不能挂进 %s 的提案", p.Req, r.ProjectKey, p.ProjectKey))
		}
		if r.Status == "closed" {
			return errors.New(i18n.Sf("需求 %s 已关闭，不再接受拆解", p.Req))
		}
	}
	// 指派范围（v2.9 项目隔离）：每条任务的非空 assignee 必须在目标
	// 项目的在册名单——房主、本房在座、编制在册（招聘中未入座）任一；
	// 别家项目的成员不点名。空 assignee 是任务池，不查。
	for i, pt := range p.Tasks {
		if pt.Assignee == "" {
			continue
		}
		if !pn.ProjectRosterHas(p.ProjectKey, pt.Assignee) {
			return errors.New(i18n.Sf("第 %d 条的指派对象「%s」不在项目 %s 的在册名单（房主/本房在座/编制在册）——项目隔离不认跨房指派；要用人先把人招进本项目", i+1, pt.Assignee, p.ProjectKey))
		}
	}
	return nil
}

// planAcceptAs is the host's confirmation gate. revised (optional) is
// the entry-edited full replacement — identity fields (id, project,
// req, submitter) stay locked to the pending plan, content fields
// (title/need/version/tasks) take the edit. Acceptance pops the slot,
// creates every entry through the engine (ids assigned here; in-plan
// deps indices translate to the fresh ids of already-landed earlier
// entries), flips the requirement to split and broadcasts (to the
// plan's room, with a private receipt when the requester sits
// elsewhere). The landing also taps Options.PlanLanded — the
// dispatcher's addressed wake, because the broadcast frames carry no
// @ and wake nobody. A denial leaves the slot untouched.
func (pn *Face) PlanAcceptAs(hub *chat.Hub, c *chat.Client, actor, planID string, revised *plan.Plan, project string) chat.Message {
	if pn.Stores.PlanStore == nil {
		return planDenied(actor, i18n.S("提案系统未启用"))
	}
	if actor != pn.Local {
		return planDenied(actor, i18n.S("提案的接受/拒绝是房主专属关口"))
	}
	return pn.AcceptPlanCore(hub, c, actor, planID, revised, project, "")
}

// acceptPlanCore is the landing leg AFTER the host gate — everything
// from the pending peek to the PlanLanded wake. The host verb reaches
// here through planAcceptAs; the autopilot engine (server/autopilot.go)
// calls it directly as the actor 自动驾驶 with a note suffix spelling
// out the grace that stood in for the host. c nil means no requester
// connection to receipt (the engine path). Byte-for-byte the host path
// with note "".
func (pn *Face) AcceptPlanCore(hub *chat.Hub, c *chat.Client, actor, planID string, revised *plan.Plan, project, note string) chat.Message {
	if pn.Stores.PlanStore == nil {
		return planDenied(actor, i18n.S("提案系统未启用"))
	}
	if pn.Stores.Engine == nil {
		return planDenied(actor, i18n.S("任务系统未启用"))
	}
	key := project
	if key == "" {
		key = pn.RoomKey(hub)
	}
	cur := pn.Stores.PlanStore.Pending(pn.Subject, key)
	if cur == nil {
		return planDenied(actor, i18n.Sf("项目 %s 没有待审提案", key))
	}
	if cur.ID != planID {
		return planDenied(actor, i18n.Sf("提案 %s 已不是当前待审件（现为 %s）", planID, cur.ID))
	}
	cand := *cur
	if revised != nil {
		rev := revised.Sanitized()
		if rev.Title != "" {
			cand.Title = rev.Title
		}
		cand.Need, cand.Version, cand.Tasks = rev.Need, rev.Version, rev.Tasks
	}
	if err := pn.CheckPlanRefs(&cand); err != nil {
		return planDenied(actor, i18n.Sf("修订稿被拒：%s", err.Error()))
	}
	if err := cand.Validate(); err != nil {
		return planDenied(actor, i18n.Sf("修订稿被拒：%s", err.Error()))
	}
	// headroom check before the first CreatePlanned: a batch that
	// cannot fit whole refuses whole (MaxTasks semantics stay honest).
	if pn.Stores.Engine.Count()+len(cand.Tasks) > tasks.MaxTasks {
		return planDenied(actor, i18n.Sf("接受将超出任务上限 %d 条（现 %d + 提案 %d）",
			tasks.MaxTasks, pn.Stores.Engine.Count(), len(cand.Tasks)))
	}
	// —— 动词粒度收口（转正硬条件①）：三腿暂存→一个事务→内存收编 ——
	//
	// 弹槽、逐条入库、需求标拆解在各自引擎里先「暂存」（锁持住、
	// 内存不动或可回滚），三份后状态文档经 Stores.LedgerTx 写进同
	// 一事务；提交成功才让引擎内存收编，失败则全部弃置——提案不
	// 丢、任务账不半、需求不半，三处写要么全落地要么全没有（旧
	// 路径是每动词各自落盘，批中撕裂缝留给重启）。
	by := cand.SubmittedBy
	if by == "" {
		by = actor
	}
	dst := pn.PlanHub(key)
	planEng := pn.Stores.PlanStore
	taskEng := pn.Stores.Engine
	reqEng := pn.Stores.Requirements

	// 暂存第一腿：弹槽（一次性 id 门在锁内过——peek 与 take 之间的
	// 顶替在这里现形，槽原封不动）。
	if _, err := planEng.AcceptBegin(pn.Subject, key, planID); err != nil {
		return planDenied(actor, err.Error())
	}
	// 暂存第二腿：逐条入库（分片锁持住，可整批回滚）。
	taskEng.AcceptBegin()
	reqStaged := false
	abortAll := func() {
		taskEng.AcceptAbort()
		planEng.AcceptAbort()
		if reqStaged {
			reqEng.AcceptAbort()
		}
	}
	var ids []string
	landed := make([]*tasks.Task, 0, len(cand.Tasks))
	var frames []chat.Message
	for _, pt := range cand.Tasks {
		draft := tasks.Task{
			Title: pt.Title, Desc: pt.Desc, Assignee: pt.Assignee,
			ProjectKey: cand.ProjectKey, Version: cand.Version,
			Req: cand.Req, PlanID: cand.ID,
			StartTS: pt.StartTS, EndTS: pt.EndTS, Milestone: pt.Milestone,
			Deps: plan.TranslateDeps(pt.Deps, ids),
		}
		out := taskEng.AcceptCreatePlanned(by, draft)
		if out.Denied {
			// pathological mid-batch refusal (content was pre-validated;
			// the whole-set gate disagreed): the staged accept aborts
			// WHOLE — nothing landed, the slot stands（语义变更，成文：
			// 旧路径留「前缀已入库＋槽已消费」的半批）。
			abortAll()
			return planDenied(actor, i18n.Sf("第 %d 条入库被拒：%s（整批未落地，提案仍在槽内，请修订重试）",
				len(ids)+1, out.Reason))
		}
		ids = append(ids, out.Task.ID)
		snapshot := out.Task // the engine's Outcome already carries a copy
		landed = append(landed, &snapshot)
		frames = append(frames, chat.Message{Type: chat.MsgTask, Event: out.Event,
			Task: out.Task.Wire(), From: by, Text: out.Text, TS: time.Now().Unix()})
	}
	// 暂存第三腿：需求标拆解（best-effort 同旧——再拆解/已关闭只记
	// 日志，不拦落地）。
	if cand.Req != "" && reqEng != nil {
		if err := reqEng.AcceptBegin(pn.Subject, cand.ProjectKey, cand.Req); err != nil {
			log.Printf("plan %s: mark req %s split: %v", cand.ID, cand.Req, err)
		} else {
			reqStaged = true
		}
	}
	// 三份后状态写进同一事务（无 LedgerTx 的内存 boot＝零缝空写，
	// 内存即真源）；任一失败＝全批弃置，槽未消费。
	writeErr := error(nil)
	if pn.Stores.LedgerTx != nil {
		writeErr = pn.Stores.LedgerTx(func(tx verbs.LedgerSeam) error {
			if err := planEng.AcceptWrite(tx.Plan()); err != nil {
				return err
			}
			if err := taskEng.AcceptWrite(tx.Tasks()); err != nil {
				return err
			}
			if reqStaged {
				return reqEng.AcceptWrite(tx.Requirements())
			}
			return nil
		})
	}
	if writeErr != nil {
		log.Printf("plan %s accept tx: %v — 整批回滚（槽未消费）", cand.ID, writeErr)
		abortAll()
		return planDenied(actor, i18n.Sf("提案落地未成（%v）——整批未落地，提案仍在槽内，请重试", writeErr))
	}
	planEng.AcceptCommit()
	taskEng.AcceptCommit()
	if reqStaged {
		reqEng.AcceptCommit()
	}
	for _, f := range frames {
		dst.Broadcast(f)
	}
	if reqStaged && pn.Chronicle != nil {
		// r_10 编年史钩子（t_152）：需求立顶（首次 split 成功才录）；
		// 行落提案所属办公室的志
		title := cand.Req
		if r, ok := reqEng.GetIn(pn.Subject, cand.ProjectKey, cand.Req); ok && r.Title != "" {
			title = r.Title
		}
		pn.Chronicle.OnReqSplit(cand.ProjectKey, cand.Req, title, actor)
		// r_18 成就引擎（t_187）：需求线落地事件（零新埋点同位）
		pn.ObserveAchieve(dst, Event{Kind: "req-split", Member: actor, ReqID: cand.Req, Project: cand.ProjectKey, At: time.Now().Unix()})
	}
	msg := chat.Message{Type: chat.MsgPlan, Event: "accepted",
		Plan: cand.Wire(), From: actor,
		Text: i18n.Sf("%s 接受了提案 %s「%s」：%d 条任务入库%s%s",
			actor, cand.ID, cand.Title, len(ids), ReqSuffix(cand.Req), note),
		TS: time.Now().Unix()}
	dst.Broadcast(msg)
	planReceipt(dst, hub, c, msg)
	// r_18（t_189 表换入）：提案被接受事件喂判定——A4 一锤定音（首个
	// 提案获通过）的事件源；主语是提交人（by），不是接受的房主/代收者
	pn.ObserveAchieve(dst, Event{Kind: "plan-accept", Member: by, At: time.Now().Unix()})
	pn.planLandedNotify(dst, key, cand.ID, cand.Title, by, landed)
	return msg
}

// planLandedNotify taps Options.PlanLanded for the tasks a plan_accept
// already landed (the whole batch, or the prefix that survived a
// pathological mid-batch refusal). The publish wake runs off the
// connection's read loop (the NoticeWake contract): a member's
// cold-session reactivation must not stall the owner's next frame.
// Failure is an ambient note — the tasks are already in the ledger.
func (pn *Face) planLandedNotify(dst *chat.Hub, key, planID, planTitle, by string, landed []*tasks.Task) {
	if pn.Fleet == nil || len(landed) == 0 {
		return
	}
	go util.Guard("server: fleet notify", func() {
		if err := pn.Fleet.PlanLanded(key, planID, planTitle, by, landed); err != nil {
			log.Printf("[提案] %s 入库后通知牛马失败：%v", key, err)
			dst.System(i18n.Sf("提案 %s 已入库，但通知牛马未送达（%v）——任务已创建，请房主在对话里 @相关成员告知", planID, err))
		}
	})
}

// planRejectAs is the host's refusal: pops the slot, broadcasts
// rejected to the plan's room (plus the cross-room receipt). The
// requirement's status is left alone (a declined or superseded plan
// changes nothing — requirements.MarkSplit's contract).
func (pn *Face) PlanRejectAs(hub *chat.Hub, c *chat.Client, actor, planID, project string) chat.Message {
	if pn.Stores.PlanStore == nil {
		return planDenied(actor, i18n.S("提案系统未启用"))
	}
	if actor != pn.Local {
		return planDenied(actor, i18n.S("提案的接受/拒绝是房主专属关口"))
	}
	key := project
	if key == "" {
		key = pn.RoomKey(hub)
	}
	p, err := pn.Stores.PlanStore.Take(pn.Subject, key, planID)
	if err != nil {
		return planDenied(actor, err.Error())
	}
	msg := chat.Message{Type: chat.MsgPlan, Event: "rejected",
		Plan: p.Wire(), From: actor,
		Text: i18n.Sf("%s 拒绝了提案 %s「%s」", actor, p.ID, p.Title),
		TS:   time.Now().Unix()}
	dst := pn.PlanHub(key)
	dst.Broadcast(msg)
	planReceipt(dst, hub, c, msg)
	return msg
}

// reqSuffix renders the requirement trace for broadcast one-liners.
func ReqSuffix(req string) string {
	if req == "" {
		return ""
	}
	return i18n.Sf("（需求 %s）", req)
}

// handleProjectPlan serves the pending-review read (GET /p/{key}/plan,
// v2 P4-b): {"project": key, "plan": <pending | null>}. Unknown
// projects 404 through the project store; draft/archived still read
// (sealed is not invisible — the /p/{key}/tasks rule).
func (pn *Face) HandleProjectPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	key := r.PathValue("key")
	if pn.Stores.PlanStore == nil || pn.Stores.ProjectStore == nil || !pn.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	httputil.WriteJSON(w, map[string]any{
		"project": key,
		"plan":    pn.Stores.PlanStore.Pending(pn.Subject, key),
	})
}
