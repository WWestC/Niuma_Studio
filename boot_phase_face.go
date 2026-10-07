package main

// boot_phase_face.go — phase 3 of 4 (startServerFace): assembling
// server.Options and starting the listener. Split from boot_phases.go,
// zero logic edits — the ordering invariants live on the function
// below.

import (
	"log"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/util"
)

// startServerFace assembles server.Options and starts the listener.
// INVARIANTS it owns:
//   - it runs AFTER openStores and bootDispatch (the Options literal
//     drinks storeSet + dispatchSet) and AFTER the seat snapshots
//     restored (the listener answering observers is the deadline);
//   - srv is written back into main's slot right after this returns —
//     the fleet's RebuildFire leg and the shell's teardown both read
//     it through that slot.
func startServerFace(port int, forceTakeover, noAutoPilot bool,
	hub *chat.Hub, registry *chat.Registry, projStore *projects.Store, snapshotPath string,
	st storeSet, dp dispatchSet) faceSet {

	fleet, dispatchHTTP, zcodeStatus, ast := dp.fleet, dp.dispatchHTTP, dp.zcodeStatus, dp.ast
	agentStore, engine, docs := st.agentStore, st.engine, st.docs
	staff, lib, pluginStore := st.staff, st.lib, st.pluginStore
	plans, merges, reqs, meets := st.plans, st.merges, st.reqs, st.meets
	notices, media, discoFile, ownerName := st.notices, st.media, st.discoFile, st.ownerName
	projStore, audit := projStore, st.audit
	// 台账事务缝（转正硬条件①）：库在＝accept 三腿一事务；内存 boot
	// 恒 nil（暂存写落在零缝上，内存即真源）。
	var ledgerTx func(fn func(server.LedgerSeam) error) error
	if st.ledgerDB != nil {
		db := st.ledgerDB
		ledgerTx = func(fn func(server.LedgerSeam) error) error {
			return db.Tx(func(tx *sqlstore.TxStores) error { return fn(tx) })
		}
	}

	devFS, devOn, devDir := devWebRoot()
	// 重置信号线（POST /reset 的前半）：设置卡「重置工作室」确认后，
	// HTTP 面只投一枚令牌进来——编排本体挂在下面的 goroutine（等
	// teardown/srv 都已就位才诞生）。缓冲 1：重复请求在 HTTP 面就拿
	// 409，不排第二次队。
	resetCh := make(chan struct{}, 1)
	// 重新编译重启信号线（POST /rebuild 的前半）：设置卡「重新编译并
	// 重启」确认后，HTTP 面在前台跑完 go build 并把新二进制原子换位
	// （失败旧进程原样活着，应用零感知），成功才投一枚令牌进来——下面
	// 的 goroutine 只做交接。缓冲 1：交接中的重复请求在 HTTP 面就拿
	// 409，不排第二次队。
	rebuildCh := make(chan struct{}, 1)
	// 信任模型的可部署腿（gate.go）：NIUMA_ADDR 把监听端出去（LAN IP
	// 或 0.0.0.0），NIUMA_GATE_TOKEN 武装令牌门——回环访问照旧零摩
	// 擦，远端必须持令牌。远程绑定而未给令牌时自动铸一枚写进
	// ~/.niuma_gate_token（0600）并打进日志；server.Start 的哨兵对
	// 「远程绑定且无令牌」fail-close，这里保证正常路径永远到不了那。
	bindAddr := util.Env("ADDR")
	gateToken := util.Env("GATE_TOKEN")
	if bindAddr != "" && !loopbackBind(bindAddr) && gateToken == "" {
		if tok, terr := server.MintGateToken(); terr == nil {
			if path, perr := server.GateTokenPath(); perr == nil {
				if serr := server.SaveGateToken(path, tok); serr == nil {
					gateToken = tok
					log.Printf("访问令牌：远程绑定 %s 已自动铸令牌，写入 %s（0600）。远端访问携带 Authorization: Bearer <令牌> 或 ?token=<令牌>；本机回环访问不变", bindAddr, path)
				} else {
					log.Printf("访问令牌落盘失败（%v）——远程绑定将因无令牌被拒绝", serr)
				}
			}
		}
	}
	srv, err := server.Start(hub, server.Options{
		Endpoint: server.EndpointConfig{
			PreferredPort: port,
			ForceTakeover: forceTakeover,
			Version:       version,
			DiscoveryFile: discoFile,
			SnapshotPath:  snapshotPath,
			Heartbeat:     25 * time.Second,
			BindAddr:      bindAddr,
			GateToken:     gateToken,
		},
		Stores: server.StoreSet{
			AgentStore:   agentStore,
			Engine:       engine,
			Docs:         docs,
			Library:      lib,         // the skill & MCP faces (libraries, authoring switch, fabric preview)
			Plugins:      pluginStore, // the plugin face (v3 preview): /plugins.json + /plugins/<id>/ assets
			StaffStore:   staff,
			ProjectStore: projStore,
			Audit:        audit,
			PlanStore:    plans,    // plan verbs + /p/{key}/plan (v2 P4-b)
			MergeStore:   merges,   // merge verbs + /p/{key}/merge（v2.8 gitflow 合并评审门）
			Requirements: reqs,     // the plan gate's r_NN linkage (v2 P4-b)
			Meetings:     meets,    // the meeting room's read face (需求评审会议）
			Notices:      notices,  // the per-room group announcement (飞书式群公告)
			LedgerTx:     ledgerTx, // accept 路径三腿一事务（转正硬条件①：库在＝一提交全有或全无）
			MediaStore:   media,    // 输入图片：POST/GET /media（聊天发图）
		},
		Web: server.WebConfig{
			WebFS:     devFS,
			WebDev:    devOn, // NIUMA_DEV=1: disk assets + live-reload endpoint
			WebDevDir: devDir,
		},
		Faces: server.FaceConfig{
			DispatchHandler: dispatchHTTP,
			Assistant:       ast.HTTPHandler(), // the 小助手 panel's face (/assistant)
			ZCodeStatus:     zcodeStatus,       // the splash's boot gate (GET /zcode)
		},
		LocalName: ownerName,
		// 身份层（多用户形态）：登录/会话服务＋本机服务凭证（回环操
		// 作者自动入座之外的载体——CLI 指向远端实例时作 Bearer）。
		Auth:          st.ident.auth,
		OperatorToken: st.ident.serviceToken,
		// NIUMA_STRICT_LOOPBACK=1：收掉「回环即操作者」（本机 CLI 凭
		// 服务令牌照旧零摩擦，工作台窗口需登录）。
		StrictLoopback: util.Env("STRICT_LOOPBACK") == "1",
		Subject:        st.ident.subject,                      // storage namespace (multi-user: the admin account name)
		Fleet:          bootFleet{fleet: fleet, staff: staff}, // the server's whole fleet view (fleetapi.go's adapter)
		Registry:       registry,                              // hello.project routing (v2 P3-a)
		ResetCh:        resetCh,                               // factory reset: the settings card's danger zone (POST /reset)
		RebuildCh:      rebuildCh,                             // recompile-restart: the settings card's 维护区 (POST /rebuild)
		// 全智能模式引擎（v2.7）：按项目的 staffing 开关驱动——待审提案
		// 满宽限代房主接受（同一部落库腿，落款「自动驾驶」）、需求池低
		// 水位点名编排者补货蓄水（Poke 这条腿路由到该房调度器，忙闲皆
		// 然）、闲置且池有货走拆解注入（Divert 独立路由）、进行中任务停
		// 滞超阈点名负责人续做（Resume 同一条腿）、每日接受上限熔断。
		// --no-autopilot 整体停用（开关面仍可读写、只是无人执行）。
		// 环境旋钮：NIUMA_AUTOPILOT_EVERY/_ACCEPT/_POKE/_STALL/
		// _MAXPLANS。
		AutoPilot: func() *server.AutoPilotConfig {
			if noAutoPilot || fleet == nil {
				return nil
			}
			return &server.AutoPilotConfig{
				Poke:         fleet.AutoPilotPoke,
				Divert:       fleet.AutoPilotDivert, // r_17 拆解走自己的腿（旧误复用选题腿）
				Resume:       fleet.AutoPilotResume,
				Patrol:       fleet.AutoPilotPatrol, // r_13 巡报：在岗 HR 的产能巡报注入
				Every:        server.AutoPilotEveryFromEnv(),
				AcceptDelay:  server.AutoPilotAcceptDelayFromEnv(),
				PokeCooldown: server.AutoPilotPokeEveryFromEnv(),
				StallDelay:   server.AutoPilotStallDelayFromEnv(),
				MaxPlans:     server.AutoPilotMaxPlansFromEnv(),
			}
		}(),
	})
	if err != nil {
		log.Fatalf("start server: %v", err)
	}

	return faceSet{srv: srv, resetCh: resetCh, rebuildCh: rebuildCh, devOn: devOn, devDir: devDir}
}
