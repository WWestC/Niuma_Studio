// wire.js — the workbench protocol layer (v2 P1 skeleton).
//
// Frame vocabulary mirrors chat/wire.go one-for-one (frontend 稿 §2.5:
// the same-shape discipline's JS side — same names, same shapes, no
// DTO in between). The Msg* strings are byte-stable wire constants;
// renaming anything here breaks clients, it is not a refactor.

/** Wire frame type constants — byte-identical to chat/wire.go. */
// ProtoVersion mirrors wire.ProtoVersion (Go side owns the truth) — the
// hello dials carry it for version negotiation; it is a scalar, not frame
// vocabulary, so it lives outside the Msg mirror the alignment test pins.
export const ProtoVersion = 1;

export const Msg = {
  Hello: 'hello',
  Say: 'say',
  Report: 'report',
  Bye: 'bye',
  Welcome: 'welcome',
  Join: 'join',
  Leave: 'leave',
  System: 'system',
  Rename: 'rename',
  // S->C reminder-family broadcast: the dispatcher's turn tell —
  // {from: name, working, since}. No history, no badge, no @: it
  // only feeds the roster chip and the 「正在处理…」stream hint.
  MemberWork: 'member_work',
  // S->C observer-only keepalive (mirrors chat/wire.go): the server's
  // heartbeat tick SendTos one {type:"ping"} so a quiet room still feeds
  // conn.js's inbound watchdog — protocol pings are invisible to JS.
  // Swallowed at the transport; never rendered, never in history.
  Ping: 'ping',
  TaskCreate: 'task_create',
  TaskUpdate: 'task_update',
  TaskConfirm: 'task_confirm',
  TaskDecline: 'task_decline',
  // 房主仲裁（host bypass）：批准跳过剩余确认人直接生效、驳回作废
  // 提案。房主从不在 Need 名单里，接单/婉拒对房主只会被拒。
  TaskArbitrate: 'task_arbitrate',
  Task: 'task',
  // S->C 需求帧：立案成功的房间广播（t_170 声色同门——与 task 族同
  // 快照同语言，绿点门同门放行）＋立案者的私回帧（created|denied）
  Req: 'req',
  ReqCreate: 'req_create',
  AgentSave: 'agent_save',
  Agent: 'agent',
  KbWrite: 'kb_write',
  KbAppend: 'kb_append',
  KbRestore: 'kb_restore',
  // v3 治理三帧：归档/出档（房主/owner/rank≥owner）、删除（仅房主）
  KbArchive: 'kb_archive',
  KbUnarchive: 'kb_unarchive',
  KbDelete: 'kb_delete',
  Kb: 'kb',

  // Management verbs the owner's seatless face speaks (the constants
  // live in Go's server package for historical reasons — kick.go,
  // rank.go, archive.go — but the wire strings are byte-stable all
  // same, and this file is the workbench's single naming entry).
  Kick: 'kick',
  RankSet: 'rank_set',
  AgentArchive: 'agent_archive',

  // Skill & MCP assembly (mirrors chat/wire.go).
  SkillSave: 'skill_save',
  MCPSave: 'mcp_save',
  Assemble: 'assemble',
  SkillEvt: 'skill',
  McpEvt: 'mcp',

  // Plan proposals (v2 P4-b, mirrors chat/wire.go).
  PlanSubmit: 'plan_submit',
  PlanAccept: 'plan_accept',
  PlanReject: 'plan_reject',
  Plan: 'plan',

  // Requirement-review meeting (需求评审会议, mirrors chat/wire.go):
  // reminder-only occupancy broadcasts from the dispatcher's clock.
  Meeting: 'meeting',
  // The chair's meeting clock (write side): meeting_end fires the
  // current window early, meeting_extend {minutes} pushes its deadline
  // out (both chair-only). Found missing by contract.test.js.
  MeetingEnd: 'meeting_end',
  MeetingExtend: 'meeting_extend',
  // Goal-scoped autopilot wrap-up (全智能模式): the orchestrator's
  // autopilot_done write closes the session; the S->C reply is the
  // CLI's receipt. Found missing by contract.test.js.
  Autopilot: 'autopilot',
  AutopilotDone: 'autopilot_done',

  // Room pause (房间暂停, mirrors chat/wire.go): reminder-only
  // data-sync broadcast {event: paused|resumed} — the office sim
  // freezes on it in every window; welcome frames carry the current
  // state as a paused field (hydration); the recorded trail is a
  // system line with event "pause". Write face: POST /p/{key}/pause.
  Pause: 'pause',

  // Per-room group announcement (飞书式群公告, mirrors chat/wire.go):
  // notice_save rides the owner's write face (text/expect_rev/silent;
  // empty text = 撤下); "notice" is the reminder-only data-sync
  // broadcast (published|updated|cleared|denied) — the recorded trail
  // is a system line with event "notice" instead.
  NoticeSave: 'notice_save',
  Notice: 'notice',

  // Ack receipts (飞书式收到, mirrors chat/wire.go): ack rides the
  // owner's write face with the say line's seq; the "ack" broadcast is
  // the reminder-only increment (from + seqs) — the facts' store is
  // GET /p/{key}/acks. A receipt is a fact about the original line,
  // never a new chat message: nothing badges, nothing wakes.
  Ack: 'ack',

  // Emoji reactions (飞书式表情回应, mirrors chat/wire.go): react
  // rides any write face with {seq, emoji, on} — add (on) or remove
  // the dialer's own reaction on that say line; the "react" broadcast
  // is the reminder-only fact (from + seq + emoji + on, absent on
  // reads off) — the facts' store is GET /p/{key}/reacts. A reaction
  // is an opinion about the original line, never a new chat message:
  // nothing badges, nothing wakes (and reacting to one's own line is
  // fine — unlike the 收到 ack, an opinion is not an answer).
  React: 'react',

  // Read receipts (飞书式已读, mirrors chat/wire.go): the reminder-only
  // increment broadcast (from + seqs) — From's turn CONSUMED those say
  // lines (已读 lands at the turn's terminal, not at injection). The
  // facts' store is GET /p/{key}/reads; nothing badges, nothing wakes.
  Read: 'read',

  // Delivery-queue projection (排队中, mirrors chat/wire.go): the
  // reminder-only FULL-STATE broadcast (from + seqs; empty = drained) —
  // From's next-turn lane currently holds exactly those say lines:
  // queued behind a busy turn or a restart, not yet injected, neither
  // read nor lost. The facts ride the reads snapshot's queues rows;
  // nothing badges, nothing wakes.
  Queue: 'queue',

  // Interactive questions (向房主提问, mirrors chat/wire.go): a
  // member's AskUserQuestion call becomes a clickable option card —
  // "question" carries the structured asks, "answer" (owner write
  // face) lands the choice back on the parked tool call, and
  // "question_done" closes the card (answered|expired). Reminder-only
  // frames (never in history): hydration is GET /p/{key}/questions.
  Question: 'question',
  QuestionDone: 'question_done',
  Answer: 'answer',

  // 工作过程 (v2.8, mirrors chat/wire.go): the dispatcher's trace tap
  // folds a member's displayable session moments (thinking, draft
  // reply, tool calls/results, model iterations, turn boundaries,
  // errors — subagent streams tagged agent+agent_id) into TraceEntry
  // rows. S->C {from, trace:[…]} is one flush-batch; reminder-only
  // (never in history, no badge, no wake). Hydration is GET
  // /p/{key}/trace — the 工作过程 drawer consumes it live.
  Trace: 'trace',

  // 终端转录 (r_17, mirrors chat/wire.go): the hub's term journal
  // flushes the SAME fold shape (merged text / updated tool cards ride
  // their original seq) plus the transcript-only kinds — input 注入行
  // (src = 来处), ask 反问, answer 应答, sys 系统行. Reminder-only,
  // same discipline as trace. Hydration is GET /p/{key}/term — the
  // 终端 board consumes it live.
  Term: 'term',

  // 版本管理事件流 (v2.8 gitflow, mirrors chat/wire.go): "git" 是
  // post-commit 钩子报来的提交播报（提醒族广播——不进历史、不 badge、
  // 不唤醒），聊天流出一张提交卡（谁、说了什么、动了哪些文件各红绿
  // 多少行）；merge_submit/accept/reject 是合并评审门的 WS 动词（提交
  // 属编排者座位，接受/拒绝是房主专属关口——卡的按钮经 frameTo 送）。
  Git: 'git',
  MergeSubmit: 'merge_submit',
  MergeAccept: 'merge_accept',
  MergeReject: 'merge_reject',
  Merge: 'merge',

  // 工作区变更推送（文件板自动刷新的服务端半边，mirrors wire.go）：
  // workspace watcher 看见某项目工作区动了就广播 {project, ts}——帧只
  // 说「脏了」，不带增量（查询式真源仍是 /p/{key}/fs/tree），正浏览该
  // 项目的文件板收到后重拉已展开的目录。Reminder-only：不进历史、不
  // badge、不唤醒。
  FsDirty: 'fs_dirty',
};

/** The reserved lobby room key (chat.LobbyKey = projects.LobbyKey). */
export const LobbyKey = 'default';

/** say origin markers. Whisper marks the owner's private line to ONE
 *  member (never broadcast, never in history — the 🔒 receipt's frame).
 *  Found missing by the frame-contract test (wire/contract_gen_test.go
 *  ↔ contract.test.js): the Go side grew it, the JS mirror hadn't. */
export const Origin = { Mirror: 'mirror', Bridge: 'bridge', Whisper: 'whisper' };

/** system-frame event markers (chat/wire.go) — Agg marks the lobby
 *  aggregator's roll-up of another room's activity: render as a glance
 *  line, never badge the lobby unread. Interrupted marks the restart's
 *  unfinished-work roll call (frame.interrupted): the farewell snapshot
 *  caught these seats mid-turn — render the continue card. Rebuild marks
 *  a studio-level rebuild announcement (「已发起源码重编译」family,
 *  recorded in every room at once): render as a normal system line, but
 *  never badge — ambient studio news owes no red dot. */
export const Event = { Agg: 'agg', Interrupted: 'interrupted', Rebuild: 'rebuild' };

/** The DOM mirror's per-room message-window cap — shared by the state
 *  core (rooms.js trims to it) and the hydrator (conn.js asks the tail
 *  read for exactly this many, so a cold open refills the window the
 *  live stream would have filled; v2.8.1). */
export const MESSAGE_CAP = 300;

/**
 * @typedef {Object} LookAcc — one accessory in the look projection
 * (chat.LookAcc): Style is the wardrobe key (primary id since the
 * t_128 merge), Color rides along for callers that only paint.
 * Full mirror of the Go wire type — pinned by contract.test.js.
 * @property {string} [slot]
 * @property {string} [style]
 * @property {string} [color]
 */

/**
 * @typedef {Object} Look — the lifetime-layer look pick (chat.Look):
 * pixart wardrobe slots riding the Member. Full mirror of the Go wire
 * type — pinned field-for-field by contract.test.js.
 * @property {string} [hair_style]
 * @property {string} [skin]
 * @property {LookAcc[]} [accessories]
 */

/**
 * @typedef {Object} Member — chat.Member. Full mirror of the Go wire
 * type (contract.test.js pins the field set); the working pair rides
 * status broadcasts, look rides the pixart wardrobe.
 * @property {string} name
 * @property {string} color     hex shirt color, e.g. "#e07a5f"
 * @property {string} hair      hex hair color
 * @property {Look} [look]      lifetime-layer look (r_05 t_126)
 * @property {string} [role]
 * @property {string} [last_report]
 * @property {number} [last_report_ts]
 * @property {boolean} [working]       mid-turn (the status light's truth)
 * @property {number} [working_since]
 */

/**
 * @typedef {Object} Frame — one WS frame (chat.Message). Only the
 * fields the workbench consumes are typed; every field is omitempty on
 * the wire.
 * @property {string} type
 * @property {string} [from]
 * @property {string} [text]
 * @property {number} [ts]
 * @property {number} [seq]          hub delivery order (say/report)
 * @property {string[]} [mentions]   display names the text @-addressed
 * @property {string} [origin]       "mirror" | "bridge"
 * @property {Member} [member]       join
 * @property {Member} [you]          welcome (absent on observer echo)
 * @property {Member[]} [members]    welcome roster
 * @property {string} [project]      hello/welcome echo; capability frames carry the seat's project
 * @property {boolean} [paused]      welcome: the room's pause state (办公室冻结的水合面)
 * @property {string} [to]           rename: the deduped "-2" name
 * @property {string} [event]        task/agent/kb/capability event name; system frames carry "agg" (lobby roll-up, badge-exempt)
 * @property {string} [task_id]
 * @property {TaskFull} [task]       S->C "task" broadcasts: the snapshot after the op
 * @property {Object} [patch]        task C->S update: the partial payload (wire.Patch — field ops on the named task)
 * @property {boolean} [approve]     task_arbitrate C->S (owner): bypass verdict — true applies the pending patch, false discards; absent refuses rather than reads decline
 * @property {Requirement} [req]     S->C "req" frames: the requirement snapshot（广播 created/deleted（房主 HTTP 腿）｜私回 created/denied）
 * @property {string} [name]         kick/rank_set target (owner verbs)
 * @property {number} [rank]         rank_set payload
 * @property {Skill} [skill]         skill{saved|denied} receipt
 * @property {MCPServer} [mcp]       mcp{saved|denied} receipt (header values masked)
 * @property {string} [person]       skill{assembled|denied} target
 * @property {string} [target]       assemble target staffing|profile
 * @property {string} [op]           assemble C->S: the list op set|add|remove
 * @property {string} [via]          capability C->S: the CLI rider's audit marker ("cli"; empty reads as "ws")
 * @property {string[]} [skills]     skill{assembled} final skill assembly
 * @property {string[]} [mcps]       skill{assembled} final MCP assembly
 * @property {Plan} [plan]           plan{submitted|accepted|rejected|superseded} payload
 * @property {string} [plan_id]      plan_accept/plan_reject C->S: the pending plan
 * @property {Plan} [revised]        plan_accept C->S: the host's entry-edited full replacement plan
 * @property {Object} [merge]        merge_submit C->S: the proposal core (branch required, into/req optional — wire.Merge); S->C "merge" broadcasts carry the snapshot
 * @property {string} [merge_id]     merge_accept/merge_reject C->S: the pending merge
 * @property {MeetingRecord} [meeting] meeting{started|advanced|extended|ended} payload
                                        (advanced＝主持提前进总结、extended＝主持延时)
 * @property {number} [minutes]      meeting_extend C->S: the extension in minutes (0/absent = the 10 default)
 * @property {Notice} [notice]       notice{published|updated|cleared} payload (absent on cleared/denied)
 * @property {KbDoc} [doc]           kb_write C->S: the write payload (key/title/body); S->C "kb" broadcasts carry the doc's new metadata
 * @property {string} [key]          kb_write/append/restore/delete C->S: the target doc key
 * @property {number} [rev]          kb_restore C->S: the revision to roll back to
 * @property {Object} [agent]        agent_save C->S: the onboarding config (wire.AgentConfig); S->C "agent" broadcasts carry the saved summary
 * @property {number} [current_rev]  kb/notice{denied}: the room's real rev on an expect_rev conflict
 * @property {number} [expect_rev]   kb_write/notice_save C->S: conditional write
 * @property {boolean} [silent]      notice_save C->S: publish without waking the staff
 * @property {number[]} [seqs]       read/ack S->C: the say seqs the frame's from has read / acked（收到）
 * @property {string} [emoji]        react S->C/C->S: the palette slot (e.g. "👍")
 * @property {boolean} [on]          react frames: add the dialer's own reaction (true) / remove (absent reads off)
 * @property {string} [qid]          question family: the pending ask's id
 * @property {Question} [question]   S->C "question": the option card's payload
 * @property {Object.<string, string>} [answer] answer C->S / question_done S->C: chosen answers keyed by question text
 * @property {string} [qstatus]      question_done S->C: answered|expired
 * @property {TraceEntry[]} [trace]  S->C "trace": one member's flush-batch (工作过程)
 * @property {MsgImage[]} [images]   say: the line's picture attachments (输入图片)
 * @property {Quote} [quote]         say: the structured reply-quote (引用回复) —
 *                                   fields on the message, the body stays clean
 */

/**
 * @typedef {Object} Quote — one structured reply-quote (chat.Quote): the
 * quoted line's identity riding the say as FIELDS, never as a text
 * prefix baked into text (复制/转发/检索不再把引用带下来). Legacy history
 * lines carry their quote embedded in text instead — the renderer's
 * prefix parser stays for them.
 * @property {number} [seq]   quoted line's seq in THIS room (0 = 无从精确跳转)
 * @property {string} from    quoted speaker's display name (the block's 名牌)
 * @property {boolean} [at]   reply-mention (引用即点名 — the hub wakes `from` by field)
 * @property {string} [snip]  one-line summary of the quoted line
 * @property {string} [via]   forward provenance (转发自的房间名)
 */

/**
 * @typedef {Object} MsgImage — one stored image reference (media.Image):
 * the say frame's images field, POST /media's receipt and the history
 * record carry exactly this. The bytes live behind GET /media/{id}.
 * @property {string} id
 * @property {string} [name]  original filename (display only)
 * @property {string} [mime]
 * @property {number} [w]     pixel width (client-declared display hint)
 * @property {number} [h]     pixel height
 * @property {number} [bytes]
 */

/**
 * @typedef {Object} TraceEntry — one displayable moment of a member's
 * session work (chat.TraceEntry; the "trace" frame's payload and
 * GET /p/{key}/trace's row). Fold discipline mirrors the hub ring:
 * think/draft merge into the same-stream tail; a tool entry replaces
 * the entry with the same call (result fills the card in place).
 * Lifecycle rows (input/sys/ask/answer, v2.9 双门) never fold — each
 * lands its own line: 「干活中」亮起时本回合的注入即在此。
 * @property {number} seq            hub trace seq — replace key
 * @property {number} ts             unix seconds
 * @property {string} from           the member's name
 * @property {string} kind           think|draft|tool|model|turn|error|input|sys|ask|answer
 * @property {boolean} [agent]       entry from a subagent session
 * @property {string} [agent_id]     subagent session id (short tag)
 * @property {string} [text]         think/draft/turn/error body; input 注入原文; sys 注记; ask 问题; answer 应答
 * @property {string} [src]          input only: 注入来处（发言者/编制/房主）
 * @property {string} [call]         toolCallId — the tool card's identity
 * @property {string} [tool]         tool name, e.g. "Bash" / "Read"
 * @property {string} [state]        tool: call→run→done|error; turn: start|done
 * @property {*} [input]             tool input (raw JSON value)
 * @property {string} [output]       tool result content / error text
 * @property {boolean} [ok]          tool result success; answer 应答裁定
 * @property {number} [ms]           tool/turn duration
 * @property {string} [model]        model request: providerId/modelId
 * @property {number} [iter]         model request: iteration within the turn
 * @property {string} [stop]         turn terminal: stop|success|error…
 */

/**
 * @typedef {Object} TraceMemberRow — GET /p/{key}/trace members row.
 * @property {string} from
 * @property {boolean} working
 * @property {TraceEntry[]} entries
 */

/**
 * @typedef {Object} TraceReport — GET /p/{key}/trace[?name=]
 * @property {string} project
 * @property {TraceMemberRow[]} members
 */

/**
 * @typedef {Object} QuestionOption — one selectable choice (chat.QuestionOption).
 * @property {string} label
 * @property {string} [desc]
 */

/**
 * @typedef {Object} QuestionAsk — one question inside a card (chat.QuestionAsk).
 * @property {string} question
 * @property {string} [header]
 * @property {boolean} [multi]
 * @property {QuestionOption[]} [options]
 */

/**
 * @typedef {Object} Question — one open interactive ask (chat.Question;
 * GET /p/{key}/questions row, the "question" frame's payload).
 * @property {string} id
 * @property {string} from            the asking member
 * @property {number} ts
 * @property {number} [due]           unix seconds the window closes
 * @property {string} [prompt]        the call's overall context lead
 * @property {QuestionAsk[]} [asks]
 */

/**
 * @typedef {Object} Notice — one room's current group announcement
 * (chat.Notice; GET /p/{key}/notice's notice slot, null = none).
 * @property {string} content
 * @property {string} [by]
 * @property {number} [ts]
 * @property {number} [rev]
 */

/**
 * @typedef {Object} HelloFrame
 * @property {string} type      "hello"
 * @property {boolean} [observer] v2 P1: seatless read-only dial
 * @property {string} [project]   v2 P1/P3: room key, absent = "default"
 * @property {boolean} [replay]   false skips the history replay
 * @property {string} [visitor]   r_19: the /view share token — token-carrying
 *   observer dials are the only ones the server meters as visitors; the
 *   host's own windows never carry it
 * @property {string} [name]      owner write dial: the owner's name
 * @property {string} [role]      owner write dial: the owner's role
 * @property {string} [auth]      identity layer: the login session token (or the studio
 *   service token for local CLI) — required for non-loopback dials under multi-user;
 *   browsers ride the session cookie on the handshake instead
 * @property {number} [proto]     protocol version negotiation: the client's max;
 *   the welcome echoes the server's; a value above the server's refuses the dial
 */

/**
 * @typedef {Object} ProjectSummary — GET /projects row
 * @property {string} key       slug; "default" is the lobby
 * @property {string} name
 * @property {string} status    draft|active|archived (switcher lists active)
 * @property {string} workspace
 * @property {string} [desc]
 * @property {Array<{name: string, start_ts?: number, end_ts?: number, goal?: string}>} [versions]
 * @property {string} [created_by]
 * @property {number} [created_ts]
 */

/**
 * @typedef {Object} PersonSummary — GET /kb/people row
 * @property {string} name
 * @property {string} [role]
 * @property {string} [color]    shirt hex, online members only
 * @property {boolean} online
 * @property {boolean} [grace]   presence-grace seat (no live connection)
 * @property {boolean} [local]   the human behind the window — the owner
 * @property {boolean} [working]       dispatcher's turn-in-flight tell (live seats only) — the 干活中 chip's cold source, same bit the CLI roster prints
 * @property {number}  [working_since] unix seconds the current turn lit the tell (0/absent when off)
 * @property {number} rank       1–9, 99 = the host
 * @property {string} [last_report]
 * @property {number} [last_report_ts]
 */

/**
 * @typedef {Object} HistoryPage — GET /p/{key}/history (oldest matches
 * first within the batch; forward paging with next_since until has_more
 * clears is lossless; tail=1 flips the batch to the NEWEST limit
 * matches — the cold open's hydration shape, has_more then false and
 * next_since the newest seq)
 * @property {string} project
 * @property {number} count
 * @property {Frame[]} messages
 * @property {boolean} has_more
 * @property {number} next_since
 */

// --- people-domain projections (CAP-M4) ---------------------------------
//
// Shapes mirror the Go projections field-for-field (same-shape
// discipline, PRD §3.5): kb.PersonDetail, agents summary rows,
// dispatch.Status, staffing.Entry, kb.EstablishmentReport,
// capability.Pack / EffectiveFabric. No DTOs in between.

/**
 * @typedef {Object} Task — GET /kb/tasks row (tasks.Task, ten-field v2
 * extensions omitted where the cards never show them)
 * @property {string} id           t_NN
 * @property {string} title
 * @property {string} [desc]
 * @property {string} [assignee]
 * @property {string} status      todo|doing|pending|done|cancelled
 * @property {boolean} [pending]  proposal awaiting the assignee
 * @property {string} [progress]
 * @property {string} [project_key]
 * @property {number} [updated_ts]
 */

/**
 * @typedef {Object} KbDocMeta — GET /kb/docs index row (kb.DocMeta):
 * the metadata face; the body stays behind GET /kb/docs/{key}.
 * @property {string} key
 * @property {string} [title]
 * @property {string} [owner]
 * @property {string} [updated_by]
 * @property {number} [updated_ts]
 * @property {number} [rev]
 * @property {number} [size]
 * @property {boolean} [archived] — v3 治理：归档位（归档架 ?archived=1 面）
 */

/**
 * @typedef {KbDocMeta} KbDoc — GET /kb/docs/{key}?rev=: the doc in
 * full, body included (the C->S kb_write payload's read twin).
 * @property {string} body
 */

/**
 * @typedef {Object} KbDocRev — GET /kb/docs/{key}/history row: one
 * version of the chain (bodies stay behind ?rev=N).
 * @property {number} rev
 * @property {string} [by]
 * @property {number} [ts]
 * @property {string} [title]
 * @property {number} [size]
 */

/**
 * @typedef {PersonSummary} PersonDetail — GET /kb/people/{name}: the
 * roster row plus the saved profile's prompt body, manual key and the
 * person's task ledger (in-progress first).
 * @property {string} [prompt]
 * @property {string} [manual]   kb doc key, e.g. "roles/hr"
 * @property {Task[]} [tasks]
 */

/**
 * @typedef {Object} AgentSummary — GET /agents row (the archive toggle
 * /kb/people itself cannot serve: the roster is active-only by design)
 * @property {string} name
 * @property {string} [role]
 * @property {boolean} [archived]
 */

/**
 * @typedef {Object} DispatchStatus — one /dispatch snapshot row
 * @property {string} name
 * @property {string} [role]
 * @property {string} [session_id]
 * @property {string} status      idle|running
 * @property {number} queued
 * @property {number} inject
 */

/**
 * @typedef {Object} ModelTier — the seat's model pick (staffing row's
 * model slot; skills never carry one)
 * @property {string} id
 * @property {string} [reasoning]  empty = platform default
 */

/**
 * @typedef {Object} Skill — capability.Skill (GET /skills/{key};
 * library rows omit body and carry body_bytes instead)
 * @property {string} key
 * @property {string} name
 * @property {string} [desc]
 * @property {string} [body]         instruction fabric (detail/save only)
 * @property {number} [body_bytes]   library row weight
 * @property {string[]} [manuals]    ordered kb doc keys
 * @property {string} [created_by]
 * @property {number} [created_ts]
 */

/**
 * @typedef {Object} MCPHeader — one request header on an MCP server.
 * @property {string} name
 * @property {string} value   masked in list/broadcast faces (loopback detail carries cleartext)
 */

/**
 * @typedef {Object} MCPServer — capability.MCPServer (GET /mcps/{key})
 * @property {string} key
 * @property {string} name
 * @property {string} [desc]
 * @property {string} type      http|sse
 * @property {string} url
 * @property {MCPHeader[]} [headers]
 * @property {string} [created_by]
 * @property {number} [created_ts]
 */

/**
 * @typedef {Object} EffectiveFabric — Compose output (the seat's
 * everything): full injection text plus the folded manuals/model/MCP.
 * @property {string} text
 * @property {string[]} [manuals]
 * @property {ModelTier} [model]
 * @property {MCPServer[]} [mcp]
 */

/**
 * @typedef {Object} FabricPreview — GET /p/{key}/staffing/{person}/fabric
 * @property {string} project
 * @property {string} person
 * @property {string[]} skills      effective skill keys (profile ⊕ seat)
 * @property {string[]} mcps        effective MCP server keys (profile ⊕ seat)
 * @property {EffectiveFabric} fabric
 */

/**
 * @typedef {Object} EstablishmentRow — one /kb/establishment row
 * @property {string} key
 * @property {string} name
 * @property {string} role
 * @property {number} headcount
 * @property {boolean} auto_fill
 * @property {string} manual
 * @property {boolean} must          第七列「必须」：系统岗=是（创建工作室时自动招募/自动创建，界面锁定）
 * @property {boolean} [system]      服务端判定（key ∈ 系统岗注册表）——UI 据此置灰锁行
 * @property {string[]} present
 * @property {number} missing        小助手行恒为 0（顾问无座位，随调度常驻）
 */

/**
 * @typedef {Object} EstablishmentReport — GET /kb/establishment: a
 * broken table is 200 + parse_error + empty rows, never an HTTP error.
 * @property {EstablishmentRow[]} rows
 * @property {string} parse_error
 * @property {number} rev            表文档当前 rev——行写接口的 expect_rev 锚（0=缺表/坏表）
 */

/**
 * @typedef {Object} StaffingSkill — one effective skill of a staffing
 * row (GET /p/{key}/staffing): the resolved key/name through
 * capability's EffectiveSkills (profile ⊕ seat override, Q18).
 * @property {string} key
 * @property {string} [name]   empty = not in the library (degrades at compose)
 */

/**
 * @typedef {Object} StaffingRow — GET /p/{key}/staffing row (v2 P4-c
 * projection): the staffing.Entry facts plus the room's seat presence
 * and the seat's effective skill list.
 * @property {string} person
 * @property {string} [project_role]
 * @property {string} state       onboard|active|offboard
 * @property {string} [session_id]
 * @property {boolean} seated     a live seat holds the name right now
 * @property {StaffingSkill[]} skills
 */

/**
 * @typedef {Object} StaffingReport — GET /p/{key}/staffing
 * @property {string} project
 * @property {StaffingRow[]} rows
 */

// --- project / gantt domain projections (P4-d) ---------------------------
//
// Shapes mirror the Go projections field-for-field (same-shape
// discipline, PRD §3.5): requirements.Req, plan.Plan / plan.PlanTask,
// tasks.Task's ten-field extension, tasks.View (the ScheduleView
// gantt object, server-painted with chat's member palette). No DTOs
// in between.

/**
 * @typedef {Object} Requirement — requirements.Req (GET /p/{key}/reqs)
 * @property {string} id          r_NN
 * @property {string} project_key
 * @property {string} title
 * @property {string} [body]
 * @property {string} status      open|split|closed
 * @property {string} [created_by]
 * @property {number} [created_ts] Unix seconds
 * @property {number} [closed_ts]  Unix seconds — the completion stamp
 *   (MarkClosed's write; absent while open/split)
 */

/**
 * @typedef {Object} ReqsReport — GET /p/{key}/reqs
 * @property {string} project
 * @property {Requirement[]} reqs
 */

/**
 * PlanTask — plan.PlanTask. Deps carry PLAN-INTERNAL 1-based entry
 * indices referencing only EARLIER entries; start/end are Unix
 * seconds (O6). Full field mirror generated into gen/types.js
 * (`go run ./tools/wiregen`) — this alias keeps the consumption
 * vocabulary; field-level truth lives on the Go wire mirror.
 * @typedef {import('./gen/types.js').PlanTask} PlanTask
 */

/**
 * Plan — plan.Plan (plan frames + GET /p/{key}/plan). The review
 * overlay's edits touch title/need/version/tasks only — id/project_
 * key/req/submitted_* stay locked to the pending plan. Full field
 * mirror generated into gen/types.js.
 * @typedef {import('./gen/types.js').Plan} Plan
 */

/**
 * @typedef {Object} PlanReport — GET /p/{key}/plan: plan is null when
 * the project's single review slot is empty.
 * @property {string} project
 * @property {Plan|null} plan
 */

// --- meeting domain (需求评审会议) ----------------------------------------

/**
 * MeetingLine — one turn of a review meeting's real discussion (the
 * member's mirrored session reply, verbatim). Full field mirror
 * generated into gen/types.js (wire.Line).
 * @typedef {import('./gen/types.js').Line} MeetingLine
 */

/**
 * MeetingRecord — meeting.Meeting (meeting frames + GET /p/{key}/
 * meeting). status "live" = the room is occupied and the screen lit;
 * "done" rows ride the history list. Full field mirror generated
 * into gen/types.js (wire.Meeting).
 * @typedef {import('./gen/types.js').Meeting} MeetingRecord
 */

/**
 * @typedef {Object} MeetingReport — GET /p/{key}/meeting: the room's
 * occupancy truth (meeting null = free) plus recent finished reviews.
 * @property {string} project
 * @property {MeetingRecord|null} meeting
 * @property {MeetingRecord[]} history
 */

/**
 * TaskFull — tasks.Task, the ten-field extension the ledger cards and
 * drawer consume (superset of the Task subset typedef above). Full
 * field mirror generated into gen/types.js (`go run ./tools/wiregen`)
 * — field-level truth lives on the Go wire mirror's comments; this
 * alias keeps the consumption vocabulary.
 * @typedef {import('./gen/types.js').Task} TaskFull
 */

/**
 * @typedef {Object} ScheduleLane — tasks.Lane painted by the server
 * with chat's member palette (the same hex a joined Member carries).
 * @property {string} assignee
 * @property {string} color
 */

/**
 * @typedef {Object} ScheduleBar — tasks.Bar plus the painted color.
 * Derived states (blocked/overdue) and group aggregates arrive
 * computed — the gantt never re-derives (A-P1).
 * @property {string} task_id
 * @property {string} kind        task (leaf) | group (non-leaf aggregate)
 * @property {string} label
 * @property {string} assignee    lane placement; "" = pool bar
 * @property {string} [parent]
 * @property {number} start       Unix seconds
 * @property {number} [end]
 * @property {number} [progress_pct]
 * @property {string[]} [deps]    resolvable in-view edges
 * @property {boolean} [milestone]
 * @property {string} status
 * @property {boolean} blocked
 * @property {boolean} overdue
 * @property {string} color
 */

/**
 * @typedef {Object} ScheduleView — GET /p/{key}/schedule: the gantt
 * projection object (object dictionary's 排期视图 row). Times are Unix
 * seconds throughout; range/version_window are the view-fit material.
 * @property {string} project_key
 * @property {string} [version]   requested lens; "" = full project
 * @property {number[]} range     [min,max] fit material; [0,0] = nothing schedules
 * @property {number[]} [version_window] Q7 anchor; 0 side = open
 * @property {number} today_ts    today-line anchor
 * @property {ScheduleLane[]} lanes
 * @property {ScheduleBar[]} bars
 * @property {string[]} inbox     待排期 task ids (project-wide)
 */
