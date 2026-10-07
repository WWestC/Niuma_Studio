package wire

// Package wire owns the studio's wire protocol: every frame type,
// constant and payload exchanged over WebSocket/HTTP lives here and
// NOWHERE else. The package is deliberately self-contained — it
// imports nothing from this module — so an external client (or a
// future platform front end) can speak the protocol by importing this
// one package, and so a domain-model change can never leak onto the
// wire by accident: domain payloads ride as MIRROR types owned here
// (JSON-tag-for-tag copies), and the boundary conversions live with
// the domain packages (tasks.Wire / tasks.TaskFromWire, …). Changing a
// mirror is a wire decision and must be made deliberately, in the same
// commit as both conversion directions.
//
// Evolution rule (unchanged from when this lived in chat/wire.go):
// additive only — new fields carry omitempty, new frame types get new
// constants, renaming anything here is a wire break, not a refactor.
// The frontend mirror web/wire/wire.js must stay byte-aligned.

// The wire protocol's truth (范式重构 S6): every frame type exchanged
// over WebSocket/HTTP lives here — the Msg* vocabulary (byte-stable:
// README 入职提示词与全部在房 AI 钉死这些常量), the Member/Message
// payloads, the agent-config and KB-doc projections. Renaming anything
// here is a wire break, not a refactor.

// LobbyKey is the reserved lobby room key ("default") — protocol
// vocabulary (hello.project routing, frame scopes), owned here; the
// projects package and chat's re-export stay as the historical aliases.
const LobbyKey = "default"

// Message types exchanged over WebSocket (JSON, one object per frame).
const (
	MsgHello      = "hello"       // C->S: {name, role} join request
	MsgSay        = "say"         // C->S: {text, images?, origin?}; S->C: {from,text,ts,images?,origin?}
	MsgReport     = "report"      // C->S: {text}; S->C: {from,text,ts} periodic task report
	MsgBye        = "bye"         // C->S: graceful leave
	MsgWelcome    = "welcome"     // S->C: {you, members}
	MsgJoin       = "join"        // S->C: {name}
	MsgLeave      = "leave"       // S->C: {name}
	MsgSystem     = "system"      // S->C: {text}
	MsgRename     = "rename"      // S->C: {from,to,text}: seat-collision dedup made visible (t_09)
	MsgMemberWork = "member_work" // S->C: {name,working,since}: the dispatcher's turn tell — reminder-family (no history, no badge, no @)
	// ProtoVersion is THIS binary's protocol version (the hello
	// handshake's negotiation anchor; see Message.Proto).
	ProtoVersion = 1

	MsgPing = "ping" // S->C observer-only: {ts} heartbeat keepalive. Browsers hide protocol ping/pong from JS, so the workbench's inbound watchdog only sees data frames — this feeds it in a quiet room. No seq, no history, never broadcast: SendTo one observer per heartbeat tick.

	// EventAgg (★B11, retired 2026-10) marks a system line as the old
	// lobby aggregator's roll-up of ANOTHER room's activity — an ambient
	// glance line ("[proj-x] 张三 等 3 条动静：…"). The aggregator is
	// gone (project rooms are fully isolated from the lobby now — their
	// activity never crosses), but the marker lives on in existing lobby
	// history files, so clients still render those lines badge-exempt:
	// a roll-up owes the reader no red dot. No new frame carries it.
	EventAgg = "agg"

	// EventInterrupted marks a system line as the restart's
	// unfinished-work roll call: the seats the farewell snapshot
	// caught mid-turn, riding the frame as []InterruptedSeat so the
	// workbench renders a continue card. Recorded like any operator
	// line — the prompt must survive into the history replay. Additive;
	// old clients ignore the marker and read the Text alone.
	EventInterrupted = "interrupted"

	// EventRebuild marks a system line as a studio-level rebuild
	// announcement (「已发起源码重编译」/「源码已重新编译」/失败收口——
	// server/rebuild.go's systemAllRooms). It rides the frame into history
	// so replaying clients see the same badge-exempt semantics a live one
	// does: the line is ambient operator news every room receives at once,
	// not conversation anyone owes a read — a recompile would otherwise
	// redden every project's dot, twice per restart. Additive; old clients
	// ignore the marker and read the Text alone.
	EventRebuild = "rebuild"

	// OriginMirror is the only origin value the say path knows: the
	// room owner's words relayed from a session window (v0.6 M1). Any
	// other origin reads as unset.
	OriginMirror = "mirror"

	// OriginBridge (v1.0) marks a line the dispatcher mirrored out of a
	// member's ZCode session — the member's turn reply relayed through
	// their dispatcher seat. The dispatcher's own router ignores bridged
	// lines (they are reactions, not new input), so mirroring can never
	// loop back into another injection; the hub's summons family
	// (all-address expansion, roll-calls, role-group wake) skips them
	// for the same reason — a deliverable that quotes "@所有人" must not
	// summon the room again. Per-name @-resolution still applies.
	OriginBridge = "bridge"

	// OriginWhisper (私信, manual v0.10 M4 占位落地) marks the owner's
	// private line to ONE member: the frame never broadcasts, never
	// enters the history ring (a whisper leaves no replayable trace),
	// and rides no say-feed — delivery to the member goes through the
	// server's whisper sink (the Fleet lane), not the mention router.
	// The frame exists for exactly one pair of eyes: the owner's own
	// workbench, which renders the 🔒 receipt.
	OriginWhisper = "whisper"

	// CloseReasonSuperseded is the transport close reason when a
	// same-token dial takes over a live seat (seat-M1, v0.10). It
	// deliberately does NOT contain "removed from room", so the t_12
	// kick contract (wait/guard exit 2, never reconnect) stays
	// untouched — a superseded guard backs off and reconnects as usual.
	CloseReasonSuperseded = "seat superseded by same token"

	// Task ledger (v0.4). Writes over WS; reads over HTTP/CLI. The
	// S->C "task" frame is a reminder-only broadcast (never stored in
	// the chat history); "denied" replies go to the requester alone.
	MsgTaskCreate    = "task_create"    // C->S: {task:{title,desc,assignee}}
	MsgTaskUpdate    = "task_update"    // C->S: {task_id, patch}
	MsgTaskConfirm   = "task_confirm"   // C->S: {task_id}
	MsgTaskDecline   = "task_decline"   // C->S: {task_id}
	MsgTaskArbitrate = "task_arbitrate" // C->S: {task_id, approve} — 房主仲裁专属：
	// 批准跳过剩余确认人直接生效、驳回作废提案（assign 提案驳回即取消
	// 任务）。Host-only by construction（seatless 房主面；seated 面门控
	// LocalName）——Need 名单外的房主点「接单」只会吃到 Confirm 的
	// 「你不是该变更的需确认人」拒绝，仲裁才是房主的协议动词。
	MsgTask = "task" // S->C: {event, task?, from, text, ts}

	// Room memory (v0.5). Writes over WS, reads over HTTP/CLI; the
	// S->C "kb" frame is a reminder-only broadcast (never stored in
	// the chat history); "denied" replies go to the requester alone.
	MsgAgentSave = "agent_save" // C->S: {agent:{name,role,manual,prompt}} — upsert (D3)
	MsgAgent     = "agent"      // S->C: {event: saved, agent, from, ts}

	MsgKbWrite     = "kb_write"     // C->S: {doc:{key,title,body}}
	MsgKbAppend    = "kb_append"    // C->S: {key, text}
	MsgKbRestore   = "kb_restore"   // C->S: {key, rev}
	MsgKbArchive   = "kb_archive"   // C->S: {key} — 归档（房主/owner/rank≥owner，v3 治理）
	MsgKbUnarchive = "kb_unarchive" // C->S: {key} — 恢复（同上门）
	MsgKbDelete    = "kb_delete"    // C->S: {key} — 删除（仅房主）
	MsgKb          = "kb"           // S->C: {event: written|appended|restored|archived|unarchived|deleted|denied, doc?, from, ts}

	// Skill & MCP assembly. Writes over WS (skill_save/mcp_save/
	// assemble); reads over HTTP/CLI. The S->C "skill"/"mcp" frames
	// are reminder-only broadcasts (never stored in the chat history);
	// "denied" replies go to the requester alone (the task/kb frame
	// contract).
	MsgSkillSave = "skill_save" // C->S: {skill:{key,name,desc,body,manuals}}
	MsgMCPSave   = "mcp_save"   // C->S: {mcp:{key,name,type,url,headers}}
	MsgAssemble  = "assemble"   // C->S: {project, person, target: staffing|profile, op: set|add|remove, skills, mcps}
	MsgSkillEvt  = "skill"      // S->C: {event: saved|removed|assembled|denied, skill?, project?, person?, skills?, mcps?, from, text, ts}
	MsgMcpEvt    = "mcp"        // S->C: {event: saved|removed|denied, mcp?, from, text, ts}

	// Plan proposals (v2 P4-b, pm-orchestration §5.2 — the scheduling
	// face's only new frame family). Writes over WS; reads over HTTP/CLI
	// (GET /p/{key}/plan). The S->C "plan" frame is a reminder-only
	// broadcast (never stored in the chat history); "denied" replies go
	// to the requester alone — the task/kb frame contract. plan_submit
	// is the orchestrator's channel (its per-project session); accept/
	// reject are the host's confirmation gate. A same-project new
	// proposal supersedes the previous pending one.
	MsgPlanSubmit = "plan_submit" // C->S: {plan:{title,need,req,project_key,version,tasks:[…]}}
	MsgPlanAccept = "plan_accept" // C->S: {plan_id, revised?} — host; revised = the entry-edited full plan
	MsgPlanReject = "plan_reject" // C->S: {plan_id} — host
	MsgPlan       = "plan"        // S->C: {event: submitted|accepted|rejected|superseded|denied, plan?, from, ts}

	// Requirement filing (全智能模式 v2.8). The orchestrator's
	// req_create writes over WS dialing INTO the project room (the
	// plan-submit shape); reads ride the HTTP /p/{key}/reqs face. The
	// S->C "req" reply is PRIVATE to the requester alone; the room
	// hears one broadcast frame per mutation — created on file (t_170
	// 声色同门), deleted on the host's DELETE cleanup (same door).
	MsgReqCreate = "req_create" // C->S: {req:{title, body}} (project rides hello)
	MsgReq       = "req"        // S->C: {event: created|deleted|denied, req?, from, text, ts} — private reply; created/deleted also broadcast to the room

	// Goal-scoped autopilot wrap-up (目标制补货收摊). The orchestrator's
	// autopilot_done writes over WS dialing INTO the project room (the
	// req_create shape): the 选题 injection taught it the verb — judge
	// the host's 本次目标 met (or provably unreachable), then close the
	// session. The server flips BOTH r_19 switches off (auto_stock +
	// auto_advance) and leaves one system line in the room; the private
	// S->C reply is the CLI's receipt. Host-only elsewhere: the owner
	// re-arms from the settings card (the AUTOPILOT confirm door).
	MsgAutopilotDone = "autopilot_done" // C->S: {text: 一句话结论}（project 骑 hello；发送者须为本房在编编排者）
	MsgAutopilot     = "autopilot"      // S->C: {event: completed|denied, from, text, ts} — private reply（房间侧只见 SystemRecorded 系统行）

	// Room pause (房间暂停). Reminder-only broadcast (never stored in
	// the chat history — the meeting/notice frame contract): the pause
	// write face (POST /p/{key}/pause) announces the flip so every
	// window of the room freezes its office sim (and the switcher's
	// row flips its button) at once; the welcome frame carries the
	// current state for late joiners (hydration is the welcome's own
	// Paused field + GET /p/{key}/pause). The recorded trail is a
	// system line with event "pause" instead. Additive; old clients
	// ignore both.
	MsgPause = "pause" // S->C: {event: paused|resumed, from, ts}; welcome: {paused}

	// Requirement-review meeting (需求评审会议). Reminder-only broadcast
	// (never stored in the chat history — the task/kb frame contract):
	// the dispatcher's meeting clock announces the room's occupancy
	// changes; readers who missed the frame refetch GET /p/{key}/meeting.
	// started/ended open and free the room; advanced/extended are the
	// CHAIR's clock grip (the dispatcher's MeetingCtl): advanced = the
	// chair ended the discuss window early (the agenda jumped into the
	// summary ask), extended = the chair pushed the current window's
	// deadline out.
	MsgMeeting = "meeting" // S->C: {event: started|advanced|extended|ended, meeting?, ts}

	// The chair's meeting clock (the same family's write side).
	// meeting_end fires the CURRENT window early — during the discuss
	// window the agenda advances straight into the summary ask, during
	// the summary window the room frees now (散会); meeting_extend
	// {minutes} pushes the current window's deadline out (default 10,
	// clamped 1–60 a call; calls stack). Chair-only (the live meeting's
	// 主持, the convening orchestrator): the dispatcher gates it again
	// even though the server already did. The S->C "meeting" frames
	// above are the receipts (the plan-frame contract): success rides
	// the broadcast the requester's CLI reads, "denied" goes to the
	// requester alone.
	MsgMeetingEnd    = "meeting_end"    // C->S: {} (chair)
	MsgMeetingExtend = "meeting_extend" // C->S: {minutes} (chair)

	// Per-room group announcement (飞书式群公告). Writes over WS (the
	// owner's seatless face only); reads over HTTP/CLI (GET
	// /p/{key}/notice). The S->C "notice" frame is a reminder-only data
	// sync broadcast (never stored in the chat history); "denied" replies
	// go to the requester alone — the task/kb frame contract. The
	// recorded trail rides a separate SystemRecordedEvent("notice", …)
	// line so late joiners meet the announcement in the history replay.
	// notice_save: empty text clears the notice (撤下); expect_rev keeps
	// the write conditional (the kb_write contract); silent=true skips
	// the staff wake (publish without notifying).
	MsgNoticeSave = "notice_save" // C->S: {text, expect_rev?, silent?}
	MsgNotice     = "notice"      // S->C: {event: published|updated|cleared|denied, notice?, from, ts}

	// Read receipts (飞书式已读). S->C reminder-only broadcast (never
	// stored in the chat history — the task/kb frame contract): the
	// dispatcher injected the say lines named in Seqs into reader From's
	// ZCode session, so From has read them. The facts' store is the
	// hub's read ledger (GET /p/{key}/reads); this frame only carries
	// the increment. Who counts as a reader and why the moment of the
	// bridge.Send ack is the truth lives in chat/reads.go. Additive;
	// old clients ignore.
	MsgRead = "read" // S->C: {from, seqs, ts}

	// Delivery-queue projection (飞书式投递状态的「排队中」档). S->C
	// reminder-only broadcast (never stored — the read-frame contract):
	// From's next-turn lane currently holds exactly the say seqs named
	// in Seqs (empty = drained) — a FULL-STATE replace per member, no
	// add/remove ambiguity for a dropped client to corrupt. The facts'
	// store is the hub's queue projection (folded into GET
	// /p/{key}/reads as the queues rows); the durable truth is the
	// dispatcher's inbox lanes, and boot re-derives the set from them.
	// A queued line has NOT been injected into the member's session
	// yet — neither read nor lost, just waiting for the busy turn to
	// end. Additive; old clients ignore.
	MsgQueue = "queue" // S->C: {from, seqs, ts}

	// Ack receipts (飞书式收到). The loop-breaker for @-ping-pong: an
	// @-addressed member who need not answer — the message was FYI —
	// acknowledges instead of replying, and the acknowledgement is a
	// FACT about the original line, never a new chat message: no
	// history row, no stream line, no @-wake, nothing for anyone to
	// reply to, so it cannot loop (by construction, not by etiquette).
	// C->S: {seq} — one member acks that say line (the Web workbench's
	// owner face and any seated client). S->C: {from, seqs, ts} —
	// reminder-only broadcast (the read-frame contract): the increment
	// only; the facts' store is the hub's ack ledger
	// (GET /p/{key}/acks, chat/acks.go). Honesty rules: only say lines
	// earn acks, nobody acks their own line, and the dispatcher swallows
	// a turn that answers a mention-wake with exactly 「收到」 into a
	// receipt instead of mirroring it (the reply-storm's engine off).
	// Additive; old clients ignore.
	MsgAck = "ack" // C->S: {seq}; S->C: {from, seqs, ts}

	// Emoji reactions (飞书式表情回应, v2.6). A reaction is a FACT
	// about a message, never a new chat message — the ack/read
	// discipline: no history row, no badge, no @-wake, nothing for
	// anyone to reply to. C->S: {seq, emoji, on} — one client adds
	// (on) or removes (on=false, absent reads off) its own reaction on
	// that say/report line (the Web workbench's owner face and any
	// seated member; unlike an ack, reacting to one's own line is fine
	// — an opinion is not an answer). S->C: {from, seq, emoji, on} —
	// reminder-only broadcast carrying the one fresh fact; the ledger
	// itself is GET /p/{key}/reacts (chat/reacts.go). Honesty rules
	// mirror the sibling ledgers: only member conversation lines earn
	// reactions, seqs below the tracking floor are ignored, and the
	// per-line palette is capped. Additive; old clients ignore.
	MsgReact = "react" // C->S: {seq, emoji, on}; S->C: {from, seq, emoji, on}

	// Interactive questions (向房主提问, v2.5). A member's ZCode
	// session calls the AskUserQuestion tool mid-turn; the dispatcher
	// turns that reverse request into a "question" broadcast carrying
	// the structured asks (question text + labeled options) and the
	// workbench renders a clickable option card in the chat. The
	// owner's choice rides "answer" over the owner write face and
	// resolves the pending tool call IN PLACE — the member's turn
	// continues holding the chosen answers, so 需求澄清/规格拍板
	// becomes a dialogue instead of a task parked on the owner with
	// nowhere to write. "question_done" closes the card (answered —
	// From names the answerer and Answer the choices — or expired).
	// Question frames are reminder-only (never stored in the chat
	// history, the read-frame contract): the ask also lands as the
	// member's own say line (the transcript's durable copy), and the
	// open set hydrates from GET /p/{key}/questions. Additive; old
	// clients ignore.
	MsgQuestion     = "question"      // S->C: {qid, question, from, ts}
	MsgQuestionDone = "question_done" // S->C: {qid, qstatus: answered|expired, answer?, from, ts}
	MsgAnswer       = "answer"        // C->S: {qid, answer:{<问题>: <所选 label 或自填文本>}} — owner write face

	// 工作过程 (v2.8): the dispatcher's trace tap folds a member's
	// displayable session moments (thinking, draft reply, tool calls
	// and results, model iterations, turn boundaries, errors — and the
	// same stream from their subagent sessions) into TraceEntry rows.
	// S->C: {from, trace:[…]} — one flush-batch per member. Reminder-
	// only (never stored in the chat history, no badge, no wake): the
	// panel consumes it live, cold windows hydrate from GET
	// /p/{key}/trace. Additive; old clients ignore.
	MsgTrace = "trace"

	// 终端转录 (r_17): the hub's term journal flushes one batch of
	// TraceEntry rows per member — the SAME fold semantics the trace
	// frame carries (merged text blocks and updated tool cards ride
	// their original seq), plus the transcript-only kinds (input 注入
	// 行 / ask 反问 / answer 应答 / sys 系统行). S->C: {from,
	// trace:[…]}. Reminder-only (never stored in the chat history, no
	// badge, no wake): the terminal board consumes it live, cold
	// windows hydrate from GET /p/{key}/term. Additive; old clients
	// ignore.
	MsgTerm = "term"

	// 版本管理事件流 (v2.8 gitflow)。MsgGit 的 S->C "git" 帧是提醒族广
	// 播（不进聊天历史、不 badge、不唤醒——task/kb 帧契约）：post-commit
	// 钩子报来的每笔提交出一张提交卡（谁、说了什么、动了哪些文件各
	// 红绿多少行），事件枚举目前仅 "commit"。合并评审门复用 plan 的形
	// 状：merge_submit 是编排者（或房主）的提交通道——把一根领先分支连
	// 同提交清单/红绿量/引用的台账号提交进每项目单槽；merge_accept/
	// merge_reject 是房主专属确认门，accept 由工作室代执行
	// git merge --no-ff（冲突即还原回滚）。S->C "merge" 帧事件：
	// submitted | superseded | merged | rejected | denied。Additive；旧
	// 客户端忽略。
	MsgGit         = "git"          // S->C: {event: commit, git?, from, ts}
	MsgMergeSubmit = "merge_submit" // C->S: {merge:{branch, into?, req?}}（project 骑 hello/project 槽）
	MsgMergeAccept = "merge_accept" // C->S: {merge_id, project?} — host
	MsgMergeReject = "merge_reject" // C->S: {merge_id, project?} — host
	MsgMerge       = "merge"        // S->C: {event, merge?, from, ts}

	// 工作区变更推送 (文件板自动刷新的服务端半边)：workspace watcher 在
	// 磁盘上看见某项目的工作区动了，广播此帧——Project 带项目键，各房
	// 全员收（观察者按房常开，正浏览该项目工作区的文件板才消费）。查
	// 询式真源仍是 GET /p/{key}/fs/tree：帧只说「脏了」，不带增量，
	// 前端自己重拉已展开的目录（与 trace/term 的帧指路＋HTTP 水合同
	// 分工）。Reminder-only（不进聊天历史、不 badge、不唤醒）。Additive；
	// 旧客户端忽略。
	MsgFsDirty = "fs_dirty" // S->C: {project, ts}
)

// Look is the lifetime look projection the staffing store serves the
// hub (r_05 t_126): hair style/skin/accessories ride the member record
// so join/welcome frames carry the layered look; omitempty keeps old
// clients byte-compatible.
type Look struct {
	HairStyle   string    `json:"hair_style,omitempty"`
	Skin        string    `json:"skin,omitempty"`
	Accessories []LookAcc `json:"accessories,omitempty"`
}

// LookAcc is one accessory in the wire projection: Style is the
// wardrobe key (the primary id since the t_128 merge), Color its
// library-defined color riding along for callers that only paint.
type LookAcc struct {
	Slot  string `json:"slot"`
	Style string `json:"style,omitempty"`
	Color string `json:"color,omitempty"`
}

// Member is one participant of the room.
type Member struct {
	Name  string `json:"name"`
	Color string `json:"color"` // hex shirt color, e.g. "#e07a5f"
	Hair  string `json:"hair"`  // hex hair color
	Role  string `json:"role,omitempty"`
	Look  *Look  `json:"look,omitempty"` // 终身层（r_05 t_126）

	// LastReport / LastReportTS are the member's latest task report,
	// surfaced by /members so anyone can see who is doing what.
	LastReport   string `json:"last_report,omitempty"`
	LastReportTS int64  `json:"last_report_ts,omitempty"`

	// Working / WorkingSince are the dispatcher's turn-in-flight tell:
	// a member whose ZCode session currently runs a turn (patch.status
	// "running"). Since is util.Now() seconds. Set by the dispatcher
	// through SetWorkState; broadcast as member_work frames; surfaced
	// by /kb/people so the chat panel can say 「小牛 正在处理…」
	// instead of looking like a hang. Ghosts never carry it (a dead
	// process works nowhere).
	Working      bool  `json:"working,omitempty"`
	WorkingSince int64 `json:"working_since,omitempty"`
}

// InterruptedSeat is one seat the farewell snapshot caught mid-turn:
// the member's turn died with the ZCode child at quit time, so the
// restart hands the owner a continue prompt instead of silently
// dropping the work. Project is the seat's room key (absent = the
// lobby); Room is that project's display name for the prompt card.
type InterruptedSeat struct {
	Name    string `json:"name"`
	Project string `json:"project,omitempty"`
	Room    string `json:"room,omitempty"`
	Since   int64  `json:"since,omitempty"` // seconds, the turn's start
	// ContextNotes (r_20 t_195): the farewell snapshot's 引用式接续
	// 要点 (ResumeNotes JSON, frozen contract) — the resume injection
	// replays it three-段. Empty = the bare ContinueText fallback.
	ContextNotes string `json:"context_notes,omitempty"`
}

// Message is the single wire format for both directions.
type Message struct {
	Type string `json:"type"`

	// hello (C->S)
	Name   string `json:"name,omitempty"`
	Role   string `json:"role,omitempty"`
	Replay *bool  `json:"replay,omitempty"` // false: skip history replay

	// hello (C->S), additive (v2 P1): observer marks a seatless
	// read-only dial (no seat, no roster entry, no join/leave broadcast,
	// all broadcasts received, optional replay) — the Web workbench
	// window's connection shape (frontend 稿 §4.6-1). project names the
	// room the dialer wants (multi-room routing lands in P3; today the
	// single room echoes it back in the welcome). Both stay absent on
	// every legacy dial; old clients ignore them.
	Observer bool   `json:"observer,omitempty"`
	Project  string `json:"project,omitempty"` // hello C->S / welcome S->C echo / assemble C->S target room / task C->S writes over the host channel (v2.10: the shelf the bare TaskID resolves on — the workspace binding's default, "-" asks the studio-wide unique resolve, absent = the lobby shelf)
	// Visitor (hello C->S, additive, r_19 治理修订): the /view share
	// page's observer dial carries the visitor token. ONLY token-carrying
	// dials are visitors — the server's 访客治理 (并发帽/重连窗/4h TTL/
	// 彩蛋) rides this field alone. The host's own workbench windows dial
	// observer WITHOUT it and are never metered (the pre-fix bug metered
	// every observer dial, so the owner's own extra windows locked him
	// out of「当前访客较多」— the 仅缓存·HTTP存活 incident).
	Visitor string `json:"visitor,omitempty"`
	// Force (hello C->S, additive): the explicit opt-in to be deduped.
	// The server's seat door refuses a plain dial whose name a live seat
	// already holds under a different credential (the accidental "-2"
	// twin — join broadcast + 10-minute grace ghost; a member's
	// hand-rolled helper dialing as its own name mid-turn, 小鸟-2). A
	// forced dial passes the door and lands on the documented twin (the
	// CLI's --force-seat). Absent on every legacy dial.
	Force bool `json:"force,omitempty"`

	// say / report / join / leave
	From string `json:"from,omitempty"`
	Text string `json:"text,omitempty"`
	TS   int64  `json:"ts,omitempty"`

	// rename: the requested name (from) and the deduped "-2" name the
	// join actually landed on (to) — additive, old clients ignore it.
	// say+origin:whisper reuses the slot as the 私信 target (the ONE
	// member the line is for; same additive contract, different verb).
	To string `json:"to,omitempty"`

	// member_work (S->C): the dispatcher's turn tell — Working flips
	// with the member's turn, Since is the util.Now() seconds the turn
	// started. Additive; old clients ignore it.
	Working bool  `json:"working,omitempty"`
	Since   int64 `json:"since,omitempty"`

	// Seq is the hub's monotonic delivery order for history-recorded
	// messages (say/report), floored at wall-clock milliseconds on boot
	// so it never rewinds across restarts. Second-granular TS cannot
	// order same-second messages, which made a delivered-and-cursored
	// mention resurrect on the next run when a later message shared its
	// second (t_37 mode 2's real root cause). Additive; old clients
	// ignore it.
	Seq int64 `json:"seq,omitempty"`

	// say / report: display names the text @-mentions, resolved by the
	// hub against the roster at send time (canonical casing). Lets
	// clients and agents react to being addressed without re-parsing.
	Mentions []string `json:"mentions,omitempty"`

	// say: the structured reply-quote (引用回复，根治版). The quote rides
	// the message as FIELDS, no longer as a 「引用 #N @X：snip」 prefix
	// baked into Text — the body stays clean, so 复制/转发/检索 never
	// carry the header along (the renderer used to strip the prefix back
	// out at display; now there is nothing to strip). See Quote. Nil on
	// every non-quoted line; additive, old clients ignore it.
	Quote *Quote `json:"quote,omitempty"`

	// say (输入图片): the line's image attachments — references into
	// the studio's media warehouse (wire.Image: id + display hints).
	// GET /media/{id} serves the bytes for <img> rendering; the
	// dispatcher resolves the ids back to paths and injects them as
	// session/send attachments so an @-addressed member's turn actually
	// sees the images. Empty text with images is a legal image-only
	// say. Additive; old clients ignore the field.
	Images []Image `json:"images,omitempty"`

	// read/ack frames (S->C): the say-message seqs the From member has
	// read / acked（收到）— the increment this broadcast carries, never
	// the whole ledger (that is GET /p/{key}/reads|acks' job). Absent on
	// every other frame. The C->S ack rides the plain Seq field above.
	Seqs []int64 `json:"seqs,omitempty"`

	// react frames (C->S the request, S->C one fresh fact): Emoji names
	// the palette slot, On adds (true) or removes (false) the sender's
	// own reaction on the seq-named line. On is omitempty — an absent
	// field reads as off, so old and new peers agree without a pointer.
	// Absent on every other frame.
	Emoji string `json:"emoji,omitempty"`
	On    bool   `json:"on,omitempty"`

	// Interactive questions (向房主提问, v2.5): QID names the pending
	// ask on every question-family frame; Question is the card payload
	// (S->C "question"); Answer is the owner's chosen answers keyed by
	// question text (C->S "answer", and the S->C "question_done"
	// receipt echoes what landed); QStatus closes the done frame —
	// "answered" (From names the answerer) or "expired". Additive;
	// absent on every other frame.
	QID      string            `json:"qid,omitempty"`
	Question *Question         `json:"question,omitempty"`
	Answer   map[string]string `json:"answer,omitempty"`
	QStatus  string            `json:"qstatus,omitempty"`

	// system+interrupted (S->C): the restart's unfinished-work roll
	// call — the seats the farewell snapshot caught mid-turn (a member
	// whose ZCode session was running a turn when the studio quit).
	// The Text alone stays readable (CLI, old clients); the list
	// powers the workbench's 「让 TA 继续」 buttons, which are plain
	// owner says into each seat's own room. Additive; absent on every
	// other frame.
	Interrupted []InterruptedSeat `json:"interrupted,omitempty"`

	// say: "mirror" marks the line as the room owner's own words typed
	// into an AI session window and relayed by the mirror hook (v0.6 M1)
	// — the full say path plus this marker, so @-mentions wake peers and
	// the line enters history exactly like a direct say. Only the
	// owner's name earns the marker; anyone else's origin is stripped to
	// a plain say. Additive; old clients ignore it.
	Origin string `json:"origin,omitempty"`

	// join (the joiner's member info)
	Member *Member `json:"member,omitempty"`

	// welcome: the room's current pause state (hydration for late
	// joiners — the office sim freezes on it; additive, old clients
	// ignore it). pause broadcasts carry the flip in Event alone.
	Paused bool `json:"paused,omitempty"`
	// Proto is the protocol version the welcome negotiates (hello C->S
	// carries the client's max; the welcome echoes the server's). The
	// wire contract stays additive-only: a newer SERVER may add frames
	// and fields, an older client ignores them; a newer CLIENT against
	// this server is refused at hello (never assumed compatible).
	Proto int `json:"proto,omitempty"`
	// Auth is the hello dial's identity proof (multi-user shape): the
	// login session token, or the studio service token for local CLI
	// tooling. Browser workbenches ride the session cookie on the WS
	// handshake instead. Absent under single-user.
	Auth string `json:"auth,omitempty"`

	// welcome
	You     *Member  `json:"you,omitempty"`
	Members []Member `json:"members,omitempty"`

	// task frames. C->S create/update carry the payload; S->C "task"
	// broadcasts carry the event name, the task snapshot and a
	// human-readable summary (or, for "denied", just the reason).
	Task   *Task  `json:"task,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	Patch  *Patch `json:"patch,omitempty"`
	Event  string `json:"event,omitempty"` // created|updated|proposal|confirmed|declined|denied | written|appended|restored

	// task_arbitrate C->S: the host's bypass verdict — true applies the
	// pending patch at once (skipping remaining confirmations), false
	// discards it (an assign proposal's rejection cancels the task).
	// Pointer so an absent field never reads as a silent 驳回.
	Approve *bool `json:"approve,omitempty"`

	// kb frames (v0.5 room memory). C->S write/append/restore carry the
	// request; S->C "kb" broadcasts carry the doc's new metadata (or,
	// for "denied", just the reason). KbDoc lives here because kb
	// imports chat, not the other way around.
	KbDoc *KbDoc `json:"doc,omitempty"`
	Key   string `json:"key,omitempty"`
	Rev   int    `json:"rev,omitempty"`

	// kb S->C broadcasts also carry the write's line diff (黑板红绿
	// 对比): Diff rows paint red/green in the chat notice card ('-'
	// removed, '+' added — chat.LineDiff), DiffMore counts the rows
	// folded past the cap. Additive; absent on "denied", on no-op
	// writes and on frames from producers that predate the field.
	Diff     []DiffLine `json:"diff,omitempty"`
	DiffMore int        `json:"diff_more,omitempty"`

	// hello (C->S) / welcome (S->C): the seat token (seat-M1, v0.10) —
	// the server hands one out with every welcome; a persistent client
	// (guard) presents it on reconnect to take its own live seat back
	// (supersede, no "-2"). One-shot clients simply never send it.
	Token string `json:"token,omitempty"`

	// kb denied (v0.6 §3.3): on an expect_rev conflict, the rev the room
	// actually holds now — the caller re-reads, merges, retries.
	CurrentRev int `json:"current_rev,omitempty"`

	// kb_write C->S (v0.6 §3.3): the rev the caller read, making the
	// write conditional — mismatch denies with CurrentRev above. Nil or
	// negative means unset (last-writer-wins), 0 means "must not exist".
	ExpectRev *int `json:"expect_rev,omitempty"`

	// agent_save (C->S) / the agent broadcast (S->C)
	Agent *AgentConfig `json:"agent,omitempty"`

	// skill/mcp frames. C->S skill_save carries the skill; C->S
	// mcp_save carries the server; C->S assemble carries the
	// seat/profile target, the list op and the asset keys (project
	// rides the hello field); S->C "skill"/"mcp" broadcasts carry the
	// event and a human-readable summary (or, for "denied", just the
	// reason). Via is the CLI rider's audit marker ("cli"; empty
	// reads as "ws").
	Skill      *Skill     `json:"skill,omitempty"`
	Mcp        *MCPServer `json:"mcp,omitempty"`
	Person     string     `json:"person,omitempty"`
	Target     string     `json:"target,omitempty"` // staffing|profile
	Skills     []string   `json:"skills,omitempty"`
	MCPServers []string   `json:"mcps,omitempty"`
	Op         string     `json:"op,omitempty"` // set|add|remove
	Via        string     `json:"via,omitempty"`

	// plan frames (v2 P4-b). C->S plan_submit carries the proposal;
	// C->S plan_accept/plan_reject name the pending plan (accept's
	// revised is the host's entry-edited full replacement; project rides
	// the hello/project field for lobby-dialed hosts). S->C "plan"
	// broadcasts carry the event, the plan snapshot and a human-readable
	// summary (or, for "denied", just the reason).
	Plan    *Plan  `json:"plan,omitempty"`
	PlanID  string `json:"plan_id,omitempty"`
	Revised *Plan  `json:"revised,omitempty"`

	// merge frames (v2.8 gitflow). C->S merge_submit carries the
	// proposal's core (branch required; into/req optional — into 缺省
	// 取主工作区当前分支的提交时快照)；C->S merge_accept/merge_reject
	// name the pending merge (project rides the hello/project field for
	// lobby-dialed hosts). S->C "merge" broadcasts carry the event, the
	// merge snapshot and a human-readable summary (or, for "denied",
	// just the reason) — the plan payload discipline.
	Merge   *Merge `json:"merge,omitempty"`
	MergeID string `json:"merge_id,omitempty"`

	// git frames (v2.8 gitflow): the post-commit hook's broadcast —
	// one freshly landed commit with its per-file red/green rows. The
	// From slot carries the git author's name; TS is the commit date.
	// Reminder-only (the task/kb frame contract). Additive; absent on
	// every other frame.
	Git *GitCommit `json:"git,omitempty"`

	// req_create (全智能模式 v2.8): the payload on the C->S write, the
	// store's entry on the S->C private reply (the task/plan payload
	// discipline — the wire shape IS the store's shape).
	Req *Req `json:"req,omitempty"`

	// meeting frames (需求评审会议). S->C "meeting" broadcasts carry the
	// event (started|advanced|extended|ended) and the meeting snapshot —
	// the office board's occupancy cue (participants head for the meeting
	// room, the room screen lights while a meeting is live; advanced and
	// extended are the chair's clock grip, same snapshot discipline).
	// C->S meeting_extend carries its minutes on Minutes.
	Meeting *Meeting `json:"meeting,omitempty"`
	Minutes int      `json:"minutes,omitempty"`

	// notice frames (飞书式群公告). C->S notice_save carries the body in
	// Text (empty = 撤下) with ExpectRev/Silent riding the generic slots;
	// S->C "notice" broadcasts carry the event and the notice snapshot
	// (absent on "cleared"; on "denied" just the reason, with CurrentRev
	// on an expect_rev conflict).
	Notice *Notice `json:"notice,omitempty"`
	Silent bool    `json:"silent,omitempty"` // notice_save C->S: skip the staff wake

	// trace frames (工作过程, v2.8): one flush-batch of TraceEntry rows
	// for the From member (see chat/trace.go — the ring's fold rules
	// mirror here for live clients). Additive; absent on every other
	// frame.
	Trace []TraceEntry `json:"trace,omitempty"`
}

// Quote is a say's structured reply-quote (引用回复的根治形态)： the
// quoted line's identity rides the message as fields, not as a text
// prefix baked into Text — the body stays clean, so 复制/转发/检索 never
// carry the引用头 along. Seq is the quoted line's seq in THIS room
// (0 = 无从精确跳转： cross-room forwards and legacy quotes); From names
// the quoted speaker (the block's 名牌); At marks the reply-mention
// (引用即点名： the hub adds From to Mentions so the wake resolves by
// field, the body need not carry an @); Snip is the one-line summary
// (the composer's snipOf / the dispatcher's snipLine shape); Via names
// the source room of a forward (转发自). Snip/Via are hygiene-capped by
// the hub (normQuote); old history lines carry their quotes embedded in
// Text instead — the frontend's legacy prefix parser stays for them.
type Quote struct {
	Seq  int64  `json:"seq,omitempty"`
	From string `json:"from"`
	At   bool   `json:"at,omitempty"`
	Snip string `json:"snip,omitempty"`
	Via  string `json:"via,omitempty"`
}

// AgentConfig is the agent_save payload and the saved broadcast's
// summary (chat cannot import agents — cycle via kb — so the wire
// shape lives here and the server converts).
type AgentConfig struct {
	Name   string `json:"name"`
	Role   string `json:"role,omitempty"`
	Manual string `json:"manual,omitempty"`
	Prompt string `json:"prompt,omitempty"` // C->S only

	// Archived marks the agent{event:"archived"} broadcast's summary
	// (v0.8 M1) — reminder-only, never stored in the chat history.
	Archived bool `json:"archived,omitempty"`
}

// Skill is skill_save's payload and the saved broadcast's summary —
// the wire shape of capability.Skill (kept local under the same
// import discipline as AgentConfig; the server converts both ways).
// Body rides C->S saves only; broadcasts and receipts summarize
// without the body — the detail stays behind GET /skills/{key}.
type Skill struct {
	Key       string   `json:"key"`
	Name      string   `json:"name,omitempty"`
	Desc      string   `json:"desc,omitempty"`
	Body      string   `json:"body,omitempty"` // C->S skill_save only
	Manuals   []string `json:"manuals,omitempty"`
	CreatedBy string   `json:"created_by,omitempty"` // S->C only
	CreatedTS int64    `json:"created_ts,omitempty"` // S->C only
}

// MCPServer is mcp_save's payload and the saved broadcast's summary —
// the wire shape of capability.MCPServer. Header VALUES ride C->S
// saves and the loopback detail read only; broadcasts and receipts
// carry the masked form (the server masks on the way out).
type MCPServer struct {
	Key       string      `json:"key"`
	Name      string      `json:"name,omitempty"`
	Desc      string      `json:"desc,omitempty"`
	Type      string      `json:"type,omitempty"` // http|sse
	URL       string      `json:"url,omitempty"`
	Headers   []MCPHeader `json:"headers,omitempty"`
	CreatedBy string      `json:"created_by,omitempty"` // S->C only
	CreatedTS int64       `json:"created_ts,omitempty"` // S->C only
}

// MCPHeader is one request header on the mcp wire shape.
type MCPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// GitFile is one path's change row on the commit card（vcs.FileChange 的
// wire 镜像——chat 不引 vcs，形状在此同构）。
type GitFile struct {
	Path    string `json:"path"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
}

// GitCommit is the "git" broadcast's payload: one freshly landed commit
// （post-commit 钩子报来）。Files 是逐文件红绿行；Refs 是提交信息里提
// 及的台账号（t_NN/r_NN）——提交卡与任务/需求的可视关联。SHA 给全长
// （跳转/git show 复用），展示侧自行短缩。
type GitCommit struct {
	SHA       string    `json:"sha"`
	Author    string    `json:"author,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	Body      string    `json:"body,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	DateTS    int64     `json:"date_ts,omitempty"`
	Files     []GitFile `json:"files,omitempty"`
	Additions int       `json:"additions,omitempty"`
	Deletions int       `json:"deletions,omitempty"`
	Refs      []string  `json:"refs,omitempty"`
}

// KbDoc is the payload of kb_write (key/title/body going in) and of
// the kb broadcast (the doc's metadata coming out).
type KbDoc struct {
	Key       string `json:"key,omitempty"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"` // C->S kb_write only
	Owner     string `json:"owner,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
	UpdatedTS int64  `json:"updated_ts,omitempty"`
	Rev       int    `json:"rev,omitempty"`
	Size      int    `json:"size,omitempty"`

	// Archived rides the kb broadcast for the v3 lifecycle events
	// ("archived"/"unarchived") and rollover volumes, so list faces
	// can re-sort without a re-read.
	Archived bool `json:"archived,omitempty"`
}

// Notice is the room's current group announcement — the payload of the
// S->C "notice" broadcast (and GET /p/{key}/notice's body). Chat keeps
// its own wire shape; the server converts from the notice package's
// store type.
type Notice struct {
	Content string `json:"content"`
	By      string `json:"by,omitempty"`
	TS      int64  `json:"ts,omitempty"`
	Rev     int    `json:"rev"`
}

// ---------------------------------------------------------------------------
// Domain payload mirrors: JSON-tag-for-tag copies of tasks/plan/merge/
// requirements/meeting/media store types. The protocol's OWN copies —
// a domain field change does NOT touch these until someone decides it
// is a wire change and updates the mirror + both conversions together.
// ---------------------------------------------------------------------------

// Image mirrors media.Image: the say-attachment reference (id + display
// hints) — the bytes stay behind GET /media/{id}.
type Image struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"` // 原文件名（展示用，无路径语义）
	Mime  string `json:"mime,omitempty"`
	W     int    `json:"w,omitempty"`
	H     int    `json:"h,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
}

// LogEntry mirrors tasks.LogEntry: one audit row on a task's ledger.
type LogEntry struct {
	TS   int64  `json:"ts"`
	By   string `json:"by"`
	Note string `json:"note"`
}

// Patch mirrors tasks.Patch: the task_update write face's patch. All
// fields are strings (the CLI passes argv verbatim); the engine parses.
type Patch struct {
	Status   string `json:"status,omitempty"`
	Progress string `json:"progress,omitempty"`
	Note     string `json:"note,omitempty"`
	Assignee string `json:"assignee,omitempty"`

	Project   string `json:"project,omitempty"`
	Version   string `json:"version,omitempty"`
	Start     string `json:"start,omitempty"`
	End       string `json:"end,omitempty"`
	Deps      string `json:"deps,omitempty"`
	Milestone string `json:"milestone,omitempty"`
	Pct       string `json:"pct,omitempty"`
	Priority  string `json:"priority,omitempty"`
}

// Proposal mirrors tasks.Proposal: a pending change awaiting
// confirmation from everyone in Need (any decline discards it).
type Proposal struct {
	By    string   `json:"by"`
	Patch Patch    `json:"patch"`
	Need  []string `json:"need"`
	Got   []string `json:"got,omitempty"`
	Kind  string   `json:"kind,omitempty"` // assign | change
	TS    int64    `json:"ts"`
}

// Task mirrors tasks.Task: one ledger entry — the "task" frame's
// snapshot, ten-field scheduling extension and all.
type Task struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Desc      string     `json:"desc,omitempty"`
	Assignee  string     `json:"assignee,omitempty"` // empty = task pool
	Status    string     `json:"status"`
	Progress  string     `json:"progress,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	CreatedTS int64      `json:"created_ts,omitempty"`
	UpdatedTS int64      `json:"updated_ts,omitempty"`
	Log       []LogEntry `json:"log,omitempty"`
	Pending   *Proposal  `json:"pending,omitempty"`

	ProjectKey  string   `json:"project_key,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Req         string   `json:"req,omitempty"`
	Version     string   `json:"version,omitempty"`
	PlanID      string   `json:"plan_id,omitempty"`
	StartTS     int64    `json:"start_ts,omitempty"`
	EndTS       int64    `json:"end_ts,omitempty"`
	Deps        []string `json:"deps,omitempty"`
	Milestone   bool     `json:"milestone,omitempty"`
	ProgressPct int      `json:"progress_pct,omitempty"`
	Priority    string   `json:"priority,omitempty"`

	DoingTS int64 `json:"doing_ts,omitempty"`
	DoneTS  int64 `json:"done_ts,omitempty"`
}

// PlanTask mirrors plan.PlanTask: one row of a scheduling proposal.
type PlanTask struct {
	Title     string `json:"title"`
	Desc      string `json:"desc,omitempty"`
	Assignee  string `json:"assignee,omitempty"`
	StartTS   int64  `json:"start,omitempty"`
	EndTS     int64  `json:"end,omitempty"`
	Deps      []int  `json:"deps,omitempty"`
	Milestone bool   `json:"milestone,omitempty"`
}

// Plan mirrors plan.Plan: the scheduling proposal (plan frames).
type Plan struct {
	ID          string     `json:"id,omitempty"` // p_NN, server-stamped
	Req         string     `json:"req,omitempty"`
	Title       string     `json:"title"`
	Need        string     `json:"need,omitempty"`
	ProjectKey  string     `json:"project_key,omitempty"`
	Version     string     `json:"version,omitempty"`
	Tasks       []PlanTask `json:"tasks"`
	SubmittedBy string     `json:"submitted_by,omitempty"`
	SubmittedTS int64      `json:"submitted_ts,omitempty"`
}

// Merge mirrors merge.Merge: the single-slot merge review gate's card.
type Merge struct {
	ID          string   `json:"id,omitempty"` // m_NN, server-stamped
	ProjectKey  string   `json:"project_key,omitempty"`
	Branch      string   `json:"branch"`
	Into        string   `json:"into"`
	Req         string   `json:"req,omitempty"`
	Tasks       []string `json:"tasks,omitempty"`
	Commits     int      `json:"commits,omitempty"`
	Files       int      `json:"files,omitempty"`
	Additions   int      `json:"additions,omitempty"`
	Deletions   int      `json:"deletions,omitempty"`
	SubmittedBy string   `json:"submitted_by,omitempty"`
	SubmittedTS int64    `json:"submitted_ts,omitempty"`
}

// Req mirrors requirements.Req: the requirement entry (req frames).
type Req struct {
	ID         string `json:"id"`
	ProjectKey string `json:"project_key"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	Status     string `json:"status"`
	CreatedBy  string `json:"created_by,omitempty"`
	CreatedTS  int64  `json:"created_ts,omitempty"`

	ParkNote    string `json:"park_note,omitempty"`
	ParkTS      int64  `json:"park_ts,omitempty"`
	ReviewAfter int64  `json:"review_after,omitempty"`
	ClosedTS    int64  `json:"closed_ts,omitempty"`
}

// Line mirrors meeting.Line: one transcript row.
type Line struct {
	TS   int64  `json:"ts"`
	By   string `json:"by"`
	Text string `json:"text"`
}

// Meeting mirrors meeting.Meeting: the review meeting snapshot.
type Meeting struct {
	ID           string   `json:"id"` // m_NN, store-stamped
	ProjectKey   string   `json:"project_key"`
	ReqID        string   `json:"req,omitempty"`
	ReqTitle     string   `json:"req_title,omitempty"`
	Chair        string   `json:"chair"`
	Participants []string `json:"participants,omitempty"`
	Status       string   `json:"status"`
	Phase        string   `json:"phase,omitempty"`
	Opening      string   `json:"opening,omitempty"`
	Conclusion   string   `json:"conclusion,omitempty"`
	Transcript   []Line   `json:"transcript,omitempty"`
	StartTS      int64    `json:"start_ts,omitempty"`
	EndTS        int64    `json:"end_ts,omitempty"`
}

// ---------------------------------------------------------------------------
// Trace / question / diff payloads (moved from chat — Message fields
// reference them, so the contract package owns the SHAPES; the fold,
// ring and dedup LOGIC stays in chat).
// ---------------------------------------------------------------------------

// TraceEntry 的 kind 词汇（前端 traceview.js 同一套；r_17 终端转录在
// 同一词汇表上追加四个 kind——转录专用，trace 环不产不收）。
const (
	TraceThink = "think" // 思考增量（reasoning_delta 的合并块）
	TraceDraft = "draft" // 回复草稿增量（text_delta 的合并块；终点另有 turn 条目全文）
	TraceTool  = "tool"  // 工具调用卡（按 call 原位更新：call→run→done|error）
	TraceModel = "model" // 模型请求轮次（第 N 轮 · 模型名）
	TraceTurn  = "turn"  // 回合分隔（start/done）
	TraceError = "error" // 错误（模型/回合错误）

	// 回合生命周期行（r_17 为终端转录而立，现双门）：input 注入行、
	// sys 系统注记、ask 反问、answer 应答。事件流拣选器不产这些
	// kind，但 dispatcher 的生命周期落账（deliver/steer/超时弃单/
	// 失败终态/回收/长回合/权限问答）经 emitTrace 一并送进环——环
	// 照 append 规则落（displayGrade 在冲水口收展示帽），「干活中」
	// 亮起时抽屉里就有本回合的注入与注记。
	TraceInput  = "input"  // 注入的输入行（房间消息/公告/出生提示/steer 的注入原文）
	TraceAsk    = "ask"    // 反向请求落问（权限请求/向房主提问）
	TraceAnswer = "answer" // 应答落账（房主点击/策略裁定/窗口超时）
	TraceSys    = "sys"    // 系统事件行（注入超时结果未知/注入失败已排队/失败中止/回收/长回合等）
)

// TraceEntry is one displayable moment of a member's session work —
// the "trace" frame's payload and GET /p/{key}/trace's row. Same-shape
// discipline: this struct IS the wire shape (web/wire/wire.js typedefs
// mirror it field-for-field).
type TraceEntry struct {
	Seq  int64  `json:"seq"`  // hub trace seq：插入序（替换键之一）
	TS   int64  `json:"ts"`   // unix 秒
	From string `json:"from"` // 成员名
	Kind string `json:"kind"` // Trace* 词汇

	// 子智能体归属：Agent=true 且 AgentID 为子会话短 id（前端缩进渲染
	// 子智能体自己的思考/工具流）。
	Agent   bool   `json:"agent,omitempty"`
	AgentID string `json:"agent_id,omitempty"`

	// think/draft/turn/error 的文本（turn.done 带最终回复全文）。
	Text string `json:"text,omitempty"`

	// input 行的来处（r_17 终端转录）：注入原文出自谁——「办公室消息/
	// 公告」解析出的发言者、steer 的房主、出生提示的「编制」。仅
	// kind=input 有值；additive，旧消费者无感。
	Src string `json:"src,omitempty"`

	// tool：Call 是 toolCallId（原位更新键），State 走 call→run→done|error；
	// Input 是工具入参的原样 JSON 值；Output 是结果正文或错误文本；
	// OK 随结果落定；Ms 是执行耗时。
	Call   string `json:"call,omitempty"`
	Tool   string `json:"tool,omitempty"`
	State  string `json:"state,omitempty"`
	Input  any    `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	OK     *bool  `json:"ok,omitempty"`
	Ms     int64  `json:"ms,omitempty"`

	// model：Model 是 providerId/modelId，Iter 是回合内第几轮请求。
	Model string `json:"model,omitempty"`
	Iter  int    `json:"iter,omitempty"`

	// turn：Stop 是终点 stopReason（stop|error|…）。
	Stop string `json:"stop,omitempty"`
}

// Question is one interactive ask: the structured form of an
// AskUserQuestion tool call, as the workbench's option card renders
// it. Asks mirrors the tool's questions array 1:1 (question text,
// header chip, options with label + description, multiSelect).
type Question struct {
	ID     string        `json:"id"`
	From   string        `json:"from"`             // the asking member's name
	TS     int64         `json:"ts"`               // util.Now() seconds at ask
	Due    int64         `json:"due,omitempty"`    // unix seconds the window closes (0 = unknown)
	Prompt string        `json:"prompt,omitempty"` // the call's overall context, shown as the card's lead
	Asks   []QuestionAsk `json:"asks"`
}

// QuestionAsk is one question inside a Question.
type QuestionAsk struct {
	Question string           `json:"question"`
	Header   string           `json:"header,omitempty"`
	Multi    bool             `json:"multi,omitempty"`
	Options  []QuestionOption `json:"options,omitempty"`
}

// QuestionOption is one selectable choice.
type QuestionOption struct {
	Label string `json:"label"`
	Desc  string `json:"desc,omitempty"`
}

// DiffLine is one row of a kb write's diff: '-' the line left the doc,
// '+' the line entered it. Kind is the literal wire glyph (the card
// paints by it); unchanged lines never ride — the head already names
// the doc, the rows are the change itself.
type DiffLine struct {
	Kind string `json:"k"`
	Text string `json:"t,omitempty"`
}
