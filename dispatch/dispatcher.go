// Package dispatch is the room's in-process member dispatcher (v1.0):
// when someone speaks, the dispatcher routes the line into the
// addressed member's ZCode session — as the session's next user
// input, exactly as if the member had typed it — and mirrors the
// session's turn reply back into the room through the member's own
// seat. This replaces the guard/watchdog integration: members no
// longer run resident listeners of their own.
//
// Shape: one goroutine-set inside the room process (the StartWatch
// pattern — zero extra seats beyond the members' own), one zcode
// app-server child process driving every member session.
//
// Routing rules (pure rules, no model in the loop — the v1.0 ruling):
//   - a say/report mentioning dispatcher members → deliverSend to
//     each (session/send; busy (-32010) queues on the member's
//     next-turn lane, one message per turn);
//   - a say mentioning nobody → inject lane (context only, no wake;
//     prefixed onto the member's next delivered message, capped);
//   - Steer (host interrupt) → session/steer at the turn boundary.
//
// Delivery semantics follow the durable-inbox verb table studied in
// deepseek-harness; the queue is
// dispatcher-side because ZCode's legacy session/send refuses busy
// turns — and durable on disk
// (~/.niuma/inbox/<name>.json, one file per member with a
// lane per project, v2 P3-b) because a resumed session
// discards its own pending inputs (M1 spike: resume.ts drops them).
package dispatch

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/media"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/wire"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// Bridge is the zcode surface the dispatcher drives (satisfied by
// *zcode.Client; faked in tests).
type Bridge interface {
	ListSessions(ctx context.Context) ([]zcode.SessionInfo, error)
	CreateSession(ctx context.Context, workspace, mode string) (string, error)
	SetModel(ctx context.Context, sessionID string, sel zcode.ModelSelection) error
	ResumeSession(ctx context.Context, sessionID string) (*zcode.ResumeResult, error)
	Subscribe(ctx context.Context, sessionID string) (*zcode.SubscribeResult, error)
	Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error)
	Steer(ctx context.Context, sessionID, content string) error
	// ProbeSession keeps a busy member's session out of the app-server's
	// idle reaper and reports cold sessions (-32004) — the watchdog's
	// one-RPC keepalive+liveness pair (zcode.Client.ProbeSession).
	ProbeSession(ctx context.Context, sessionID string) error
	SetHooks(hooks zcode.Hooks)
	SetReverse(fn func(method string, params map[string]any) (any, error))
}

// attBridge is Bridge's optional attachments extension (输入图片):
// session/send's attachments param, which *zcode.Client carries as
// SendAttached. Deliver type-asserts — a bridge without it (the
// pre-image fakes) degrades to the text-only Send, so the interface
// stays byte-compatible for every existing fake.
type attBridge interface {
	SendAttached(ctx context.Context, sessionID, content, inputID string, deny []string, atts []zcode.Attachment) (*zcode.SendAck, error)
}

// member is one dispatcher-managed member: a room seat plus the ZCode
// session the dispatcher drives for it.
// memberLanes groups the delivery-lane state (lanes.go): the parked
// next-turn queue, the background lane, and the kick/fold/teach
// bookkeeping around them. All under d.mu.
type memberLanes struct {
	// queue is the next-turn lane — FIFO, one laneEntry per turn (the
	// entry carries its stamp, sender, read/ack cargo and pictures).
	// inject is the context-only lane, prefixed onto the next send.
	queue  []laneEntry
	inject []injectEntry

	// injDropped 计上次投递以来让位的背景条数（InjectCap 截断＋回看
	// 预算裁剪都计）——下次投递在背景块头上留一行计数与房史指针再清
	// 零；跨重启随车道落盘（老文件读 0，语义不变）。
	injDropped int

	// draining marks a lane kick in flight (onSay's async delivery
	// path): the head line has popped and its Send is running on the
	// kick's own goroutine. Later kicks see it and leave the lane
	// alone — the queue stays the member's FIFO, a second line parks
	// behind the in-flight one instead of racing it.
	draining bool
	// kickAfter 记一记被 draining 吞掉的叫醒（见 laneKick）：kick 收尾
	// 时替它补一脚，续投意图不随竞态蒸发。
	kickAfter bool

	// kickTimer is L1's pending deferred kick: armed when a fresh head
	// is younger than the coalesce window, fired once into laneKick.
	// nil = no kick pending. Its firing re-checks every laneKick guard
	// (stopped, membership, draining, Running), so a stale timer is
	// always a no-op, never a hazard.
	kickTimer *time.Timer

	// inboxSeq 是本成员车道快照的单调序号（d.mu 下递增）：投递路径的
	// ordered 落盘改为锁外入队（prepareInboxSaveOrdered/enqueueInboxSave），
	// 通道位置不再等于快照新旧——flusher 按序号取最新，乱序迟到的旧
	// 快照当场丢弃（已捎带的线不因迟到旧档复活，不变量①不变）。
	inboxSeq uint64

	// backNoted is L4 背压's watermark: the lane depth at the last
	// congestion note to this member's senders (0 = healthy since).
	// Re-noting wants depth ≥ watermark+4, so a steadily deepening
	// lane speaks up at 4, 8, 12… without spamming on every arrival.
	backNoted int

	// ackTaught counts this member's room-cargo deliveries (L5 会话闩):
	// the 收到-discipline footer rides the first few and then a
	// periodic refresher, not every delivery — birth already taught the
	// protocol (joinprompt). Guarded by mu.
	ackTaught int
}

// memberCargo groups the live turn's receipt cargo: the arm-before-send
// slots (the read/ack seqs the in-flight turn owns, its auto-quote
// arm), and the barge-in deliveries queued behind a live turn (each
// pendCargo entry carries its own drop token). All under d.mu.
type memberCargo struct {
	// readSeqs is the live turn's read cargo (飞书式已读的终点纪律):
	// the say seqs the turn carries — armed when a delivery's Send
	// acks, flushed to the hub's read ledger only at the turn's
	// terminal. An input accepted into the session is 送达, not 已读.
	readSeqs []int64
	// turnAck/turnAt keep the delivery's addressed seqs and original
	// lane stamp for the terminal's auto-quote (a stale single-address
	// reply prefixes 引用 #N).
	turnAck []int64
	turnAt  int64
	// ackSeqs is the live turn's 收到 cargo: the ADDRESSED say seqs a
	// bare 「收到」 reply converts into receipts (never a new chat line
	// — the reply-storm's engine stays off).
	ackSeqs []int64
	// pend is the 待晋升货单 (v2.14 撞车让位): barge-in deliveries
	// (巡报/日报/加编通知/会议注入…不带车道守卫，Running 成员照发)
	// whose input queues at the app-server behind the live turn. Their
	// cargo cannot arm the slot (it would launder the live turn's
	// unsettled cargo — the 「回话了还挂未读」 root cause); the
	// terminal's takeReadCargo promotes the head. In-memory only:
	// restarts evaporate it, the cargo slots' own discipline.
	pend []pendCargo
}

// memberFabric groups the capability assembly state: the composed
// fabric (immutable once set, swapped wholesale — readers may follow
// the pointer outside the lock; nil = no assembly layer, the v1 path),
// the requested model tier, and what the session actually runs on
// ("" = unknown/process default). All under d.mu.
type memberFabric struct {
	fabric           *capability.EffectiveFabric
	skillKeys        []string
	mcpKeys          []string
	pendingCap       string // 【技能更新】 header to prepend onto the next deliver
	wantModel        *capability.ModelTier
	appliedModel     string
	appliedReasoning string
}

// memberWatch groups the turn-health bookkeeping: the watchdog's
// running-stretch state, the resume/ack idempence marks, the
// pending-reply latch, the compact cooldown, and the failure-explainer
// throttle. resumeAcked/pendingReply keep their read-loop-goroutine
// discipline (touched only there — same rule, now written down at the
// fields). All under d.mu.
type memberWatch struct {
	// runningSince stamps the current Running stretch's start (zero
	// while idle); stallNoted caps the 「长回合」 room note at one per
	// stretch — a nagging clock must not become the noise it watches
	// for.
	runningSince time.Time
	stallNoted   bool
	// resumeAcked (r_20 t_195): 确认行只发一次——同会话第二次「已读
	// 要点」直接吞（协议幂等），根治重复回执。仅读循环 goroutine
	// 触碰。
	resumeAcked bool
	// pendingReply（复读根治）：本回合最近一条请求边界帧的收尾话——
	// 边界帧只暂存不镜像（真终点另有 turn.completed，其 response 缺形
	// 时以它兜底）。仅读循环 goroutine 触碰（同 resumeAcked 纪律），
	// turn.started 时作废。
	pendingReply string
	// compactAt/compacting（P0 会话折叠，compact.go）：上次折叠的冷却
	// 锚＋「当前 Running 的这一回合是折叠回合」标记——折叠终点的摘要
	// 不镜像进房，onEvent 按标记吞掉并翻回 Idle。
	compactAt  time.Time
	compacting bool
	// failExpAt/failExpText（failexplain.go）：失败补遗的节流锚与
	// 正文——同一失败的补遗只发一次，除非它换了说法。
	failExpAt   time.Time
	failExpText string
}

// member is one dispatched AI employee: their seat in the room, the
// two delivery lanes, the live turn's cargo, the capability assembly,
// and the watch bookkeeping — each cluster a named sub-struct so the
// 39-field sprawl reads as five facts instead of one bag. All state
// under d.mu unless a field says otherwise.
type member struct {
	name      string
	role      string
	sessionID string
	seat      *chat.Client
	status    string // idle | running (the six-value session status collapsed)

	lanes memberLanes
	cargo memberCargo
	fab   memberFabric
	watch memberWatch
}

// pendCargo is one barge-in delivery's read/ack cargo awaiting its own
// turn (member.cargo.pend — see the field's comment for the full story).
type pendCargo struct {
	token    int64   // drop-by-token on the failure paths (re-park = never shipped)
	readSeqs []int64 // the delivery's read cargo (its turn's terminal marks these)
	turnAck  []int64 // the delivery's addressed seqs (acks + quote arm)
	at       int64   // the delivery's lane stamp (queuedAt discipline)
}

// Config shapes the dispatcher. Workspace is where member sessions
// live; Model/Reasoning select what born sessions run on (the M1
// spike G birth formula: create WITHOUT a model, then SetModel —
// passing model inside create trips a CLI persistence bug). Perm is
// the reverse-request policy (PermAsk default).
type Config struct {
	Workspace string
	Model     string // e.g. "GLM-5.3"; empty = no explicit selection
	Reasoning string // e.g. "high"; empty = omit
	Perm      string // PermAsk | PermAllow | PermDeny; "" = PermAsk
	// InboxDir overrides the durable-lane directory ("" = the v2 root's
	// inbox/ under the home dir); tests point it at a temp dir.
	InboxDir string

	// CtxGauge resolves the members' live context gauges for the P0
	// fold pass (nil = the zcode ledger's one-pass read). Tests inject
	// — the fold threshold reads each member's ctx_estimate from here.
	CtxGauge func(ids []string) (map[string]zcode.SessionGauge, error)

	// PauseProbe reads the room's pause state live (nil = never
	// paused). While paused EVERYTHING the dispatcher could start is
	// held: lane kicks and direct delivers park instead of sending (a
	// turn already in flight finishes — its terminal's next kick is the
	// one that waits), the patrol/daily/meeting/watchdog clocks skip
	// their beats, and a live meeting's agenda windows stop advancing.
	// Resume() (the pause write face's un-pause leg) re-kicks every
	// lane, so parked lines flow again in FIFO order. The Fleet wires
	// this to the staffing settings' per-project Paused switch.
	PauseProbe func() bool

	// GitOffProbe reads the project's git master switch live (nil =
	// git management on)——两级门顶层（Project.GitDisabled，POST
	// /p/{key}/git/settings 写面）。分支隔离的出生建树用它把关：总开
	// 关关着的项目，带分支座位的成员出生被拒（绝不静默落回共享主树
	// ——「以为隔离其实共享」是最坏状态）。The Fleet wires this to
	// the project registry's GitDisabled bit.
	GitOffProbe func() bool

	// AutoSeatProbe reads the project's 自动驻分支 plan live（on, base）：
	// on＝成员出生时座位若空，自动各驻专属枝 wt/<人名>（base 为切出
	// 基线，空＝main）；off/nil 照旧共享主树。The Fleet wires this to
	// the project registry's GitPlan.AutoSeat bit.
	AutoSeatProbe func() (bool, string)

	// MediaStore resolves say-line image references back to disk paths
	// for session/send attachments (输入图片). Nil (or an unresolvable
	// id) degrades the injection to its textual note — the line itself
	// never fails over a picture.
	MediaStore *media.Store

	// ProjectKey + StaffStore turn the dispatcher into one project's
	// room dispatcher (v2 P3-b): the staffing rows of THIS project
	// become the member registry — adopt/birth/bind read and write
	// staffing.session_id (the single binding truth; agents.Config
	// stops carrying sessions), Birth creates sessions in
	// Config.Workspace (the project's workspace, set by the Fleet),
	// and a lost seat offboards the row. Both zero/nil = the v1
	// single-room shape, byte-for-byte (agents-driven binding).
	ProjectKey string
	StaffStore *staffing.Store

	// ProjectName is the project's display name (the sidebar title's
	// 【项目】 slot; "" falls back to ProjectKey). Set by the Fleet from
	// the project store — the single source.
	ProjectName string

	// IndexWorkspace is the workspace the ZCode desktop task-index row
	// registers under (v2 P7). The desktop's sidebar only lists rows
	// whose workspace_key matches a workspace the app has open, so the
	// row must live where the operator actually watches the office —
	// the Fleet stamps the HOST workspace (the process cwd, the lobby's
	// own birthplace) here for every project instance. "" = Workspace
	// (the v1 single-room shape, unchanged).
	IndexWorkspace string

	// WorktreeRoot is the root of the per-member isolated worktrees
	// (v2.7.1 分支隔离), conventionally ~/.niuma/wt — the Fleet injects
	// it from projects.RootDir(). A seat with a Branch set refuses
	// birth honestly when this is empty ( NEVER silently fall back to
	// the shared workspace — 以为隔离其实共享是最坏状态).
	WorktreeRoot string

	// RankOf reads a member's rank for the sidebar title's Lv.N slot
	// (main wires the task engine's registry; nil = the slot is
	// omitted).
	RankOf func(name string) int

	// Capability assembly injection. Nil Library = v1 behavior,
	// byte-for-byte. The Library resolves skill/MCP keys; missing keys
	// degrade to nothing. StaffingAssembly returns a member's
	// seat-level skill and MCP keys (nil/empty skills = no override,
	// the profile default stands — Q18; the two lists follow the rule
	// independently).
	Library          *capability.Store
	StaffingAssembly func(person string) (skills, mcps []string)

	// StaffingModel reads a member's seat-level model tier (the 招牛马/
	// 改档 write face lands in staffing.Entry.Model/Reasoning; model
	// empty = no pick, the pack slot ⊕ process default chain decides —
	// reasoning only rides with a pick, the tier is one atomic triple).
	// Fleet wires it per-project beside StaffingPacks.
	StaffingModel func(person string) (model, reasoning string)

	// ProviderResolve overrides the provider lookup the assembly's
	// model slot uses (nil = zcode.ResolveModelProvider, the ordered
	// pick-list walk — account faces first, then ~/.zcode/v2/
	// provider_config.json's personal rules; tests inject a fake so
	// model switches stay hermetic). The process-level cfg.Model birth
	// path keeps its own boot-time resolution.
	ProviderResolve func(model string) (string, bool)

	// SendWait is deliver's injection-ack wait: how long session/send
	// waits for its ack before the 结果未知 drop (see DefaultSendWait).
	// 0 = default. The settings card retunes it live via
	// Fleet.SetSendWait — the fleet's base config carries the tuned
	// value onto dispatchers born after the change (Attach/EnsureRoom).
	SendWait time.Duration

	// Patrol clock (v2 P4-b, orchestration §2.5). PatrolEvery sets the
	// tick interval: 0 = DefaultPatrolEvery, negative = no patrol
	// goroutine. PatrolTick, when non-nil, REPLACES the wall-clock
	// timer — every value received on it fires one patrol — the
	// injectable fake clock (tests drive ticks deterministically; the
	// channel is read-only by contract, closing it parks the loop
	// until Stop). SetRank is the orchestrator birth's Lv.8 stamp
	// (nil = no rank side effect; main wires it to the engine).
	PatrolEvery time.Duration
	PatrolTick  <-chan time.Time
	SetRank     func(name string, lv int)

	// ReseatPatience bounds how long the re-seat loop waits out a
	// same-token seat lease (the member's one-shot CLI command)
	// before deciding the holder outstayed the lease and treating
	// the seat as gone. 0 = DefaultReseatPatience; tests tighten it.
	ReseatPatience time.Duration

	// Daily-report clock (v2 P5-a): every project's orchestrator gets
	// one 【日报】 injection per day at a fixed local time — the same
	// tick-goroutine + deliver mechanism as the patrol clock, silent
	// by contract (the injection rides session/send; the orchestrator's
	// summary broadcast is their own action). DailyAt is the local
	// "HH:MM" ("off"/"-" disables; "" = the 21:00 default — main wires
	// DailyAtFromEnv()). DailyTick, when non-nil, REPLACES the
	// wall-clock timer wholesale: every value received fires one
	// report (the fake-clock injection point).
	DailyAt   string
	DailyTick <-chan time.Time

	// Requirement-review meeting clock (需求评审会议). Meetings is the
	// occupancy ledger — nil disables the whole feature. Reqs and Tasks
	// feed the trigger (open requirements, members' doing/todo loads).
	// Plans is the pending-proposal slot: a proposal awaiting the host
	// holds the clock off convening (the valve gate — a review's own
	// product is a proposal, and the single slot means a new meeting
	// could only displace the one waiting; nil = no gate, the unwired
	// shape). MeetingEvery tunes the check beat (0 = the default,
	// negative disables); MeetingTick REPLACES the wall-clock timer
	// wholesale (the fake-clock injection point, the patrol/daily
	// contract).
	Meetings meeting.Store
	Reqs     requirements.Store
	Tasks    *tasks.Engine
	Plans    plan.Store
	// Subject is the storage namespace the dispatcher's store calls
	// carry ("" single-user; multi-user boot binds the studio owner
	// account name — the dispatcher acts FOR the studio, so it shares
	// the studio's namespace).
	Subject      string
	MeetingEvery time.Duration
	MeetingTick  <-chan time.Time

	// MeetingDiscuss/MeetingClose override the live agenda's window
	// lengths (0 or negative = the package defaults). Tests shrink
	// them to milliseconds so a window can fire naturally against
	// the recording bridge.
	MeetingDiscuss time.Duration
	MeetingClose   time.Duration

	// Keepalive watchdog (忙成员保活＋静默死亡看护). WatchdogEvery sets
	// the probe beat (0 = DefaultWatchdogEvery, negative = off);
	// WatchdogTick REPLACES the wall-clock timer wholesale (the
	// patrol/daily fake-clock contract). TurnStall bounds how long a
	// Running stretch may stay event-silent before the room gets its
	// one-time 「长回合」 note (0 = DefaultTurnStall, negative = no
	// stall note at all — the keepalive itself still runs).
	WatchdogEvery time.Duration
	WatchdogTick  <-chan time.Time
	TurnStall     time.Duration

	// StaleDelay is how long a queue entry may wait before its deliver
	// counts as LATE (0 = DefaultStaleDelay, negative = never late —
	// stamps and auto-quotes off, the pre-upgrade behavior). A late
	// deliver ships with a 排队说明 header (original send time + wait
	// + judge-it-by-now advice) and the turn's reply auto-quotes the
	// addressed line (引用 #N) — a 20-minute-old 「稍等」 must read as
	// stale context to the member and as a visible quote to the room.
	StaleDelay time.Duration

	// CoalesceWindow (L1 汇聚窗, v2.11): a freshly parked head line
	// waits out this window before its kick so near-simultaneous
	// mentions collect into one turn (0 = DefaultCoalesceWindow,
	// negative = off, the pre-v2.11 immediate kick).
	CoalesceWindow time.Duration

	// SenderFoldWindow (L2 同发送方连发合并, v2.11): consecutive lines
	// from the SAME sender parked closer than this merge into one lane
	// entry (0 = DefaultSenderFoldWindow, negative = off).
	SenderFoldWindow time.Duration

	// NoticeOf reads a project's current group announcement (飞书式群
	// 公告) for the birth injection: a fresh joiner meets the room's
	// notice before anything else (the Feishu new-member rule). Nil =
	// no slot, byte-for-byte the pre-notice birth.
	NoticeOf func(project string) (content, by string, ok bool)

	// RebuildFire routes a PASSED rebuild vote to the studio's
	// recompile-restart face (AI 重编译投票): main wires the server's
	// MemberRebuild — the same compile/swap/handoff core the owner's
	// button rides, announced under the tally. Async by contract (the
	// caller launches it on its own goroutine; the compile runs
	// seconds-to-minutes). Nil = votes can never fire (the pass still
	// lands, with an honest unwired line).
	RebuildFire func(project, by, tally string) error

	// Docs is the room's document store (v2): the orchestrator's birth
	// self-registers its post row into the project's establishment
	// table (p/<key>/establishment — a missing table is created under
	// the frozen contract, a broken one is refused aloud), putting the
	// seat under the keeper's watch. Nil = no registration; the birth
	// itself is unaffected either way.
	Docs *kb.DocsStore
}

// Dispatcher routes room lines into member sessions and mirrors turn
// replies back. Safe for concurrent use.
type Dispatcher struct {
	hub    *chat.Hub
	store  *agents.Store
	bridge Bridge
	cfg    Config

	// projectKey is the room this dispatcher serves (Config.ProjectKey,
	// defaulted to the lobby) — the staffing rows it consumes and the
	// inbox lane it writes both key on it. staff is nil in the v1
	// single-room shape (agents-driven binding).
	projectKey string
	staff      *staffing.Store

	// providerID is the resolved personal provider for cfg.Model
	// (empty = model selection unavailable; births fall back to the
	// CLI default and a system notice says so once).
	providerID    string
	providerNoted bool

	mu      sync.Mutex
	members map[string]*member

	// resumePulse（房间暂停）: Resume() 的脉冲——每拍放行一个等待在
	// waitUnpaused 上的议程窗（或被下一个等待者取走），车道重踢不靠它
	//（Resume 直接逐个 laneKick）。带 1 缓冲，无等待者的拍作废即可。
	resumePulse chan struct{}

	// apYieldUntil (r_17 让拍): the meeting clock holds off convening
	// until this moment after an autopilot injection reached this
	// room's orchestrator — see AutoInjectMeetingYield. Guarded by mu.
	apYieldUntil time.Time

	// registerIndex is the sidebar-row upsert as an injectable seam:
	// nil means the package-level taskIndexRegister (python3 against
	// the desktop's own sqlite — nothing a unit test should touch),
	// tests swap it to capture the row body.
	registerIndex func(zcode.TaskIndexEntry) error

	// stampIndex is the sidebar unread stamp's twin seam: nil means the
	// package-level taskIndexUnread, tests neutralize it to keep
	// python3 off the desktop's live database.
	stampIndex func(sessionID string) error

	// archiveIndex is the sidebar-row fold's seam (registerIndex's
	// third twin): nil means the real zcode.ArchiveTaskIndexForce.
	archiveIndex func(sessionID string) error

	// asks is the room's pending AskUserQuestion reverse requests
	// (向房主提问, keyed by the minted qid). The onReverse goroutine
	// parks on pendingAsk.ch until the owner answers (Dispatcher.
	// Answer, routed from the workbench's answer verb through the
	// fleet) or askWindow expires — first-come deletes the entry, so
	// a late second click finds nothing to land on.
	asks map[string]*pendingAsk

	// permParks dedupes requestPermission re-announcements the same
	// way asks' signature does: ZCode ≥3.14 re-sends an unanswered
	// request under fresh ids, and the parking permission branch
	// would stack one room line per re-send. Keyed by session+tool;
	// the value closes when the owning park returns.
	permParks map[string]chan struct{}

	// autoAskSeen folds the 自动推进 ask line (r_19): the same question
	// (same askSignature) re-arriving inside autoAskFold is a re-ask,
	// not a new question — the room line and the term pair land once
	// (the line carries the question digest, so the fold loses
	// nothing). Signature → unix-seconds stamp, lazily pruned.
	// Guarded by mu.
	autoAskSeen map[string]int64

	// agenda is the live meeting's window control channel (dispatch/
	// meeting.go): non-nil while runAgenda is parked in one window —
	// the chair's MeetingCtl (meeting end / meeting extend) rides it,
	// nil between windows and after the close. Guarded by mu.
	agenda chan agendaReq

	// vote is the room's open AI-rebuild ballot (dispatch/vote.go):
	// nil = no vote in flight. Guarded by mu.
	vote *voteBallot

	// cargoTok mints pendCargo tokens (unique per armed barge-in, so a
	// re-parking delivery can drop exactly its own pending entry).
	// Guarded by mu.
	cargoTok int64

	// sendWait is deliver's injection-ack wait (Config.SendWait
	// normalized at Start, retuned live by SetSendWait — the settings
	// card's write face can land mid-flight, so the hot path reads it
	// atomically without touching mu).
	sendWait atomic.Int64

	// trace is the 工作过程 tap's state (dispatch/trace.go): pending
	// flush lanes + subagent attribution tables. All under mu; nil
	// until the first event lazy-builds it (a bridge-less embed never
	// taps anything).
	trace *traceTap

	// stallPrev（r_14 微迭代）: 上一轮巡检停滞清单里出现过的任务 ID——
	// 本轮仍在清单即标「（再）」（定稿纪律：编排者不得连续两轮催同一
	// 单）。每轮 PatrolNow 整体重记。Guarded by mu.
	stallPrev map[string]bool
	// resumeMark（r_14 微迭代）: 任务 ID → 自动驾驶续命注入送达的时刻。
	// 停滞清单共享这份记忆（15min 窗）标「（已续命）」防二次催；懒清理。
	// Guarded by mu.
	resumeMark map[string]int64

	// laneStats（v2.11 L6 度量闭环）: the delivery lane's three
	// distributions — enqueue→ship wait seconds, queue depth at
	// enqueue, folded-per-delivery — as bounded rings (oldest evicted),
	// plus the overflow-dropped total. Read by LaneStats for the
	// /dispatch/lanestats face; the knobs above (CoalesceWindow,
	// SenderFoldWindow, the fold ladder) get tuned against these
	// numbers, not by feel. All under mu.
	laneWaits   []int64
	laneDepths  []int
	laneFolds   []int
	laneDropped int64

	wg sync.WaitGroup
	// explainWG tracks only the failexplain goroutines (drainExplains'
	// wait set) — the seam's restore point; d.wg additionally covers
	// patrol/watchdog/meeting long-runners Stop must gather.
	explainWG sync.WaitGroup
	stop      chan struct{}

	// inboxSave queues inbox-file writes for the ordered flusher
	// (lanes.go): the lane paths marshal under mu and enqueue — the
	// read-modify-write + fsync left the critical section. The
	// consume-before-send ordering rides per-job done tokens
	// (prepareInboxSaveOrdered + enqueueInboxSave), waited outside mu. Nil until Start (zero-value
	// test stubs fall back to the synchronous write).
	//
	// The FIELD itself is guarded by inboxSwapMu (not d.mu): the flusher
	// must never take d.mu (FlushInbox holds it across blocking enqueues
	// — a flusher waiting on mu there is the deadlock the "never" rule
	// exists for), yet tests swap the channel to stall the flusher and
	// the flusher snapshots it per batch. Lock order is one-way:
	// d.mu → inboxSwapMu, never reversed.
	inboxSave   chan inboxJob
	inboxSwapMu sync.Mutex
	// inboxDirty: members whose last snapshot could not enqueue (queue
	// full) — swept by the next save or FlushInbox. Under mu.
	inboxDirty map[string]bool
	// inboxLanded: the last snapshot seq per member that actually
	// reached disk — the flusher's CROSS-BATCH memory. The ordered save
	// enqueues outside d.mu (position in the channel no longer orders
	// snapshots), so a straggler older than what already landed must be
	// dropped against THIS, not just against its batch mates. Under
	// inboxLandedMu (flusher-side only; never taken under d.mu).
	inboxLanded   map[string]uint64
	inboxLandedMu sync.Mutex
}

// Start wires the dispatcher: hub observer, bridge hooks + reverse
// policy, adoption of every stored binding (seat + resume +
// subscribe + lane restore). A nil bridge (zcode unavailable) leaves
// the dispatcher inert — the room runs on without dispatch, exactly
// like --no-watch.
func Start(hub *chat.Hub, store *agents.Store, bridge Bridge, cfg Config) *Dispatcher {
	if cfg.Perm == "" {
		cfg.Perm = PermAsk
	}
	if cfg.SendWait <= 0 {
		cfg.SendWait = DefaultSendWait
	}
	if cfg.ProjectKey == "" {
		cfg.ProjectKey = chat.LobbyKey
	}
	d := &Dispatcher{
		hub: hub, store: store, bridge: bridge, cfg: cfg,
		projectKey: cfg.ProjectKey, staff: cfg.StaffStore,
		members:     map[string]*member{},
		asks:        map[string]*pendingAsk{},
		permParks:   map[string]chan struct{}{},
		autoAskSeen: map[string]int64{},
		stallPrev:   map[string]bool{},
		resumeMark:  map[string]int64{},
		stop:        make(chan struct{}),

		resumePulse: make(chan struct{}, 1),
	}
	d.sendWait.Store(int64(cfg.SendWait))
	// the inbox flusher rides Start unconditionally (lane parking needs
	// no bridge); zero-value stubs built without Start keep the
	// synchronous fallback in queueInboxSaveLocked.
	d.inboxSave = make(chan inboxJob, 128)
	d.inboxDirty = map[string]bool{}
	go d.inboxFlushLoop()
	if bridge == nil {
		return d
	}
	if cfg.Model != "" {
		// ProviderResolve is the same hermetic seam applyModel honors —
		// the constructor must not resolve the process default through
		// the real ~/.zcode config behind a test's back.
		resolve := cfg.ProviderResolve
		if resolve == nil {
			resolve = zcode.ResolveModelProvider
		}
		if id, _ := resolve(cfg.Model); id != "" {
			d.providerID = id
		}
	}
	// A shared (fleet) bridge fans hooks out to every dispatcher; a
	// raw bridge keeps the v1 single-owner slots.
	if sb, ok := bridge.(*sharedBridge); ok {
		sb.add(d)
	} else {
		bridge.SetHooks(zcode.Hooks{
			Session: d.onEvent,
			State:   d.onState,
		})
		bridge.SetReverse(d.onReverse)
	}
	hub.OnSay(d.onSay)
	// Nobody is parked on this hub's open questions anymore (a fresh
	// dispatcher means the old waiter crashed or was swapped out) —
	// close the stale cards before adopting, or their clicks would
	// land on a card with no one behind it.
	hub.ExpireAllQuestions()
	d.adopt()
	d.startPatrol()
	d.startDaily()
	d.startMeetingClock()
	d.startWatchdog()
	d.startTraceLoop()
	return d
}

// SetSendWait retunes the injection-ack wait live (the settings card's
// 「消息注入等待」 write face, via Fleet.SetSendWait): the next deliver
// reads the new value — a send already in flight keeps the window it
// started with. Non-positive falls back to the default.
func (d *Dispatcher) SetSendWait(dt time.Duration) {
	if dt <= 0 {
		dt = DefaultSendWait
	}
	d.sendWait.Store(int64(dt))
}

// SendWait reports the current injection-ack wait (tests assert the
// fleet wiring; deliver is the only production reader).
func (d *Dispatcher) SendWait() time.Duration {
	return time.Duration(d.sendWait.Load())
}

// startTraceLoop runs the 工作过程 tap's 500ms flush heartbeat: text
// tails that never trip the urgent line land on the hub ring here, so
// a long quiet think still surfaces within half a second.
func (d *Dispatcher) startTraceLoop() {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		tick := time.NewTicker(traceFlushEvery)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				util.Guard("dispatch: trace flush", d.traceFlushTick)
			case <-d.stop:
				util.Guard("dispatch: trace flush", d.traceFlushTick) // 残留尾巴一并落水，别带进坟墓
				return
			}
		}
	}()
}

// Stop tears the dispatcher down (tests, graceful shutdown).
func (d *Dispatcher) Stop() {
	select {
	case <-d.stop:
		return
	default:
	}
	close(d.stop)
	d.wg.Wait()
	// 告别排水：在途的 inbox 写先落地，收摊后的文件才是下一轮生命的
	// 读档真相（Stop 后偶发的残余入队由常驻 flusher 照写，无害）。
	d.FlushInbox()
	// 开着的重编译投票收摊（AfterFunc 的回调先查 d.stop，这里是双保险：
	// 定时器本身也不再走）。
	d.mu.Lock()
	if d.vote != nil {
		voteStopLocked(d.vote)
	}
	d.vote = nil
	d.mu.Unlock()
	// A live meeting dies with its dispatcher (the agenda goroutine
	// parked on d.stop): release the room quietly — no broadcast, the
	// process this room belonged to is going away.
	if d.cfg.Meetings != nil {
		_, _ = d.cfg.Meetings.End(d.projectKey)
	}
}

// reseatPoll/reseatPatience bound the re-seat loop: one-shot CLI
// commands hold the seat for seconds, so a 200ms poll re-takes it
// almost the moment the command's connection leaves; a holder that
// outstays the patience window is either our own long-lived guard
// (same credential — supersede it back; its yield window stands it
// down) or a stranger (give the seat up).
const (
	reseatPoll = 200 * time.Millisecond
	// DefaultReseatPatience: one-shot CLI commands hold the seat for
	// seconds; a holder outstaying half a minute is a guard (same
	// credential — superseded back) or a stranger (seat given up).
	DefaultReseatPatience = 30 * time.Second
)

// DefaultSendWait is deliver's injection-ack wait: how long a send
// waits for session/send's ack before declaring the result unknown and
// dropping the line (the room owner's 「注入超时」 notice). The const is
// the boot default — the settings card retunes it live through the
// server's /dispatch-wait face → Fleet.SetSendWait.
const DefaultSendWait = 5 * time.Minute

// stopped reports whether the dispatcher is shutting down (poll-safe
// for the brief waits in sayThrough).
func (d *Dispatcher) stopped() bool {
	select {
	case <-d.stop:
		return true
	default:
		return false
	}
}

// --- the room pause (房间暂停) ----------------------------------------
//
// One switch freezes the whole room from first principles: while
// paused, nothing the dispatcher could START starts — no lane kicks,
// no direct delivers (they park with their lane stamp, so staleness
// headers stay honest when they finally ship), no patrol/daily/
// meeting/watchdog beats, and a live meeting's agenda windows hold
// their timers. A turn already in flight finishes on its own (its
// reply lands; the terminal's next kick is the one that waits) —
// killing a model call mid-wire saves nothing and loses the turn.
// Resume() is the un-pause leg: pulse the waiting agenda windows and
// re-kick every lane, FIFO order intact.

func (d *Dispatcher) pausedNow() bool {
	return d.cfg.PauseProbe != nil && d.cfg.PauseProbe()
}

// waitUnpaused blocks while the room is paused, returning false when
// the dispatcher stopped instead (the caller folds into its stop
// path). Each Resume() pulse lets one waiter re-check; a waiter that
// finds itself still paused (a racing re-pause) loops back to sleep.
func (d *Dispatcher) waitUnpaused() bool {
	for d.pausedNow() {
		select {
		case <-d.stop:
			return false
		case <-d.resumePulse:
		}
	}
	return true
}

// Resume re-kicks every member's lane after an un-pause (idempotent:
// guards re-decide everything, an idle lane is a no-op) and pulses
// the agenda windows waiting out the pause. The pause write face
// calls it on every un-pause; boot-time resumes ride the same lanes'
// natural kicks.
func (d *Dispatcher) Resume() {
	if d.stopped() {
		return
	}
	select {
	case d.resumePulse <- struct{}{}:
	default: // a pulse is already pending — waiters share it
	}
	d.mu.Lock()
	members := make([]*member, 0, len(d.members))
	for _, m := range d.members {
		members = append(members, m)
	}
	d.mu.Unlock()
	for _, m := range members {
		d.laneKick(m)
	}
}

// The desktop sidebar's four real side effects (row upsert / row fold
// / unread dot / reveal deep link) ride these package-level seams:
// the dispatch test binary's TestMain stubs all four, so a birth in a
// test that forgot its per-dispatcher registerIndex seam can never
// touch the operator's LIVE ZCode database — such births used to pin
// phantom members (【proj-p1】小鹿-人事 …) into the real sidebar,
// 1459 rows of 实录. Production keeps the real verbs.
var (
	taskIndexRegister = zcode.RegisterTaskIndex
	taskIndexArchive  = zcode.ArchiveTaskIndexForce
	taskIndexUnread   = zcode.MarkTaskUnread
	taskIndexReveal   = zcode.RevealWorkspaceForce
)

// Status is one member's dispatcher view (the /dispatch surface).
type Status struct {
	Name      string `json:"name"`
	Role      string `json:"role,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Status    string `json:"status"`
	Queued    int    `json:"queued"`
	Inject    int    `json:"inject"`
	// Model is what the seat runs on for display: the staffing pick
	// when one is stamped, else the tier the session actually applied
	// ("" = the CLI default). A pick differs from applied only in the
	// mid-turn window before the deferred switch lands.
	Model string `json:"model,omitempty"`
	// Reasoning mirrors Model for the thinking intensity (the CLI's
	// reasoningLevel): the staffing pick's when one is stamped — a
	// reasoning-only pick rides the default-chain model's display —
	// else what the session applied ("" = the model's default tier).
	Reasoning string `json:"reasoning,omitempty"`
}

// Snapshot lists the managed members.
func (d *Dispatcher) Snapshot() []Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Status, 0, len(d.members))
	for _, m := range d.members {
		model, reasoning := m.fab.appliedModel, m.fab.appliedReasoning
		pick, pickReasoning := d.seatModel(m.name)
		if pick != "" {
			model = pick
		}
		if pickReasoning != "" {
			reasoning = pickReasoning
		}
		out = append(out, Status{
			Name: m.name, Role: m.role, SessionID: m.sessionID,
			Status: m.status, Queued: len(m.lanes.queue), Inject: len(m.lanes.inject),
			Model: model, Reasoning: reasoning,
		})
	}
	return out
}

func (d *Dispatcher) lookup(name string) *member {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.members[name]
}

// sessionOf snapshots the member's session id under d.mu — Rebirth
// (gitlink) swaps the field in place, so a bare read off the read
// loop, kick or explain goroutines races that write.
func (d *Dispatcher) sessionOf(m *member) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return m.sessionID
}

// senderFoldMarker is the merge seam's marker line: the supplement's
// own 【办公室消息 header follows verbatim, so attribution is carried
// by the text itself — the system never rewrites a speaker's words.
const senderFoldMarker = "〔同一发言人紧接着补发，与上一条同源——一并阅读〕"

// backpressureAt is L4's first congestion watermark: queue depth from
// which on the senders deserve to hear the lane is backed up (the
// re-note ladder steps by 4 — see member.lanes.backNoted).
const backpressureAt = 4

// ackNoteMark is the 收到-discipline footer's idempotence marker: both
// footer variants carry it, so a re-parked delivery never stacks a
// second copy (deliverQuoted's Contains guard).
const ackNoteMark = "整轮只回"

// LaneStatsSnapshot is the delivery lane's three distributions plus the
// overflow total (L6): bounded rings, oldest evicted, newest last.
// WaitSeconds is per shipped line (the head plus each folded entry);
// QueueDepth is taken at each enqueue; FoldedPerDelivery counts how
// many lines each delivery carried besides its head. The tuning knobs
// (CoalesceWindow, SenderFoldWindow, the fold ladder) answer to these
// numbers — /dispatch/lanestats serves them.
type LaneStatsSnapshot struct {
	WaitSeconds       []int64 `json:"wait_seconds"`
	QueueDepth        []int   `json:"queue_depth_at_enqueue"`
	FoldedPerDelivery []int   `json:"folded_per_delivery"`
	OverflowDropped   int64   `json:"overflow_dropped_total"`
}

// SeatSession is one seated member's identity triple for the fleet's
// context gauge (v2.11 P1): the dispatcher holds the session id the
// CLI ledger keys on. Name-sorted for a deterministic read face.
type SeatSession struct {
	Name    string `json:"name"`
	Session string `json:"session"`
	Status  string `json:"status"`
}

// SeatSessions snapshots the seated members as (name, session, status)
// triples — the fleet's ContextGauges feeds these into the ledger.
func (d *Dispatcher) SeatSessions() []SeatSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]SeatSession, 0, len(d.members))
	for _, m := range d.members {
		out = append(out, SeatSession{Name: m.name, Session: m.sessionID, Status: m.status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// --- capability assembly (v2 CAP-M2) ----------------------------------
//
// The per-slot effect matrix (pm-capability §3): prompt/manual slots
// land on the next injection; the model slot switches immediately
// when idle, at turn end when running; the tool denylist rides the
// next send; the perm slot is immediate.

// --- patrol clock (v2 P4-b, orchestration §2.5) -----------------------
//
// One tick goroutine per project dispatcher, living and dying with the
// Dispatcher (the StartWatch pattern; the time.AfterFunc precedent is
// the hub's @所有人 roll-call window). Each tick delivers the 【巡检】
// injection to THIS project's orchestrator session — silent by
// contract: the delivery rides session/send (no broadcast, no chat
// history); the orchestrator's ACTIONS then produce task/plan frames
// through the usual paths. The trigger itself never touches the room.

// DefaultPatrolEvery is the patrol interval (30 minutes, PRD §4.2).
const DefaultPatrolEvery = 30 * time.Minute

// --- keepalive watchdog (忙成员保活＋静默死亡看护) -----------------------
//
// The room's turn terminals are push-only: onEvent's turn-done
// envelope and onState's failure patch, both riding the child's event
// stream. When a member's session dies WITHOUT either signal — the
// app-server's resident pool reaps it at 10 minutes of protocol-idle
// (a turn that only emits subagent traffic never touches its parent
// session's record; the 美术设计师's acceptance loop measured exactly
// that), or the child is swapped mid-turn — the member stays Working
// forever, mute, and every later @mention rides a dead session's
// queue. The watchdog adds the pull half:
//
//   - every beat, each Running member gets one ProbeSession RPC — a
//     touch that resets the reaper's idle clock (keepalive), whose
//     -32004 answer names a cold session (liveness);
//   - a cold session under a Running member is RESURRECTED in place:
//     the reactivate ladder (resume + subscribe — memory intact, the
//     session store outlives the runtime) plus one continuation
//     injection, so the member picks the interrupted work back up and
//     the room hears about it instead of silence;
//   - a Running stretch silent past TurnStall gets ONE room note
//     (「长回合」) — no auto-action: a long art turn is legitimate,
//     and the note hands the judgement to the owner. The keepalive
//     keeps the session alive for as long as the turn really runs.

// DefaultWatchdogEvery is the probe beat: comfortably under the
// app-server's 10-minute idle-reap window (resident idleTimeoutMs
// 600000), so one missed beat still leaves a full window of margin.
const DefaultWatchdogEvery = 4 * time.Minute

// DefaultStaleDelay is how long a queue entry may wait before its
// deliver counts as LATE (the 排队说明 header + the reply's auto-quote
// arm): comfortably past a normal turn boundary's tail latency, far
// under the rebuild/interruption stretches that make staleness sting —
// the 20-minute 「稍等」 measured exactly this gap.
const DefaultStaleDelay = time.Minute

// neverLate is the sentinel staleAfter returns for StaleDelay<0 (never
// late): a max-int-family constant keeps every comparison a plain
// greater-than at the call sites; foldAfter passes it through untouched.
const neverLate = 1 << 62

// DefaultTurnStall is how long a Running stretch may stay event-silent
// before the room gets its one-time 「长回合」 note. Generous on
// purpose — subagent-heavy art turns legitimately run tens of minutes.
const DefaultTurnStall = 30 * time.Minute

// --- daily-report clock (v2 P5-a) --------------------------------------
//
// One 【日报】 injection per project per day, at a fixed local time
// (NIUMA_DAILY_AT, default 21:00), delivered to THIS project's
// orchestrator session — the patrol clock's own mechanism (tick
// goroutine + deliver, silent trigger: no broadcast, no history; the
// room hears the orchestrator's summary, not the clock). Killing it is
// one env var: NIUMA_DAILY_AT=off leaves the goroutine unborn (P5's
// "关掉即静默" acceptance).

// --- durable lanes -------------------------------------------------
//
// ~/.niuma/inbox/<name>.json mirrors a member's two lanes
// PER PROJECT: {"<project_key>":{"queue":[...],"inject":[...]}} — one
// file per member however many projects ever held their rows (★B6,
// v2 P3-b), each project's dispatcher owning exactly its own lane.
// The engine discards pending inputs on resume (M1 spike, resume.ts),
// so cross-restart durability is ours to keep: every mutation
// rewrites the file atomically (agents-store style) under a
// process-wide lock (project dispatchers share the file). The v1 flat (pre-rename)
// ~/.desktop_harness_inbox_<name>.json is neither read nor migrated
// (constitution §0-6, zero old-data awareness).

// inboxLanes is one project's lane pair inside a member's inbox file.
// The readq/readInj cargo (飞书式已读) rides beside its lane so a
// restart-resumed queue still marks its messages read when delivered;
// ackq (飞书式收到) rides the queue the same way; imgq (输入图片)
// rides the queue so a parked picture survives restarts — pre-cargo
// files simply omit the fields; queue_from (v2.11 L2) rides the queue
// so a restarted lane still knows its tail entry's sender (missing =
// "" = never merge).
type inboxLanes struct {
	Queue      []string       `json:"queue"`
	ReadQueue  [][]int64      `json:"read_queue,omitempty"`
	AckQueue   [][]int64      `json:"ack_queue,omitempty"`
	QueueAt    []int64        `json:"queue_at,omitempty"`
	QueueFrom  []string       `json:"queue_from,omitempty"`
	ImgQueue   [][]wire.Image `json:"img_queue,omitempty"`
	Inject     []string       `json:"inject"`
	ReadInject []int64        `json:"read_inject,omitempty"`
	InjDropped int            `json:"inj_dropped,omitempty"` // 让位背景计数（回看预算/InjectCap），投递时留注记后清零
}
