package staffing

// Per-project behavior switches (v2 P5-a, model §4.1 "Schedule" note +
// M5): report_every_min / auto_recall are the project-level BEHAVIOR
// layer — whether the keeper should auto-refill a missing post — while
// the interval itself stays with the keeper clock (the patrol-clock
// pattern, env-tunable). Only auto_recall is consumed today ("no
// consumer, no face": report_every_min lands when a reporter exists);
// the shape rides along so the file format does not churn twice.
//
// 实施选位（P5-a 注记）：model 稿写「内嵌 staffing」——落位为 staffing
// store 的伴生文件 staffing.settings.json（同域同生命周期），不改
// staffing.json 的数组形状（那是一行=一人一岗的干净事实表，混入
// 项目配置会污染它）。默认 auto_recall=开（v1 看护的自动补员语义
// 平移；岗级开关仍由编制表「自动补员」列控制——两级门：项目总闸
// ∧ 岗位列）。关掉项目总闸即静默（P5 验收口径）。

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/persist"
)

// Settings is one project's behavior-switch block (model §4.1).
type Settings struct {
	// AutoRecall is the keeper's project master switch: when false the
	// keeper alarms on shortfalls but never refills (the per-post
	// 自动补员 column still gates each row on top of this).
	AutoRecall bool `json:"auto_recall"`
	// ReportEveryMin is the (future) periodic-report beat in minutes;
	// 0 = off. Reserved by the model draft; no consumer yet.
	ReportEveryMin int `json:"report_every_min,omitempty"`
	// AutoStock / AutoAdvance（r_19）: 全智能模式拆成的双开关——
	// AutoStock＝自动补货（需求池低于水位线注入选题蓄水），AutoAdvance＝
	// 自动推进（代收到龄提案、停滞续命、闲置拆解、HR 巡报、权限/提问
	// 放行——「房主不介入」的那一半）。两者独立（双开＝旧全智能）。
	// Defaults OFF (unlike AutoRecall): it burns tokens unattended, so
	// the enable write face demands a protocol-level confirm token
	// (POST /p/{key}/autopilot).
	AutoStock   bool `json:"auto_stock,omitempty"`
	AutoAdvance bool `json:"auto_advance,omitempty"`
	// StockGoal（目标制补货）: 开启自动补货时房主填写的「本次目标」——
	// 这轮补货要达成什么（自由文本）。选题注入原样带给编排者：目标未
	// 达成前不得以「本期无新需求」应付；编排者判定达成（或确认无法达
	// 成）后经 autopilot_complete 帧自动关闭补货与推进。空＝无目标模式
	//（回落 r_17 既有纪律：确无可做明说即可）——直连 SetAutoStock 的
	// 旧路径与存量已开的房都走这条。HTTP 开启面强制非空（人话 400）。
	StockGoal string `json:"stock_goal,omitempty"`
	// AutoPilot is the LEGACY pre-r_19 master switch — read-only
	// migration source: loadSettings folds a persisted true into both
	// new switches and clears it (one-way, idempotent). Never written.
	AutoPilot bool `json:"autopilot,omitempty"`
	// Achievements（r_18 t_187）: the unlocked-medal set（key → 达成
	// unix ts）——成就引擎的持久化真源。只存已达成（重启即恢复基准，
	// 不回放历史重算——防补算洪水）。key 形如 "tasks-10@小猿"（个人）
	// 或 "tests-100"（全室）。
	Achievements map[string]int64 `json:"achievements,omitempty"`

	// Paused（房间暂停）: 整间办公室的运行开关——开着时该房的调度
	// 回合（对话）、巡逻/日报/会议钟、看护补员、全智能引擎全部停摆，
	// 前端办公室小人同步定格；成员车道里的待投线原样停着，恢复即续投。
	// 与其他开关同域持久化（重启仍是暂停态，不会自作主张复工烧 token）。
	// 写面是 POST /p/{key}/pause（无需确认令牌——两个方向都只省不费）。
	Paused bool `json:"paused,omitempty"`

	// CompactCtxTokens（P0 会话折叠）: 成员活体上下文估计
	// （ctx_estimate，上下文仪表口径）越过这条线且成员空闲时，看护钟
	// 代发一次会话折叠（/compact——保留在办与关键决定的摘要，丢弃过程
	// 性细节）。0＝关（缺省——折叠会花一个模型回合，得房主显式开）；
	// 合法域 5 万–1000 万 token（normalize 钳制）。同一成员两次折叠
	// 至少隔 1 小时（调度器侧冷却，不在此存）。
	CompactCtxTokens int64 `json:"compact_ctx,omitempty"`

	// RebuildVote（AI 重编译投票）: 按项目的行为开关——开着时该房的
	// 调度器受理成员的重编译投票（「提议重编译」开场，全房投票超半数
	// 同意即自动执行 rebuild）。默认关（开启走 POST /p/{key}/rebuild-
	// vote 的协议确认门）：AI 能重启整个工作室是高权面，房主知情后
	// 逐房放开。dispatcher 侧实时读（SettingsOf），翻转即时生效；关
	// 闭会作废进行中的投票（server 的关闭腿经 fleet 通知调度器）。
	RebuildVote bool `json:"rebuild_vote,omitempty"`

	// PatrolStallMin（r_16 t_179）: PatrolNow 的停滞检查阈值——在途单
	//（todo/doing 非 pending）updated_ts 距今超此分钟数即入停滞清单。
	// 0=未设置走默认（15min）；普通巡检模式也生效（不依赖 autopilot）。
	PatrolStallMin int `json:"patrol_stall_min,omitempty"`

	// AutoPilotKnobs（r_14 t_171）: the switches' companion knob block —
	// per-project overrides for the engine's five timings (r_19 增
	// water_target，补货侧）。Written only when the user turns a knob;
	// reading tolerates the legacy bare-boolean shape (on = all-default
	// knobs). All fields are 0 = unset (walk the chain: settings > env >
	// built-in).
	AutoPilotKnobs *AutoPilotKnobs `json:"autopilot_knobs,omitempty"`
}

// AutoPilotKnobs（r_14 t_171，design/r12-knobs §一/§二）: per-project
// autopilot timing overrides. Units: minutes (poke/stall), seconds
// (accept); max_plans 0 = unlimited *as an explicit setting* (the
// unset zero walks the default chain — -1 is the API-only "unlimited"
// marker and never persists). WaterTarget（r_19）＝蓄水目标线条，
// 补货闸与「⚠ 水位偏低」快照的活口径（1–9，0=未设置走默认 3）。
type AutoPilotKnobs struct {
	PokeEveryMin  int `json:"poke_every,omitempty"`   // 5–120 min
	AcceptDelayS  int `json:"accept_delay,omitempty"` // 30–300 s
	StallDelayMin int `json:"stall_delay,omitempty"`  // 10–60 min
	MaxPlans      int `json:"max_plans,omitempty"`    // 1–99 (0 unset; -1 never persists)
	WaterTarget   int `json:"water_target,omitempty"` // 1–9 条（0 unset → 默认 3）
}

// DefaultAcceptDelayS is the single source of the 代行宽限 default:
// how long a proposal waits for a watching host's veto before the
// engine accepts on their behalf, and how long an absent owner's ask
// waits before the assumption release. Both halves of the grace —
// server's engine (accept) and dispatch's ask gate — read this one
// number; before this constant they were two deliberately duplicated
// 120s values kept in sync by hand ("the number is the contract").
const DefaultAcceptDelayS = 120

// settingsPathOf derives the companion settings file for a staffing
// path (staffing.json → staffing.settings.json, same directory); an
// in-memory store ("" path) keeps settings in memory only.
func settingsPathOf(staffingPath string) string {
	if staffingPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(staffingPath), "staffing.settings.json")
}

// loadSettings reads the companion file into the store (missing file =
// defaults; a corrupt file degrades to defaults — the switches must
// never refuse the boot).
func (s *Store) loadSettings() {
	s.settings = map[string]Settings{}
	if s.settingsPath == "" {
		return
	}
	b, err := os.ReadFile(s.settingsPath)
	if err != nil {
		return
	}
	var m map[string]Settings
	if json.Unmarshal(b, &m) != nil {
		log.Printf("parse %s: 设置降级为默认（auto_recall=开）", s.settingsPath)
		return
	}
	s.settings = m
	// r_19 迁移：旧 AutoPilot 总闸折进双开关（true → 补货+推进都开），
	// 旧位清 false。必须当场落盘——omitempty 会把「关」的新键从文件里
	// 抹掉，若旧键残留，用户关掉双开关后下次启动会被复活。
	migrated := false
	for k, set := range s.settings {
		if !set.AutoPilot {
			continue
		}
		set.AutoStock = true
		set.AutoAdvance = true
		set.AutoPilot = false
		s.settings[k] = set
		migrated = true
	}
	if migrated {
		s.saveSettingsLocked()
	}
}

// saveSettingsLocked persists the settings map atomically. Callers hold
// s.mu; failures log, never abort (the in-memory switch stands).
func (s *Store) saveSettingsLocked() {
	if s.settingsPath == "" {
		return
	}
	if err := persist.SaveJSON(s.settingsPath, s.settings, 0o644); err != nil {
		log.Printf("save staffing settings: %v", err)
	}
}

// SettingsOf returns the project's switch block. A project with no
// entry runs the defaults (auto_recall on) — v1 keeper parity.
func (s *Store) SettingsOf(projectKey string) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if set, ok := s.settings[projectKey]; ok {
		return set
	}
	return defaultSettings()
}

func defaultSettings() Settings { return Settings{AutoRecall: true} }

// AnyAutoOn（r_19）: 任一自动开关（补货/推进）在开——引擎到访与
// /projects 徽标的口径。双开即旧全智能（「完全不介入」）；单开各有
// 边界（补货＝只蓄水不代收，推进＝只推进不进新货）。
func (set Settings) AnyAutoOn() bool { return set.AutoStock || set.AutoAdvance }

// DeleteSettings drops the project's switch block — the project
// reset's half: first-open defaults come back (auto-recall on, the
// 全智能 double switch off, no achievements, no knobs). Idempotent;
// a project with no entry was never touched.
func (s *Store) DeleteSettings(projectKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.settings[projectKey]; !ok {
		return
	}
	delete(s.settings, projectKey)
	s.rev++
	s.saveSettingsLocked()
}

// SetAutoRecall flips the project's refill master switch and persists
// it (POST /p/{key}/establishment's write face). The value is returned
// as stored for the receipt.
func (s *Store) SetAutoRecall(projectKey string, on bool) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.settings[projectKey]
	if _, ok := s.settings[projectKey]; !ok {
		set = defaultSettings()
	}
	set.AutoRecall = on
	s.settings[projectKey] = set
	s.rev++
	s.saveSettingsLocked()
	return set
}

// SetAchievements（r_18 t_187）: 达成集整体覆写（引擎在每次 unlock
// 后全量落盘——map 尺寸是奖章数级别，整写最简且无合并歧义）。
func (s *Store) SetAchievements(projectKey string, set map[string]int64) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set2 := s.settings[projectKey]
	if _, ok := s.settings[projectKey]; !ok {
		set2 = defaultSettings()
	}
	set2.Achievements = set
	s.settings[projectKey] = set2
	s.rev++
	s.saveSettingsLocked()
	return set2
}

// SetPatrolStallMin（r_16 t_179）: 巡检停滞阈写入（夹 5–60 分钟）。
func (s *Store) SetPatrolStallMin(projectKey string, min int) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.settings[projectKey]
	if _, ok := s.settings[projectKey]; !ok {
		set = defaultSettings()
	}
	if min != 0 {
		set.PatrolStallMin = clampInt(min, 5, 60)
	} else {
		set.PatrolStallMin = 0 // 0=重置回默认链
	}
	s.settings[projectKey] = set
	s.rev++
	s.saveSettingsLocked()
	return set
}

// SetAutoPilotKnobs（r_14 t_171）: per-project knob write (the settings
// pane's single-knob hot update). Values clamp into their legal domains
// before persisting; zero fields mean "unset" and are dropped.
func (s *Store) SetAutoPilotKnobs(projectKey string, k AutoPilotKnobs) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.settings[projectKey]
	if _, ok := s.settings[projectKey]; !ok {
		set = defaultSettings()
	}
	// 夹紧（design/r12-knobs §一合法域）；0＝未设置——保留已存值
	//（「只改一个旋钮」的请求不重置其他三个），非 0 值夹进合法域
	prev := AutoPilotKnobs{}
	if set.AutoPilotKnobs != nil {
		prev = *set.AutoPilotKnobs
	}
	if k.PokeEveryMin != 0 {
		k.PokeEveryMin = clampInt(k.PokeEveryMin, 5, 120)
	} else {
		k.PokeEveryMin = prev.PokeEveryMin
	}
	if k.AcceptDelayS != 0 {
		k.AcceptDelayS = clampInt(k.AcceptDelayS, 30, 300)
	} else {
		k.AcceptDelayS = prev.AcceptDelayS
	}
	if k.StallDelayMin != 0 {
		k.StallDelayMin = clampInt(k.StallDelayMin, 10, 60)
	} else {
		k.StallDelayMin = prev.StallDelayMin
	}
	if k.MaxPlans != 0 {
		if k.MaxPlans < 0 {
			k.MaxPlans = 0 // -1（不限）只在 API 回读面出现，不落盘
		}
		k.MaxPlans = clampInt(k.MaxPlans, 1, 99)
	} else {
		k.MaxPlans = prev.MaxPlans
	}
	if k.WaterTarget != 0 {
		k.WaterTarget = clampInt(k.WaterTarget, 1, 9)
	} else {
		k.WaterTarget = prev.WaterTarget
	}
	set.AutoPilotKnobs = &k
	s.settings[projectKey] = set
	s.rev++
	s.saveSettingsLocked()
	return set
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// SetAutoStock / SetAutoAdvance（r_19）: 双开关各自的写面（POST
// /p/{key}/autopilot；引擎熔断落在 SetAutoAdvance）。The value is
// returned as stored for the receipt.
func (s *Store) SetAutoStock(projectKey string, on bool) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.settings[projectKey]
	if _, ok := s.settings[projectKey]; !ok {
		set = defaultSettings()
	}
	set.AutoStock = on
	s.settings[projectKey] = set
	s.rev++
	s.saveSettingsLocked()
	return set
}

func (s *Store) SetAutoAdvance(projectKey string, on bool) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.settings[projectKey]
	if _, ok := s.settings[projectKey]; !ok {
		set = defaultSettings()
	}
	set.AutoAdvance = on
	s.settings[projectKey] = set
	s.rev++
	s.saveSettingsLocked()
	return set
}

// SetStockGoal（目标制补货）: 覆写本次目标（两端去空白；空串＝清除，
// 回落无目标模式）。开启面每次开补货都会重填——旧目标不会跨轮残留到
// 新一轮。独立于开关位：关掉补货不清目标（收摊播报要先读它，且留着
// 供 GET 面回显「上次目标」）。
func (s *Store) SetStockGoal(projectKey, goal string) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.settings[projectKey]
	if _, ok := s.settings[projectKey]; !ok {
		set = defaultSettings()
	}
	set.StockGoal = strings.TrimSpace(goal)
	s.settings[projectKey] = set
	s.rev++
	s.saveSettingsLocked()
	return set
}

// SetPaused（房间暂停）: 翻写该房的运行开关并落盘（POST /p/{key}/pause
// 的写面）。回执按存储后的值回。
func (s *Store) SetPaused(projectKey string, on bool) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.settings[projectKey]
	if _, ok := s.settings[projectKey]; !ok {
		set = defaultSettings()
	}
	set.Paused = on
	s.settings[projectKey] = set
	s.rev++
	s.saveSettingsLocked()
	return set
}

// SetRebuildVote（AI 重编译投票）: 翻写该项目的投票开关并落盘（POST
// /p/{key}/rebuild-vote 的写面）。回执按存储后的值回。
func (s *Store) SetRebuildVote(projectKey string, on bool) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.settings[projectKey]
	if _, ok := s.settings[projectKey]; !ok {
		set = defaultSettings()
	}
	set.RebuildVote = on
	s.settings[projectKey] = set
	s.rev++
	s.saveSettingsLocked()
	return set
}
