package autopilot

// Package autopilot is the 全智能模式 domain (v2.8): the per-project
// hands-off engine closing the three host gates server-side. Split
// from the shell's autopilot.go; the shell boots it (Start wires
// Options.AutoPilot) and mounts its write face.

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/server/gitops"
	"github.com/WWestC/Niuma_Studio/server/plans"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// budgetPolicyFromEnv is the chain's deployment leg (no file on disk).
func BudgetPolicyFromEnv() BudgetPolicy {
	var pol BudgetPolicy
	if v, ok := BudgetEnvTokens("BUDGET_DAY_TOKENS"); ok {
		pol.DayTokens = v
	}
	if v, ok := BudgetEnvTokens("BUDGET_WEEK_TOKENS"); ok {
		pol.WeekTokens = v
	}
	return pol
}

// ResolveBudgetPolicy: file > env > 0/0（the knob chain, wholesale per
// file — a present file owns both knobs).
func ResolveBudgetPolicy(root string) BudgetPolicy {
	if root != "" {
		if pol, ok := LoadBudgetPolicy(BudgetPath(root)); ok {
			return pol
		}
	}
	return BudgetPolicyFromEnv()
}

// budgetWindows anchors the two spend windows: 今日本地零点 and 本周一
// 零点（自然周，周一起——人记账的口径，不是滚动 7×24）。
func BudgetWindows(now time.Time) (dayStart, weekStart time.Time) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	week := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7)) // Mon→0 … Sun→6
	return day, week
}

// fmtBudgetTokens says a token count in 人话 (the frontend fmtTokens
// twin): 亿两位小数、万一位小数、万以下原样——熔断播报读得出来。
// en 走英文数量级 B/M/K（阈值随语言换，web/ui/usageview.js 同款）。
func FmtBudgetTokens(n int64) string {
	if i18n.En() {
		switch {
		case n >= 1_000_000_000:
			return strconv.FormatFloat(float64(n)/1e9, 'f', 2, 64) + " B"
		case n >= 1_000_000:
			return strconv.FormatFloat(float64(n)/1e6, 'f', 2, 64) + " M"
		case n >= 1_000:
			return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + " K"
		default:
			return strconv.FormatInt(n, 10)
		}
	}
	switch {
	case n >= 100_000_000:
		return strconv.FormatFloat(float64(n)/1e8, 'f', 2, 64) + " 亿"
	case n >= 10_000:
		return strconv.FormatFloat(float64(n)/1e4, 'f', 1, 64) + " 万"
	default:
		return strconv.FormatInt(n, 10)
	}
}

func BudgetEnvTokens(name string) (int64, bool) {
	v := strings.TrimSpace(util.Env(name))
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || !ValidBudgetTokens(n) {
		return 0, false
	}
	return n, true
}

const BudgetMaxTokens = int64(1) << 40

// BudgetPath is the policy file's slot under a v2 root (sendwait.json's
// neighbor; said once, shared by the handler and the engine's resolver).
func BudgetPath(root string) string {
	return filepath.Join(root, "budget.json")
}

// ValidBudgetTokens: 0（不限）或 1..BudgetMaxTokens。
func ValidBudgetTokens(v int64) bool {
	return v == 0 || (v > 0 && v <= BudgetMaxTokens)
}

// LoadBudgetPolicy reads the persisted knobs; ok=false means absent,
// corrupt or out-of-range (the caller walks down to the env leg).
func LoadBudgetPolicy(path string) (BudgetPolicy, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return BudgetPolicy{}, false
	}
	var pol BudgetPolicy
	if json.Unmarshal(b, &pol) != nil ||
		!ValidBudgetTokens(pol.DayTokens) || !ValidBudgetTokens(pol.WeekTokens) {
		return BudgetPolicy{}, false
	}
	return pol, true
}

// SaveBudgetPolicy persists the knobs atomically (tmp + rename — every
// ledger write's discipline). The caller validates.
func SaveBudgetPolicy(path string, pol BudgetPolicy) error {
	b, err := json.Marshal(pol)
	if err != nil {
		return err
	}
	return persist.Save(path, b, 0o600)
}

func budgetEnvTokens(name string) (int64, bool) {
	v := strings.TrimSpace(util.Env(name))
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || !ValidBudgetTokens(n) {
		return 0, false
	}
	return n, true
}

// BudgetMaxTokensAlias re-exports the cap (const alias needs Go 1.22+? use var? keep const below)
const BudgetMaxTokensAlias = int64(1) << 40

// Face is the 全智能模式 domain's world: the seat, the plan-room
// resolver, the git and plans sibling faces (project probing + the
// landing leg), and the spend cache's read. Split from the shell's
// autopilot.go.
type Face struct {
	*verbs.Seat
	PlanHub func(projectKey string) *chat.Hub
	Git     *gitops.Face
	Plans   *plans.Face
	Spend   func(dayStartMS, weekStartMS int64) (day, week int64, err error)

	// eng is the running engine (set by NewEngine; the status face
	// reads its water targets and patrol stamps).
	eng *Engine
}

const (
	// AutoPilotEvery is the check beat: accept/poke decisions are
	// cheap store reads, so a short beat keeps the grace window honest.
	AutoPilotEvery = 60 * time.Second
	// AutoPilotAcceptDelay is how long a submitted proposal waits for
	// a watching host's veto before the engine accepts on their
	// behalf (房主完全不介入 ≠ 房主在场也不给否决窗). The number
	// itself lives in staffing.DefaultAcceptDelayS — the 代行宽限's
	// single source, shared with dispatch's ask gate.
	AutoPilotAcceptDelay = time.Duration(staffing.DefaultAcceptDelayS) * time.Second
	// AutoPilotPokeEvery bounds how often the idle room may be poked
	// for a next requirement — also the retry backoff when a landing
	// or a poke is refused (no nagging every beat).
	AutoPilotPokeEvery = 30 * time.Minute
	// AutoPilotStallDelay is how long a doing task may sit without a
	// ledger touch before the resume gate wakes its assignee — the
	// 「任务已派出去、人趴着等房主一句话」threshold. Long enough
	// that a legitimately long turn (its own task_update lands at the
	// terminal) never trips it, short enough that a parked member
	// does not idle away the afternoon.
	AutoPilotStallDelay = 30 * time.Minute
	// AutoPilotPatrolEvery (r_13 t_175) is the HR capacity patrol's
	// interval — how often a seated HR hears the 产能巡报 injection.
	AutoPilotPatrolEvery = 60 * time.Minute
	// AutoPilotMaxPlans is the default daily auto-accept cap (the
	// circuit breaker; NIUMA_AUTOPILOT_MAXPLANS=0 lifts it). r_17
	// recalibration: the stocked pool plus the direct-submit bypass
	// consume requirements far faster than the one-at-a-time cycle the
	// old 12 was sized for — 24 keeps the breaker a runaway guard
	// (≈ one landing per hour), not a daily speed limit.
	AutoPilotMaxPlans = 24
	// AutoPilotActor is the landing actor name on auto-accepted
	// proposals (broadcast text + the plan frame's From).
	AutoPilotActor = "自动驾驶"
	// PlanPendingRemindAfter (挂审提醒) is how long a proposal may sit
	// in the pending slot before the host gets ONE recorded @房主 line:
	// in manual mode the whole room yields to the awaiting verdict (the
	// meeting clock's backpressure gate, the engine's 无职权 return),
	// so past this age the silence needs a face — the host otherwise
	// never learns the office is parked on them. Once per plan ID (a
	// same-req revision re-arms it); neither autopilot switch gates it
	// (the sweepProposals hygiene rule), the room-pause freeze does.
	PlanPendingRemindAfter = 30 * time.Minute
)

// autopilotConfirmToken is the protocol-level opt-in (the reset
// precedent): enabling an unattended token-burning loop must never
// ride a bare POST body.
const autopilotConfirmToken = "AUTOPILOT"

// AutoPilotEveryFromEnv resolves the beat from NIUMA_AUTOPILOT_EVERY
// (duration or bare positive minutes; malformed keeps the default —
// the WatchEveryFromEnv discipline).
func AutoPilotEveryFromEnv() time.Duration {
	v := strings.TrimSpace(util.Env("AUTOPILOT_EVERY"))
	if v == "" {
		return AutoPilotEvery
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return AutoPilotEvery
}

// AutoPilotAcceptDelayFromEnv resolves the grace window from
// NIUMA_AUTOPILOT_ACCEPT (duration or bare positive minutes).
func AutoPilotAcceptDelayFromEnv() time.Duration {
	v := strings.TrimSpace(util.Env("AUTOPILOT_ACCEPT"))
	if v == "" {
		return AutoPilotAcceptDelay
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return AutoPilotAcceptDelay
}

// AutoPilotPokeEveryFromEnv resolves the poke cooldown from
// NIUMA_AUTOPILOT_POKE (duration or bare positive minutes).
func AutoPilotPokeEveryFromEnv() time.Duration {
	v := strings.TrimSpace(util.Env("AUTOPILOT_POKE"))
	if v == "" {
		return AutoPilotPokeEvery
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return AutoPilotPokeEvery
}

// AutoPilotMaxPlansFromEnv resolves the daily cap from
// NIUMA_AUTOPILOT_MAXPLANS: "" keeps the default, "0" lifts it
// (returns -1), a positive int caps, malformed keeps the default.
func AutoPilotMaxPlansFromEnv() int {
	v := strings.TrimSpace(util.Env("AUTOPILOT_MAXPLANS"))
	if v == "" {
		return AutoPilotMaxPlans
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n <= 0 {
			return -1
		}
		return n
	}
	return AutoPilotMaxPlans
}

// AutoPilotStallDelayFromEnv resolves the resume gate's stall window
// from NIUMA_AUTOPILOT_STALL (duration or bare positive minutes).
func AutoPilotStallDelayFromEnv() time.Duration {
	v := strings.TrimSpace(util.Env("AUTOPILOT_STALL"))
	if v == "" {
		return AutoPilotStallDelay
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return AutoPilotStallDelay
}

// BudgetPolicy is the spend policy face the engine breaks on (the
// budget read's vocabulary, owned here so the engine's contract is
// self-contained; the shell's budget.go re-exports it).
type BudgetPolicy struct {
	DayTokens  int64 `json:"day_tokens"`
	WeekTokens int64 `json:"week_tokens"`
}

// Config wires the engine. Poke routes the idle
// topic-selection injection to the project's orchestrator through the
// fleet (main wires fleet.AutoPilotPoke; nil = pokes off). Every < 0
// never starts the loop (tests, opt-out). Tick, when non-nil,
// REPLACES the wall-clock timer wholesale (the fake-clock injection
// point; every value received fires one round).
type Config struct {
	Poke func(project string, openN int) error
	// Divert routes the stalled-backlog wake (【自动驾驶·拆解】: pool
	// stocked but nobody decomposing) to the project's orchestrator
	// through the fleet (main wires fleet.AutoPilotDivert; nil =
	// diverts off). r_17 gives it its own hook — the pre-r_17 shape
	// reused cfg.Poke, so the 拆解 intent arrived wearing the 选题
	// header in production.
	Divert func(project string, openN int) error
	// Resume routes the stalled-task wake (the resume gate) to one
	// doing-task assignee through the fleet (main wires
	// fleet.AutoPilotResume; nil = resumes off). An error means
	// nobody to wake — the engine turns it into the room's ambient
	// note, never a refusal.
	Resume func(project, assignee string, stalled []*tasks.Task) error
	// Patrol routes the HR capacity patrol (r_13 t_175) to the seated
	// HR through the fleet (nil = patrol off).
	Patrol func(project string, context string) error
	// PatrolEvery (r_13 t_175): the patrol clock's interval. Zero =
	// AutoPilotPatrolEvery (60min default).
	PatrolEvery time.Duration
	// Budget (r_26 预算熔断) resolves the studio token-budget knobs each
	// beat. Nil = the budget.json > env chain over the registry root
	// (ResolveBudgetPolicy); tests inject a stub to trip windows without
	// a ledger.
	Budget func() BudgetPolicy
	// BudgetSpend (r_26) resolves the studio's token spend inside the
	// two anchored windows (ms epoch; day = since local midnight, week =
	// since Monday). Nil = zcode.StudioUsageWindows (the ~/.zcode
	// ledger's one-pass read).
	BudgetSpend func(dayStartMS, weekStartMS int64) (day, week int64, err error)

	Every        time.Duration
	AcceptDelay  time.Duration
	PokeCooldown time.Duration
	// StallDelay is the resume gate's threshold: how long a doing
	// task may sit without a ledger touch before its assignee is
	// woken. Zero = AutoPilotStallDelay; also the per-assignee
	// re-wake bound (one nudge per window).
	StallDelay time.Duration
	// MaxPlans: 0 = AutoPilotMaxPlans default, negative = unlimited,
	// positive = the cap. main resolves the env up front.
	MaxPlans int
	Tick     <-chan time.Time
}

// apDay is one project's autopilot day: the auto-accept count (the
// cap's numerator, rolled at local midnight). The page persists to
// autopilot_day.json under the registry root — a same-day restart
// re-seeds the breaker's numerator (a mid-day bounce used to hand a
// looping studio a fresh 24-plan budget), any other day tag loads as
// zero. The restock cooldown's anchor deliberately does NOT live here
// (复查#12): rolling the day at midnight must not wipe it — a 23:59
// restock used to become re-pokeable at 00:01.
type apDay struct {
	day      string
	accepted int
}

// ApDayBook is the counter book's on-disk shape (autopilot_day.json):
// one calendar-day tag plus each project's accepted count. The tag is
// the whole staleness story — boot ignores any page not dated today.
type ApDayBook struct {
	Day    string         `json:"day"`
	Counts map[string]int `json:"counts"`
}

// Engine is the per-studio autopilot loop. Created by
// Server.Start when Options.AutoPilot is set; safe for concurrent use
// (the loop goroutine plus the enable face's Kick).
type Engine struct {
	ap         *Face
	cfg        Config
	stop       chan struct{}
	patrolAt   map[string]time.Time // r_13 巡报钟：project → 下次巡报时刻
	patrolDone map[string]time.Time // r_24：上次巡报送达时刻（驾驶舱「采集于 HH:MM」的数据面）
	debug      chan struct{}        // closed-into when one injected round finished

	mu          sync.Mutex
	days        map[string]*apDay    // project → today
	deniedUntil map[string]time.Time // project → retry-backoff after a refused landing
	resumes     map[string]time.Time // project·assignee → last 续命 wake (one per stall window)
	diverts     map[string]time.Time // project → last 拆解 wake (r_17 own stamps — 补货/拆解互不挤占)
	pokes       map[string]time.Time // project → last 选题 wake（冷却锚，不随日翻篇——复查#12）
	pokeWater   map[string]int       // project → open 数 at last poke（干涸连击的对照面）
	pokeDry     map[string]int       // project → 连续水位纹丝不动的补货次数（复查#11 退避指数）

	// pendReminded (mu-guarded) is the 挂审提醒 once-per-plan ledger:
	// plan ID → reminder already sent. Memory-only by design — a
	// restart forgets it and a still-aged plan re-reminds once, the
	// catch-up a returning host wants (the line itself is recorded
	// history; the engine does not outlive the app that owns it).
	pendReminded map[string]bool

	// budgetFailAt is the last room-visible notice of a failed spend
	// read (mu-guarded): the breaker's failure face is throttled to one
	// line per 30min streak so a dead ledger can't stay silent, while a
	// 60s beat can't spam the rooms either. A successful read zeroes it
	// — the next streak notices immediately.
	budgetFailAt time.Time
}

// startAutoPilot builds the engine and launches its clock. Called from
// NewEngine boots the engine over this face (the shell calls it at
// Start, before serving).
func NewEngine(cfg Config, ap *Face) *Engine {
	e := &Engine{ap: ap, cfg: cfg,
		stop:         make(chan struct{}),
		patrolAt:     map[string]time.Time{},
		patrolDone:   map[string]time.Time{},
		debug:        make(chan struct{}, 1),
		days:         map[string]*apDay{},
		deniedUntil:  map[string]time.Time{},
		resumes:      map[string]time.Time{},
		diverts:      map[string]time.Time{},
		pokes:        map[string]time.Time{},
		pokeWater:    map[string]int{},
		pokeDry:      map[string]int{},
		pendReminded: map[string]bool{},
	}
	ap.eng = e
	e.loadDayBook(time.Now())
	if cfg.Every < 0 {
		return e
	}
	every := cfg.Every
	if every == 0 {
		every = AutoPilotEvery
	}
	go e.loop(every)
	return e
}

// Stop ends the loop (tests, teardown).
func (e *Engine) Stop() { close(e.stop) }

// loop waits one beat between rounds — the keeper loop shape (cfg.Tick
// replaces the wall clock; each fired round is synchronous so a test
// that sent a tick can await done() before asserting).
func (e *Engine) loop(every time.Duration) {
	var timer *time.Timer
	if e.cfg.Tick == nil {
		timer = time.NewTimer(every)
		defer timer.Stop()
	}
	for {
		var tick <-chan time.Time
		if timer != nil {
			tick = timer.C
		} else {
			tick = e.cfg.Tick
		}
		select {
		case <-e.stop:
			return
		case <-tick:
			if timer != nil {
				timer.Reset(every)
			}
			// The fence keeps the engine alive: a dead autopilot loop is
			// proposals that stop auto-accepting and orchestrators that
			// stop getting poked — autonomy silently off.
			util.Guard("server: autopilot", func() { e.round(time.Now()) })
			select {
			case e.debug <- struct{}{}:
			default:
			}
		}
	}
}

// done reports (bounded wait) that one injected round has finished —
// the fake-clock tests' synchronization point.
func (e *Engine) Done() bool {
	select {
	case <-e.debug:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

// round is one pass over the lobby (when its switch is on) plus every
// ACTIVE project room whose autopilot switch is on. The lobby is a
// first-class scope: the flagship orchestrator sits there, so
// studio-level autonomy runs the same loop (房主要求大厅也能进).
func (e *Engine) round(now time.Time) {
	ap := e.ap
	if ap.Stores.StaffStore == nil || ap.Stores.PlanStore == nil ||
		ap.Stores.Requirements == nil || ap.Stores.Engine == nil {
		return
	}
	// r_26 预算熔断先行：token 到额先关推进，本拍随后的逐房巡视即按
	// 「推进关」行事（与 autoAccept 里 max_plans 熔断的落点同一拍序）。
	e.budgetBreaker(now)
	// 卡死提案出清（卫生面，开关无关）：挂过 24h 且无法再生效（或需确
	// 认人全部离场）的提案自动出清——不再只有房主仲裁一条出路。暂停房
	// 与其余引擎路同冻结。
	e.sweepProposals(now)
	// 挂审提醒（卫生面同款）：待审提案满龄给房主一行落史的 @房主 定向
	// 提醒——手动模式下全房对这个决定让位，静默本身需要一张脸。
	e.remindPendingPlans(now)
	// 大厅先行，hub 用 ap.Hub（keeper 同款：无人拨号的房在 Registry.
	// Rooms() 里没有位，大厅的真身就是服务端自己的 hub）。房间暂停的
	// 房整房跳过（keeper 同款门）——引擎每一路都是「会自己动」的事。
	lobbySet := ap.Stores.StaffStore.SettingsOf(chat.LobbyKey)
	if lobbySet.AnyAutoOn() && !lobbySet.Paused {
		e.roundProject(chat.LobbyKey, ap.Hub, now)
	}
	if ap.Stores.ProjectStore == nil || ap.Registry == nil {
		return
	}
	rooms := ap.Registry.Rooms()
	for _, p := range ap.Stores.ProjectStore.List() {
		if p.Status != projects.StatusActive || p.Key == chat.LobbyKey {
			continue
		}
		set := ap.Stores.StaffStore.SettingsOf(p.Key)
		if !set.AnyAutoOn() || set.Paused {
			continue
		}
		if hub, ok := rooms[p.Key]; ok {
			e.roundProject(p.Key, hub, now)
		}
	}
}

// roundProject runs one project's autopilot decisions, self-gated on
// the r_19 双开关 (a room with neither switch on is never visited —
// and Kick on a just-turned-off room no-ops for free): 补货闸只认
// AutoStock，代收/续命/拆解/巡报只认 AutoAdvance。First the aged
// proposal (one landing per beat is plenty), then the stocking gate
// (r_17: water below target restocks regardless of busy/idle — the
// pool is inventory), then — while the room has work in flight — the
// stalled doing tasks' resume wakes, and when the room is fully idle
// with a stocked-but-untouched pool the 拆解 divert (its own cooldown,
// so restock and decompose can ride the same beat: 边拆边补).
func (e *Engine) roundProject(key string, hub *chat.Hub, now time.Time) {
	ap := e.ap
	set := ap.Stores.StaffStore.SettingsOf(key)
	stock, advance := set.AutoStock, set.AutoAdvance
	// v2.8 gitflow 合并代收闸先行（独立于 plan 槽——一个在审不挡另一
	// 个的钟，且只认推进关）：纯新增形状的合并提案满宽限即代房主并
	// 入；其余形状永远留给房主，不出声。
	if advance {
		e.mergeIfDue(key, hub, now)
	}
	if cur := ap.Stores.PlanStore.Pending(ap.Subject, key); cur != nil {
		if !advance {
			// 推进关：提案回房主手动审——引擎对在槽提案无职权；蓄水照旧
			// 让位（消费在飞，不添新货）。
			return
		}
		// 提案在槽 ≠ 其余的钟停摆（复查#10）：巡报与续命是独立闸，
		// 一个最长 30min 的拒绝退避窗不该饿死它们；蓄水/拆解仍留在
		// 槽后——消费在飞，不添新货。
		if now.Sub(time.Unix(cur.SubmittedTS, 0)) < e.AcceptDelay(key) ||
			hub.OwnerActiveWithin(chat.OwnerPresenceWindow) ||
			now.Before(e.deniedBackoff(key)) {
			// fresh pending: still inside the host's grace window (or a
			// refused landing is backing off one poke window). The
			// presence clause is the r_19 修订 (第一性：代行只服务
			// 缺席) — an owner seen active in this room within the
			// presence window holds the landing this beat; the next
			// beat after they go quiet accepts as usual, so 代行
			// never stalls on an owner who merely walked past.
			e.resumeStalled(key, hub, now)
			e.patrolIfDue(key, hub, now)
			return
		}
		e.autoAccept(key, hub, now, cur)
		return
	}
	// r_17 蓄水闸先行：水位低于目标线即补货——忙闲皆然（旧版选题挂
	// 在全闲置分支且 open==0 才触发，池子被钉死在 0..1；补货是库存
	// 动作，不等产线停机才做）。
	if stock {
		e.ReplenishIfDue(key, hub, now)
	}
	if !advance {
		return
	}
	if !e.Idle(key) {
		e.resumeStalled(key, hub, now)
		e.patrolIfDue(key, hub, now) // r_13 巡报钟：忙房也巡（房主看得见忙闲）
		return
	}
	if e.StalledBacklog(key) {
		e.divertIfDue(key, hub, now)
	}
	e.patrolIfDue(key, hub, now)
}

// sweepProposals releases jammed task proposals across the lobby and
// every active project room (ProposalStaleAfter aged AND invalid-or-
// crewless — tasks.Engine.SweepStaleProposals owns the test). Hygiene,
// not autonomy: neither autopilot switch gates it, the room-pause
// freeze does. Lines land in each task's own room hub.
func (e *Engine) sweepProposals(now time.Time) {
	ap := e.ap
	if ap.Stores.Engine == nil {
		return
	}
	keys := []string{chat.LobbyKey}
	if ap.Stores.ProjectStore != nil {
		for _, p := range ap.Stores.ProjectStore.List() {
			if p.Status != projects.StatusActive || p.Key == chat.LobbyKey {
				continue
			}
			keys = append(keys, p.Key)
		}
	}
	for _, key := range keys {
		if ap.Stores.StaffStore.SettingsOf(key).Paused {
			continue
		}
		hub := e.hubFor(key)
		if hub == nil {
			continue
		}
		for _, oc := range ap.Stores.Engine.SweepStaleProposals(key, now, e.proposalAround(key)) {
			hub.SystemRecorded(oc.Text)
		}
	}
}

// remindPendingPlans is the 挂审提醒 leg: every pending proposal older
// than PlanPendingRemindAfter gets ONE recorded @房主 line in its own
// room — the host-facing mirror of the valve discipline (the meeting
// clock holds, the engine returns 无职权, and the one voice that can
// open the valve needs to hear it is being waited on). Hygiene, not
// autonomy: neither switch gates it, the room-pause freeze does (the
// sweepProposals rule). A hub that does not exist yet (nobody has
// dialed the room since boot) skips UNMARKED — the reminder catches up
// on a later beat once the room is back.
func (e *Engine) remindPendingPlans(now time.Time) {
	ap := e.ap
	if ap.Stores.PlanStore == nil || ap.Stores.StaffStore == nil {
		return
	}
	for _, key := range ap.Stores.PlanStore.PendingIDs(ap.Subject) {
		if ap.Stores.StaffStore.SettingsOf(key).Paused {
			continue
		}
		hub := e.hubFor(key)
		if hub == nil {
			continue
		}
		p := ap.Stores.PlanStore.Pending(ap.Subject, key)
		if p == nil {
			continue // raced the host's verdict this beat
		}
		age := now.Sub(time.Unix(p.SubmittedTS, 0))
		if age < PlanPendingRemindAfter {
			continue
		}
		e.mu.Lock()
		sent := e.pendReminded[p.ID]
		e.mu.Unlock()
		if sent {
			continue
		}
		hub.SystemRecorded(i18n.Sf(
			"【挂审提醒】@房主 提案 %s「%s」%s已待审 %.0f 分钟——会议钟与全房的下一步都在等这个决定：在提案选项卡批准或驳回，或 plan accept %s / plan reject %s（驳回后需求回池重排）",
			p.ID, p.Title, plans.ReqSuffix(p.Req), age.Minutes(), p.ID, p.ID))
		e.mu.Lock()
		e.pendReminded[p.ID] = true
		e.mu.Unlock()
	}
}

// proposalAround answers whether one confirmation-owed name is still on
// the project's books (any staffing row not offboard). No staffing face
// = nil — the sweep then only releases structurally-invalid proposals.
func (e *Engine) proposalAround(key string) func(string) bool {
	ap := e.ap
	if ap.Stores.StaffStore == nil {
		return nil
	}
	return func(name string) bool {
		for _, row := range ap.Stores.StaffStore.ListByProject(key) {
			if row.Person == name {
				return row.State != staffing.StateOffboard
			}
		}
		return false
	}
}

// budgetBreaker is the token-budget circuit breaker (r_26 预算熔断):
// the studio's ledger spend inside the day/week window reached the
// knob → every room's 自动推进 flips off, the r_19 breaker discipline
// verbatim — 只关推进（自动补货、房主手动路、在途任务一概不动），
// 每间到场的房各听一行播报。与每日代收上限熔断完全同构，只是分子
// 从「接受的份数」换成「烧掉的 token」，复位从「明日计数归零」换成
// 「窗口自然翻篇」（到额后房主仍可手动重开——重开时若仍在额上，下一
// 拍会再次熔断，与 max_plans 重开即再熔的形状一致）。Runs BEFORE the
// per-room pass. The spend read spawns a subprocess — skipped entirely
// when no knob is set or no room runs advance (the default studio pays
// nothing for a feature it never armed).
func (e *Engine) budgetBreaker(now time.Time) {
	ap := e.ap
	pol := e.budgetPolicy()
	if pol.DayTokens <= 0 && pol.WeekTokens <= 0 {
		return
	}
	keys := e.advanceRooms()
	if len(keys) == 0 {
		return
	}
	dayStart, weekStart := BudgetWindows(now)
	day, week, err := e.budgetSpend(dayStart.UnixMilli(), weekStart.UnixMilli())
	if err != nil {
		// 台账读挂不熔断（读面失败不是超支，下一拍再查），但也不再
		// 静默：每条失败链每 30 分钟向到场的推进房落一行系统行——
		// 预算防线在最需要它的时刻失效，房主至少要能在房间里看见。
		log.Printf("[自动驾驶] 预算耗粮读取失败（本拍跳过熔断检查）：%v", err)
		e.noticeBudgetFail(now, keys)
		return
	}
	e.mu.Lock()
	e.budgetFailAt = time.Time{}
	e.mu.Unlock()
	var which, reset string
	var limit, used int64
	switch {
	case pol.DayTokens > 0 && day >= pol.DayTokens:
		which, limit, used, reset = i18n.S("今日"), pol.DayTokens, day, i18n.S("明日零点窗口重置")
	case pol.WeekTokens > 0 && week >= pol.WeekTokens:
		which, limit, used, reset = i18n.S("本周"), pol.WeekTokens, week, i18n.S("下周一零点窗口重置")
	default:
		return
	}
	msg := i18n.Sf(
		"[自动驾驶] %s token 耗粮已达预算上限（已耗 %s / 上限 %s）——自动推进已自动关闭（预算熔断，与每日代收上限同语义：只关推进；自动补货与人力操作不受影响，待审提案回到房主关口。%s后可重开，或调整预算：设置卡·智能·token 预算）",
		which, FmtBudgetTokens(used), FmtBudgetTokens(limit), reset)
	for _, key := range keys {
		ap.Stores.StaffStore.SetAutoAdvance(key, false)
		if hub := e.hubFor(key); hub != nil {
			hub.SystemRecorded(msg)
		}
	}
}

// budgetFailNoticeEvery throttles the breaker's failure face: one room
// line per streak window, the same cadence family as the keeper's
// WatchCooldown.
const budgetFailNoticeEvery = 30 * time.Minute

// noticeBudgetFail is the breaker's degraded-mode face: a failed spend
// read leaves the cap effectively unset for that beat, so the rooms
// running advance hear it — throttled, and reset by the next good read.
func (e *Engine) noticeBudgetFail(now time.Time, keys []string) {
	e.mu.Lock()
	if !e.budgetFailAt.IsZero() && now.Sub(e.budgetFailAt) < budgetFailNoticeEvery {
		e.mu.Unlock()
		return
	}
	e.budgetFailAt = now
	e.mu.Unlock()
	msg := i18n.S("[自动驾驶] 预算耗粮台账读取失败——本拍跳过熔断检查（预算上限暂时相当于未设置），下一拍自动重试。若持续出现请检查 ~/.zcode 台账；此提醒每 30 分钟至多一条")
	for _, key := range keys {
		if hub := e.hubFor(key); hub != nil {
			hub.SystemRecorded(msg)
		}
	}
}

// budgetPolicy resolves the knobs each beat (设置热更：写 budget.json
// 后无需重启，下一拍即按新值判). cfg.Budget (tests) > the root's
// file/env chain.
func (e *Engine) budgetPolicy() BudgetPolicy {
	ap := e.ap
	if e.cfg.Budget != nil {
		return e.cfg.Budget()
	}
	if ap.Registry != nil {
		return ResolveBudgetPolicy(ap.Registry.Root())
	}
	return BudgetPolicyFromEnv()
}

// budgetSpend resolves the two windows' spend (cfg stub > the ledger,
// the latter through the Server's short-TTL cache — the breaker's beat
// and GET /budget coalesce into at most one python3 read per window).
func (e *Engine) budgetSpend(dayStartMS, weekStartMS int64) (int64, int64, error) {
	ap := e.ap
	if e.cfg.BudgetSpend != nil {
		return e.cfg.BudgetSpend(dayStartMS, weekStartMS)
	}
	return ap.Spend(dayStartMS, weekStartMS)
}

// advanceRooms lists every scope whose 自动推进 is on — the breaker's
// flip list (lobby first, then ACTIVE projects; archived rooms keep
// their switch but have nothing running to break).
func (e *Engine) advanceRooms() []string {
	ap := e.ap
	if ap.Stores.StaffStore == nil {
		return nil
	}
	var keys []string
	if ap.Stores.StaffStore.SettingsOf(chat.LobbyKey).AutoAdvance {
		keys = append(keys, chat.LobbyKey)
	}
	if ap.Stores.ProjectStore != nil {
		for _, p := range ap.Stores.ProjectStore.List() {
			if p.Status != projects.StatusActive || p.Key == chat.LobbyKey {
				continue
			}
			if ap.Stores.StaffStore.SettingsOf(p.Key).AutoAdvance {
				keys = append(keys, p.Key)
			}
		}
	}
	return keys
}

// hubFor is the breaker's broadcast leg: the lobby's hub is the
// server's own; a project room's hub exists only while the registry
// holds one (无人拨号的房静默翻转开关——开关是持久的，播报是现场的).
func (e *Engine) hubFor(key string) *chat.Hub {
	ap := e.ap
	if key == chat.LobbyKey {
		return ap.Hub
	}
	if ap.Registry == nil {
		return nil
	}
	return ap.Registry.Rooms()[key]
}

// mergeIfDue is the merge-proposal gate (v2.8 gitflow): a pending merge
// older than the accept grace is landed through acceptMergeCore as the
// actor 自动驾驶 — ONLY when the branch's three-dot diff is exclusively
// NEW files（vcs.AdditiveOnly：与演化后的主线不可能正面冲突的形状）.
// Any other shape stays the host's call, silently — the room heard the
// submitted card; nagging the host is not this engine's business. A
// refused landing (主树脏/驻枝变了/已被并入) leaves the slot for the
// host the same way autoAccept's denials back off.
func (e *Engine) mergeIfDue(key string, hub *chat.Hub, now time.Time) {
	ap := e.ap
	if ap.Stores.MergeStore == nil {
		return
	}
	cur := ap.Stores.MergeStore.Pending(ap.Subject, key)
	if cur == nil {
		return
	}
	if now.Sub(time.Unix(cur.SubmittedTS, 0)) < e.AcceptDelay(key) ||
		hub.OwnerActiveWithin(chat.OwnerPresenceWindow) {
		// Same presence yield as autoAccept (r_19 修订): an owner seen
		// here recently keeps the merge their call this beat.
		return
	}
	_, repo, err := ap.Git.ProjectGit(key)
	if err != nil || repo == nil {
		return // 总开关关闭（errGitDisabled）/非仓库/探测失败：静默留给房主——与拒收同姿态
	}
	additive, err := repo.AdditiveOnly(context.Background(), cur.Into, cur.Branch)
	if err != nil || !additive {
		return
	}
	grace := util.ShortDur(e.AcceptDelay(key))
	note := i18n.Sf("（全智能模式：纯新增分支满宽限 %s 无人否决，代房主放行）", grace)
	msg := ap.Git.AcceptMergeCore(hub, nil, AutoPilotActor, cur.ID, key, note)
	if msg.Event == "denied" {
		log.Printf("[自动驾驶] %s: 合并代收被拒（槽件留给房主）：%s", key, msg.Text)
		return
	}
	hub.SystemRecorded(i18n.Sf(
		"[自动驾驶] 合并提案 %s（%s → %s）为纯新增形状、待审满 %s 无人否决，已代房主自动并入",
		cur.ID, cur.Branch, cur.Into, grace))
}

// divertIfDue delivers the【自动驾驶·拆解】wake (r_13 t_175; r_17 moves
// it onto cfg.Divert and its own cooldown stamps so restocking and
// decomposing never crowd each other out — 边拆边补): the pool has
// open requirements but nobody is decomposing them — the orchestrator
// gets a "评审立项推进，不是再立新需求" nudge. One nag per window.
func (e *Engine) divertIfDue(key string, hub *chat.Hub, now time.Time) {
	ap := e.ap
	if e.cfg.Divert == nil {
		return
	}
	if now.Before(e.divertBackoff(key)) {
		return
	}
	// 反向让位（复查#1 的引擎半边）：会议室正开着会＝消费在飞——
	// 主持已按会序推进这条需求，拆解注入此刻插进来只会给出相互冲
	// 突的提交指令（plan 槽是单槽）。散会后的下一拍照常。
	if ap.Stores.Meetings != nil {
		if _, live := ap.Stores.Meetings.Active(key); live {
			return
		}
	}
	n := e.NetOpenCount(key) // r_31 净 open：parking 不被拆解钟捞走
	e.stampDivert(key, now)  // 先落戳再投递：Kick 与主循环并发不双发
	if err := e.cfg.Divert(key, n); err != nil {
		log.Printf("[自动驾驶] %s: 拆解注入未送达：%v", key, err)
		hub.System(i18n.Sf("[自动驾驶] %s", err.Error()))
	}
}

// PatrolDueNowForTest is the patrol clock's test seam: it rewinds the
// project's next-patrol stamp so the very next beat fires (tests
// simulate the window elapsing without sleeping).
func (e *Engine) PatrolDueNowForTest(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.patrolAt[key] = time.Time{}
}

// patrolEvery resolves the patrol interval (r_13 t_175).
func (e *Engine) patrolEvery() time.Duration {
	if e.cfg.PatrolEvery > 0 {
		return e.cfg.PatrolEvery
	}
	return AutoPilotPatrolEvery
}

// patrolDue/stampPatrol read and write the next-patrol stamp under the
// engine lock (复查#12: Kick's async roundProject races the loop's —
// an unlocked map write here is a panic, not a missed patrol).
func (e *Engine) patrolDue(key string, now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !now.Before(e.patrolAt[key])
}

func (e *Engine) stampPatrol(key string, until time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.patrolAt[key] = until
}

// lastPatrol (r_24 t_205): the previous patrol's delivery time — the
// cockpit renders「采集于 HH:MM」beside the snapshot so an hour-old
// 快照 never reads as now (评审补充②). Zero = never patrolled.
func (e *Engine) LastPatrol(key string) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.patrolDone[key].Unix()
}

// stampPatrolDone records the patrol's delivery moment (r_24).
func (e *Engine) stampPatrolDone(key string, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.patrolDone[key] = at
}

// capacity is the saturation snapshot one API feeds three consumers
// (r_24 定稿·评审补充①：引擎注入/巡报/驾驶舱同函数不同公式——只有
// 时间差没有口径差)。sat ＝ 在办任务数/在座成员数（定稿 §一钉死，
// ≤100%；零座零活都给 0 而不是除零）。纯函数：两个 store 的即时读。
func (ap *Face) Capacity(key string) (doing, seated, sat int) {
	for _, t := range ap.Stores.Engine.ListFiltered("", tasks.StatusDoing, key, "") {
		if t.Assignee != "" {
			doing++
		}
	}
	for _, row := range ap.Stores.StaffStore.ListByProject(key) {
		if row.Occupying() {
			seated++
		}
	}
	if seated > 0 {
		sat = doing * 100 / seated
		if sat > 100 {
			sat = 100
		}
	}
	return doing, seated, sat
}

// patrolIfDue delivers the HR capacity patrol (r_13 t_175): autopilot
// on + a seated HR + the window elapsed → one【全智能·产能巡报】
// injection carrying the room's task-load snapshot. Refusals cool
// down too (no nag); autopilot-off rooms never reach here.
func (e *Engine) patrolIfDue(key string, hub *chat.Hub, now time.Time) {
	ap := e.ap
	if e.cfg.Patrol == nil {
		return
	}
	if !e.patrolDue(key, now) {
		return
	}
	e.stampPatrol(key, now.Add(e.patrolEvery()))
	// 上下文快照（巡报模板的数据面——HR 直接引用不自己再查一遍）：
	// 饱和度走 capacity 同一份数据（评审补充①的三消费者之一）
	tasksAll, seated, sat := ap.Capacity(key)
	stock := e.StockCount(key)  // 合计（蓄着也算库存）
	open := e.NetOpenCount(key) // 净 open（水位偏低判据——活粮）
	parked := stock - open
	e.stampPatrolDone(key, now)
	poolLine := i18n.Sf("需求池 open %d 条", stock)
	if parked > 0 {
		poolLine += i18n.Sf("（open %d + parking %d）", open, parked) // r_31 构成注
	}
	ctx := i18n.Sf("饱和度 %d%%（在办 %d/%d 座）｜%s", sat, tasksAll, seated, poolLine)
	if open < e.WaterTarget(key) {
		ctx += "｜⚠ 水位偏低"
	}
	if err := e.cfg.Patrol(key, ctx); err != nil {
		// 无人可巡（HR 不在岗）——静默冷却，不打扰房间
		log.Printf("[自动驾驶] %s: 巡报未送达（%v）——本窗跳过", key, err)
	}
}

// resumeStalled is the resume gate: a doing task whose ledger has been
// silent past the stall window gets its assignee woken once per window
// (cfg.Resume, the fleet route). The parked-member stall — the task
// was handed out, the member's turn ended, and nothing in the three
// gates' shapes (no pending proposal, no question card, the room not
// idle) ever re-kicks it — is invisible to every other clock, so the
// engine supplies the host's 「接着干」 itself. Pool tasks (""
// assignee) stay the patrol's beat; a refused wake (the assignee is
// the host or a 待招 seat, not a managed member) surfaces once in the
// room and still stamps the window — the poke's no-nag discipline.
func (e *Engine) resumeStalled(key string, hub *chat.Hub, now time.Time) {
	ap := e.ap
	if e.cfg.Resume == nil {
		return
	}
	stall := e.StallDelay(key)
	by := map[string][]*tasks.Task{}
	list := ap.Stores.Engine.ListFiltered("", tasks.StatusDoing, key, "")
	for i := range list {
		t := &list[i]
		if t.Assignee == "" || t.UpdatedTS <= 0 ||
			now.Sub(time.Unix(t.UpdatedTS, 0)) < stall {
			continue
		}
		by[t.Assignee] = append(by[t.Assignee], t)
	}
	assignees := make([]string, 0, len(by))
	for who := range by {
		assignees = append(assignees, who)
	}
	sort.Strings(assignees)
	for _, who := range assignees {
		if now.Before(e.resumeBackoff(key, who)) {
			continue
		}
		e.stampResume(key, who, now)
		stalled := by[who]
		ids := make([]string, len(stalled))
		for i, t := range stalled {
			ids[i] = t.ID
		}
		if err := e.cfg.Resume(key, who, stalled); err != nil {
			log.Printf("[自动驾驶] %s: 续命注入未送达：%v", key, err)
			hub.SystemRecorded(i18n.Sf("[自动驾驶] %s", err.Error()))
			continue
		}
		hub.SystemRecorded(i18n.Sf(
			"[自动驾驶] 进行中任务 %s 已超 %s 无台账更新，点名 %s 续做（每停滞窗只点一次；确已完成的请落账 done）",
			strings.Join(ids, "/"), util.ShortDur(stall), who))
	}
}

// autoAccept lands the aged pending proposal through the shared leg
// (acceptPlanCore, c nil — no requester to receipt) as the actor
// 自动驾驶. A refused landing (task-cap headroom, a raced supersede)
// backs off one poke window before retrying — the engine never spams
// a failing accept every beat. The breaker fires BEFORE an
// over-cap landing: the cap-th accept of the day lands, the
// (cap+1)-th trips the switch off and leaves the slot to the host.
func (e *Engine) autoAccept(key string, hub *chat.Hub, now time.Time, cur *plan.Plan) {
	ap := e.ap
	if max := e.maxPlans(key); max > 0 && e.AcceptedToday(key, now) >= max {
		// r_19：熔断只关推进——代收是推进侧的职权；补货（蓄水）不受
		// 牵连，池子照常蓄着等房主回流手动审。
		ap.Stores.StaffStore.SetAutoAdvance(key, false)
		hub.SystemRecorded(i18n.Sf(
			"[自动驾驶] 今日自动接受已达上限 %d 份、新提案不再代收——自动推进已自动关闭（防失控熔断；自动补货不受影响，待审提案回到房主关口。明日重开，或 NIUMA_AUTOPILOT_MAXPLANS 调整）", max))
		return
	}
	if now.Before(e.deniedBackoff(key)) {
		return
	}
	grace := util.ShortDur(e.AcceptDelay(key))
	note := i18n.Sf("（自动推进：宽限 %s 无人否决，代房主放行）", grace)
	msg := ap.Plans.AcceptPlanCore(hub, nil, AutoPilotActor, cur.ID, nil, key, note)
	if msg.Event == "denied" {
		log.Printf("[自动驾驶] %s: 自动接受被拒：%s", key, msg.Text)
		hub.SystemRecorded(i18n.Sf("[自动驾驶] 自动接受被拒（%s）——已退避稍后再试，房主可手动处置", msg.Text))
		e.setDeniedBackoff(key, now.Add(e.PokeCooldown(key)))
		return
	}
	e.BumpAccepted(key, now)
	hub.SystemRecorded(i18n.Sf("[自动驾驶] 提案 %s 待审满 %s 无人否决，已代房主自动接受%s", cur.ID, grace, e.todayCount(key, now)))
}

// idle reports whether the project has nothing in flight: no pending
// proposal, no todo/doing task (r_13 t_175 三条件版——开放需求不再
// 单独挡 idle：池里挂着 open 没人拆也算闲置，改道给 stalledBacklog).
func (e *Engine) Idle(key string) bool {
	ap := e.ap
	if ap.Stores.PlanStore.Pending(ap.Subject, key) != nil {
		return false
	}
	for _, t := range ap.Stores.Engine.ListFiltered("", "", key, "") {
		if t.Status == tasks.StatusTodo || t.Status == tasks.StatusDoing {
			return false
		}
	}
	return true
}

// stalledBacklog (r_13 t_175): open requirements exist but nothing is
// in flight — the room isn't idle (work exists) but nobody is doing
// it. The idle route diverts to【自动驾驶·拆解】(cfg.Divert, r_17 its
// own route) instead of restocking.
func (e *Engine) StalledBacklog(key string) bool {
	ap := e.ap
	if !e.Idle(key) {
		return false
	}
	for _, r := range ap.Stores.Requirements.ListByProject(ap.Subject, key) {
		if r.Status == requirements.StatusOpen {
			return true
		}
	}
	return false
}

// stockCount (r_13 t_175 → r_31 改名双口径): the pool's stocked total —
// open+parking rows（「蓄着也算库存」——房主的水位线本意是库存；名字
// 改名防撒谎：openCount 数 parking 的时代一去不返）。
func (e *Engine) StockCount(key string) int {
	ap := e.ap
	n := 0
	for _, r := range ap.Stores.Requirements.ListByProject(ap.Subject, key) {
		if r.Status == requirements.StatusOpen || r.Status == requirements.StatusParking {
			n++
		}
	}
	return n
}

// netOpenCount (r_31): the 活粮 count — open only. 蓄水闸用净 open
// （冻结的库存不是可拆粮，「一个数字两个消费者两种口径」——r_24 同
// 函数教训的 r_31 新实例，两口径写死）。
func (e *Engine) NetOpenCount(key string) int {
	ap := e.ap
	n := 0
	for _, r := range ap.Stores.Requirements.ListByProject(ap.Subject, key) {
		if r.Status == requirements.StatusOpen {
			n++
		}
	}
	return n
}
