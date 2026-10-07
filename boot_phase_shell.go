package main

// boot_phase_shell.go — phase 4 of 4 (runShell): the exit/webview
// choreography, the ONE teardown ladder, the reset and rebuild
// handoffs. Split from boot_phases.go, zero logic edits — the ordering
// invariants live on the function below.

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/shell"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// runShell is the exit/webview choreography: the ONE teardown ladder,
// signal handling, the reset and rebuild handoffs, the interrupted-
// seats cards, the boot banner and the native window's main loop.
// INVARIANTS it owns:
//   - teardown order is fixed (fold sidebar rows → stop keepers → stop
//     fleet → close the ZCode child → srv.Close flushes snapshots
//     exactly once) and every exit path funnels through sync.Once;
//   - closing flips FIRST on any exit, so the boot gate's retry and
//     the watchdog can never respawn the child behind a closing room;
//   - the reset path snapshots BEFORE wiping (srv.Close), then fuses
//     further snapshots — a wiped disk must never receive the old
//     room back.
func runShell(d shellDeps) {
	fleet, ast, keepers := d.fleet, d.ast, d.keepers
	closing, closeBridge, srv := d.closing, d.closeBridge, d.srv
	hub, registry := d.hub, d.registry
	resetCh, rebuildCh, staffStore := d.resetCh, d.rebuildCh, d.staff
	interrupted, devOn, devDir, ownerName := d.interrupted, d.devOn, d.devDir, d.ownerName

	// v0.6 graceful takeover → v2 P6: SIGTERM from a taking-over
	// instance means "announce, hold the room open briefly, close" —
	// the members get a readable notice instead of a blind drop
	// (server.GracefulRestart). SIGINT (a human's Ctrl+C) is a clean
	// stop: flush the snapshots through srv.Close and exit — the same
	// close path a window close takes. The process exits from the
	// goroutine in both cases (the AppKit loop cannot be torn down
	// portably from a signal handler).
	//
	// Every exit path first folds our ZCode sidebar rows (牛马＋小助
	// 手) — 开着＝置顶在岗，关了＝归档收摊；接管/重启的下一棒会在
	// 收编时各自重新登记，侧栏不留「运行中」的幽灵行。
	foldSidebarRows := func() {
		if fleet != nil {
			fleet.ArchiveTaskIndexes()
		}
		ast.Archive()
	}
	// The ONE exit choreography (企业级收摊纪律：先停生产者，再拆传输
	// 层，落盘恰好一次), funneled through sync.Once because every exit
	// path — window close, Ctrl+C, a takeover's SIGTERM — funnels here
	// and exactly one wins. Order matters:
	//   1. fold the ZCode sidebar rows while the dispatchers still know
	//      their members (开着＝置顶在岗，关了＝归档收摊);
	//   2. stop the keepers (no alarm may fire from a half-dead room);
	//   3. stop the fleet — patrol/daily/meeting clocks die with their
	//      dispatchers;
	//   4. close the ZCode app-server child (stdin → SIGTERM → 3s →
	//      SIGKILL): member sessions and the 小助手 ride this child, so
	//      no node process outlives the window — this step used to be
	//      missing and orphans kept running after「关闭」;
	//   5. srv.Close flushes the room snapshots and releases the port.
	// Every step is bounded (3s kill ladder, 2s HTTP drain), so a quit
	// cannot hang. closing flips FIRST: the boot gate's 10s retry must
	// not respawn the child behind a closing room.
	teardown := func() {
		// step 0: drain every live room's pending history lines — the
		// farewell flush must land BEFORE the room stops answering and
		// the process exits (the ring dies with the process; the file is
		// what the next boot replays).
		registry.FlushAllHistory()
		foldSidebarRows()
		if keepers != nil {
			keepers.Stop()
		}
		if fleet != nil {
			fleet.Stop()
		}
		closeBridge()
	}
	var shutdownOnce sync.Once
	shutdown := func() {
		closing.Store(true)
		shutdownOnce.Do(func() {
			teardown()
			srv.Close()
		})
	}
	sigRecv := make(chan os.Signal, 1)
	signal.Notify(sigRecv, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		switch sig := <-sigRecv; sig {
		case syscall.SIGTERM:
			// takeover: fold everything but the room itself, then let
			// GracefulRestart announce, hold the door for the grace
			// delay and flush the snapshots on its srv.Close — the next
			// instance re-registers every member on boot.
			log.Printf("SIGTERM: graceful restart requested by a new instance")
			closing.Store(true)
			shutdownOnce.Do(teardown)
			server.GracefulRestart(hub, srv, server.TakeoverGraceDelay)
		default:
			log.Printf("%v: closing the room", sig)
			shutdown()
		}
		os.Exit(0)
	}()

	// 工厂重置编排（POST /reset 的后半，设置卡「重置工作室」）：与退出
	// 共用同一条收摊纪律，顺序更苛——快照落盘（srv.Close 会把房间快照
	// 写回 ~/.niuma）必须发生在擦除之前，否则刚擦干净的地上又落一层
	// 旧账；ZCode 侧的成员痕迹必须真删（归档≠删除，桌面端历史里照样
	// 看得见）；替身进程必须等端口让出之后才拉起（它起来就是全新一间
	// 工作室，多开铁律与接管流程一步都不绕）：
	//   0. 先采集成员会话 id（编制表绑定＋小助手会话钉）——擦除后这些
	//      记录就没了，而 ZCode 侧的深清恰恰要靠它们圈定自己出生的行；
	//   1. closing 先封复产（重置路上不得再拉 ZCode 子进程）；
	//   2. shutdownOnce.Do(teardown)——停看护、停调度、关 ZCode 子进程
	//      （子进程先关，后面的会话库写入不与它抢锁）；
	//   3. srv.Close 落盘快照、让出端口、摘发现文件；随后熔断快照——
	//      擦除后任何路径（关窗/SIGTERM/defer）都不得把旧房间写回净盘；
	//   4. ZCode 侧深清：任务索引行（dhEmployee 标记 ∪ 已知会话 id ∪
	//      【项目】名-岗-Lv.N 标题契约三重圈定，房主自己的行一根不碰）
	//      → 会话正文（cli/db/db.sqlite 按 id 级联删）→ per-session
	//      文件（rollout/exec/image-cache）；
	//   5. WipeStudioData 擦除 ~/.niuma*（台账/房间/历史/档案/编制/
	//      公告/技能与 MCP 库/座位令牌……）——下一棒启动按首装重新播种；
	//   6. RelaunchSelf 拉起替身，随后本进程退场；拉不起来也只是少
	//      了自动重启，数据已然重置，手动打开即全新状态。
	go func() {
		<-resetCh
		log.Printf("reset: 工作室重置开始——收摊、清 ZCode、擦除、重启")
		harvest := harvestStudioSessions(staffStore)
		closing.Store(true)
		shutdownOnce.Do(teardown)
		srv.Close()             // 最终落盘：旧数据的告别快照（此后不再有第二次）
		srv.SuppressSnapshots() // 熔断：净盘上不得再落任何旧房间
		purged, perr := zcode.PurgeStudioTasks(harvest)
		if perr != nil {
			log.Printf("reset: ZCode 任务索引清理——%v（残留行在桌面端历史可见）", perr)
		}
		ids := unionIDs(harvest, purged)
		if serr := zcode.PurgeStudioSessions(ids); serr != nil {
			log.Printf("reset: ZCode 会话正文清理——%v（残留会话不再出现在列表，仅在库里）", serr)
		}
		if ferr := zcode.PurgeSessionFiles(ids); ferr != nil {
			log.Printf("reset: ZCode 会话文件清理——%v", ferr)
		}
		log.Printf("reset: ZCode 侧已清 %d 个成员/小助手会话", len(ids))
		removed, werr := util.WipeStudioData()
		log.Printf("reset: 已擦除 %d 项店铺数据（%s）", len(removed), strings.Join(removed, "、"))
		if werr != nil {
			log.Printf("reset: 擦除有残留——%v（替身启动按缺失项重新播种）", werr)
		}
		if err := util.RelaunchSelf(); err != nil {
			log.Printf("reset: %v——请手动重新打开应用（数据已重置）", err)
		}
		os.Exit(0)
	}()

	// 重新编译并重启（POST /rebuild 的后半，设置卡「重新编译并重启」）：
	// 编译与换位已在 HTTP 面完成——失败的到不了这里，旧进程从未被打扰。
	// 这里与重置共用同一条收摊纪律，只是没有擦除：通告已由 HTTP 面落
	// 历史 → 收摊（折侧栏行/停看护/停调度/关 ZCode 子进程）→ srv.Close
	// 落盘快照、让出端口 → RelaunchSelf 拉起新二进制（此刻端口已净，
	// 无多开之虞、无接管舞步）→ 本进程退场。正在干活的成员随告别快照
	// 进中断接续卡，重启后点名即接续——与任何一次手动重启同一语义。
	go func() {
		<-rebuildCh
		log.Printf("rebuild: 新二进制已就位——收摊、落盘快照、拉起替身")
		closing.Store(true)
		shutdownOnce.Do(teardown)
		srv.Close()
		if err := util.RelaunchSelf(); err != nil {
			log.Printf("rebuild: %v——请手动重新打开应用（新二进制已换位就绪）", err)
		}
		os.Exit(0)
	}()

	// restart observability: everyone still connected (the local player)
	// sees WHY the room just blinked; reconnecting members' watchdogs
	// fill the seats back within seconds — the sentinel's
	// post-restart roster check catches anyone who doesn't.
	// SystemRecorded 而非 System：重启通告必须落历史——否则告别的
	// 「将于 3 秒后重启」永远是房间最后一条，侧栏预览一直像在重启。
	hub.SystemRecorded(fmt.Sprintf("办公室服务已重启（v%s）——调度成员将由调度器自动归位，其余 2 分钟未归由看护按编制核对", version))

	// CLI 可达性自检：在座成员拿的是绝对路径（agents.SelfExe 进提示词）
	// 不受影响，但房主自己的终端与自开的 AI 会话只能靠 PATH 找 niuma——
	// 缺席时那些会话只能直读 ~/.niuma 猜状态（book 项目一次实录：新会话
	// 回「niuma CLI 未安装，改从数据目录直读」）。环境事实要在撞上之前
	// 被告知，不是之后：启动探测一次，缺席即播一条带装法的通告。
	if util.NiumaOnPath() == "" {
		tpl, args := util.CLIMissingNotice(agents.SelfExe())
		hub.SystemRecorded(i18n.Sf(tpl, args...))
		log.Printf("boot: PATH 上没有 niuma 命令——终端/外部会话无法直连本工作室（成员走绝对路径不受影响）")
	}

	// v2.5 中断接续提示：告别快照抓到有人正在干活时工作室退场——那
	// 一轮随子进程死掉、重启不重放（结果未知宁可弃单），但用户不该
	// 靠自己想起来。v2.12 卡跟房走：席位按去向分组，哪间房的活儿断
	// 了卡就播在哪间房（项目隔离同口径——大厅不再替各项目挡话，大
	// 厅只收大厅自己的席位；各房消息照挂侧栏徽标，房主开屏也看得见
	// 哪房有接续）。前端把每个名字渲染成「让 TA 继续」——替房主向
	// TA 所在的房间发一条点名，接不接由成员会话自己拿上下文续。
	// 重启自动接续：中断名单现在直接喂 fleet——调度器替房主把接续点
	// 名逐人送上（车道有停车线的走车道，空车道的注入一条接续线），
	// 卡片降级为兜底手动通道。桥还没 Attach 的（启动时 ZCode 未就绪）
	// 名单先存桩，房起调度器时 start 自己消费。
	if len(interrupted) > 0 {
		if fleet != nil {
			fleet.ContinueInterrupted(interrupted)
		}
		hint := "点名即可让他们接着干"
		if fleet != nil && fleet.HasBridge() {
			hint = "已自动替你逐一点名接续；若有人迟迟未接上，可再点名催一次"
		}
		registry.PostInterruptedCards(interrupted, func(names []string) string {
			return fmt.Sprintf("上次退出时 %s 正在干活，本轮已中断（对话记忆完好）——%s",
				strings.Join(names, "、"), hint)
		})
	}

	fmt.Printf("牛马工作室 niuma v%s\n", version)
	fmt.Printf("  room   : %s\n", srv.Addr())
	fmt.Printf("  you    : %s\n", ownerName)
	fmt.Printf("  app    : 一个窗口即整个应用——办公室 + 牛马/项目/对话/黑板管理（关闭窗口即退出）\n")
	fmt.Printf("  agents : %s say --name Bot \"hello\"    (one-shot)\n", exeName())
	fmt.Printf("           %s wait --name Bot --json      (block until @Bot)\n", exeName())
	fmt.Printf("  ai     : 工作台「办公室黑板 → AI 接入」一键复制提示词（curl 127.0.0.1:%d/prompt 同文）\n", srv.Port)
	fmt.Printf("  kb     : 工作台「办公室黑板」板块——说明书/公告/手册文档（可编辑）\n")
	if devOn {
		fmt.Printf("  dev    : NIUMA_DEV=1 —— web 资源直读磁盘（%s），改完存盘窗口自动重载，免编译免重启\n", devDir)
	}
	fmt.Printf("  task   : %s task list                   (任务台账：谁在干什么)\n", exeName())
	fmt.Printf("           %s task create --name %s --title \"...\" --assignee Bot\n", exeName(), ownerName)
	fmt.Printf("  close the app window or Ctrl+C here to quit.\n\n")

	// v2 P6: the app face is ONE native window over this process's own
	// /app — management boards and the pixel room's canvas board in
	// one page. Closing the window returns here and the deferred
	// srv.Close flushes the room snapshot. The app starts as the app
	// and only as the app, on every platform (Cocoa on macOS, WebView2
	// on Windows, WebKitGTK on Linux — 和 ZCode 一样): headless serving
	// is gone, a window-face failure is fatal (no silent fallback), and
	// the deprecated --no-window changes nothing.
	if err := shell.RunMainWindow(srv.Port); err != nil {
		log.Printf("app window: %v — 无头模式已移除，无法以应用方式启动", err)
		shutdown()
		os.Exit(1)
	}
	// 关窗即退：走同一条退出编排（折侧栏行 → 停看护 → 停调度 → 关
	// ZCode 子进程 → 落盘快照、让出端口）——不留半只进程，下一次
	// 启动（或 rebuild 后的重启）拿到的永远是干净现场。
	shutdown()
}
