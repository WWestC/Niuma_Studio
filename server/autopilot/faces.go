package autopilot

// faces.go — the HTTP read/write faces + the capacity/orchestrator
// helpers, split from autopilot.go (zero logic edits): the engine's
// loop stays there, the mounts live here.

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/util"
)

// HandleKBCapacity is the saturation read face (r_24 t_205): one
// endpoint, three consumers' shared source — the patrol line, the
// 选题 injection and the owner cockpit all render THIS snapshot, so
// 「今晚快照 40% vs 实时 20%」只有时间差没有口径差. Pure read: no
// clock it touches fires (读接口不敲巡报钟——r_24 定稿 §四边界条款).
func (ap *Face) HandleKBCapacity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteJSONErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if ap.Stores.StaffStore == nil || ap.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	rooms := []string{chat.LobbyKey}
	if r.URL.Query().Get("all") != "" {
		for _, p := range ap.Stores.ProjectStore.List() {
			// 大厅是 store 里的常驻 active 项目（EnsureLobby），已在首行
			// 手动放入——这里再追加一次就是驾驶舱里两行一模一样的大厅。
			if p.Status == projects.StatusActive && p.Key != projects.LobbyKey {
				rooms = append(rooms, p.Key)
			}
		}
	}
	type row struct {
		Room    string `json:"room"`
		Doing   int    `json:"doing"`
		Seated  int    `json:"seated"`
		Sat     int    `json:"sat"`
		Open    int    `json:"open"`   // r_31 合计（open+parking——库存口径）
		Parked  int    `json:"parked"` // r_31 暂缓数（驾驶舱「另有 N 条暂缓」灰字行）
		Water   int    `json:"water"`  // r_13 同口径：水位目标线（色签阈值）
		Patrol  int64  `json:"patrol_ts,omitempty"`
		Updated int64  `json:"updated_ts"`
	}
	now := util.Now()
	out := make([]row, 0, len(rooms))
	for _, key := range rooms {
		doing, seated, sat := ap.Capacity(key)
		open, parked := 0, 0
		if ap.Stores.Requirements != nil {
			for _, rq := range ap.Stores.Requirements.ListByProject(ap.Subject, key) {
				switch rq.Status {
				case requirements.StatusOpen:
					open++
				case requirements.StatusParking:
					parked++ // r_31：合计可见（蓄着也算库存），驾驶舱灰字行数据面
				}
			}
		}
		target := agents.WaterTarget
		patrolTS := int64(0)
		if ap.eng != nil {
			target = ap.eng.WaterTarget(key)
			patrolTS = ap.eng.LastPatrol(key)
		}
		out = append(out, row{Room: key, Doing: doing, Seated: seated, Sat: sat,
			Open: open + parked, Parked: parked, Water: target, Patrol: patrolTS, Updated: now})
	}
	httputil.WriteJSONZip(w, r, map[string]any{"rooms": out, "now": now})
}

// replenishIfDue is the stocking gate (r_17 蓄水闸): the pool's water
// level below agents.WaterTarget pokes the orchestrator through cfg.Poke
// to FILE requirements — busy or idle alike (the pre-r_17 shape poked
// only a fully idle room with an empty pool and rode the single filing
// straight to a proposal, pinning the level at 0..1: the pool never
// held inventory). The 选题 injection's own discipline caps the filing
// at the target line and leaves the entries IN the pool — consumption
// is the 拆解 divert and the meeting clock's beat, never the restock's.
// A refused poke (no dispatcher, no seated orchestrator) still stamps
// the cooldown — the room must not hear the same complaint every beat.
// Dry pokes back off (复查#11): when the water hasn't moved since the
// last poke the window doubles (capped 4h) — 「本期无新需求」answered
// into the room is not machine-readable, but an unchanged level is.
func (e *Engine) ReplenishIfDue(key string, hub *chat.Hub, now time.Time) {
	if e.cfg.Poke == nil {
		return
	}
	n := e.NetOpenCount(key) // r_31 净 open：蓄水闸对活粮判线（parking 不算可拆粮）
	if n >= e.WaterTarget(key) {
		e.resetDryPoke(key) // 到线即清连击：编排者在正常补货
		return
	}
	if now.Before(e.pokeBackoff(key)) {
		return
	}
	e.stampPoke(key, now, n) // 先落戳再投递：Kick 与主循环并发不双发
	if err := e.cfg.Poke(key, n); err != nil {
		log.Printf("[自动驾驶] %s: 选题注入未送达：%v", key, err)
		hub.System(i18n.Sf("[自动驾驶] %s", err.Error()))
	}
}

// Kick runs one single-project pass off the beat (the enable face's
// immediate feedback — a just-enabled idle room hears its first poke
// within moments, not next minute). Asynchronous: the handler never
// blocks on the landing leg.
func (e *Engine) Kick(project string) {
	ap := e.ap
	go func() {
		util.Guard("server: autopilot", func() { e.roundProject(project, ap.PlanHub(project), time.Now()) })
		select {
		case e.debug <- struct{}{}:
		default:
		}
	}()
}

// resetStockClock（目标制补货）: a fresh enable (off→on) or a goal
// rewrite is a NEW session — the old cooldown anchor and dry streak
// belong to the previous conversation. Without this reset the enable
// face's Kick lands inside the pre-enable poke's window (30min, or 4h
// at the dry cap) and silently no-ops: the host just set a goal and
// hears nothing for half an hour. Clear all three stamps; the next
// round or Kick pokes at once.
func (e *Engine) resetStockClock(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.pokes, key)
	delete(e.pokeWater, key)
	delete(e.pokeDry, key)
}

// --- knobs & counters ----------------------------------------------------

// knobsFor（r_14 t_171）: per-project knob overlay — the four-value
// chain is 设置(staffing) > env(cfg, main 注入) > 内置常量. Reading
// staffing every call is cheap (in-memory map) and makes knob writes
// hot (no engine restart, no cache to invalidate).
func (e *Engine) knobsFor(key string) staffing.AutoPilotKnobs {
	ap := e.ap
	if ap.Stores.StaffStore == nil {
		return staffing.AutoPilotKnobs{}
	}
	if k := ap.Stores.StaffStore.SettingsOf(key).AutoPilotKnobs; k != nil {
		return *k
	}
	return staffing.AutoPilotKnobs{}
}

func (e *Engine) AcceptDelay(key string) time.Duration {
	if k := e.knobsFor(key); k.AcceptDelayS > 0 {
		return time.Duration(k.AcceptDelayS) * time.Second
	}
	if e.cfg.AcceptDelay > 0 {
		return e.cfg.AcceptDelay
	}
	return AutoPilotAcceptDelay
}

func (e *Engine) PokeCooldown(key string) time.Duration {
	if k := e.knobsFor(key); k.PokeEveryMin > 0 {
		return time.Duration(k.PokeEveryMin) * time.Minute
	}
	if e.cfg.PokeCooldown > 0 {
		return e.cfg.PokeCooldown
	}
	return AutoPilotPokeEvery
}

func (e *Engine) StallDelay(key string) time.Duration {
	if k := e.knobsFor(key); k.StallDelayMin > 0 {
		return time.Duration(k.StallDelayMin) * time.Minute
	}
	if e.cfg.StallDelay > 0 {
		return e.cfg.StallDelay
	}
	return AutoPilotStallDelay
}

func (e *Engine) maxPlans(key string) int {
	if k := e.knobsFor(key); k.MaxPlans != 0 {
		return k.MaxPlans
	}
	if e.cfg.MaxPlans != 0 {
		return e.cfg.MaxPlans
	}
	return AutoPilotMaxPlans
}

// waterTarget（r_19）: 蓄水目标线两级链——设置旋钮（staffing
// water_target，1–9）> 内置默认 agents.WaterTarget（3）。无 env 档
// （水位线是产品口径不是部署口径）。每拍直读即热更；蓄水闸与巡报
// 快照的「⚠ 水位偏低」共用这一口径。
func (e *Engine) WaterTarget(key string) int {
	if k := e.knobsFor(key); k.WaterTarget > 0 {
		return k.WaterTarget
	}
	return agents.WaterTarget
}

// dayFor returns (rolling as needed) the project's counter block.
// Callers hold e.mu.
func (e *Engine) dayFor(key string, now time.Time) *apDay {
	day := now.Format("2006-01-02")
	d := e.days[key]
	if d == nil || d.day != day {
		d = &apDay{day: day}
		e.days[key] = d
	}
	return d
}

func (e *Engine) AcceptedToday(key string, now time.Time) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dayFor(key, now).accepted
}

func (e *Engine) BumpAccepted(key string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dayFor(key, now).accepted++
	e.saveDayBookLocked(now)
}

// dayBookPath is the counter book's home; "" when no registry root is
// wired (tests, embedded faces) — the counter then stays in-memory,
// the pre-persistence shape.
func (e *Engine) dayBookPath() string {
	ap := e.ap
	if ap == nil || ap.Registry == nil || ap.Registry.Root() == "" {
		return ""
	}
	return filepath.Join(ap.Registry.Root(), "autopilot_day.json")
}

// loadDayBook re-seeds today's counters after a restart: same-day page
// plants, any other page is a leaf the calendar already turned. Boot
// only, before the first round.
func (e *Engine) loadDayBook(now time.Time) {
	path := e.dayBookPath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[自动驾驶] 日代收台账读取失败（计数从零开始）：%v", err)
		}
		return
	}
	var book ApDayBook
	if err := json.Unmarshal(raw, &book); err != nil {
		log.Printf("[自动驾驶] 日代收台账解析失败（计数从零开始）：%v", err)
		return
	}
	if book.Day != now.Format("2006-01-02") {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, n := range book.Counts {
		if n > 0 {
			e.days[key] = &apDay{day: book.Day, accepted: n}
		}
	}
}

// saveDayBookLocked persists today's page (temp+rename; a failure only
// logs — the counter is a runaway guard, a lost page just resets the
// cap's numerator, it never bills anyone). Callers hold e.mu.
func (e *Engine) saveDayBookLocked(now time.Time) {
	path := e.dayBookPath()
	if path == "" {
		return
	}
	day := now.Format("2006-01-02")
	book := ApDayBook{Day: day, Counts: map[string]int{}}
	for key, d := range e.days {
		if d.day == day && d.accepted > 0 {
			book.Counts[key] = d.accepted
		}
	}
	raw, err := json.Marshal(book)
	if err != nil {
		return
	}
	if err := persist.Save(path, raw, 0o644); err != nil {
		log.Printf("[自动驾驶] 日代收台账落盘失败：%v", err)
	}
}

// todayCount renders the landing line's audit tail（今日第 n 份）.
func (e *Engine) todayCount(key string, now time.Time) string {
	if max := e.maxPlans(key); max > 0 {
		return i18n.Sf("（今日第 %d/%d 份）", e.AcceptedToday(key, now), max)
	}
	return i18n.Sf("（今日第 %d 份）", e.AcceptedToday(key, now))
}

func (e *Engine) pokeBackoff(key string) time.Time {
	e.mu.Lock()
	last, dry := e.pokes[key], e.pokeDry[key]
	e.mu.Unlock()
	if last.IsZero() {
		return time.Time{}
	}
	if dry > 3 {
		dry = 3
	}
	cd := e.PokeCooldown(key) << uint(dry)
	if cd > 4*time.Hour || cd <= 0 { // 封顶 4h；溢出/零值夹回
		cd = 4 * time.Hour
	}
	return last.Add(cd)
}

// stampPoke anchors the cooldown and advances the dry streak: a poke
// landing on the SAME water level as its predecessor (nothing filed,
// nothing consumed — the 选题 injection allows 「本期无新需求」as an
// answer, but the engine can't hear the say) counts dry, and the next
// window doubles.
func (e *Engine) stampPoke(key string, now time.Time, water int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	dry := 0
	if !e.pokes[key].IsZero() && e.pokeWater[key] == water {
		dry = e.pokeDry[key] + 1
		if dry > 3 {
			dry = 3
		}
	}
	e.pokeDry[key] = dry
	e.pokes[key] = now
	e.pokeWater[key] = water
}

// resetDryPoke clears the streak — the pool topped the line, the
// orchestrator IS stocking; the next dip starts fresh.
func (e *Engine) resetDryPoke(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pokeDry[key] = 0
}

// divertBackoff is when the 拆解 wake may fire again — its OWN window
// (r_17), same no-nag shape the restock rides; separate stamps so a
// restock never postpones a decompose or vice versa (边拆边补).
func (e *Engine) divertBackoff(key string) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.diverts[key].Add(e.PokeCooldown(key))
}

func (e *Engine) stampDivert(key string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.diverts[key] = now
}

// resumeBackoff is when the assignee may hear the 续命 wake again —
// one nudge per stall window, the same no-nag shape the poke rides.
func (e *Engine) resumeBackoff(key, assignee string) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resumes[key+"\x00"+assignee].Add(e.StallDelay(key))
}

func (e *Engine) stampResume(key, assignee string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resumes[key+"\x00"+assignee] = now
}

func (e *Engine) deniedBackoff(key string) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deniedUntil[key]
}

func (e *Engine) setDeniedBackoff(key string, until time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deniedUntil[key] = until
}

// --- the autopilot write face -------------------------------------------
//
// GET /p/{key}/autopilot: {"project", "autopilot", "max_plans"} — the
// settings card's state source (max_plans: -1 = unlimited). POST
// flips the switch: turning ON demands the protocol confirm token
// (reset precedent) AND a seated orchestrator (an engine with nobody
// to drive it would idle silently); turning OFF is always free. Both
// directions land a recorded system line in the project room — the
// mode flip is history-grade news, not a transient toast.

func (ap *Face) HandleProjectAutopilot(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if ap.Stores.StaffStore == nil || ap.Stores.ProjectStore == nil || !ap.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		// r_14 t_171：GET 回显带全部生效旋钮（设置面滑杆的回读面——
		// 引擎在跑用引擎的解析值〔设置>env>内置三级已折算〕，没跑用包默认）；
		// r_19：双开关各自回显，autopilot＝两者之或（房间 ⚡ 徽标的兼容口径）。
		knobs := map[string]any{
			"max_plans": ap.autoPilotMaxPlans(key),
		}
		if ap.eng != nil {
			knobs["poke_every_min"] = int(ap.eng.PokeCooldown(key) / time.Minute)
			knobs["accept_delay_s"] = int(ap.eng.AcceptDelay(key) / time.Second)
			knobs["stall_delay_min"] = int(ap.eng.StallDelay(key) / time.Minute)
			knobs["water_target"] = ap.eng.WaterTarget(key)
		} else {
			knobs["poke_every_min"] = int(AutoPilotPokeEvery / time.Minute)
			knobs["accept_delay_s"] = int(AutoPilotAcceptDelay / time.Second)
			knobs["stall_delay_min"] = int(AutoPilotStallDelay / time.Minute)
			knobs["water_target"] = agents.WaterTarget
		}
		set := ap.Stores.StaffStore.SettingsOf(key)
		httputil.WriteJSON(w, map[string]any{
			"project":      key,
			"auto_stock":   set.AutoStock,
			"auto_advance": set.AutoAdvance,
			"autopilot":    set.AnyAutoOn(),
			"stock_goal":   set.StockGoal,
			"knobs":        knobs,
			"max_plans":    ap.autoPilotMaxPlans(key),
		})
	case http.MethodPost:
		var body struct {
			// r_19 双开关写面：Stock/Advance 各自可选（nil＝本次不动），
			// 可同请求双写（双开＝旧全智能）。旧 `on` 字段退役。
			Stock   *bool  `json:"stock"`
			Advance *bool  `json:"advance"`
			Confirm string `json:"confirm"`
			// Goal（目标制补货）: 本次目标——开补货必填（off→on 时缺它
			// 即 400）；补货已开时随带即热更（编排者下一轮选题注入按新
			// 目标判定）。推进侧不读它。
			Goal string `json:"goal"`
			// r_14 t_171：单旋钮热更——带 knobs 时只写旋钮不动开关（开关
			// 仍走 stock/advance+confirm 协议门；旋钮写无确认门——可逆、
			// 非危险操作）
			Knobs *staffing.AutoPilotKnobs `json:"knobs"`
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 安全核查修复：有界读取
		if err == nil && len(data) > 0 {
			err = json.Unmarshal(data, &body)
		} else if err == nil {
			err = errors.New(i18n.S("载荷缺失"))
		}
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
			return
		}
		if body.Knobs != nil {
			// 旋钮热更路径：写 staffing、下一拍生效（引擎每拍读设置）
			set := ap.Stores.StaffStore.SetAutoPilotKnobs(key, *body.Knobs)
			httputil.WriteJSON(w, map[string]any{
				"project": key,
				"knobs":   set.AutoPilotKnobs,
			})
			return
		}
		if body.Stock == nil && body.Advance == nil && strings.TrimSpace(body.Goal) == "" {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("载荷无事可做：开关写请带 stock/advance 字段（布尔），旋钮写请带 knobs，目标写请带 goal"))
			return
		}
		enabling := (body.Stock != nil && *body.Stock) || (body.Advance != nil && *body.Advance)
		if enabling {
			if body.Confirm != autopilotConfirmToken {
				httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("开启自动补货/自动推进需确认令牌 confirm=AUTOPILOT——无人值守将消耗 token，请从前端设置卡的警告对话框发起"))
				return
			}
			if ap.eng == nil {
				httputil.WriteJSONErr(w, http.StatusConflict, i18n.S("自动驾驶引擎未启用（--no-autopilot 或内嵌形态）——开关状态可写但无人执行"))
				return
			}
			// 编排者门只卡补货：选题注入需要接收人；推进侧（代收/续命）
			// 无编排者也照跑（拆解注入会拒送并留房内提示，可接受）。
			pre := ap.Stores.StaffStore.SettingsOf(key)
			if body.Stock != nil && *body.Stock && !ap.hasSeatedOrchestrator(key) {
				httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("项目 %s 没有在编的编排者（岗位「%s」）——自动补货的选题注入无人可送；请先在人员管理补齐编排者再开启", key, agents.OrchestratorRole))
				return
			}
			// 目标门（目标制补货）：off→on 必须带本次目标——没有目标的
			// 蓄水就是上一轮「每 4 小时一句本期无新需求」的死循环；目标
			// 达成后编排者会自动收摊（关补货＋推进）。存量已开的房不追
			// 补（直连 SetAutoStock 的路径与引擎侧都容忍空目标，回落
			// r_17 旧纪律）。
			if body.Stock != nil && *body.Stock && !pre.AutoStock && strings.TrimSpace(body.Goal) == "" {
				httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("开启自动补货需填写本次目标（goal 字段）——说明这轮要达成什么；编排者判定目标达成（或确认无法达成）后会自动关闭自动补货与自动推进"))
				return
			}
		}
		goal := strings.TrimSpace(body.Goal)
		if goal != "" {
			ap.Stores.StaffStore.SetStockGoal(key, goal)
		}
		set := ap.Stores.StaffStore.SettingsOf(key)
		// 目标制新会话：off→on 或「开着补货换目标」——冷却锚/干涸连击
		// 属于上一段对话，Kick 必须立刻点名而不是被旧 30min/4h 窗吞掉。
		//（按写入后的补货态判：off→on 时 set 还没翻，得看 body.Stock。）
		stockAfter := set.AutoStock || (body.Stock != nil && *body.Stock)
		freshStockSession := goal != "" && stockAfter
		hub := ap.PlanHub(key)
		turnedOn := false
		if body.Stock != nil && *body.Stock != set.AutoStock {
			set = ap.Stores.StaffStore.SetAutoStock(key, *body.Stock)
			if *body.Stock {
				turnedOn = true
				if goal != "" {
					hub.SystemRecorded(i18n.Sf("[自动驾驶] 房主开启了自动补货（本次目标：%s）：需求池 open 低于目标线（缺省 3 条）即注入【自动驾驶·选题】补货蓄水——先蓄需求不急开工，选题注入只送编排者。编排者判定本次目标达成（或确认无法达成）后将自动关闭自动补货与自动推进。注意 token 消耗（设置卡·智能 可随时关闭）", goal))
				} else {
					hub.SystemRecorded(i18n.S("[自动驾驶] 房主开启了自动补货：需求池 open 低于目标线（缺省 3 条）即注入【自动驾驶·选题】补货蓄水——先蓄需求不急开工，选题注入只送编排者。注意 token 消耗（设置卡·智能 可随时关闭）"))
				}
			} else {
				hub.SystemRecorded(i18n.S("[自动驾驶] 房主关闭了自动补货：需求池不再自动补货，既有池内需求不受影响"))
			}
		}
		if body.Advance != nil && *body.Advance != set.AutoAdvance {
			set = ap.Stores.StaffStore.SetAutoAdvance(key, *body.Advance)
			if *body.Advance {
				turnedOn = true
				hub.SystemRecorded(i18n.S("[自动驾驶] 房主开启了自动推进：评审/直提拆解 → 排期 → 提案满宽限自动接受 → 执行；房主在场（本房近 10 分钟内有发言/点卡）时代收与提问卡放行让位、照常等你点选；不在场时提问卡先给约 2 分钟点选窗（宽限随 accept_delay 旋钮），满窗按合理假设放行；进行中任务停滞超阈（缺省 30 分钟）会被点名续做。注意 token 消耗（设置卡·智能 可随时关闭）"))
			} else {
				hub.SystemRecorded(i18n.S("[自动驾驶] 房主关闭了自动推进：待审提案回到房主关口；进行中的任务不受影响"))
			}
		}
		if freshStockSession && ap.eng != nil {
			ap.eng.resetStockClock(key)
		}
		if (turnedOn || freshStockSession) && ap.eng != nil {
			ap.eng.Kick(key)
		}
		httputil.WriteJSON(w, map[string]any{
			"project":      key,
			"auto_stock":   set.AutoStock,
			"auto_advance": set.AutoAdvance,
			"autopilot":    set.AnyAutoOn(),
			"stock_goal":   set.StockGoal,
		})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}

// autoPilotMaxPlans is the GET face's cap reflection (the engine's
// resolved value when one runs, the package default otherwise).
func (ap *Face) autoPilotMaxPlans(project string) int {
	if ap.eng != nil {
		return ap.eng.maxPlans(project)
	}
	return AutoPilotMaxPlans
}

// hasSeatedOrchestrator reports whether the project's staffing table
// holds an on-board row carrying the orchestrator role marker — the
// autopilot's topic-selection and proposal loop is the orchestrator's
// to run; enabling the mode without one would idle silently.
func (ap *Face) hasSeatedOrchestrator(projectKey string) bool {
	return ap.seatedOrchestratorNamed(projectKey, "")
}

// seatedOrchestratorNamed is hasSeatedOrchestrator with an optional
// name pin: non-empty name demands THAT person be the seated
// orchestrator (the wrap-up leg's sender gate). Empty = any.
func (ap *Face) seatedOrchestratorNamed(projectKey, name string) bool {
	if ap.Stores.StaffStore == nil {
		return false
	}
	for _, row := range ap.Stores.StaffStore.ListByProject(projectKey) {
		if row.Occupying() && row.ProjectRole == agents.OrchestratorRole &&
			(name == "" || row.Person == name) {
			return true
		}
	}
	return false
}

// autoPilotDoneAs is the goal-scoped wrap-up leg（目标制补货收摊，
// reqCreateAs 的形状）: the room's seated orchestrator dials in and
// declares the host's 本次目标 met (or provably unreachable) — the
// server closes BOTH r_19 switches and leaves one recorded system line;
// the private "autopilot" reply is the CLI verb's receipt. Idempotent:
// both switches already off → completed, no broadcast (nothing happened,
// nothing to announce). The host is deliberately NOT admitted here —
// 收摊是编排者的判定职权，房主的手动开关走设置卡（AUTOPILOT 确认门
// 的那半边世界）。
func (ap *Face) AutoPilotDoneAs(hub *chat.Hub, actor, note, projectKey string) chat.Message {
	now := time.Now().Unix()
	deny := func(text string) chat.Message {
		return chat.Message{Type: chat.MsgAutopilot, Event: "denied", From: actor, Text: text, TS: now}
	}
	if ap.Stores.StaffStore == nil {
		return deny(i18n.S("编制表不可用——无法收摊自动驾驶"))
	}
	if !ap.seatedOrchestratorNamed(projectKey, actor) {
		return deny(i18n.Sf("收摊只能由本房在岗编排者（岗位「%s」）发起——房主请走设置卡·智能手动关闭", agents.OrchestratorRole))
	}
	set := ap.Stores.StaffStore.SettingsOf(projectKey)
	if !set.AnyAutoOn() {
		return chat.Message{Type: chat.MsgAutopilot, Event: "completed", From: actor,
			Text: i18n.S("本房的自动补货与自动推进均已关闭——无事可收摊"), TS: now}
	}
	goalClause := ""
	if set.StockGoal != "" {
		goalClause = " " + i18n.Sf("（本次目标：%s）", set.StockGoal)
	}
	noteClause := ""
	if note = strings.TrimSpace(note); note != "" {
		noteClause = " " + i18n.Sf("结论：%s。", note)
	}
	if set.AutoStock {
		ap.Stores.StaffStore.SetAutoStock(projectKey, false)
	}
	if set.AutoAdvance {
		ap.Stores.StaffStore.SetAutoAdvance(projectKey, false)
	}
	hub.SystemRecorded(i18n.Sf("[自动驾驶] 编排者 %s 判定本次目标已达成%s%s——自动补货与自动推进已自动关闭（本就未开的不受影响）；下次使用请房主到设置卡·智能重新开启", actor, goalClause, noteClause))
	return chat.Message{Type: chat.MsgAutopilot, Event: "completed", From: actor,
		Text: i18n.S("收摊完成：自动补货与自动推进均已关闭（房内已播报系统行）"), TS: now}
}

// Locked runs fn under the engine's mutex (the tests' read face).
func (e *Engine) Locked(fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn()
}

// PokeDryStreak reads the restock backoff counter (the tests' read face).
func (e *Engine) PokeDryStreak(project string) int {
	return e.pokeDry[project]
}
