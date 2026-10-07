package server

// session_mirror.go — the owner's seatless mirror face: the write
// loop behind the workbench's owner channel (say/task/plan/merge
// verbs dialed as the host). Split out of session.go; session.go
// keeps the seated-member loop and the handshake.

import (
	"context"
	"crypto/subtle"
	"github.com/WWestC/Niuma_Studio/server/roomops"
	"log"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/server/skills"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/wire"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func (s *Server) serveMirror(ctx context.Context, conn *websocket.Conn, hub *chat.Hub, presented string) {
	// The registered owner seat carries the member info and speaks
	// mirror says. An embedder that never called SetOwnerSeat still gets
	// the MANAGEMENT frames — they act by LocalName, not by a seat; only
	// say{origin:mirror} needs the seat and reports "unavailable" per
	// request instead.
	me, ok := hub.OwnerMember()
	if !ok {
		for _, m := range hub.Members() {
			if m.Name == s.opts.LocalName {
				me = m
				ok = true
				break
			}
		}
	}
	if !ok {
		_ = conn.Close(websocket.StatusPolicyViolation, "owner seat unavailable")
		return
	}
	// The speech credential: the live owner seat's own token. A dead
	// seat (embed edge — the router checked liveness, so this is
	// defense in depth) disarms speech entirely; no token, no speech.
	liveTok, live := hub.SeatHolderToken(me.Name)
	speech := live && presented != "" &&
		subtle.ConstantTimeCompare([]byte(presented), []byte(liveTok)) == 1
	// refuseSpeech is the per-frame refusal for every attribution-
	// bearing verb without the credential: nothing is recorded, the
	// reason names the fix (GUI and niuma mirror present it
	// automatically).
	refuseSpeech := func() {
		reply := func(text string) {
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = wsjson.Write(wctx, conn, chat.Message{Type: chat.MsgSystem, Text: text, TS: time.Now().Unix()})
			cancel()
		}
		reply(i18n.S("房主发言被拒：本连接未携带房主座位凭证（token）——工作台与 niuma mirror 会自动携带；凭名字发言已不再受理"))
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := wsjson.Write(wctx, conn, chat.Message{
		Type: chat.MsgWelcome, You: &me, Members: hub.Members(), TS: time.Now().Unix()})
	cancel()
	if err != nil {
		return
	}
	log.Printf("owner seatless connection opened (as %s)", me.Name)

	reply := func(text string) {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = wsjson.Write(wctx, conn, chat.Message{Type: chat.MsgSystem, Text: text, TS: time.Now().Unix()})
		cancel()
	}
	for {
		// 每帧全新信封（同 seated 循环）：*Patch/*Task/*Doc/*Pack/*Plan
		// 是可选对象，复用循环外变量时 encoding/json 会原地并入旧值，
		// 上一帧的补丁字段就漏进下一帧。零值重来。
		var in struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Origin string `json:"origin,omitempty"`
			Name   string `json:"name,omitempty"`
			Rank   int    `json:"rank,omitempty"`
			Seq    int64  `json:"seq,omitempty"`
			// say+to (私信, manual v0.10 M4 占位落地): the whisper target —
			// a say frame carrying it takes the private lane (deliver via
			// WhisperSink first, echo to this connection only, never a
			// broadcast/history trace).
			To string `json:"to,omitempty"`
			// say images (输入图片): the composer's image-carrying say —
			// media-warehouse references (POST /media receipts), spoken
			// through the owner's real seat like any say.
			Images []wire.Image `json:"images,omitempty"`
			// react (飞书式表情回应): the say line's seq (Seq above), the
			// palette emoji and the add/remove direction (absent = off).
			Emoji     string      `json:"emoji,omitempty"`
			On        bool        `json:"on,omitempty"`
			Doc       *chat.KbDoc `json:"doc,omitempty"`
			Key       string      `json:"key,omitempty"`
			ExpectRev *int        `json:"expect_rev,omitempty"`
			Rev       int         `json:"rev,omitempty"` // kb_restore's target revision

			// task writes (t_64): offboard's reassign and the owner-named
			// CLI task commands ride the seatless channel.
			TaskID string      `json:"task_id,omitempty"`
			Patch  *wire.Patch `json:"patch,omitempty"`
			Task   *wire.Task  `json:"task,omitempty"`
			// task_arbitrate: the host's bypass verdict (approve/reject
			// the pending proposal) rides the same seatless task face.
			Approve *bool `json:"approve,omitempty"`

			// skill/mcp verbs: the owner-named CLI skill/mcp save and
			// assemble commands ride the same channel.
			Skill      *chat.Skill     `json:"skill,omitempty"`
			Mcp        *chat.MCPServer `json:"mcp,omitempty"`
			Person     string          `json:"person,omitempty"`
			Target     string          `json:"target,omitempty"`
			Op         string          `json:"op,omitempty"`
			Skills     []string        `json:"skills,omitempty"`
			MCPServers []string        `json:"mcps,omitempty"`
			Project    string          `json:"project,omitempty"`
			Via        string          `json:"via,omitempty"`

			// plan verbs (v2 P4-b): the host's accept/reject (and a
			// lobby-plan submit) ride the seatless channel; the returned
			// frame is the receipt (this connection receives no broadcasts).
			Plan    *wire.Plan `json:"plan,omitempty"`
			PlanID  string     `json:"plan_id,omitempty"`
			Revised *wire.Plan `json:"revised,omitempty"`

			// merge verbs (v2.8 gitflow): the host's merge accept/reject
			// (and a 代提 submit) ride the seatless channel, plan 同款.
			Merge   *wire.Merge `json:"merge,omitempty"`
			MergeID string      `json:"merge_id,omitempty"`

			// notice verb (飞书式群公告): the owner publishes, updates or
			// clears the DIALED room's announcement here; text empties =
			// 撤下, expect_rev keeps it conditional, silent skips the
			// staff wake. The receipt frame comes straight back.
			Silent bool `json:"silent,omitempty"`

			// question answer (向房主提问, v2.5): the owner's click on a
			// member's option card — qid names the open ask, answer maps
			// question text to the chosen label or free text.
			QID    string            `json:"qid,omitempty"`
			Answer map[string]string `json:"answer,omitempty"`
		}
		if err := wsjson.Read(ctx, conn, &in); err != nil {
			return
		}
		switch {
		case in.Type == chat.MsgSay && in.To != "":
			// 私信（manual v0.10 M4 占位落地）：say+to 走私道——先经
			// WhisperSink 投进目标成员的车道（Fleet lane，忙时排队），
			// 成了才回执 whisper 帧（仅此连接可见，不广播不进史）；
			// sink 缺席/拒收都如实回一句，绝不装发送成功。
			if !speech {
				refuseSpeech()
				continue
			}
			if s.opts.Fleet == nil {
				reply(i18n.S("私信未发送：调度通道未接线（--no-dispatch 或内嵌模式）"))
				continue
			}
			if err := s.opts.Fleet.Whisper(s.roomKey(hub), in.To, in.Text); err != nil {
				reply(i18n.Sf("私信未送达：%s", err.Error()))
				continue
			}
			msg, ok := hub.WhisperAsOwner(in.To, in.Text)
			if !ok {
				// 投递已成功但房主座位不可用——回执画不出来，如实说明
				reply(i18n.Sf("私信已投递 %s，但房主座位不可用——本条无回执", in.To))
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = wsjson.Write(wctx, conn, msg)
			cancel()
		case in.Type == chat.MsgSay && in.Origin == chat.OriginMirror:
			if !speech {
				refuseSpeech()
				continue
			}
			msg, ok := hub.SayMirrorAsOwner(in.Text)
			if !ok {
				reply(i18n.S("镜像发送失败：房主座位不可用"))
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = wsjson.Write(wctx, conn, msg)
			cancel()
		case in.Type == chat.MsgSay:
			// The owner's plain say over the seatless channel (t_64:
			// offboard's departure notice) — history record and
			// @-resolution like any say, spoken through the real
			// seat, no origin marker; the delivered frame echoes
			// back as the receipt. Images (输入图片) ride the same
			// say — the workbench's composer speaks here. Slash
			// commands (/skill, /mcp) intercept BEFORE the say: the
			// composer's management face — executed, receipted as a
			// system line, never entering the stream as a say.
			if strings.HasPrefix(in.Text, "/skill ") || strings.HasPrefix(in.Text, "/mcp ") {
				s.skillsFace().ChatCommand(hub, me.Name, in.Text)
				continue
			}
			if !speech {
				refuseSpeech()
				continue
			}
			var msg chat.Message
			var ok bool
			if len(in.Images) > 0 && s.opts.Stores.MediaStore != nil {
				msg, ok = hub.SayAsOwnerImages(in.Text, in.Images)
			} else {
				msg, ok = hub.SayAsOwner(in.Text)
			}
			if !ok {
				reply(i18n.S("发言失败：房主座位不可用"))
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = wsjson.Write(wctx, conn, msg)
			cancel()
		case in.Type == chat.MsgTaskUpdate && in.Patch != nil,
			in.Type == chat.MsgTaskCreate && in.Task != nil,
			in.Type == chat.MsgTaskConfirm,
			in.Type == chat.MsgTaskDecline,
			in.Type == chat.MsgTaskArbitrate && in.Approve != nil:
			// The owner's task ledger writes over the seatless channel
			// (t_64: offboard's reassign; create/confirm/decline ride the
			// same shape so any owner-named CLI task command works), and
			// the host's arbitration verdict too — the confirm/decline
			// verbs reject the host ("你不是该变更的需确认人"), arbitrate
			// is the host's own protocol verb. The outcome frame goes
			// straight back — this connection receives no broadcasts, so
			// the CLI's awaitFrame matches the same-shaped task frame the
			// seated path broadcasts.
			out := s.engineOp(func(e *tasks.Engine) tasks.Outcome {
				// v2.10 号段分家：房主通道的裸号在 project 槽指定的架
				// 上解析（缺省＝大厅）；"-" = 全室唯一解析（工作区未绑
				// 定时的逃生口——同号两架则引擎报不存在，如实）。
				scope := in.Project
				if scope == "-" {
					if t, ok := e.Get(in.TaskID); ok {
						scope = t.ProjectKey
					}
				}
				switch in.Type {
				case chat.MsgTaskUpdate:
					return e.Update(scope, me.Name, in.TaskID, tasks.PatchFromWire(in.Patch))
				case chat.MsgTaskCreate:
					// "-" 是解析提示不是架名：建单回落池（大厅架）
					where := scope
					if where == "-" {
						where = ""
					}
					return e.Create(where, me.Name, in.Task.Title, in.Task.Desc, in.Task.Assignee)
				case chat.MsgTaskConfirm:
					return e.Confirm(scope, me.Name, in.TaskID)
				case chat.MsgTaskArbitrate:
					return e.Arbitrate(scope, in.TaskID, *in.Approve)
				default:
					return e.Decline(scope, me.Name, in.TaskID)
				}
			})
			// Denials must keep taskOp's private shape (Event
			// "denied" + the reason in Text) — Outcome carries the
			// reason in Reason with Event/Text empty, so echoing the
			// fields verbatim makes the CLI read a refusal as success.
			ev, txt := out.Event, out.Text
			if out.Denied {
				ev, txt = "denied", out.Reason
			}
			t := out.Task
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			// TaskID rides every outcome (denials carry an empty Task
			// snapshot) so the CLI can match its own request.
			_ = wsjson.Write(wctx, conn, chat.Message{Type: chat.MsgTask, Event: ev,
				TaskID: in.TaskID, Task: t.Wire(), From: me.Name, Text: txt, TS: time.Now().Unix()})
			cancel()
		case in.Type == roomops.MsgKick:
			// Receipt text is the denial, or the very notice that just
			// broadcast — the CLI's single success check serves both
			// the seated and the seatless path. These roster verbs stay
			// LOBBY-anchored (they act on the lobby roster); a project
			// room's face points the caller home instead of acting
			// cross-room.
			if hub != s.hub {
				reply(i18n.S("kick 目前仅限 Niuma_Studio 房主通道（项目房编制随项目调度管理）"))
			} else {
				reply(s.roomopsFace().KickAs(me.Name, in.Name))
			}
		case in.Type == roomops.MsgRankSet:
			if hub != s.hub {
				reply(i18n.S("rank_set 目前仅限 Niuma_Studio 房主通道（等级是牛马全局属性）"))
			} else {
				reply(s.roomopsFace().RankAs(me.Name, in.Name, in.Rank))
			}
		case in.Type == roomops.MsgAgentArchive:
			if hub != s.hub {
				reply(i18n.S("agent_archive 目前仅限 Niuma_Studio 房主通道（牛马档案是全局的）"))
			} else {
				reply(s.roomopsFace().ArchiveAs(me.Name, in.Name))
			}
		case in.Type == chat.MsgKbWrite && in.Doc != nil:
			// the owner's own CLI kb writes (t_39): same conditional
			// semantics as the seated path — expect_rev denials carry
			// current_rev on the frame written back
			d := *in.Doc
			expectRev := -1
			if in.ExpectRev != nil && *in.ExpectRev >= 0 {
				expectRev = *in.ExpectRev
			}
			frame := s.kbOpSeatless(me.Name, "written", func(store *kb.DocsStore) (kb.DocMeta, error) {
				if expectRev >= 0 {
					return store.WriteExpect(d.Key, d.Title, d.Body, me.Name, expectRev)
				}
				return store.Write(d.Key, d.Title, d.Body, me.Name)
			})
			writeBack(ctx, conn, frame)
		case in.Type == chat.MsgKbAppend:
			frame := s.kbOpSeatless(me.Name, "appended", func(store *kb.DocsStore) (kb.DocMeta, error) {
				return store.Append(in.Key, in.Text, me.Name)
			})
			writeBack(ctx, conn, frame)
		case in.Type == chat.MsgKbRestore:
			// the owner's CLI kb restore (t_39's trio completed): same
			// permission rule as the seated path (kbRestoreAs — the host,
			// the doc's owner, or rank ≥ owner; the host passes trivially),
			// same receipt shape, straight back.
			frame := s.kbOpSeatless(me.Name, "restored", func(store *kb.DocsStore) (kb.DocMeta, error) {
				return s.kbRestoreAs(me.Name, in.Key, in.Rev)
			})
			writeBack(ctx, conn, frame)
		case in.Type == chat.MsgKbArchive:
			// v3 治理三件套的房主通道面：归档/恢复（同门），删除
			// （仅房主——seatless 本就是房主身份，恒过门）
			frame := s.kbOpSeatless(me.Name, "archived", func(store *kb.DocsStore) (kb.DocMeta, error) {
				return s.kbArchiveAs(me.Name, in.Key, true)
			})
			writeBack(ctx, conn, frame)
		case in.Type == chat.MsgKbUnarchive:
			frame := s.kbOpSeatless(me.Name, "unarchived", func(store *kb.DocsStore) (kb.DocMeta, error) {
				return s.kbArchiveAs(me.Name, in.Key, false)
			})
			writeBack(ctx, conn, frame)
		case in.Type == chat.MsgKbDelete:
			writeBack(ctx, conn, s.kbDeleteFrame(s.hub, me.Name, in.Key))
		case in.Type == chat.MsgSkillSave && in.Skill != nil:
			// The broadcast went to the dialed room inside; the
			// receipt copy comes straight back (denials included).
			writeBack(ctx, conn, s.skillsFace().SkillSaveAs(hub, me.Name, in.Skill, in.Via))
			in.Skill, in.Via = nil, ""
		case in.Type == chat.MsgMCPSave && in.Mcp != nil:
			writeBack(ctx, conn, s.skillsFace().McpSaveAs(hub, me.Name, in.Mcp, in.Via))
			in.Mcp, in.Via = nil, ""
		case in.Type == chat.MsgAssemble:
			writeBack(ctx, conn, s.skillsFace().AssembleAs(hub, me.Name, skills.AssembleReq{
				Project: in.Project, Person: in.Person, Target: in.Target,
				Op: in.Op, Skills: in.Skills, MCPServers: in.MCPServers,
			}, in.Via))
			in.Person, in.Target, in.Op, in.Project, in.Via = "", "", "", "", ""
			in.Skills, in.MCPServers = nil, nil
		case in.Type == chat.MsgPlanSubmit && in.Plan != nil:
			// the host may file a plan for the DIALED room over their own
			// channel (the member-facing submit belongs to the
			// orchestrator's seat); the receipt comes straight back.
			writeBack(ctx, conn, s.plansFace().PlanSubmitAs(hub, nil, me.Name, plan.PlanFromWire(in.Plan)))
			in.Plan = nil
		case in.Type == chat.MsgPlanAccept:
			writeBack(ctx, conn, s.plansFace().PlanAcceptAs(hub, nil, me.Name, in.PlanID, plan.PlanFromWire(in.Revised), in.Project))
			in.PlanID, in.Revised, in.Project = "", nil, ""
		case in.Type == chat.MsgPlanReject:
			writeBack(ctx, conn, s.plansFace().PlanRejectAs(hub, nil, me.Name, in.PlanID, in.Project))
			in.PlanID, in.Project = "", ""
		case in.Type == chat.MsgMergeSubmit && in.Merge != nil:
			// v2.8 合并评审门（plan_submit 无座腿的同款）：房主可在自己
			// 的通道代提合并提案（成员面的提交属于编排者座位）；回执直回。
			writeBack(ctx, conn, s.gitFace().MergeSubmitAs(hub, nil, me.Name, merge.MergeFromWire(in.Merge)))
			in.Merge = nil
		case in.Type == chat.MsgMergeAccept:
			writeBack(ctx, conn, s.gitFace().MergeAcceptAs(hub, nil, me.Name, in.MergeID, in.Project))
			in.MergeID, in.Project = "", ""
		case in.Type == chat.MsgMergeReject:
			writeBack(ctx, conn, s.gitFace().MergeRejectAs(hub, nil, me.Name, in.MergeID, in.Project))
			in.MergeID, in.Project = "", ""
		case in.Type == chat.MsgNoticeSave:
			// the owner's notice verb (飞书式群公告) for the DIALED room:
			// publish/update (text) or clear (empty text), conditional on
			// expect_rev. Same receipt discipline as the kb verbs — the
			// broadcast went to the room inside, the copy comes back.
			expectRev := -1
			if in.ExpectRev != nil && *in.ExpectRev >= 0 {
				expectRev = *in.ExpectRev
			}
			writeBack(ctx, conn, s.noticeOp(hub, me.Name, in.Text, expectRev, in.Silent))
			in.Text = ""
		case in.Type == chat.MsgAck:
			// the owner's 收到 (飞书式) for the DIALED room's say line in
			// in.Seq: recorded under the owner's name, broadcast by
			// MarkAck, receipt straight back (the read-frame shape) so the
			// write face can verify it landed. Nothing recorded (self-ack,
			// stale/unknown seq) reports the reason instead of a fake echo.
			if !speech {
				refuseSpeech()
				in.Seq = 0
				continue
			}
			if fresh := hub.MarkAck(me.Name, in.Seq); len(fresh) > 0 {
				writeBack(ctx, conn, chat.Message{Type: chat.MsgAck,
					From: me.Name, Seqs: fresh, TS: time.Now().Unix()})
			} else {
				reply(i18n.S("收到回执未记上：消息不在当前回执窗口内，或那是你自己的发言（收到只答别人的消息）"))
			}
			in.Seq = 0
		case in.Type == chat.MsgReact:
			// the owner's emoji reaction (飞书式表情回应) for the DIALED
			// room's say line in in.Seq: recorded under the owner's name,
			// broadcast by MarkReact to the observers; a quiet false
			// (stale seq / bad emoji / full palette) reports the reason
			// so the write face never mistakes silence for success (the
			// ack verb's discipline — reacting to one's own line is fine
			// here, an opinion is not an answer).
			if !speech {
				refuseSpeech()
				in.Seq, in.Emoji, in.On = 0, "", false
				continue
			}
			if hub.MarkReact(me.Name, in.Seq, in.Emoji, in.On) {
				writeBack(ctx, conn, chat.Message{Type: chat.MsgReact, From: me.Name,
					Seq: in.Seq, Emoji: in.Emoji, On: in.On, TS: time.Now().Unix()})
			} else {
				reply(i18n.S("回应未记上：消息不在当前窗口内，或表情不受支持"))
			}
			in.Seq, in.Emoji, in.On = 0, "", false
		case in.Type == chat.MsgAnswer:
			// the owner's answer to a member's open question card (向
			// 房主提问): routed to the room's dispatcher, whose parked
			// AskUserQuestion call wakes holding the chosen answers and
			// whose question_done broadcast goes to the observers — the
			// receipt copy comes straight back here (the ack verb's
			// discipline).
			if s.opts.Fleet == nil {
				reply(i18n.S("提问应答未启用（调度器未接入）"))
			} else if s.opts.Fleet.AnswerQuestion(s.roomKey(hub), in.QID, me.Name, in.Answer) {
				writeBack(ctx, conn, chat.Message{Type: chat.MsgQuestionDone, QID: in.QID,
					From: me.Name, QStatus: "answered", Answer: in.Answer, TS: time.Now().Unix()})
			} else {
				reply(i18n.S("应答失败：这个问题可能已应答、已过期，或所选项为空——过期的问题请直接 @成员 补充说明"))
			}
			in.QID, in.Answer = "", nil
		case in.Type == chat.MsgBye:
			_ = conn.Close(websocket.StatusNormalClosure, "")
			log.Printf("owner seatless connection closed (as %s)", me.Name)
			return
		default:
			reply(i18n.S(`房主专用连接支持：say（含 origin:"mirror" 镜像与普通发言；/skill、/mcp 斜杠命令随 say 拦截）/ ack（收到回执）/ react（表情回应）/ answer（应答提问卡）/ task_update / kick / rank_set / agent_archive / kb_write / kb_append / kb_restore / skill_save / mcp_save / assemble / plan_submit（Niuma_Studio）/ plan_accept / plan_reject / notice_save——以房主名义发言、回执收到、表情回应、应答提问、执行管理、改派、写手册、存技能或 MCP 服务、装配、审提案或发公告`))
		}
	}
}
