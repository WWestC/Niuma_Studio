package main

// boot_phase_stores.go — phase 1 of 4 (openStores): every domain store
// plus the owner's lobby seat. Split from boot_phases.go, zero logic
// edits — the ordering invariants live on the function below.

import (
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/media"
	"github.com/WWestC/Niuma_Studio/plugins"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// openStores boots every domain store (agents → media) plus the owner's
// lobby seat. INVARIANTS it owns:
//   - it runs BEFORE bootDispatch and the server face — the fleet, the
//     keepers and the Options literal all drink from its storeSet;
//   - the owner seat joins BEFORE the server starts so /kb/people can
//     flag the host on the very first request;
//   - the tasks corrupt-reset path may hub.System() — hub must exist
//     (main builds it first), and a reset engine is announced, never
//     silently empty;
//   - every store degrades to in-memory on failure — the room must
//     boot (the standing discipline, one log line each);
//   - STORE OPENS RUN AS PARALLEL LEGS (Phase 1): the mutually
//     independent open families (agents/staffing/capability/plugins/
//     docs/the six-domain ledger family) each
//     open on their own goroutine — they share nothing but the log.
//     Each leg is one function in boot_stores.go (signature = the
//     dependency contract, per-store failure disciplines in situ);
//     the serial tail's heavy steps (seatLocalOwner / openTaskEngine /
//     convergeEstablishmentTables) live there too.
//     The DEPENDENCY EDGES stay serial, in order, after the legs join:
//     the owner join waits for staffing (hub.LookFn reads it), the
//     task engine waits for the join (it stamps the owner's name) and
//     for projStore (SetProjectKeys), the kb convergence migrations
//     wait for docs+projStore, and the cross-store migration chain
//     (RefileProjectMinutes → RenumberIsolatedLedgers) waits for every
//     store it rewrites. bootStoresParallel=false (the timing test's
//     serial baseline) runs the same legs inline.
func openStores(projStore *projects.Store, registry *chat.Registry, hub *chat.Hub, role string) storeSet {
	var (
		agentStore  *agents.Store
		staffStore  *staffing.Store
		lib         *capability.Store
		audit       *capability.AuditLog
		pluginStore *plugins.Store
		docs        *kb.DocsStore
		ledgers     ledgerStores
	)
	// 八条开店腿一函数一仓（boot_stores.go），签名即依赖契约；腿间共享
	// 的只有日志。包装闭包各写各的结果变量——不交集，并行无竞态。
	legs := []func(){
		func() { agentStore = openAgentsLeg() },
		func() { staffStore = openStaffingLeg(hub, registry) },
		func() { lib, audit = openCapabilityLeg() },
		func() { pluginStore = openPluginsLeg() },
		func() { docs = openDocsLeg() },
		func() {
			l, err := openLedgersLeg(hub, defaultName(), multiuserEnabled())
			if err != nil {
				log.Fatal(err) // 拒起类（旋钮退役/②类版本超前）：无回退架，响亮拒起
			}
			ledgers = l
		},
	}
	if bootStoresParallel {
		var wg sync.WaitGroup
		for _, leg := range legs {
			wg.Add(1)
			go func(run func()) {
				defer wg.Done()
				run()
			}(leg)
		}
		wg.Wait()
	} else {
		for _, run := range legs {
			run()
		}
	}

	// —— 串行尾：有依赖边的步骤按原次序走（每步的理由在其函数注释里）——
	discoFile, err := server.PortFilePath()
	if err != nil {
		log.Printf("discovery file unavailable: %v", err)
		discoFile = ""
	}

	// 房主先于服务面入座（/kb/people 首请求就能标出 host）；任务引擎
	// 等这次入座（它盖房主名）。
	ownerName := seatLocalOwner(hub, registry, role)

	engine := openTaskEngineNS(hub, ledgers.db, ownerName, ledgers.ident.subject)

	// v2 P4-a: the task write path's structural validation needs the
	// legal project set — active ∪ draft from the registry (archived
	// projects are frozen: a task attached to one refuses further
	// writes, which IS the 结项封存 semantics).
	engine.SetProjectKeys(func() []string {
		var keys []string
		for _, p := range projStore.List() {
			if p.Status == projects.StatusActive || p.Status == projects.StatusDraft {
				keys = append(keys, p.Key)
			}
		}
		return keys
	})

	// kb 收敛三迁（哨兵退役/必须列/项目编制地板）：等 docs＋projStore。
	convergeEstablishmentTables(docs, projStore)

	// v2.9 隔离追迁：历史评审纪要从大厅架（ops/meetings/）按需求归属
	// refile 回各项目的 p/<key>/meetings/ 架——新纪要已跟房落键，这里
	// 只搬存量；归属不可考的（自由议题、需求已删）如实留在大厅架。
	if moved := server.RefileProjectMinutes(docs, ledgers.reqs, ledgers.ident.subject); len(moved) > 0 {
		log.Printf("minutes: 评审纪要已按项目归架（%d 篇：%s）", len(moved), strings.Join(moved, "、"))
	}

	// v2.11 号段完全重排：所有项目（大厅在内）的需求/会议/任务从各自
	// r_01/m_01/t_01 起紧凑重排，账内引用（提案 req 与文案、任务
	// req/parent/deps、纪要键、合并提案）、知识库正文、聊天历史全量
	// 改写跟着走；git 提交信息与终端转录里的旧号悬空属接受面。跑在
	// 纪要归架之后、聊天 hub 建房之前，幂等可重跑——等全部仓。
	histDir := ""
	if root := v2Root(); root != "" {
		histDir = filepath.Join(root, "history")
	}
	if report := server.RenumberIsolatedLedgers(docs, ledgers.reqs, ledgers.meets, engine, projStore, ledgers.plans, ledgers.merges, histDir, ledgers.ident.subject); len(report) > 0 {
		log.Printf("isolate: 号段完全重排完成（%d 笔：%s）", len(report), strings.Join(report, "；"))
	}

	// 输入图片的媒体仓库（~/.niuma/media/）：聊天发图的落盘点。
	// POST /media 存、GET /media/{id} 取、调度器注入时还原路径作
	// session/send 附件。工作室级一份，不按房分（归属关系在聊天
	// 历史里）。
	mediaStore := media.NewStore(filepath.Join(v2Root(), "media"))

	return storeSet{
		agentStore: agentStore, staff: staffStore, lib: lib, audit: audit,
		pluginStore: pluginStore, engine: engine, docs: docs,
		plans: ledgers.plans, merges: ledgers.merges, reqs: ledgers.reqs, meets: ledgers.meets,
		notices: ledgers.notices, media: mediaStore, discoFile: discoFile,
		ownerName: ownerName, ledgerDB: ledgers.db, ident: ledgers.ident,
	}
}
