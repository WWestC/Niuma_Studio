package main

// boot_stores.go — openStores 的八条开店腿与串行尾的三个重步骤（房
// 主入座、任务引擎、编制表收敛）：一函数一仓，签名即依赖契约——与
// boot_phases.go 的四个阶段函数同一纪律。各仓的「为什么」注释随身
// 体从 openStores 搬来，一字未改；openStores 本体只剩编排与次序。

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/plugins"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// openAgentsLeg opens the AI onboarding configs (~/.niuma_agents.json).
func openAgentsLeg() *agents.Store {
	// AI onboarding configs survive restarts in ~/.niuma_agents.json
	agentPath, err := agents.DefaultPath()
	if err != nil {
		log.Printf("agents store path unavailable: %v", err)
		agentPath = ""
	}
	store, err := agents.Open(agentPath)
	if err != nil {
		log.Printf("agents store: %v — starting empty", err)
		store, _ = agents.Open("")
	}
	return store
}

// openStaffingLeg opens the staffing table and wires the hub's two
// staffing-backed lookups (LookFn/PausedFn) — it must run before the
// owner seat joins (hub.LookFn reads it at join time).
func openStaffingLeg(hub *chat.Hub, registry *chat.Registry) *staffing.Store {
	// v2 P3-b: the staffing table (~/.niuma/staffing.json) is
	// the member registry the per-project dispatchers consume — the
	// session binding's single truth. An unreadable file degrades to
	// in-memory exactly like the agents store; the room must boot.
	staffPath, err := staffing.DefaultPath()
	if err != nil {
		log.Printf("staffing store path unavailable: %v", err)
		staffPath = ""
	}
	st, err := staffing.Open(staffPath)
	if err != nil {
		log.Printf("staffing store: %v — 以内存编制表运行（重启后编制清空）", err)
		st, _ = staffing.Open("")
	}
	// 终身层形象（r_05 t_126）：hub 的 LookFn 从 staffing 读档；无档
	// 成员首次入座当场掷骰落档（SetLook 幂等＝终身锁定）；存量五成员
	// 锦定 F1 标准发型＋基色肤色，上线日无人「突然换脸」。
	hub.LookFn = lookServer(st)

	// 房间暂停的 welcome 水合线：每房 hub 的 PausedFn 读 staffing 设置
	//（闭包懒读——AdoptActive 已建好的房与之后才建的房一样吃到）。
	registry.PausedFn = func(projectKey string) bool {
		return st.SettingsOf(projectKey).Paused
	}
	return st
}

// openCapabilityLeg opens the skill & MCP library plus the assembly
// audit log.
func openCapabilityLeg() (*capability.Store, *capability.AuditLog) {
	// The skill & MCP library (~/.niuma/skills.json + mcps.json) and
	// the assembly audit (~/.niuma/audit/assembly.jsonl). Both degrade
	// exactly like the agents store — the room must boot. The one-shot
	// legacy migration folds the retired capability packs
	// (~/.niuma/capabilities.json, prompt/manual slots only) into
	// skills under the same keys, then renames the old file .bak —
	// profile/staffing references ride through the stores' own
	// load-time fold (missing keys degrade, the standing discipline).
	skillsPath, mcpsPath, err := capability.DefaultPaths()
	if err != nil {
		log.Printf("skill library path unavailable: %v", err)
		skillsPath, mcpsPath = "", ""
	}
	if err := migrateLegacyPacks(skillsPath); err != nil {
		log.Printf("旧能力包迁移失败（%v）——跳过，旧库原地不动", err)
	}
	l, lerr := capability.Open(skillsPath, mcpsPath)
	if lerr != nil {
		log.Printf("skill library: %v — 以内存技能库运行（重启后技能清空）", lerr)
		l, _ = capability.Open("", "")
	}
	auditPath, err := capability.DefaultAuditPath()
	if err != nil {
		log.Printf("assembly audit path unavailable: %v", err)
		auditPath = ""
	}
	return l, capability.OpenAudit(auditPath)
}

// openPluginsLeg opens the plugin shelf and replays the wardrobe
// contributions into the dice pool.
func openPluginsLeg() *plugins.Store {
	// v3 插件面（形态 A＋B）：~/.niuma/plugins/ 一目录一插件，启用态
	// ~/.niuma/plugins.json。开店永不失败（降级为空库，插件面 404 级
	// 静默）；boot 先把衣橱贡献并进掷骰池/渲染表——之后 /plugins.json
	// 每次请求重放（CLI 启停免重启生效），终身层语义不变：只有新
	// 面孔掷新衣，已落档的牛马不换脸。
	pluginRoot, pluginState, err := plugins.DefaultPaths()
	if err != nil {
		log.Printf("plugins store path unavailable: %v — 插件面停用", err)
		pluginRoot, pluginState = "", ""
	}
	store := plugins.Open(pluginRoot, pluginState)
	// 出厂预装（M2）：官方市场插件 niuma.market 铺进插件根——不在（且
	// 用户没卸过）才铺；之后这台机器的插件目录就是用户领地，市场自己
	// 的升级也走市场。
	if store.ShouldSeed("niuma.market") {
		if sub := marketSeedFS(); sub != nil {
			if serr := plugins.SeedPlugin(sub, "niuma.market", pluginRoot); serr != nil {
				log.Printf("出厂插件预装失败：%v", serr)
			} else {
				log.Printf("出厂插件 niuma.market 已预装（插件市场开张）")
			}
		}
	}
	if ps := store.Load(); len(ps) > 0 {
		store.ApplyWardrobe(ps)
		for _, p := range ps {
			state := "启用"
			if !p.Enabled {
				state = "停用"
			}
			if len(p.Problems) > 0 {
				log.Printf("插件 %s（%s）：%s", p.ID, state, strings.Join(p.Problems, "；"))
			} else {
				log.Printf("插件 %s（%s）已装载", p.ID, state)
			}
		}
	}
	return store
}

// openDocsLeg opens the v3 room memory (nil disables it — the one
// store whose failure turns the feature off instead of degrading to
// memory).
func openDocsLeg() *kb.DocsStore {
	// the v3 room memory: one log-structured store under ~/.niuma/kb
	// (log.jsonl WAL + snapshot.json). A pre-v3 ~/.niuma_kb markdown
	// tree folds in once (kept as a .imported-<ts> backup); a fresh
	// install seeds the starter manuals, never reseeded afterwards.
	if backup, err := kb.MigrateLegacyIfNeeded(); err != nil {
		log.Printf("docs legacy import: %v — continuing without it", err)
	} else if backup != "" {
		log.Printf("docs: 旧黑板文档库已并入 ~/.niuma/kb（留底 %s）", backup)
	}
	docsDir, err := kb.DefaultDocsDir()
	if err != nil {
		log.Printf("docs store path unavailable: %v", err)
	}
	d, derr := kb.OpenDocs(docsDir)
	if derr != nil {
		log.Printf("docs store: %v — room memory disabled", derr)
		d = nil
	}
	return d
}

// ledgerStores is the ledger family's open result: the shared sqlite
// handle (nil only in the degraded in-memory boot — home-less, or a
// quarantined database that would not reopen) plus the five engines
// and the identity resolution. Since the sqlite promotion this family
// IS the ledger: requirements/plan/merge/meeting/notice/tasks all
// live in studio.db, the JSON file rack is retired (the one-time
// bridge in sqlstore/import.go is its last reader).
type ledgerStores struct {
	db      *sqlstore.DB
	plans   *plan.Engine
	merges  *merge.Engine
	reqs    *requirements.Engine
	meets   meeting.Store
	notices notice.Store
	// 身份层（多用户形态的解析结果；单用户恒零值）。
	ident identitySet
}

// memLedgers is the degraded shape: every engine in-memory (nil
// seams), no database. The room must boot — loudly (the caller
// announces; writes do not survive restart).
func memLedgers() ledgerStores {
	return ledgerStores{
		plans:   plan.OpenMemory(),
		merges:  merge.OpenMemory(),
		reqs:    requirements.OpenMemory(),
		meets:   meeting.OpenMemory(),
		notices: notice.NewMemory(),
	}
}

// openLedgerEngines opens the five engines over one database, bound
// to one storage subject. A corrupt shelf document trips that domain's
// surgical corrupt-reset (export forensics → clear THAT domain's
// tables → reopen → room announcement) — never a silent empty engine,
// never the good domains' rows.
func openLedgerEngines(hub *chat.Hub, db *sqlstore.DB, subject string) (ledgerStores, error) {
	var out ledgerStores
	out.db = db
	open := func() error {
		var err error
		if out.reqs, err = requirements.OpenStore(db.ReqShelves(), subject); err != nil {
			return fmt.Errorf("需求台账: %w", err)
		}
		if out.plans, err = plan.OpenStore(db.PlanSlots(), subject); err != nil {
			return fmt.Errorf("提案槽: %w", err)
		}
		if out.merges, err = merge.OpenStore(db.MergeSlots(), subject); err != nil {
			return fmt.Errorf("合并槽: %w", err)
		}
		if out.meets, err = meeting.OpenStore(db.MeetingShelves(), subject); err != nil {
			return fmt.Errorf("会议台账: %w", err)
		}
		out.notices = notice.OpenStore(db.NoticeRows(), subject)
		return nil
	}
	if err := open(); err != nil {
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		backup := filepath.Join(v2Root(), fmt.Sprintf("studio.db.ledger-corrupt-%s.json", stamp))
		log.Printf("ledger store: %v — 导出取证副本并按域重置", err)
		if berr := db.ExportLedgerSnapshot(backup); berr != nil {
			log.Printf("ledger corrupt backup: %v — 无取证副本，继续重置", berr)
		}
		if rerr := db.ResetLedgerNS(subject); rerr != nil {
			return out, fmt.Errorf("台账重置失败: %w", rerr)
		}
		if err2 := open(); err2 != nil {
			return out, fmt.Errorf("重置后仍无法打开: %w", err2)
		}
		hub.System(fmt.Sprintf("台账加载失败（%v），损坏行已导出为 %s 并重置；若非预期请联系房主检查备份", err, backup))
	}
	return out, nil
}

// openLedgersLeg opens the ledger family (requirements/plan/merge/
// meeting/notice — the task engine follows in the serial tail, it
// waits for the owner's name). Since the sqlite promotion this is the
// studio's ONLY ledger shape: studio.db under the v2 root, engines
// over per-call-namespace seams, the JSON rack retired (its last
// reader is the one-time bridge). hub is the announcement channel for
// the fallback verdicts (they must be visible in the room, not just
// the log). An error return is a REFUSAL to boot (the ②-class matrix
// row: the file is fine, this binary must not move it — and there is
// no JSON rack to fall back onto anymore).
func openLedgersLeg(hub *chat.Hub, ownerName string, multi bool) (ledgerStores, error) {
	// NIUMA_STORE 退役：台账恒为单库。仍带着旧值的部署按其期望会
	// 破裂——响亮拒起好过静默改道（=json 求的是已死的架，=sqlite 已
	// 是唯一形态）。
	if v := util.Env("STORE"); v != "" {
		return memLedgers(), fmt.Errorf("NIUMA_STORE 已退役（收到 %q）——台账自本次升级起恒为 sqlite 单库（studio.db），请移除该环境变量后重启", v)
	}
	root := v2Root()
	if root == "" {
		// 无 home：内存台账响亮降级（房间必须能起；写入不跨重启）。
		log.Printf("ledger store: 主目录不可得 — 五域台账以内存形态运行")
		hub.SystemRecorded(i18n.S("本机无法定位主目录——台账以内存形态运行，重启后本次会话的台账写入不保留"))
		return memLedgers(), nil
	}
	dbPath := filepath.Join(root, "studio.db")
	ldb, serr := sqlstore.Open(dbPath)
	if serr != nil {
		switch {
		case errors.Is(serr, sqlstore.ErrCorrupt):
			// ①类：隔离留证→重开空库。绝不从 JSON 回导——桥的源文件是
			// 切换时刻的旧快照，回导＝静默复活陈旧数据（转正前的回落
			// 口径明文废除；一次性导入戳同判手动删库的场景）。损失响
			// 亮可见，恢复交运维（studio.db 备份）。
			stamp := strconv.FormatInt(time.Now().Unix(), 10)
			quarantineSQLite(dbPath, stamp)
			ldb2, rerr := sqlstore.Open(dbPath)
			if rerr != nil {
				if multi {
					return memLedgers(), fmt.Errorf("多用户模式下单库损坏且隔离后重开仍失败（%v）——请检查 %s.corrupt-* 取证文件", rerr, dbPath)
				}
				log.Printf("ledger store: 损坏隔离后重开仍失败: %v — 以内存台账继续（房间必须能起）", rerr)
				hub.SystemRecorded(i18n.Sf("数据库文件损坏且重开失败（%v）——已隔离取证，本轮以内存台账运行，重启后本次会话的台账写入不保留", rerr))
				return memLedgers(), nil
			}
			ldb = ldb2
			hub.SystemRecorded(i18n.Sf("数据库文件损坏（%v）——已隔离为 %s，并重开空库继续；损坏前库内数据无法自动恢复（绝不从 JSON 旧快照回导），取证请查隔离文件，如有备份请人工恢复", serr, dbPath+".corrupt-"+stamp))
		case errors.Is(serr, sqlstore.ErrMigrate), errors.Is(serr, sqlstore.ErrNewerSchema):
			// ②类：库文件不动（数据是好的，本程序升不动/读不懂）。
			// JSON 台账已退役——没有回退架，拒起是唯一诚实形态（升
			// 级程序或修复后重启；版本钉保证库不被旧程序倒退改写）。
			return memLedgers(), fmt.Errorf("单库打开失败（%v）——库文件未动；JSON 台账已退役（无回退架），请升级或修复程序后重启", serr)
		default:
			// ③类：路径/权限/磁盘——库从未真正建立，无陈旧快照可复
			// 活。单用户：内存台账＋响亮通告（房间必须能起，写入不跨
			// 重启）；多用户 fail-close（命名空间不容内存形态）。
			if multi {
				return memLedgers(), fmt.Errorf("多用户模式下单库无法建立（%v）——请检查路径/权限/磁盘", serr)
			}
			log.Printf("ledger store: %v — 以内存台账继续（房间必须能起）", serr)
			hub.SystemRecorded(i18n.Sf("单库无法建立（%v）——台账以内存形态运行，重启后本次会话的台账写入不保留", serr))
			return memLedgers(), nil
		}
	}
	taskDir, rankPath := "", ""
	if td, err := tasks.DefaultTaskDir(); err == nil {
		taskDir = td
	}
	if rp, err := tasks.DefaultRankPath(); err == nil {
		rankPath = rp
	}
	if multi {
		// 身份层一次性迁移（boot_identity.go）：建 admin、JSON 桥直导
		// admin 命名空间（含会议/公告）、遗留 ns='' 行收拢——迁移只需
		// 库句柄；引擎随后按解析出的工作室主体开（单用户的导入路径
		// 完全旁路，stamp 与 '' 表空门各自把守）。
		ident := applyMultiUser(ldb, ownerName, root, taskDir, rankPath)
		out, err := openLedgerEngines(hub, ldb, ident.subject)
		out.ident = ident
		return out, err
	}
	// 单向桥：一次性导入戳把门——首开（或戳缺失的健康首导）才跑，
	// 损坏重开/手动删库绝不回导 JSON 旧快照（防陈旧复活是转正回落
	// 矩阵的地基）。文件侧原样保留作取证副本。
	if !ledgerImportStampDone(root) {
		runLegacySplits(root, taskDir)
		rep, ierr := ldb.ImportLocal(root)
		if ierr != nil {
			log.Printf("sqlite import: %v — 导入中断，已提交部分照用（修复后重启重试）", ierr)
		} else if rep.ReqShelves+rep.Reqs+rep.Plans+rep.Merges+rep.Meetings+rep.Notices > 0 {
			log.Printf("sqlite: JSON 台账已并入单库（需求 %d 架 %d 条、提案槽 %d、合并槽 %d、会议架 %d、公告 %d 行；JSON 文件保留取证）",
				rep.ReqShelves, rep.Reqs, rep.Plans, rep.Merges, rep.Meetings, rep.Notices)
		}
		if trep, terr := ldb.ImportTasksLedger(taskDir, rankPath); terr != nil {
			log.Printf("sqlite import tasks: %v — 导入中断，已提交部分照用（修复后重启重试）", terr)
		} else if trep.Shelves+trep.Tasks+trep.Ranks > 0 {
			log.Printf("sqlite: 任务台账已并入单库（任务架 %d 架 %d 张、等级 %d 行；JSON 文件保留取证）",
				trep.Shelves, trep.Tasks, trep.Ranks)
		}
		// 旧黑板公告的一次性退休搬运（notice 的 legacy 面，桥的门内）。
		if ne := notice.OpenStore(ldb.NoticeRows(), ""); ne.ImportLegacyBlackboard(defaultName()) {
			log.Printf("notice: 旧黑板公告已导入为 Niuma_Studio 群公告（原文件保留取证）")
		}
		writeLedgerImportStamp(root)
	}
	return openLedgerEngines(hub, ldb, "")
}

// runLegacySplits performs the pre-isolation single-file splits (the
// bridge reads the split shelves): requirements/meetings/tasks each
// had a pre-v2.10 single ledger. Best-effort — a failed split leaves
// the old file in place for the next boot.
func runLegacySplits(root, taskDir string) {
	if dir, err := requirements.DefaultDir(); err == nil {
		if legacy, lerr := requirements.LegacyPath(); lerr == nil {
			if moved, serr := requirements.SplitLegacyFile(legacy, dir); serr != nil {
				log.Printf("requirements legacy split: %v — 旧单文件保持原位（修复后重启重试）", serr)
			} else if moved > 0 {
				log.Printf("requirements: 旧单文件需求台账已拆为分项目架（%d 条，原文件留档 .migrated）", moved)
			}
		}
	}
	if dir, err := meeting.DefaultDir(); err == nil {
		if legacy, lerr := meeting.LegacyPath(); lerr == nil {
			if moved, serr := meeting.SplitLegacyFile(legacy, dir); serr != nil {
				log.Printf("meetings legacy split: %v — 旧单文件保持原位（修复后重启重试）", serr)
			} else if moved > 0 {
				log.Printf("meetings: 旧单文件会议台账已拆为分项目架（%d 场，原文件留档 .migrated）", moved)
			}
		}
	}
	if taskDir != "" {
		if legacy, lerr := tasks.LegacyTaskPath(); lerr == nil {
			if moved, serr := tasks.SplitLegacyFile(legacy, taskDir); serr != nil {
				log.Printf("tasks legacy split: %v — 旧单文件保持原位（修复后重启重试）", serr)
			} else if moved > 0 {
				log.Printf("tasks: 旧单文件台账已拆为分项目架（%d 张，原文件留档 .migrated）", moved)
			}
		}
	}
}

// seatLocalOwner joins the local human's owner seat BEFORE the server
// starts (so /kb/people can flag the host from the very first request)
// and persists the studio-wide owner credential. Returns the owner's
// display name — the task engine's corrupt-reset announcements and
// every later phase stamp it.
func seatLocalOwner(hub *chat.Hub, registry *chat.Registry, role string) string {
	// the local human joins before the server starts, so /kb/people can
	// flag the host from the very first request. The seat carries the
	// registry's studio-wide owner credential (owner-M1): the same token
	// every project room's pre-seated owner seat holds — the one proof a
	// mirror/owner-channel say must present (a name alone stopped being
	// an identity after the p/book mirror impersonation incident).
	player, me := hub.JoinWithToken(defaultName(), role, false, registry.OwnerCredential())
	// roll-call exemption AND the seat the v0.6 mirror path speaks
	// through (the seatless mirror connection relays the owner's session
	// input under this seat's name)
	hub.SetOwnerSeat(player)
	// persist the credential for the two legitimate speakers: the mirror
	// CLI (loads ~/.niuma_token_<owner> on every dial) and the workbench
	// page (fetches it loopback-only via /owner/token). Same discipline
	// as the dispatcher's per-member token saves (chat/seattoken.go) —
	// the owner is studio-level, so the credential keeps the LEGACY
	// name-only key（project=""）.
	chat.SaveSeatToken("", me.Name, registry.OwnerCredential())
	return me.Name
}

// openTaskEngine opens the task ledger + rank registry: over the
// single database when the ledger leg brought one up, in-memory when
// the boot degraded to memLedgers. It runs AFTER the owner seat joins
// (the engine stamps the owner's name) and may hub.System() its
// corrupt-reset verdict — hub must exist by then.
func openTaskEngine(hub *chat.Hub, db *sqlstore.DB, owner, subject string) *tasks.Engine {
	return openTaskEngineNS(hub, db, owner, subject)
}

// openTaskEngineNS is openTaskEngine bound to one storage subject (the
// multi-user studio's admin namespace; "" the single-user shape).
func openTaskEngineNS(hub *chat.Hub, db *sqlstore.DB, owner, subject string) *tasks.Engine {
	if db == nil {
		// 内存 boot 的降级形态：引擎开在 no-op 缝上（写入不跨重启，
		// leg 已在房里通告过）。
		engine, err := tasks.OpenStoreNS(tasks.NewMemStore(), "", owner)
		if err != nil {
			log.Fatalf("task engine: 内存形态无法打开: %v", err)
		}
		return engine
	}
	// tasks 的 per-shelf corrupt-reset 口径（语义对照表 §1.6，tasks
	// 专属裁定：导出损坏行留证→清任务表重开→房间通告——绝不静默
	// 空账，也绝不连带清掉其它域的好行）。
	engine, err := tasks.OpenStoreNS(db.Tasks(), subject, owner)
	if err != nil {
		log.Printf("task engine: %v — 导出损坏任务架并重置台账", err)
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		backup := filepath.Join(v2Root(), fmt.Sprintf("studio.db.tasks-corrupt-%s.json", stamp))
		if berr := db.ExportLedgerSnapshot(backup); berr != nil {
			log.Printf("tasks corrupt backup: %v — 无取证副本，继续重置", berr)
		}
		if rerr := db.ResetTablesNS([]string{"tasks_shelves", "task_ranks"}, subject); rerr != nil {
			log.Fatalf("task engine: 重置任务台账失败: %v", rerr)
		}
		engine, err = tasks.OpenStoreNS(db.Tasks(), subject, owner)
		if err != nil {
			log.Fatalf("task engine: 重置后仍无法打开: %v", err)
		}
		hub.System(fmt.Sprintf("任务台账加载失败（%v），损坏行已导出为 %s 并重置；若非预期请联系房主检查备份", err, backup))
	}
	return engine
}

// convergeEstablishmentTables runs the kb's one-shot establishment
// migrations (sentinel retirement, the 必须 column, the per-project
// system-post floor). It runs AFTER docs and projStore are open — the
// serial tail's first consumer of both legs.
func convergeEstablishmentTables(docs *kb.DocsStore, projStore *projects.Store) {
	if docs == nil {
		return
	}
	// 哨兵退役（v2.2 一次性迁移）：编制表里的哨兵岗位行随岗位一起退役——
	// 看护自 v0.10 起常驻对账，残留的哨兵行只会让看护永远报一个没人能
	// 顶的缺岗。每张编制表（大厅＋各项目）都在房主编辑锁下安全移除；
	// 损坏的表拒绝改动、照旧走报警路径。只在确实改写了表时说一行。
	if touched := kb.RetireSentinelPosts(docs); len(touched) > 0 {
		log.Printf("establishment: 哨兵岗位已退役（%s）", strings.Join(touched, "、"))
	}
	// 「必须」列收敛（v2.4 一次性迁移）：每张编制表补上第七列「必须」
	// （系统岗=是，其余=否）、表头改写为七列规范形，并给从未有过
	// 小助手行的表补上该行（必须系统岗，看护与启动门都不计它——它的
	// 生命是小助手会话自己的）。幂等：已收敛的表一字不写；损坏的表
	// 拒绝改动、照旧走报警路径。
	if touched := kb.UpgradeMustColumn(docs); len(touched) > 0 {
		log.Printf("establishment: 编制表已补「必须」列（%s）", strings.Join(touched, "、"))
	}
	// 项目编制收敛（v2.5）：编制对项目不再是可选项——每个 active 项目
	// 的表必须在且带齐三行系统岗（编排者/HR 分项目各一套，小助手全工
	// 作室唯一一行）。缺失的表整张补建、缺的行尾行追加；已收敛的项目
	// 一字不写；损坏的表拒绝改动、照旧走报警路径。大厅不在此列（它
	// 的表归启动招聘管）。
	var projSeeded []string
	for _, p := range projStore.List() {
		if p.Status != projects.StatusActive || p.Key == chat.LobbyKey {
			continue
		}
		touched, err := kb.EnsureProjectEstablishment(docs, p.Key, "系统")
		if err != nil {
			log.Printf("establishment: 项目 %s 编制收敛失败：%v（下次启动再试）", p.Key, err)
			continue
		}
		if len(touched) > 0 {
			projSeeded = append(projSeeded, p.Key)
		}
	}
	if len(projSeeded) > 0 {
		log.Printf("establishment: 项目编制表已带齐系统岗（%s）", strings.Join(projSeeded, "、"))
	}
}
