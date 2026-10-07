package achv

// Package achv is the achievements domain (r_18 t_187): the pure
// evaluation core (Evaluate over Defs/State/Event — no I/O), the
// default 18-medal table, and the persistence/broadcast engine over
// the staffing companion. Split from the shell's achievements.go;
// the shell keeps the event taps (observeAchievements/nightLightTap)
// and the HTTP face, aliased to these types.

import (
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// AchievementDef is one medal in the table (t_186 定稿四字段——无奖励
// 字段可写，「只统计不驱动」在结构上锁死). Cond is a pure predicate:
// given the event and the current state, does this event unlock it?
type AchievementDef struct {
	Key  string // 稳定键（持久化主键）
	Name string // 名称
	Text string // 达成文案（小马筛子：只描述过去不期许未来）
	// Cond: nil = never fires from events (manual/全室被动型由挂点直调)
	Cond func(ev Event, st *AchieveState) bool
	// PerMember: true = per-person medal (state keyed by member name);
	// false = 全室集体成就 (工作室名下).
	PerMember bool
	// Medal: 展示档位 0=金 1=银 2=铜——与 pixart trophyPalettes／前端
	// MEDAL_PAL 索引同序的孪生常数。纯展示元数据（货架摆色），不进
	// 判定、不落盘、不驱动任何东西（「只统计不驱动」红线不受扰）。
	Medal int
}

// Event is one moment in the office's existing flow. Five kinds ride
// the current wiring (零新埋点): task-done, req-split, tests-green,
// night-light, plan-accept, kb-write.
type Event struct {
	Kind      string // "task-done" | "req-split" | "tests-green" | "night-light" | "plan-accept" | "kb-write"
	Member    string // 事件主语（task 的 assignee；空 = 全室事件）
	ReqID     string // req-split 带需求号
	Project   string // req-split 带需求所属项目（v2.10：号段分家，键带架限定防两房 r_01 并线）
	DocKey    string // kb-write 带文档键（design/ 前缀判设计定稿）
	TestCount int    // tests-green 带当前总数
	At        int64  // util.Now() 拍章
}

// AchieveState is the engine's whole memory: per-member counters, the
// studio-wide counters, and the unlocked sets (幂等基准). Pure value —
// Evaluate never mutates the input, it returns the next state.
type AchieveState struct {
	// per-member: name → {done 计数, 参与的需求线, 提案被接受, kb 笔数}
	Members map[string]*MemberTally
	// studio-wide
	ReqLinesClosed int // req-split 事件数（需求线闭环条数）
	TestGreenRuns  int // tests-green 上报次数
	NightLights    int // 深夜亮灯（全室被动）
	// unlocked: key → unlocked unix ts（已达成即永在——幂等基准＋纪念时间）
	Unlocked map[string]int64
}

// MemberTally is one member's counters.
type MemberTally struct {
	TasksDone     int
	ReqLines      map[string]bool // 参与的需求线（贡献记录——团队史不是绩效）
	PlansAccepted int             // 提案被接受次数（A4 首个/累计）
	DesignDocs    int             // design/ 前缀的 kb write 笔数（A6）
	KbWrites      int             // kb write 总笔数（A12）
}

func newAchieveState() *AchieveState {
	return &AchieveState{
		Members:  map[string]*MemberTally{},
		Unlocked: map[string]int64{},
	}
}

func (s *AchieveState) member(name string) *MemberTally {
	if s.Members[name] == nil {
		s.Members[name] = &MemberTally{ReqLines: map[string]bool{}}
	}
	return s.Members[name]
}

// DeepCopy: the persisted round-trip and Evaluate's next-state both
// need an independent snapshot.
func (s *AchieveState) DeepCopy() *AchieveState {
	out := newAchieveState()
	out.ReqLinesClosed = s.ReqLinesClosed
	out.TestGreenRuns = s.TestGreenRuns
	out.NightLights = s.NightLights
	for k, v := range s.Unlocked {
		out.Unlocked[k] = v
	}
	for n, m := range s.Members {
		nm := &MemberTally{TasksDone: m.TasksDone, PlansAccepted: m.PlansAccepted,
			DesignDocs: m.DesignDocs, KbWrites: m.KbWrites, ReqLines: map[string]bool{}}
		for r := range m.ReqLines {
			nm.ReqLines[r] = true
		}
		out.Members[n] = nm
	}
	return out
}

// Evaluate is the pure core: event + state → (next state, newly
// unlocked defs). The input state is never mutated (callers may share
// it); the returned state is a fresh copy. Achievements already in
// the unlocked set are skipped — 幂等 by construction, replay is a
// no-op. The member key scopes per-person medals (same member same
// medal fires once); studio medals key on the def key alone.
func Evaluate(ev Event, st *AchieveState, table []AchievementDef) (*AchieveState, []AchievementDef) {
	next := st.DeepCopy()
	// ① 先滚计数（判定谓词看的是「含本事件」的新世界）
	switch ev.Kind {
	case "task-done":
		if ev.Member != "" {
			next.member(ev.Member).TasksDone++
		}
	case "req-split":
		next.ReqLinesClosed++
		if ev.Member != "" && ev.ReqID != "" {
			line := ev.ReqID
			if ev.Project != "" {
				line = ev.Project + "/" + ev.ReqID
			}
			next.member(ev.Member).ReqLines[line] = true
		}
	case "tests-green":
		next.TestGreenRuns++
	case "night-light":
		next.NightLights++
	case "plan-accept":
		if ev.Member != "" {
			next.member(ev.Member).PlansAccepted++
		}
	case "kb-write":
		if ev.Member != "" {
			next.member(ev.Member).KbWrites++
			if strings.HasPrefix(ev.DocKey, "design/") {
				next.member(ev.Member).DesignDocs++
			}
		}
	}
	// ② 再过表（谓词读 next——含本事件的口径）
	var fired []AchievementDef
	for _, def := range table {
		key := def.Key
		if def.PerMember {
			key = def.Key + "@" + ev.Member
		}
		if _, done := next.Unlocked[key]; done {
			continue // 幂等：已达成永不再触发
		}
		// 自举型：achievement-live 事件直配 C6 一枚（上线彩蛋——挂点
		// 直调语义，键名精确匹配，其余 nil-Cond（事件面未接的 A5/A7/
		// A8/A9/A10/A11/C4/C5）不被误触发）
		if def.Cond == nil && ev.Kind == "achievement-live" && !def.PerMember && def.Key == "achievement-live" {
			next.Unlocked[key] = ev.At
			fired = append(fired, def)
			continue
		}
		if def.Cond != nil && def.Cond(ev, next) {
			next.Unlocked[key] = ev.At
			fired = append(fired, def)
		}
	}
	return next, fired
}

// ── 默认成就表（t_186 定稿 18 枚整表换入，t_189 验收裁定随 t_208 落地）──
//
// 换入口径：事件源五路（task-done / req-split / tests-green / night-light /
// plan-accept / kb-write），Cond 只写「当前事件流可算」的判定；不可从既有
// 埋点算的（A5/A7/A8/A10/A11/C4/C5）Cond=nil——nil = never fires from
// events（全室被动/人工型语义，结构注释既有约定），待对应事件面接入后补
// Cond 即激活（表驱动契约：引擎结构零改动）。
//
// 红线（t_186 §〇）：无排名/无时效/无补偿——文案只描述过去；「第一张」
// 序数合法（禁词防的是排名不是序数——t_189 裁定）。

// DefaultAchievements 定稿 18 枚（12 个人里程碑＋6 集体）。Medal 档位
// 按「累计量级/稀缺度」定：破纪录的大里程碑金、中坚银、入门铜；集体
// 记忆 6 枚全金（全室的纪念，货架顶排）。
func DefaultAchievements() []AchievementDef {
	return []AchievementDef{
		// ── 个人里程碑（12 枚）─────────────────────────────────────
		{Key: "first-task", Name: "初来乍到", Text: "完成了第一张任务单", PerMember: true, Medal: 2,
			Cond: func(ev Event, st *AchieveState) bool {
				return ev.Kind == "task-done" && ev.Member != "" && st.member(ev.Member).TasksDone == 1
			}},
		{Key: "ten-tasks", Name: "十全十美", Text: "第十张任务单完工", PerMember: true, Medal: 1,
			Cond: func(ev Event, st *AchieveState) bool {
				return ev.Kind == "task-done" && ev.Member != "" && st.member(ev.Member).TasksDone == 10
			}},
		{Key: "fifty-tasks", Name: "半百老将", Text: "累计完成五十张任务单", PerMember: true, Medal: 0,
			Cond: func(ev Event, st *AchieveState) bool {
				return ev.Kind == "task-done" && ev.Member != "" && st.member(ev.Member).TasksDone == 50
			}},
		{Key: "first-plan", Name: "一锤定音", Text: "第一个提案获得通过", PerMember: true, Medal: 1,
			Cond: func(ev Event, st *AchieveState) bool {
				return ev.Kind == "plan-accept" && ev.Member != "" && st.member(ev.Member).PlansAccepted == 1
			}},
		{Key: "first-review", Name: "主持首秀", Text: "主持了第一场评审会", PerMember: true, Medal: 2,
			Cond: nil}, // A5：评审会主持事件面未接（meeting Begin 有主持位）——接Cond时激活
		{Key: "design-final", Name: "落笔定稿", Text: "第一份设计定稿入册", PerMember: true, Medal: 1,
			Cond: func(ev Event, st *AchieveState) bool {
				return ev.Kind == "kb-write" && ev.Member != "" && strings.HasPrefix(ev.DocKey, "design/") && st.member(ev.Member).DesignDocs == 1
			}},
		{Key: "audit-pass", Name: "火眼金睛", Text: "第一次验收盖章", PerMember: true, Medal: 1,
			Cond: nil}, // A7：验收结单事件面未接——接Cond时激活
		{Key: "bug-catch", Name: "抓虫高手", Text: "测试拦下第一个真问题", PerMember: true, Medal: 1,
			Cond: nil}, // A8：真缺口判定无既有事件源——接Cond时激活
		{Key: "test-hundred", Name: "百项护栏", Text: "一百项测试用例入库", PerMember: true, Medal: 0,
			Cond: nil}, // A9：名下测试归属无既有事件源（tests-green 是全室数）——接Cond时激活
		{Key: "cross-role", Name: "左右逢源", Text: "与三种岗位并肩干过活", PerMember: true, Medal: 2,
			Cond: nil}, // A10：跨岗位协作图无既有事件源——接Cond时激活
		{Key: "apprentice", Name: "良师益友", Text: "带出的新人交出了第一单", PerMember: true, Medal: 0,
			Cond: nil}, // A11：师徒关系无既有事件源——接Cond时激活
		{Key: "kb-author", Name: "著书立说", Text: "黑板上留下十篇文档", PerMember: true, Medal: 0,
			Cond: func(ev Event, st *AchieveState) bool {
				return ev.Kind == "kb-write" && ev.Member != "" && st.member(ev.Member).KbWrites == 10
			}},
		// ── 集体记忆（6 枚，挂工作室名下，全金档）──────────────────
		{Key: "req-line-10", Name: "十全线路", Text: "十条需求线从立项走到发布", PerMember: false, Medal: 0,
			Cond: func(ev Event, st *AchieveState) bool {
				return ev.Kind == "req-split" && st.ReqLinesClosed == 10
			}},
		{Key: "test-500", Name: "半千护栏", Text: "全室测试突破五百项", PerMember: false, Medal: 0,
			Cond: func(ev Event, st *AchieveState) bool {
				return ev.Kind == "tests-green" && ev.TestCount >= 500
			}},
		{Key: "night-owl", Name: "亮灯的深夜", Text: "办公室度过的第十个深夜", PerMember: false, Medal: 0,
			Cond: func(ev Event, st *AchieveState) bool {
				return ev.Kind == "night-light" && st.NightLights == 10
			}},
		{Key: "dawn-watch", Name: "晨光行动", Text: "迎着晨光还在干活的那一夜", PerMember: false, Medal: 0,
			Cond: nil}, // C4：凌晨 4-6 点全员在线快照无既有事件源——接Cond时激活
		{Key: "full-house", Name: "满员之夜", Text: "所有岗位都亮着灯的时刻", PerMember: false, Medal: 0,
			Cond: nil}, // C5：编制满员快照无既有事件源——接Cond时激活
		{Key: "achievement-live", Name: "成就系统上线", Text: "这间办公室开始纪念自己", PerMember: false, Medal: 0,
			Cond: nil}, // C6 自举彩蛋：表换入时挂点直调一次性触发（不依赖事件流）
	}
}

// ── 引擎（持久化＋挂点接线）──────────────────────────────────────────

// AchievementEngine owns the live state (restored from the staffing
// companion at boot, flushed on every unlock) and the broadcast hook.
type AchievementEngine struct {
	mu    sync.Mutex
	state *AchieveState
	table []AchievementDef
	staff *staffing.Store
	// Broadcast: fired medals' 广播面（system 行 + star 泡由 server 侧
	// 组装——引擎只交达成名单，表现层归 caller）
	Broadcast func(hub interface{ System(string) }, fired []AchievementDef, member string)
}

// NewAchievementEngine restores the persisted unlocked set (只存已达成，
// 重启即恢复基准——不回放历史，防补算洪水).
func NewAchievementEngine(staff *staffing.Store) *AchievementEngine {
	e := &AchievementEngine{state: newAchieveState(), staff: staff}
	e.table = DefaultAchievements()
	if staff != nil {
		if s := staff.SettingsOf(chat.LobbyKey); s.Achievements != nil {
			for k, v := range s.Achievements {
				e.state.Unlocked[k] = v
			}
		}
	}
	return e
}

// Observe feeds one event through the pure core and, when medals
// unlock, persists and broadcasts. Idempotent end to end: a replayed
// event cannot re-fire (Evaluate's unlocked set is the gate).
// Counters roll forward on EVERY event — 阈值枚（十单/半百/十夜…）靠
// 跨拍累计，无达成的拍丢弃计数会让阈值永远凑不齐（night-10 接线测试
// 抓出的隐患）；落盘仍只在有达成时写（持久化面只存解锁集——口径不变）.
func (e *AchievementEngine) Observe(ev Event, hub interface{ System(string) }) []AchievementDef {
	e.mu.Lock()
	next, fired := Evaluate(ev, e.state, e.table)
	e.state = next
	unlocked := e.state.Unlocked // 锁内快照：解锁后裸读 e.state 会与下一个 Observe 的写竞态
	e.mu.Unlock()
	if len(fired) == 0 {
		return nil
	}
	// 持久化（只存已达成——staffing settings 同族）
	if e.staff != nil {
		e.staff.SetAchievements(chat.LobbyKey, unlocked)
	}
	// 广播（谁在哪枚里：fired 按定义自带 PerMember 语义）
	if e.Broadcast != nil && len(fired) > 0 {
		e.Broadcast(hub, fired, ev.Member)
	}
	return fired
}

// WallSnapshot is the honors wall's data face (t_188): the unlocked
// map (key → unix) plus the definition list rendered for the wire —
// key/name/text/perMember/medal（货架摆色的定档，0金1银2铜）. Name/Text
// ride i18n.S at THIS boundary (定义表保持中文真源，出层翻语言).
func (e *AchievementEngine) WallSnapshot() map[string]any {
	e.mu.Lock()
	snapshot := map[string]int64{}
	for k, v := range e.state.Unlocked {
		snapshot[k] = v
	}
	e.mu.Unlock()
	list := make([]map[string]any, 0, len(e.table))
	for _, def := range e.table {
		list = append(list, map[string]any{
			"key": def.Key, "name": i18n.S(def.Name), "text": i18n.S(def.Text),
			"perMember": def.PerMember, "medal": def.Medal,
		})
	}
	return map[string]any{"unlocked": snapshot, "achievements": list}
}

// NightLights is the night-light counter's read face (the shell's
// night-gate tests assert on it; the state itself stays private).
func (e *AchievementEngine) NightLights() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.NightLights
}

// Unlocked reports whether the named medal is in the unlocked set
// (the tests' read face; the state itself stays private).
func (e *AchievementEngine) Unlocked(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.state.Unlocked[key]
	return ok
}
