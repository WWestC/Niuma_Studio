package main

// boot_phase_dispatch.go — phase 2 of 4 (bootDispatch): the ZCode
// bridge, the per-project fleet, the boot gate's six-state projection
// and the 小助手. Split from boot_phases.go, zero logic edits — the
// ordering invariants live on the function below.

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/WWestC/Niuma_Studio/assistant"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/dispatch"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// bootDispatch boots the ZCode bridge, the per-project fleet, the boot
// gate's six-state projection and the 小助手. INVARIANTS it owns:
//   - SEAT SNAPSHOTS RESTORE HERE, before StartFleet's Attach → adopt
//     can take seats and before the listener answers observers (an
//     observer that connects into an unrestored roster sees an empty
//     room that never corrects — the missing-ghosts incident);
//   - reveal.json and lang.json load before any fleet/assistant spawn
//     (the sidebar backfill's first wave and every backend notice must
//     already speak the configured language/reveal mode);
//   - srv arrives LATER (the server face phase): the RebuildFire leg
//     reads it through the srvp slot under a nil guard — the only
//     forward reference in the whole boot;
//   - the bridge watchdog runs for the process's life and never
//     respawns behind a closing room (closing flips first on exit).
func bootDispatch(hub *chat.Hub, registry *chat.Registry, projStore *projects.Store,
	lobbyWS, snapshotPath string, st storeSet, noDispatch bool, srvp **server.Server) dispatchSet {

	store, staffStore, lib, engine := st.agentStore, st.staff, st.lib, st.engine
	meetStore, reqStore, noticeStore, mediaStore, docs := st.meets, st.reqs, st.notices, st.media, st.docs
	planStore := st.plans

	// v1.0 → v2 P3-b: the member dispatcher set is per-project — one
	// Dispatcher per active project room, each consuming that project's
	// staffing rows (birth workspace = the project's workspace, session
	// bindings written back to staffing, the sole truth) and each
	// isolated to its own hub's say feed. The management face keeps the
	// /dispatch shape with an additive "project" key (absent = the
	// lobby, legacy callers unchanged). One zcode app-server child
	// drives every member session process-wide. --no-dispatch opts out;
	// a missing node/zcode.cjs degrades to a notice, never a boot
	// failure.
	// The ZCode boot gate's truth (GET /zcode): the app is bound to
	// ZCode, so the workbench's splash door waits on this until the
	// bridge is recognized and the 小助手's channel warm-up lands (a
	// capped hold — see warmGateMax). Failure KEEPS THE DOOR SHUT with the
	// reason shown — no entering the office without ZCode; the one
	// open-door exception is --no-dispatch (an operator's explicit
	// opt-out, not a failure). A ZCode installed while the door waits
	// is picked up below (re-attempted at most once every 10s, so a
	// broken spawn can't fork-bomb) and the door opens on its own
	// once the child speaks — no app restart needed.
	// 桥池（v3 SPOF 根治第一段）：spawn/看护/换代收进
	// dispatch.BridgePool，包 main 只保留 spawner 与首亮钩子。
	// NIUMA_BRIDGE_POOL（1–8，默认 1）决定子进程数——每项目的
	// 调度器黏住一名成员（大厅恒为 0 号，小助手/启动招聘同乘），
	// 一名成员死了只闪断它名下的房间，10 秒节流自愈不变。
	var pool *dispatch.BridgePool
	var poolWired bool
	var fleet *dispatch.Fleet
	var dispatchHTTP http.Handler
	// closing flips before the teardown starts: every path that could
	// respawn a piece of the app mid-drain (the boot gate's 10s retry
	// re-spawning the ZCode child while the window is already closing)
	// checks it first — a quit must stay a quit.
	var closing atomic.Bool
	// ast is the 小助手 (built below the dispatch block); tryDispatch
	// pokes its warm-up the moment a bridge goes live, so it must be
	// declared up here.
	var ast *assistant.Assistant
	// tryDispatch performs one bridge attempt (assigned inside the
	// !--no-dispatch block; zcodeStatus calls it for the recovery
	// retry — nil can never be reached there because the status
	// answers "off" first when dispatch is disabled).
	var tryDispatch func(bool)
	// 侧栏揭示模式先于 fleet 装载（zcode/reveal.go 的真源链：设置文件
	// > env > 平台默认）——boot 回填 BackfillTaskIndex 的第一波注册就
	// 已经按设置投递；文件缺席（""）即回落 env/平台默认，与从前完全
	// 同语义。设置窗·连接面板的开关写的就是这份 reveal.json。
	// srv 槽位（srvp）由 main 传入：fleet 先于 server 装配，而调度器
	// 的 AI 重编译投票执行腿（Config.RebuildFire）经槽位读后装的
	// srv——闭包只在投票通过时才被调，彼时 srv 必已就位（nil 守卫
	// 兜内嵌/竞态窗口）。
	zcode.SetRevealOverride(zcode.LoadRevealMode(filepath.Join(v2Root(), "reveal.json")))
	// 界面语言（中/英）：启动按 lang.json 恢复后端文案语言；文件缺席
	// （""）＝中文默认，与从前完全同语义。前端启动时也会把它的偏好推
	// 过来对齐（POST /lang，fire-and-forget）。
	if l := i18n.LoadLang(filepath.Join(v2Root(), "lang.json")); l != "" {
		i18n.SetLang(l)
	}
	// seat-M2: 快照恢复先于一切会动房间的腿——桥（tryDispatch 的
	// Attach→adopt 会抢先入座）、listener（server.Start 起来 observer
	// 就能连，welcome 的 roster 是权威水合）。恢复是静默的（no
	// history, no broadcasts），晚于 listener 连入的观察者会拿到一份
	// 没有 ghost 的空名册，之后 attach 收回 ghost 又不广播 join——那份
	// 空名册永远等不到修正，办公室里的小人就这么不见了。恢复前置后，
	// listener 起来的那一刻名册就是全的。
	var interrupted []chat.InterruptedSeat
	for _, st := range server.LoadRoomSnapshot(snapshotPath, hub, chat.LobbyKey, staffStore) {
		// r_20 t_195：中断席位带引用式接续要点（快照冻结契约）
		interrupted = append(interrupted, st)
	}
	for key, h := range registry.Rooms() {
		if key == chat.LobbyKey {
			continue
		}
		for _, st := range server.LoadRoomSnapshot(registry.SnapshotPath(key), h, key, staffStore) {
			st.Project = key
			if p, ok := projStore.Get(key); ok {
				st.Room = p.Name
			}
			interrupted = append(interrupted, st)
		}
	}
	if !noDispatch {
		wd := lobbyWS
		// The fleet ALWAYS exists (inert without a bridge): the
		// /dispatch face mounts at process start and Attach fills it
		// the moment a bridge appears — at boot or after the recovery
		// retry below.
		fleet = dispatch.StartFleet(nil, store, projStore, staffStore, registry.Rooms(), dispatch.Config{
			Workspace: wd, // fallback only — project workspaces win per instance
			Model:     envOr("DISPATCH_MODEL", "GLM-5.3"),
			Reasoning: envOr("DISPATCH_REASONING", "high"),
			Perm:      envOr("DISPATCH_PERM", dispatch.PermAsk),
			Library:   lib,                                                // the skill & MCP library injection
			RankOf:    func(name string) int { return engine.Rank(name) }, // the sidebar title's Lv.N slot
			// v2 P4-b: the per-project patrol clock (orchestration
			// §2.5 — NIUMA_PATROL_EVERY overrides the 30-minute
			// default) and the orchestrator birth's Lv.8 stamp.
			PatrolEvery: dispatch.PatrolEveryFromEnv(),
			SetRank:     func(name string, lv int) { engine.SetRank(name, lv) },
			// v2 P5-a: the per-project daily report (NIUMA_DAILY_AT,
			// default 21:00; "off" silences it) — the patrol
			// clock's own tick+deliver mechanism.
			DailyAt: dispatch.DailyAtFromEnv(),
			// 需求评审会议时钟（NIUMA_MEETING_EVERY 调节，"off" 关闭）：
			// 牛马闲、需求在库、会议室空闲时，产品经理牵头开评审会。
			Subject:      st.ident.subject, // 身份层主体：调度器代表工作室读写共享台账
			Meetings:     meetStore,
			Reqs:         reqStore,
			Tasks:        engine,
			Plans:        planStore, // 阀门闸：待审提案在槽时会议钟让位（评审产出是提案，槽一房一件）
			MeetingEvery: dispatch.MeetingEveryFromEnv(),
			// 飞书式群公告：新牛马入职先读该房当前公告（新人进群
			// 可见群公告的同款规则）。
			NoticeOf: func(project string) (string, string, bool) {
				n, ok := noticeStore.Get(project)
				return n.Content, n.By, ok
			},
			// 编排者入职自登记：把排期编排岗位行写进该项目编制表
			// p/<key>/establishment（无表则按冻结契约新建），座位交由
			// 房内看护（缺岗报警 / 自动补员召回）。
			Docs: docs,
			// 输入图片：成员会话注入时把 say 行引用的图片还原成
			// session/send 附件（media.Store.Path）。
			MediaStore: mediaStore,
			// AI 重编译投票的执行腿：投票超半数通过 → 服务端 MemberRebuild
			//（与房主按钮同一条编译/换位/交接核心，全室通告由服务端发）。
			RebuildFire: func(project, by, tally string) error {
				if *srvp == nil {
					return fmt.Errorf("服务器未就绪（内嵌/竞态窗口）")
				}
				return (*srvp).MemberRebuild(project, by, tally)
			},
		})
		dispatchHTTP = fleet.HTTPHandler()
		// 消息注入等待（设置卡「智能」面板，POST /dispatch-wait 的落盘
		// 值）：启动即生效。赶在 Attach 之前 SetSendWait——值进 fleet 模
		// 板，晚生的调度器（桥后到、房间后激活）一样带上。
		fleet.SetSendWait(time.Duration(server.LoadSendWait(server.SendWaitPath(v2Root()))) * time.Second)
		// One bridge attempt per missing pool member. first=true is the
		// boot call (room notices on); retries are quiet — a poll every
		// 10s must not spam the room with the same notice. The pool owns
		// everything downstream of a successful spawn: wiring the fleet
		// on the first live member, swapping corpses on respawn, and the
		// watchdog loop (fenced ticks, same 10s throttle) that used to
		// be the anonymous goroutine below.
		spawnBridge := dispatch.BridgeSpawner(func(notify bool) (dispatch.Bridge, string) {
			b, _, fail := startZCodeBridge(hub, wd, notify)
			if fail != "" {
				return nil, fail
			}
			return b, ""
		})
		pool = dispatch.NewBridgePool(spawnBridge, dispatch.PoolOptions{
			Size:    bridgePoolSize(),
			Hub:     hub,
			Closing: &closing,
			OnSpawn: func() {
				if !poolWired {
					poolWired = true
					fleet.AttachPool(pool, registry.Rooms())
					fleet.KickFlagshipRecruit() // 桥到岗：按启动编制开始招聘（门等满编）
				}
				if ast != nil {
					ast.Warmup() // the bridge is live: burn the cold-start window
				}
			},
		})
		tryDispatch = func(first bool) {
			if closing.Load() {
				return // shutdown in flight: never respawn the child behind a closing room
			}
			pool.TrySpawn(!first)
		}
		tryDispatch(true)
	}

	// warmGateMax caps the door's hold on the 小助手 warm-up: a healthy
	// link round-trips in seconds, a hostile one can hang for minutes —
	// past the cap the door opens anyway and the panel's own health line
	// (plus the first ask queued behind the warm-up) picks it up inside,
	// the pre-gate behavior. NIUMA_WARMUP_GATE（整秒，5–600）改这条封
	// 顶：已知冷启动惩罚长的网络可把门撑到预热真落地，默认 90 秒。
	warmGateMax := 90 * time.Second
	if v := assistant.EnvWarmGate(util.Env("NIUMA_WARMUP_GATE")); v > 0 {
		warmGateMax = v
	}
	// warmGate reads the warm-up's flight for zcodeStatus below; the nil
	// guard covers the boot attempt that ran before ast existed.
	warmGate := func() (bool, time.Duration, string) {
		if ast == nil {
			return false, 0, ""
		}
		return ast.WarmGate()
	}
	// recruitGate reads the fleet's flagship hiring flight for
	// zcodeStatus below — the door's establishment half. A nil fleet
	// (--no-dispatch) never reaches it (the status answers "off"
	// first); a fleet without room memory never starts the loop and
	// reports Done — only a live, gated recruitment holds the door.
	recruitGate := func() dispatch.RecruitProgress {
		if fleet == nil {
			return dispatch.RecruitProgress{Done: true}
		}
		return fleet.FlagshipRecruit()
	}
	// zcodeStatus answers GET /zcode for the splash's boot gate — the
	// six-state projection over the bridge vars above (booting→warming
	// →recruiting→ready: the child speaks, the 小助手 burns the
	// cold-start window capped at warmGateMax, the flagship
	// establishment is hired to headcount, then the door opens;
	// missing/error hold the door shut; a failed start re-attempts at
	// most once per 10s so an install-while-waiting recovers by
	// itself, and a child that dies mid-shift is re-spawned by the
	// watchdog and swapped into the fleet — either way the door
	// re-opens on its own).
	zcodeStatus := func() server.ZCodeState {
		if noDispatch {
			return server.ZCodeState{State: "off", Detail: "成员调度已通过 --no-dispatch 停用"}
		}
		if pool.RetryDue() {
			tryDispatch(false)
		}
		fail, alive, ready, since := pool.Health()
		switch {
		case !alive && fail == "missing":
			// 全盘搜索在后台跑着：这不是 missing，是还在找——门面
			// 报 booting＋搜索文案，别把失败面板糊用户脸上
			if zcode.SweepInProgress() {
				return server.ZCodeState{State: "booting", Detail: i18n.S("正在全盘搜索 ZCode（首次最多约 90 秒，找到后将记住位置，下次秒进）")}
			}
			// 自检附注让失败截图自带现场（扫到几处、注册表有无记录），
			// 版本尾巴钉死构建来源——两者兼作报错的溯源指纹
			return server.ZCodeState{State: "missing", Detail: fmt.Sprintf("%s%s［v%s］", zcode.BundleHomes()[0], zcode.MissingSelfCheck(), version)}
		case !alive && fail != "":
			return server.ZCodeState{State: "error", Detail: fmt.Sprintf("%s——请处理后重启应用［v%s］", fail, version)}
		case !alive:
			if !pool.EverLive() {
				// 第一个孩子还在出生路上（spawn 现在要过完账号面推送才
				// 落账）：这是启动，不是死亡——别拿「已退出」吓人
				return server.ZCodeState{State: "booting", Detail: "ZCode app-server 启动中"}
			}
			// the pool watchdog is already re-spawning the child (10s
			// throttle): this is a recoverable blip, not a reason to
			// restart the app
			return server.ZCodeState{State: "error", Detail: fmt.Sprintf("ZCode app-server 已退出——正在自动重启，无需重启应用［v%s］", version)}
		case ready:
			// 启动编制先于一切进门仪式：旗舰岗位（编排者/HR）没招满，
			// 门保持关——招聘进度上墙，编制满员（或招聘器从未启用）
			// 才继续走预热/开门。
			if rp := recruitGate(); !rp.Done {
				detail := "正在招聘启动编制"
				if rp.Current != "" {
					detail = "正在招聘 " + rp.Current
				}
				if rp.Blocked != "" {
					detail = "招聘受阻：" + rp.Blocked + "——持续重试中"
				}
				return server.ZCodeState{State: "recruiting", Detail: detail,
					Recruit: &server.RecruitView{
						Filled: rp.Filled, Total: rp.Total,
						Current: rp.Current, Blocked: rp.Blocked,
					}}
			}
			if warming, since, phase := warmGate(); warming && since < warmGateMax {
				// the bridge just went live and the 小助手 is mid
				// warm-up: the door holds these extra beats so the
				// host enters to an already-warm channel — the
				// elapsed count AND the current leg ride Detail so
				// the splash can show the wait is moving and WHERE
				// it's moving (create = bridge/app-server leg,
				// await = model link leg)
				if phase == "" {
					phase = "预热中"
				}
				return server.ZCodeState{State: "warming",
					Detail: i18n.Sf("正在预热模型通道（已等待 %d 秒·%s）", int(since.Seconds()), i18n.S(phase))}
			}
			return server.ZCodeState{State: "ready", Detail: "ZCode app-server 已就绪"}
		case time.Since(since) > 30*time.Second:
			// alive but speechless for half a minute — a wedged child,
			// not a boot: surface it instead of spinning the splash
			return server.ZCodeState{State: "error", Detail: "ZCode app-server 30 秒内未响应——正在自动重试，无需重启应用"}
		}
		return server.ZCodeState{State: "booting", Detail: "ZCode app-server 启动中"}
	}

	// 小助手（assistant）：房主的使用顾问，住在工作台里回答「这间
	// 工作室怎么用」。深度绑定 ZCode 的红利在这里兑现——它和成员调
	// 度共用同一个 app-server 子进程与操作者的 provider 配置，零额
	// 外 AI key 接入。调度关闭时面板给出原因（不随 --no-dispatch 崩
	// 掉，只是不可用）。
	// The driver resolves THROUGH THE FLEET on every use (not a boot-
	// time snapshot): a ZCode installed while the splash door waits
	// brings the assistant alive with it, no restart needed.
	astDriverFn := func() dispatch.SessionDriver {
		if fleet == nil {
			return nil
		}
		return fleet.Driver()
	}
	astObserveFn := func(o *dispatch.Observer) {
		if fleet != nil {
			fleet.Observe(o)
		}
	}
	// 助手会话的 workspace 与侧栏行同源：大厅项目的冻结工作区（办公
	// 室自己的文件夹），退回启动时解析的稳定工作区。
	astWS := lobbyWS
	if p, ok := projStore.Get(chat.LobbyKey); ok && p.Workspace != "" {
		astWS = p.Workspace
	}
	ast = assistant.New(astDriverFn, astObserveFn, assistant.Config{
		Workspace: astWS,
		Model:     envOr("DISPATCH_MODEL", "GLM-5.3"),
		Reasoning: envOr("DISPATCH_REASONING", "high"),
		Manual:    kb.Manual(), // the embedded usage manual (the assistant's authority)
		// 固定识别：~/.niuma/assistant.json 记住小助手的会话 id，每次
		// 启动优先复用（侧边栏同一行原地置顶，不再重启一行），会话
		// 没了才新建并折掉旧行。
		SessionFile: assistant.DefaultSessionFile(),
		// r_17 终端转录：小助手的每一问/每一答落进大厅的转录册——
		// 终端板的大厅视图里与成员同列（「小助手」一行）。
		Term: func(e chat.TraceEntry) {
			hub.AppendTerm([]chat.TraceEntry{e})
		},
		// 通道预热：桥就绪即烧掉一次最小问答，把企业网络对陌生进程
		// 首批出站连接的冷启动惩罚消化在用户提问之前（NIUMA_WARMUP=
		// off 关闭——它每次启动多花一次极小的模型调用）。探针跑在独
		// 立的一次性会话上（裸文本、低推理档），真会话从不被它占用。
		Warmup: util.Env("NIUMA_WARMUP") != "off",
		// 探针自己的看门狗预算 == 启动门的封顶（NIUMA_WARMUP_GATE）：
		// 门等多久、探针就活多久——门开的那一刻预热必有结论（落地或
		// 判负）。隔离后提前放弃是安全的：迟到的探针回信落在没人听
		// 的一次性会话上，直接被丢。
		WarmupTimeout: warmGateMax,
		// 挂起预算可调：默认 12 分钟兜底，恶劣网络可用 NIUMA_TURN_
		// TIMEOUT（分钟，1-60）放宽。
		TurnTimeout: assistant.EnvTurnTimeout(util.Env("NIUMA_TURN_TIMEOUT")),
		// 每问必带的活体摘要：在线成员、活跃项目、任务量——背景信
		// 息，让「现在有谁在/开张了哪些项目」这类问题答得准。
		Digest: func() string {
			var lines []string
			if ms := hub.Members(); len(ms) > 0 {
				parts := make([]string, 0, len(ms))
				for _, m := range ms {
					parts = append(parts, m.Name+"（"+m.Role+"）")
				}
				lines = append(lines, "在线成员："+strings.Join(parts, "、"))
			}
			var actives []string
			for _, p := range projStore.List() {
				if p.Status == projects.StatusActive && p.Key != chat.LobbyKey {
					actives = append(actives, p.Name)
				}
			}
			if len(actives) > 0 {
				lines = append(lines, "活跃项目："+strings.Join(actives, "、"))
			}
			if engine != nil {
				lines = append(lines, fmt.Sprintf("任务台账：%d 条", engine.Count()))
			}
			if n, ok := noticeStore.Get(chat.LobbyKey); ok {
				first := strings.TrimSpace(strings.SplitN(n.Content, "\n", 2)[0])
				if r := []rune(first); len(r) > 40 {
					first = string(r[:40]) + "…"
				}
				lines = append(lines, "Niuma_Studio 公告："+first)
			}
			return strings.Join(lines, "\n")
		},
	})
	ast.Warmup() // the bridge went live BEFORE ast existed — kick now (a no-op when it didn't)
	return dispatchSet{
		fleet: fleet, dispatchHTTP: dispatchHTTP, zcodeStatus: zcodeStatus,
		ast: ast, closing: &closing,
		closeBridge: func() {
			if pool != nil {
				pool.Close()
			}
		},
		interrupted: interrupted,
	}
}
