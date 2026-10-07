// Niuma Studio（牛马工作室）— a pixel studio where every employee
// is an AI agent: local chat rooms, tasks, staffing and meetings in
// one binary (2D pixel-person UI).
//
// Running with no arguments starts the app — and it only ever starts
// as the app, on every platform (macOS / Windows / Linux, each with
// its native window, 和 ZCode 一样; there is no headless mode).
// Subcommands (say / listen / members) turn the same binary into an
// agent-facing CLI.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/cli"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/shell"
	"github.com/WWestC/Niuma_Studio/util"
)

// version is stamped at build time — build.sh / build.bat / CI pass
// -ldflags "-X main.version=$(git describe …)" so the git tag is the
// single source of truth. "dev" means a plain go build / go run.

func main() {
	log.SetFlags(log.Ltime)

	// desktop_harness → niuma: one best-effort rename of every home
	// path (the root dir, the flat ledgers, seat tokens, wait cursors)
	// before any store opens — see util.MigrateLegacyPaths. App mode
	// and every CLI subcommand pass through here first.
	util.MigrateLegacyPaths()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "say", "report", "listen", "wait", "dispatch", "mirror", "ack", "kick", "rank", "offboard", "agent", "recall", "members", "task", "plan", "meeting", "req", "kb", "recruit", "capability", "skill", "mcp", "assemble", "drive", "plugin", "vcs", "autopilot", "help", "-h", "--help", "version", "-v", "--version":
			if os.Args[1] == "version" || os.Args[1] == "-v" || os.Args[1] == "--version" {
				fmt.Println("niuma", version)
				return
			}
			os.Exit(cli.Run(os.Args[1:]))
		default:
			// a typo'd subcommand must not silently start the game:
			// GUI mode takes the fixed port over, killing whatever room
			// is running — it burned a live room today (t_28 debugging).
			// Flags still fall through; only unknown bare words stop here.
			if !strings.HasPrefix(os.Args[1], "-") {
				fmt.Fprintf(os.Stderr, "niuma: unknown command %q (no arguments starts the game)\n", os.Args[1])
				fmt.Fprintln(os.Stderr, "usage: niuma <say|report|listen|wait|dispatch|mirror|kick|rank|members|task|plan|kb|plugin> [flags]")
				os.Exit(2)
			}
		}
	}

	fs := flag.NewFlagSet("niuma", flag.ContinueOnError)
	// 房主名恒取本机用户名（ZCode 以该用户运行）——名字不得再被启动
	// 参数改掉（曾出现过叫「房主」的房主）。--name 留在旗标位上只为
	// 老启动脚本不炸（ContinueOnError 遇未知旗标会退出），值被忽略。
	_ = fs.String("name", defaultName(), "deprecated: ignored — the host is always named after the local user")
	role := fs.String("role", "人类玩家", "your identity shown under your name")
	port := fs.Int("port", 7777, "fixed port (never falls forward; a stale niuma holding it is terminated and the port taken over). Multi-open is forbidden: a second instance on another port is refused while one is running")
	forceTakeover := fs.Bool("force-takeover", false, "restart a healthy room holding the port from a non-interactive start (a terminal restart never needs this)")
	noWatch := fs.Bool("no-watch", false, "disable the in-instance establishment watch keeper (v0.10 seat-M4)")
	noDispatch := fs.Bool("no-dispatch", false, "disable the in-instance member dispatcher (v1.0: routes room lines into member ZCode sessions)")
	noAutoPilot := fs.Bool("no-autopilot", false, "disable the 全智能模式 engine (v2.7: auto-accepts aged proposals and pokes idle orchestrators; the per-project switch then reads/writes but never executes)")
	// --no-window 已废弃：应用只以应用窗口的形态启动，无头服务模式已
	// 移除。旗标留在旗标位上只为老启动脚本不炸（ContinueOnError 遇未
	// 知旗标会退出），值被忽略——传了它窗口照开。
	_ = fs.Bool("no-window", false, "deprecated: ignored — the app always opens its window (headless serving was removed)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	// 界面语言（中/英）尽早恢复：下方的双击聚焦/拒绝弹窗是用户在
	// lang.json 已存在时看到的第一面，文案语言必须已经就位（原装配
	// 点在调度器之前，这里提前到旗标解析后；幂等——SetLang 同值无害，
	// 后续装配点照旧再读一次）。文件缺席（""）＝中文默认。
	if l := i18n.LoadLang(filepath.Join(v2Root(), "lang.json")); l != "" {
		i18n.SetLang(l)
	}

	// v2 P6 single-process app (the P5-c dual-window spike retired
	// with the Ebiten window): one binary, one process — the room and
	// its one native window over /app. A bundle relaunch (double-click)
	// against an already-running healthy room focuses that app instead
	// of taking the room over — probe first, never a second one.
	if shell.InBundle() {
		if port, err := server.DiscoverPort(); err == nil {
			shell.FocusApp(port)
			// 双击者的 stdout 是黑洞：聚焦之外再发一条系统横幅——聚焦
			// 失败（Space 切走了/权限拒了）时这是用户能得到的唯一告知
			shell.UserNotice(i18n.S("牛马工作室已在运行"), i18n.S("已为你聚焦既有窗口——本机同时只开一间工作室"))
			fmt.Printf("niuma 已在运行：http://127.0.0.1:%d/app —— 已聚焦既有窗口\n", port)
			return
		}
	}

	// 禁止多开（单实例铁律）：同一台机器只允许一间工作室。发现文件
	// 记录的本机活实例若占着别的端口（有人用 --port 侧漏），直接拒绝
	// 启动——两个并行实例会共写同一套店铺文件，静默损坏台账。占的
	// 正是我们要的端口则不放行到这里：下方 listenPort 的接管流程就是
	// 重启语义（announce → 交接快照 → 收摊），恰好收敛为一个实例。
	if recPort, recPID, ok := server.OtherLiveInstance(*port); ok {
		fmt.Fprintf(os.Stderr, "niuma：本机已有一间工作室在运行（http://127.0.0.1:%d/app，pid %d）——禁止多开；请先关掉它，或改用端口 %d 启动以接管重启。\n", recPort, recPID, recPort)
		if shell.InBundle() { // 双击启动的 stderr 同样是黑洞——拒绝也得让用户看得见
			shell.UserNotice(i18n.S("牛马工作室启动被拒"),
				i18n.Sf("本机已有一间在运行（端口 %d）——请打开 Dock 上的既有窗口", recPort))
		}
		os.Exit(1)
	}

	// v2 P3-a: the room set is a ProjectRegistry — one Hub per active
	// project plus the lobby (default), the anchor of everything below
	// (the server's own hub, the Ebiten window, the dispatcher, the
	// watch keeper, the mirror channel, the CLI default route). Room
	// storage moves to the v2 root (~/.niuma/history|room/
	// <key>.*), empty-start per the v2 constitution: the v1 flat files
	// (~/.desktop_harness_history.json / _room.json) are neither read
	// nor migrated.
	projStore := openProjectStore()
	lobbyWS := stableWorkspace()
	if _, err := projStore.EnsureLobby(lobbyWS); err != nil {
		log.Printf("projects lobby: %v — 办公室照常运行", err)
	}
	// The registry seats the local human into every project room it
	// instantiates (lobby parity: a project room differs in data only).
	// The lobby's own owner seat main joins below, pre-server-start.
	registry := chat.NewRegistry(projStore, v2Root(), defaultName(), *role)
	if _, err := registry.AdoptActive(); err != nil {
		log.Printf("project rooms: %v — 仅已实例化办公室可用", err)
	}
	hub, err := registry.Lobby()
	if err != nil {
		log.Fatalf("lobby hub: %v", err)
	}
	// 终身层形象（r_05 t_126）：hub 的 LookFn 从 staffing 读档；无档
	snapshotPath := registry.SnapshotPath(chat.LobbyKey)

	// ---------- the ordered composition (phases live in boot_phases.go) ----------
	// 顺序即契约：每一段的签名就是它的依赖面，微妙次序的理由在各阶段
	// 函数的文档注释里（快照先于 fleet Attach、语言先于首个弹窗……）。
	st := openStores(projStore, registry, hub, *role)

	var srv *server.Server // the fleet's RebuildFire leg reads it through &srv
	dp := bootDispatch(hub, registry, projStore, lobbyWS, snapshotPath, st, *noDispatch, &srv)
	sf := startServerFace(*port, *forceTakeover, *noAutoPilot, hub, registry, projStore, snapshotPath, st, dp)
	srv = sf.srv

	// seat-M2: the seat snapshots were restored BEFORE the fleet and the
	// listener came up (see the block above StartFleet) — the seats the
	// snapshots caught MID-TURN roll up here (v2.5): the restart never
	// replays those turns (unknown-outcome discipline), but the owner
	// gets one continue prompt naming them — see the card after the
	// restart notice below.

	// v0.10 seat-M4 → v2 P5-a: the establishment keeper set — one
	// reconciliation per active project plus the lobby (tables at
	// p/<key>/establishment, the lobby keeping ops/establishment).
	// Shortfalls past the presence grace alarm in the LOBBY via
	// SystemRecorded; 自动补员 posts under a project whose staffing
	// auto_recall switch is on refill through the dispatch Recall path
	// (Birth's own formula). NIUMA_WATCH_EVERY retunes the beat;
	// --no-watch opts out entirely. The handle is kept for the exit
	// choreography (a quit stops the clocks before tearing the fleet
	// down — no alarm fired from a half-dead room).
	var keepers *server.Keepers
	if !*noWatch {
		keepers = server.StartKeepers(server.KeeperConfig{
			LobbyHub: hub, Registry: registry, Projects: projStore,
			Docs: st.docs, Agents: st.agentStore, Staff: st.staff,
			Recall: func(project, name string) bool {
				if dp.fleet == nil {
					return false
				}
				if project == chat.LobbyKey {
					return dp.fleet.Recall(name)
				}
				if d := dp.fleet.Get(project); d != nil {
					return d.Recall(name)
				}
				return false
			},
			Every: server.WatchEveryFromEnv(),
		})
	}
	runShell(shellDeps{
		fleet: dp.fleet, ast: dp.ast, keepers: keepers, closing: dp.closing,
		closeBridge: dp.closeBridge, srv: srv, hub: hub, registry: registry,
		resetCh: sf.resetCh, rebuildCh: sf.rebuildCh, staff: st.staff,
		interrupted: dp.interrupted, devOn: sf.devOn, devDir: sf.devDir,
		ownerName: st.ownerName,
	})
	return
}
