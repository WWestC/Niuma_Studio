// Package server exposes the chat room over localhost HTTP + WebSocket.
package server

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/identity"
	"github.com/WWestC/Niuma_Studio/server/autopilot"
	"github.com/WWestC/Niuma_Studio/server/fswatch"
	"github.com/WWestC/Niuma_Studio/server/gitops"
	"github.com/WWestC/Niuma_Studio/server/plans"
	"github.com/WWestC/Niuma_Studio/server/roomops"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

// Options configures the server. The fields are grouped by domain so a
// new face knows where to hang (and the multi-process split — one
// assembly point per process face — becomes a mechanical sub-literal
// instead of a re-learn): Endpoint (process/transport lifecycle),
// Stores (the domain-store set), Web (the workbench's static face) and
// Faces (mounted handler faces). The ungrouped remainder is the room
// wiring with no family. Nil-means-off stays per-field: an absent
// store/face is a feature switch, documented at the field.
type Options struct {
	// Endpoint is the process/transport lifecycle — which port, how
	// takeover behaves, what the info face reports, where the discovery
	// file and seat snapshots live, how links are kept healthy.
	Endpoint EndpointConfig
	// Stores is the domain-store set: one field per store the faces
	// drink from. Nil on any field disables that store's faces (the
	// per-field notes spell out each degradation).
	Stores StoreSet
	// Web is the workbench's static face — the embedded tree or the
	// dev-disk tree plus its live-reload watcher.
	Web WebConfig
	// Faces is the handler faces mounted beyond this package's own
	// routes: dispatch management, the 小助手 panel, the splash's boot
	// gate.
	Faces FaceConfig

	// LocalName is the human operator's display name, used to flag the
	// host in the /kb/people roster.
	LocalName string
	// Auth, when set, arms the identity layer (multi-user shape): the
	// login/session service backing /auth/* and the WS dial gate. Nil
	// (the default) keeps the single-user zero-friction trust model —
	// every role gate reads "no service" as the operator.
	Auth *identity.Service
	// OperatorToken is the studio's machine credential (boot-minted,
	// 0600 beside the data): presented as a Bearer/hello token from the
	// LOOPBACK it resolves to the operator (admin) — the machine
	// credential is only honored on the machine (a leaked file must not
	// become a remote root key). Remote CLI tooling logs in with an
	// account session instead (NIUMA_AUTH_TOKEN carries it). Empty
	// under single-user.
	OperatorToken string
	// StrictLoopback, when set, turns OFF the loopback auto-operator:
	// even same-machine requests must present a credential. The local
	// CLI stays zero-friction (authHello presents the service token
	// automatically); the workbench window then needs a login. An
	// explicit tightening posture for deployments that do not want
	// "on this machine" to imply "is the operator". Boot reads
	// NIUMA_STRICT_LOOPBACK=1.
	StrictLoopback bool
	// Subject is the storage namespace every domain-store call carries
	// ("" = the single-user historical shape; multi-user boot binds the
	// studio owner account's name — one studio, one namespace, the
	// per-call seam the Store interfaces expose). Read via s.subject().
	Subject string
	// Fleet, when set, is the server's whole view of the dispatcher
	// fleet — every ambient tap and delegated action that used to be a
	// per-field func sink here (see fleetapi.go for the one contract:
	// errors are receipt notes, never refusals). Nil = dispatch off
	// (embeds, tests); main wires an adapter over *dispatch.Fleet.
	Fleet FleetAPI
	// Registry, when set, routes hello.project to per-project rooms
	// (v2 P3-a): an explicit project key resolves through the registry
	// — 项目=办公室, one connection one room — and unknown / draft /
	// archived projects are refused at the door. An absent (or
	// "default") project keeps the legacy single-room behavior on the
	// server's own hub, which main sets to the lobby. Nil = no routing
	// at all: every dial joins this hub — the pre-P3 embedding shape,
	// byte-identical on the wire.
	Registry *chat.Registry
	// ResetCh, when set, backs POST /reset — the factory reset's
	// signal line (the settings card's 「重置工作室」 after its typed
	// confirmation). The handler validates {"confirm":"RESET"} and
	// drops one token here; the choreography itself (archive sidebar
	// rows → teardown → flush → wipe ~/.niuma* → relaunch) is main's,
	// where every part exists. Buffered-one: a second request while a
	// reset is pending answers 409 instead of queueing another exit.
	// Nil = the endpoint 404s (embeds, tests).
	ResetCh chan struct{}
	// RebuildCh, when set, backs POST /rebuild — the recompile-restart's
	// signal line (the settings card's 「重新编译并重启」). The handler
	// FIRST compiles and atomically swaps the binary in place
	// (util.RebuildSelf, synchronous — a failed build answers 400 with
	// the compiler output and changes nothing); only a good build drops
	// one token here, and the handoff itself (teardown → flush →
	// relaunch the fresh binary) is main's, where every part exists.
	// Buffered-one: a second request while a handoff is pending answers
	// 409 instead of queueing another exit. Nil = the endpoint 404s
	// (embeds, tests).
	RebuildCh chan struct{}
	// AutoPilot, when set, starts the 全智能模式 engine with the
	// server (before serving — its handlers read s.autopilot): aged
	// pending proposals land as the actor 自动驾驶, idle autopilot
	// projects get their orchestrator poked for the next requirement
	// (cfg.Poke, main wires the fleet route), and a daily accept cap
	// trips the switch back off. Nil = no engine (the /p/{key}/autopilot
	// GET still reflects the persisted switch; a POST enable answers
	// 409).
	AutoPilot *AutoPilotConfig
	// GitWorktreeRoot overrides the per-member worktree root's
	// ~/.niuma/wt convention（tests 注入临时根）. Empty = the convention.
	GitWorktreeRoot string
}

// EndpointConfig is Options' process/transport lifecycle group.
type EndpointConfig struct {
	// PreferredPort is the one fixed port to bind (default 7777). The
	// server never falls forward: if the port is already held by another
	// niuma instance, that instance is terminated and the port
	// taken over, so every caller (human or AI) can always rely on the
	// same address.
	PreferredPort int
	// ForceTakeover lifts the non-interactive guard: a bare start from
	// a non-TTY stdin (AI scripts, misfired commands) refuses to kill a
	// healthy running room and prints usage instead, unless this is set
	// (ARB-1: tonight's takeover by a mistyped bare command killed the
	// live room for two minutes). A TTY (the human host's one-click
	// start) and unhealthy/zombie holders keep the automatic takeover.
	ForceTakeover bool
	// Version reported by the info endpoint.
	Version string
	// DiscoveryFile, when non-empty, receives the actual port so the
	// CLI can find a running instance (usually ~/.niuma_port).
	DiscoveryFile string
	// SnapshotPath, when non-empty, receives the room seat snapshot on
	// every graceful Close (v0.10 seat-M2: SIGTERM handover and the UI
	// quit share Server.Close) so the takeover instance can restore the
	// roster as grace seats. Empty = no snapshot (tests).
	SnapshotPath string
	// Heartbeat pings every connected client at this interval (a pong
	// is due within the same interval), so half-open transports — dead
	// processes the OS has not reaped — release their seat to the
	// presence grace instead of lingering for a TCP timeout. Clients
	// must keep reading (all first-class forms do: wait/guard/listen
	// poll reads, the local UI drains continuously). 0 disables (tests
	// and short-lived embeds); main runs 25s (t_09).
	Heartbeat time.Duration
	// BindAddr overrides the listener's bind address (gate.go's
	// deployable seam). Empty and "localhost"/loopback literals keep
	// the historical 127.0.0.1 binding — the loopback trust root,
	// byte-for-byte. A non-loopback bind (0.0.0.0, a LAN IP) REFUSES
	// to serve unless GateToken is armed (fail-close at the sentinel
	// that used to only warn). Boot reads NIUMA_ADDR here.
	BindAddr string
	// GateToken, when non-empty, arms the token gate (gate.go):
	// loopback requests keep the zero-friction historical behavior;
	// every non-loopback request must present this token (Bearer
	// header or ?token=). Boot reads NIUMA_GATE_TOKEN, auto-minting
	// to ~/.niuma_gate_token when a remote bind needs one.
	GateToken string
}

// StoreSet and LedgerSeam moved to the verb contract layer
// (server/verbs/stores.go); these aliases keep Options assembly and
// every existing reference unchanged.
type StoreSet = verbs.StoreSet
type LedgerSeam = verbs.LedgerSeam

// WebConfig is Options' workbench static-face group.
type WebConfig struct {
	// WebFS, when set, serves the Web workbench (v2 P1): GET /app
	// returns the workbench entry (hash routing, no SPA fallback — "/"
	// keeps its info JSON) and /app/<path> the static assets, always
	// with explicit freshness headers (ETag + no-cache; see web.go).
	// Main embeds the repository's web/ directory (go:embed); nil =
	// /app absent (tests that don't care).
	WebFS fs.FS
	// WebDev flips the workbench face into disk mode (NIUMA_DEV=1):
	// assets answer no-store and GET /app/__reload is a live-reload
	// SSE — saving a web/ file reloads the open window without a
	// rebuild. WebFS is then expected to be an os.DirFS over WebDevDir
	// (the watcher walks that path). False (release): embedded,
	// ETag-validated, no reload endpoint.
	WebDev bool
	// WebDevDir is the directory the dev watcher polls (the repo's
	// web/). Dev mode only; empty disables the watcher.
	WebDevDir string
}

// FaceConfig is Options' mounted-handler-faces group.
type FaceConfig struct {
	// DispatchHandler, when set, is mounted at /dispatch/ (v1.0): the
	// member dispatcher's management face — GET /dispatch (snapshot),
	// POST /dispatch/{birth,bind,steer}. Nil = endpoint absent.
	DispatchHandler http.Handler
	// ZCodeStatus, when set, backs GET /zcode — the workbench splash's
	// boot gate (the app is bound to ZCode: the door opens when the
	// bridge reports ready). Nil = the endpoint is absent and the
	// splash's own miss-grace lets the door open (embeds, tests).
	ZCodeStatus func() ZCodeState
	// Assistant, when set, is mounted at /assistant/ — the 小助手
	// panel's face (GET /assistant state, POST /assistant/ask), the
	// host's usage advisor riding the same zcode bridge the member
	// dispatchers drive. Nil = the face is absent.
	Assistant http.Handler
}

// Server is the running localhost chat endpoint.
type Server struct {
	Port int
	hub  *chat.Hub
	opts Options
	// seat is the verb contract layer's view of this server (built
	// once in Start, before serving): what every room verb may touch.
	seat *verbs.Seat
	// gitFaceVal/roomopsFaceVal/autopilotFaceVal cache the STATEFUL
	// domain faces (git write locks, the rebuild latch, the autopilot
	// engine's back-reference) — one per server, not per call.
	gitFaceVal       *gitops.Face
	roomopsFaceVal   *roomops.Face
	autopilotFaceVal *autopilot.Face
	srv              *http.Server
	// chronicle is the 工作室志's auto-collection hub (r_10 t_152) —
	// nil-safe throughout (every hook method tolerates nil receiver).
	chronicle *Chronicle
	// achieve is the 成就判定引擎 (r_18 t_187) — nil-safe on the
	// observeAchievements tap; nil = achievements off (no staffing).
	achieve *AchievementEngine
	// nightMu/nightLast gate the night-light tap (r_18 night-10 接线):
	// one night-light event per local deep-night (0–6 点). nightHubs
	// marks hubs already carrying the say-feed tap (wireNightTap's
	// idempotence).
	nightMu   sync.Mutex
	nightLast string
	nightHubs sync.Map
	// visitor is the 访客通道 gate (r_19 t_191): toggle+token+limits.
	visitor *VisitorGate
	// visitors is the per-project connection meter (👁 N 的数据面).
	visitors *visitorMeter
	// firstSeen is the 彩蛋去重（token epoch 一次）.
	firstSeen *firstVisitTracker
	// noFlush is the snapshot breaker the factory reset trips after its
	// one final flush (see SuppressSnapshots).
	noFlush atomic.Bool
	// autopilot is the 全智能模式 engine, set once by Start when
	// Options.AutoPilot is wired (startAutoPilot) — before serving, so
	// the write face can Kick it without a race.
	autopilot *autopilot.Engine
	// gitLocks serializes the git WRITE moves per project key
	// (fetch/checkout/branch — v2.7 版本管理), keyed by project key.
	gitLocks sync.Map
	// fsWatch is the workspace change watcher (文件板自动刷新的服务端
	// 半边): poll-signature loop over every project workspace, changed
	// keys broadcast one fs_dirty frame per room hub. Set once by Start
	// (nil ProjectStore keeps it off); Close stops the loop.
	fsWatch *fswatch.Watcher
	// retentionStop ends the chat-log retention sweep loop (set once by
	// Start when the registry persists under a real root; Close closes).
	retentionStop chan struct{}
	// replays is the 一日回放 frame journal (r_32): append/read/prune
	// over replay/<key>/<yyyymmdd>.jsonl under the v2 root.
	replays *replayStore
	// dayReplay is the 一日回放分享 gate (r_32): token + per-(room,day)
	// grants — the visitor channel's discipline, day granularity.
	dayReplay *dayReplayGate
	// spend is the /budget 耗粮读数的短 TTL 缓存 (r_26)：驾驶舱 20s 一拍
	// 与熔断拍合并台账读——nil-safe（裸 Server 直读，语义不变）。
	spend *spendCache
	// usage is the /usage 与 /usage/turns 的短 TTL 缓存：耗粮面板每开一
	// 次、每点一名员工都是一次 python3 全量聚合——30s 窗内合并成一读。
	// nil-safe（裸 Server 直读，语义不变）。
	usage *usageCache
}

// Start brings up the HTTP/WebSocket server bound to 127.0.0.1.
// PreferredPort < 0 asks for an ephemeral port (127.0.0.1:0): the
// kernel picks a free one — the mode e2e test harnesses use so
// parallel test invocations can never fight over a fixed port (t_37).
func Start(hub *chat.Hub, opts Options) (*Server, error) {
	// 0 is refused on purpose: a zero-value Options (a test building
	// the struct field-by-field, a careless embedder) used to resolve
	// silently to 7777 and take over the live room — the 21:03
	// incident. Port choice is explicit: a bound port to serve, <0 for
	// the kernel's ephemeral pick (tests).
	if opts.Endpoint.PreferredPort == 0 {
		return nil, fmt.Errorf("PreferredPort must be explicit: pass the port to serve (7777) or <0 for an ephemeral test port — zero is the unset struct field, not a default")
	}
	s := &Server{hub: hub, opts: opts}
	s.seat = s.newVerbsSeat()
	// r_10 编年史钩子（t_152）：三源自动收录——nil Docs 时全静默
	s.chronicle = NewChronicle(opts.Stores.Docs)
	// r_18 成就引擎（t_187）：事件判定＋达成广播——nil StaffStore 时全静默
	s.achieve = NewAchievementEngine(opts.Stores.StaffStore)
	// r_18 C6 自举彩蛋（t_189 表换入随单触发）：18 枚表上线的这一拍
	// 达成「成就系统上线」——不依赖事件流，幂等由解锁集兜底（老库
	// 已达成的重启不重触发）。广播留给首个 hub 可用时刻（Start 里
	// hub 已在手）。
	s.observeAchievements(hub, Event{Kind: "achievement-live", At: time.Now().Unix()})
	// r_18 night-10 接线（t_189 遗留）：大厅 say 喂线先挂上——深夜
	// （0–6 点）的房内发言也是「亮灯」（项目房在 planHub 解析时挂）。
	s.wireNightTap(hub)
	// r_19 访客通道（t_191）：默认关、token 生命周期、限流与彩蛋
	s.visitor = &VisitorGate{}
	s.visitors = newVisitorMeter()
	s.firstSeen = newFirstVisitTracker()
	s.spend = newSpendCache()    // r_26：/budget 与熔断拍共读的台账缓存
	s.usage = newUsageCache()    // 耗粮读面（/usage、/usage/turns）的台账缓存
	s.replays = newReplayStore() // r_32：一日回放帧落盘（replay/<key>/<日>.jsonl）
	// 域动词注册（带缝的域：roomops 管理族、gitops 合并门）——在全部
	// 域状态（chronicle/replays）就位之后，缓存 Face 才不吞 nil。
	roomops.RegisterVerbs(s.roomopsFace())
	gitops.RegisterVerbs(s.gitFace())
	plans.RegisterVerbs(s.plansFace())
	s.dayReplay = &dayReplayGate{} // r_32：一日回放分享 token 门

	var ln net.Listener
	var err error
	ln, err = listenPort(opts.Endpoint.PreferredPort, opts.Endpoint.BindAddr, opts.Endpoint.DiscoveryFile, opts.Endpoint.ForceTakeover, stdinIsInteractive())
	if err != nil {
		return nil, err
	}
	// 信任边界哨兵（gate.go 的 fail-close 落点）：非回环绑定而没有
	// 令牌就拒绝起服务——过去这里只打一条告警日志，/owner/token、
	// /fs/ls、/mcps 明文面就裸在网上了；现在要么带令牌出门，要么
	// 根本不开门。
	if err := gateRefusal(ln, opts.Endpoint.GateToken); err != nil {
		_ = ln.Close()
		return nil, err
	}
	s.Port = opts.Endpoint.PreferredPort
	if opts.Endpoint.PreferredPort < 0 {
		s.Port = ln.Addr().(*net.TCPAddr).Port
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS) // WS 拨号：身份门在 hello 协商里（authRequired/dialAuth），不走 HTTP 矩阵
	// 身份面（多用户形态；未接线时各端点如实自报 404/单用户态）。
	mux.HandleFunc("/auth/login", s.authFace().HandleAuthLogin)
	mux.HandleFunc("/auth/logout", s.authFace().HandleAuthLogout)
	mux.HandleFunc("/auth/me", s.authFace().HandleAuthMe)
	s.route(mux, "/auth/users", s.authFace().HandleAuthUsers)                        // GET 名册 / POST 开户（admin 专属）
	s.route(mux, "/auth/users/{name}", s.authFace().HandleAuthUser)                  // GET 详情 / DELETE 销户
	s.route(mux, "/auth/users/{name}/role", s.authFace().HandleAuthUserRole)         // POST 改角色
	s.route(mux, "/auth/users/{name}/password", s.authFace().HandleAuthUserPassword) // POST 改密
	s.route(mux, "/auth/users/{name}/projects", s.authFace().HandleAuthUserProjects) // GET/POST/DELETE 项目授权
	s.route(mux, "/members", s.handleMembers)
	s.route(mux, "/agents", s.handleAgents)
	s.route(mux, "/agents/{name}/prompt", s.handleAgentPrompt)
	s.route(mux, "/prompt", s.handlePrompt)
	if s.opts.Faces.ZCodeStatus != nil {
		mux.HandleFunc("/zcode", s.handleZCode) // the splash's boot gate
	}
	s.route(mux, "/kb", s.handleKBIndex)
	s.route(mux, "/kb/manual", s.handleKBManual)
	s.route(mux, "/usage", s.handleUsage)            // 耗粮统计：牛马员工 token 台账（员工聚合）
	s.route(mux, "/usage/turns", s.handleUsageTurns) // 耗粮统计：一名员工的每轮明细
	s.route(mux, "/kb/people", s.handleKBPeople)
	s.route(mux, "/kb/capacity", s.autopilotFace().HandleKBCapacity) // r_24 驾驶舱：饱和度+在座+水位聚合只读面（巡报/注入/驾驶舱三消费者同源）
	s.route(mux, "/kb/people/{name}", s.handleKBPerson)
	s.route(mux, "/kb/people/{name}/history", s.handleKBPersonHistory) // r_26 t_212：成员交付史 JSON 出口（履历页数据面）
	s.route(mux, "/kb/tasks", s.handleKBTasks)
	s.route(mux, "/kb/tasks/{id}", s.handleKBTask)
	s.route(mux, "/kb/tasks/{id}/brief", s.handleKBTaskBrief) // 任务简报：领单/续做的一页上下文装配
	s.route(mux, "/kb/establishment", s.handleKBEstablishment)
	s.route(mux, "/kb/establishment/row", s.roomopsFace().HandleKBEstablishmentRow) // 编制表行 CRUD：大厅（v2.4 牛马管理面板）
	s.route(mux, "/kb/docs", s.handleKBDocsIndex)
	s.route(mux, "/kb/docs/", s.handleKBDocs)                // subtree: keys contain a slash (roles/hr)
	s.route(mux, "/p/{key}/history", s.handleProjectHistory) // per-room jsonl scan (v2 P3-c)
	s.route(mux, "/p/{key}/reads", s.handleProjectReads)     // per-room read receipts (飞书式已读)
	s.route(mux, "/p/{key}/acks", s.handleProjectAcks)
	s.route(mux, "/achieve/tests-green", s.handleTestsGreen)                                  // r_18 成就：测试全绿上报/达成快照                          // per-room ack receipts (飞书式收到)
	mux.HandleFunc("/view/{project}", s.authFace().HandleView)                                // r_19 访客：token 门控只读页
	s.route(mux, "/visitor", s.authFace().HandleVisitorToggle)                                // r_19 访客：开关读写面（房主专属）
	s.route(mux, "/p/{key}/reacts", s.handleProjectReacts)                                    // per-room emoji reactions (飞书式表情回应)
	s.route(mux, "/p/{key}/questions", s.handleProjectQuestions)                              // per-room open questions (向房主提问选项卡)
	s.route(mux, "/p/{key}/lifecycle", s.roomopsFace().HandleProjectLifecycle)                // 立项生命周期：activate/archive/reactivate（v2 P7）
	s.route(mux, "/p/{key}", s.roomopsFace().HandleProjectPatch)                              // 项目内容编辑（name/desc/versions，v2 P7）
	s.route(mux, "/p/{key}/tasks", s.handleProjectTasks)                                      // project task list (v2 P4-a)
	s.route(mux, "/p/{key}/plan", s.plansFace().HandleProjectPlan)                            // pending plan read (v2 P4-b)
	s.route(mux, "/p/{key}/merge", s.gitFace().HandleProjectMerge)                            // pending merge-proposal read (v2.8 gitflow)
	s.route(mux, "/p/{key}/reqs", s.plansFace().HandleProjectReqs)                            // requirement ledger (v2 P4-d)
	s.route(mux, "/p/{key}/reqs/{id}", s.plansFace().HandleProjectReqDelete)                  // requirement cleanup: DELETE one open/parked entry
	s.route(mux, "/p/{key}/reqs/{id}/park", s.plansFace().HandleProjectReqPark)               // r_31 暂缓：open→parking（编排者单方/房主，带因＋复查期）
	s.route(mux, "/p/{key}/reqs/{id}/unpark", s.plansFace().HandleProjectReqUnpark)           // r_31 解暂缓：parking→open（park 原因留行可追溯）
	s.route(mux, "/p/{key}/meeting", s.plansFace().HandleProjectMeeting)                      // meeting room occupancy (需求评审会议)
	s.route(mux, "/p/{key}/notice", s.handleProjectNotice)                                    // per-room group announcement (飞书式群公告)
	s.route(mux, "/p/{key}/schedule", s.handleProjectSchedule)                                // gantt projection (v2 P4-c)
	s.route(mux, "/p/{key}/staffing", s.handleProjectStaffing)                                // staffing rows (v2 P4-c)
	s.route(mux, "/p/{key}/establishment", s.roomopsFace().HandleProjectEstablishment)        // establishment diff + auto_recall (v2 P5-a)
	s.route(mux, "/p/{key}/establishment/row", s.roomopsFace().HandleProjectEstablishmentRow) // 编制表行 CRUD：项目（v2.4）
	s.route(mux, "/p/{key}/git", s.gitFace().HandleGitSummary)                                // 版本管理：仓库/状态/合规摘要（v2.7）
	s.route(mux, "/p/{key}/git/branches", s.gitFace().HandleGitBranches)                      // 全部分支＋需求/版本绑定
	s.route(mux, "/p/{key}/git/log", s.gitFace().HandleGitLog)                                // 提交历史（逐条规范判定）
	s.route(mux, "/p/{key}/git/graph", s.gitFace().HandleGitGraph)                            // 提交图（全分支拓扑＋父 SHA，树形视图数据源）
	s.route(mux, "/p/{key}/git/req-activity", s.gitFace().HandleGitReqActivity)               // 需求反查：绑定分支＋提及提交
	s.route(mux, "/p/{key}/git/fetch", s.gitFace().HandleGitFetch)                            // fetch --all --prune
	s.route(mux, "/p/{key}/git/push", s.gitFace().HandleGitPush)                              // 推送当前/指名分支到远端（v2.14 远端同步）
	s.route(mux, "/p/{key}/git/pull", s.gitFace().HandleGitPull)                              // 快进当前分支到上游（--ff-only，脏树 409）
	s.route(mux, "/p/{key}/git/auth", s.gitFace().HandleGitAuth)                              // 逐远端授权探测（ls-remote 体检面）
	s.route(mux, "/p/{key}/git/init", s.gitFace().HandleGitInit)                              // 房主显式初始化空 workspace（v2.14 自愈路）
	s.route(mux, "/p/{key}/git/checkout", s.gitFace().HandleGitCheckout)                      // 切分支（脏树 409）
	s.route(mux, "/p/{key}/git/branch", s.gitFace().HandleGitBranchNew)                       // 建分支＋可选自动绑定
	s.route(mux, "/p/{key}/git/bind", s.gitFace().HandleGitBind)                              // 分支↔需求/版本绑定
	s.route(mux, "/p/{key}/git/unbind", s.gitFace().HandleGitUnbind)                          // 解绑（幂等）
	s.route(mux, "/p/{key}/git/policy", s.gitFace().HandleGitPolicy)                          // commit 规范策略
	s.route(mux, "/p/{key}/git/settings", s.gitFace().HandleGitSettings)                      // 两级门写面：总开关＋基线/自动建枝（立项后可改）
	s.route(mux, "/p/{key}/git/hook", s.gitFace().HandleGitHook)                              // commit-msg/post-commit 钩子装/卸
	s.route(mux, "/p/{key}/git/seats", s.gitFace().HandleGitSeats)                            // 分支隔离：成员×分支矩阵＋残留树（v2.7.1）
	s.route(mux, "/p/{key}/git/seat", s.gitFace().HandleGitSeat)                              // 设分支/回主树（迁移由调度器经办）
	s.route(mux, "/p/{key}/git/seat-cleanup", s.gitFace().HandleGitSeatCleanup)               // 残留工作树回收
	s.route(mux, "/p/{key}/autopilot", s.autopilotFace().HandleProjectAutopilot)              // 全智能模式 switch read/write face (v2.8)
	s.route(mux, "/p/{key}/pause", s.roomopsFace().HandleProjectPause)                        // 房间暂停 read/write face（对话/会议/小人全停）
	s.route(mux, "/p/{key}/rebuild-vote", s.roomopsFace().HandleProjectRebuildVote)           // AI 重编译投票开关读写面（按项目）
	s.route(mux, "/p/{key}/trace", s.handleProjectTrace)                                      // 工作过程快照（思考/工具/回合，v2.8）
	s.route(mux, "/p/{key}/trace/file", s.handleProjectTraceFile)                             // 工作过程图片窄端点（工作区内 <img> 直出，v2.8）
	s.route(mux, "/p/{key}/term", s.handleProjectTerm)                                        // 终端转录快照（输入/输出/反问，r_17）
	s.route(mux, "/p/{key}/term/file", s.handleProjectTermFile)                               // 终端转录整册下载（r_17）
	s.route(mux, "/p/{key}/fs/tree", s.fsFace().Tree)                                         // 文件浏览器：一层目录清单（懒加载树）
	s.route(mux, "/p/{key}/fs/file", s.fsFace().File)                                         // 文件浏览器：文本文件预览信封（类 VSCode）
	s.route(mux, "/p/{key}/fs/search", s.fsFace().Search)                                     // 文件浏览器：文件名子串检索
	s.route(mux, "/p/{key}/replay", s.handleReplayFrames)                                     // 一日回放帧：POST 批量落盘 / GET 分页读（r_32）
	s.route(mux, "/p/{key}/replay/days", s.handleReplayDays)                                  // 一日回放：有录制的日期列表（播放器选日面）
	s.route(mux, "/p/{key}/replay/share", s.handleReplayShare)                                // 一日回放：分享链接铸/废/读（房主专属）
	mux.HandleFunc("/dayreplay/{project}", s.handleDayReplayPage)                             // 一日回放分享页（token 门控同壳，r_32）
	s.route(mux, "/media", s.handleMediaUpload)                                               // 输入图片上传（聊天发图）
	mux.HandleFunc("/media/{id}", s.handleMediaGet)                                           // 输入图片取回（<img> src）
	s.route(mux, "/offboard", s.roomopsFace().HandleOffboard)                                 // departure pipeline orchestration (v2 P5-a)
	s.route(mux, "/p/{key}/dismiss", s.roomopsFace().HandleProjectDismiss)                    // 清退项目组成员：踢房＋删编制行＋删档案＋ZCode 深清（口令 DISMISS）
	s.route(mux, "/p/{key}/reset", s.roomopsFace().HandleProjectReset)                        // 重置项目：成员与全部台账清空回开张态（口令 RESET，大厅/已归档拒绝）
	s.route(mux, "/p/{key}/delete", s.roomopsFace().HandleProjectDelete)                      // 删除项目：除名＋全台账＋房间档案一次清（口令 DELETE；仅草稿/归档，active 先归档）
	s.route(mux, "/reset", s.roomopsFace().HandleReset)                                       // factory reset: archive, wipe, relaunch (settings card's danger zone)
	s.route(mux, "/rebuild", s.roomopsFace().HandleRebuild)                                   // recompile-restart: build+swap in the handler, hand off via RebuildCh (settings card's 维护区)
	s.route(mux, "/skills", s.skillsFace().HandleSkills)                                      // skill library list
	s.route(mux, "/skills/authoring", s.skillsFace().HandleAuthoring)                         // 自制技能开关 read/write（工作室级）
	s.route(mux, "/skills/{key}", s.skillsFace().HandleSkill)                                 // skill detail / host upsert / delete
	s.route(mux, "/mcps", s.skillsFace().HandleMCPs)                                          // MCP server library list（请求头值脱敏）
	s.route(mux, "/mcps/{key}", s.skillsFace().HandleMCP)                                     // MCP detail（回环读原值）/ host upsert / delete
	s.route(mux, "/p/{key}/staffing/{person}/fabric", s.skillsFace().HandleStaffingFabric)    // Compose preview
	s.route(mux, "/projects", s.roomopsFace().HandleProjects)                                 // room-switcher data source
	s.route(mux, "/projects/preflight", s.roomopsFace().HandleProjectPreflight)               // 立项预检：风险与注意事项提前亮（v2.13）
	s.route(mux, "/retention", s.roomopsFace().HandleRetention)                               // 聊天记录保留期 read/write + sweep (v2.8.2)
	s.route(mux, "/dispatch-wait", s.handleSendWait)                                          // 消息注入等待 read/write，热生效到调度器
	s.route(mux, "/budget", s.handleBudget)                                                   // token 预算 read/write（日/周上限，到额熔断只关推进，r_26）
	s.route(mux, "/reveal", s.handleReveal)                                                   // ZCode 侧栏即时刷新 read/write + 开启即治疗性强投（设置窗·连接）
	s.route(mux, "/lang", s.handleLang)                                                       // 界面语言（中/英）read/write——后端文案热切（设置窗·通用）
	s.route(mux, "/owner/token", s.authFace().HandleOwnerToken)                               // 房主凭证读面（owner-M1，回环限定）：工作台 OwnerChannel 拨号携带的 proof
	s.route(mux, "/fs/ls", s.fsFace().List)                                                   // 立项工作区的目录浏览（v2 P7）
	s.route(mux, "/internal/git/commit", s.gitFace().HandleGitCommitNotify)                   // post-commit 钩子的提交播报落腿（v2.8 gitflow）
	if s.opts.Faces.DispatchHandler != nil {
		mux.Handle("/dispatch/", s.gateRoute("/dispatch/", s.opts.Faces.DispatchHandler.ServeHTTP))
	}
	if s.opts.Faces.Assistant != nil {
		mux.Handle("/assistant", s.gateRoute("/assistant", s.opts.Faces.Assistant.ServeHTTP))   // the 小助手 panel's face
		mux.Handle("/assistant/", s.gateRoute("/assistant/", s.opts.Faces.Assistant.ServeHTTP)) // (bare + subtree: no redirect hop)
	}
	s.mountArt(mux) // the pixel room's canvas layers (v2 P6)
	if s.opts.Stores.Plugins != nil {
		// the plugin face (v3 preview) + the market face (M2): mounting
		// is the shell's; the handlers live in server/skills.
		mux.HandleFunc("/plugins.json", s.skillsFace().HandlePluginsIndex)
		mux.HandleFunc("/plugins/", s.skillsFace().HandlePluginAsset)
		s.route(mux, "/market/index", s.skillsFace().HandleMarketIndex)
		s.route(mux, "/market/install", s.skillsFace().HandleMarketInstall)
		s.route(mux, "/market/enable", s.skillsFace().HandleMarketEnable)
		s.route(mux, "/market/remove", s.skillsFace().HandleMarketRemove)
	}
	if s.opts.Web.WebFS != nil {
		face := newWebFace(s.opts.Web.WebFS, s.opts.Web.WebDev, s.opts.Web.WebDevDir)
		face.onEntry = s.usage.warm // 工作台页面加载即后台预热耗粮台账（抽屉首开即渲染，30s 窗内天然合并）
		face.mount(mux)
	}
	mux.HandleFunc("/", s.handleInfo)
	// 门闩组装：hostgate（Host/Origin/Sec-Fetch-Site 三查，摘浏览器
	// 侧流量）打底；令牌门（gate.go）在外圈——回环请求走整条历史
	// 栈零摩擦，远端请求持令牌直达 mux、无令牌裸 404。
	var handler http.Handler = gateBrowserBorne(mux)
	if opts.Endpoint.GateToken != "" {
		handler = newTokenGate(opts.Endpoint.GateToken, handler, mux)
	}
	s.srv = &http.Server{Handler: handler}
	if opts.AutoPilot != nil {
		s.startAutoPilot(*opts.AutoPilot) // 全智能模式：engine up before serving (the write face Kicks it)
	}
	if s.opts.Registry != nil && s.opts.Registry.Root() != "" {
		// 聊天记录保留期（v2.8.2）：boot sweep + the slow beat keeps the
		// jsonl honest under the saved policy — the loop lives in the
		// roomops domain now; the shell owns the stop channel's life.
		s.retentionStop = s.roomopsFace().StartRetention(roomops.RetentionEvery)
	}
	s.startFsWatch() // 工作区变更推送（文件板自动刷新的服务端半边；nil store 自动关）

	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http server error: %v", err)
		}
	}()

	if opts.Endpoint.DiscoveryFile != "" {
		if err := os.WriteFile(opts.Endpoint.DiscoveryFile, []byte(fmt.Sprintf("%d %d\n", s.Port, os.Getpid())), 0o644); err != nil {
			log.Printf("write discovery file: %v", err)
		}
	}
	return s, nil
}

func (s *Server) Addr() string { return fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port) }

// subject is the storage namespace the server's store calls carry
// (Options.Subject; "" single-user).
func (s *Server) subject() string { return s.opts.Subject }

func (s *Server) Close() {
	if s.fsWatch != nil {
		s.fsWatch.Stop()
	}
	if s.retentionStop != nil {
		close(s.retentionStop)
	}
	s.writeSnapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	if s.opts.Endpoint.DiscoveryFile != "" {
		_ = os.Remove(s.opts.Endpoint.DiscoveryFile)
	}
}
