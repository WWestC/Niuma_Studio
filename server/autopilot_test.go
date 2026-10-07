package server

// autopilot_test.go — 自动补货/自动推进双开关引擎（v2.7→r_19）的行为面：
//   - 待审提案满宽限 → 以「自动驾驶」名义走同一落库腿接受（任务入库、
//     PlanLanded 唤醒、accepted 广播带宽限说明）；宽限内的提案不动；
//   - 需求池低于蓄水线（r_17）→ 经 Poke 钩子点名编排者补货选题——
//     忙闲皆然、冷却内只一次；水位足则不点；
//   - 闲置＋池有货无人拆（stalledBacklog）→ 拆解注入走 Divert 钩子
//     （r_17 独立路由与冷却，低水位时补货并行——边拆边补）；
//   - 进行中任务停滞超阈（第四闸）→ 经 Resume 钩子点名负责人续做：
//     新鲜任务不点、连拍不重复（每停滞窗一次）、负责人不在册时房间
//     留一行且不刷屏、开关关时不点；
//   - 每日接受上限：超限的提案不再代收，开关熔断自关＋房间播报；
//   - HTTP 面：GET 反射、POST 开启须确认令牌＋在编编排者。
//
// 时钟走 AutoPilotTick 假钟（keeper 契约），done() 是每拍的同步点。

import (
	"encoding/json"
	"fmt"
	"github.com/WWestC/Niuma_Studio/server/autopilot"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// apPoke counts the topic-selection pokes (the engine's fleet hook).
type apPoke struct {
	mu sync.Mutex
	n  int
}

func (p *apPoke) Poke(_ string, _ int) error {
	p.mu.Lock()
	p.n++
	p.mu.Unlock()
	return nil
}

func (p *apPoke) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// apDivert counts the 拆解 diverts and captures the last one's water
// level (the engine's fleet hook; r_17 gave the divert its own route).
type apDivert struct {
	mu    sync.Mutex
	n     int
	openN int
}

func (p *apDivert) Divert(_ string, openN int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	p.openN = openN
	return nil
}

func (p *apDivert) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

func (p *apDivert) lastOpenN() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.openN
}

// apStage wires one ACTIVE project room with the full autopilot stack.
type apStage struct {
	s       *Server
	hub     *chat.Hub // proj-ap's room
	tick    chan time.Time
	plans   *plan.Engine
	eng     *tasks.Engine
	reqs    requirements.Store
	staff   *staffing.Store
	meets   meeting.Store
	proj    *projects.Store
	pokes   *apPoke
	diverts *apDivert
	resumes *apResume
	landed  chan int
	tap     *serverTap
}

// apResume counts the resume gate's wakes and captures the last one
// (the engine's fleet hook); fail flips it into the refused shape (the
// assignee is not a managed member).
type apResume struct {
	mu       sync.Mutex
	n        int
	assignee string
	ids      []string
	fail     bool
}

func (r *apResume) Resume(project, assignee string, stalled []*tasks.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	r.assignee = assignee
	r.ids = nil
	for _, t := range stalled {
		r.ids = append(r.ids, t.ID)
	}
	if r.fail {
		return fmt.Errorf("任务 %s 负责人 %s 不在本房调度成员（房主本人或待招岗位）——停滞任务等人处置，暂无法自动续做",
			strings.Join(r.ids, "/"), assignee)
	}
	return nil
}

func (r *apResume) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

func (r *apResume) last() (string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.assignee, r.ids
}

// serverTap collects one room's frames off an observer seat (the
// broadcast assertions' eyes).
type serverTap struct {
	mu    sync.Mutex
	lines []chat.Message
}

func newServerTap(hub *chat.Hub) *serverTap {
	tap := &serverTap{}
	seat, _ := hub.Join("自动驾驶观察员", "测试", false)
	go func() {
		for msg := range seat.Receive() {
			tap.mu.Lock()
			tap.lines = append(tap.lines, msg)
			tap.mu.Unlock()
		}
	}()
	return tap
}

func (t *serverTap) find(typ, substr string) (chat.Message, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range t.lines {
		if m.Type == typ && strings.Contains(m.Text, substr) {
			return m, true
		}
	}
	return chat.Message{}, false
}

func (t *serverTap) count(typ, substr string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, m := range t.lines {
		if m.Type == typ && strings.Contains(m.Text, substr) {
			n++
		}
	}
	return n
}

// startAutoPilotStage builds the server with the engine on a fake
// clock (no round until a tick is sent).
func startAutoPilotStage(t *testing.T, cfg AutoPilotConfig) *apStage {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "")
	if _, err := projStore.Create(projects.Project{Key: "proj-ap", Name: "自动驾驶项目",
		Workspace: filepath.Join(t.TempDir(), "w")}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-ap"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	plans := plan.OpenMemory()
	eng := tasks.MustOpenMemory("房主")
	eng.SetProjectKeys(func() []string { return []string{"proj-ap", chat.LobbyKey} })
	reqs := requirements.OpenMemory()
	staff, err := staffing.Open("")
	if err != nil {
		t.Fatalf("staffing.Open: %v", err)
	}
	agentStore, err := agents.Open("")
	if err != nil {
		t.Fatalf("agents.Open: %v", err)
	}
	pokes := &apPoke{}
	diverts := &apDivert{}
	resumes := &apResume{}
	landed := make(chan int, 8)
	tick := make(chan time.Time)
	cfg.Poke = pokes.Poke
	cfg.Divert = diverts.Divert
	cfg.Resume = resumes.Resume
	cfg.Tick = tick
	meets := meeting.OpenMemory()
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			PlanStore:    plans,
			Engine:       eng,
			Requirements: reqs,
			StaffStore:   staff,
			AgentStore:   agentStore,
			Meetings:     meets,
		},
		LocalName: "房主",
		Registry:  registry,
		Fleet: FleetFuncs{PlanLandedFn: func(project, planID, planTitle, submitter string, landedTasks []*tasks.Task) error {
			landed <- len(landedTasks)
			return nil
		}},
		AutoPilot: &cfg})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	hub, err := registry.Hub("proj-ap")
	if err != nil {
		t.Fatalf("registry hub: %v", err)
	}
	return &apStage{s: s, hub: hub, tick: tick, plans: plans, eng: eng,
		reqs: reqs, staff: staff, proj: projStore, pokes: pokes, diverts: diverts,
		resumes: resumes, landed: landed, tap: newServerTap(hub), meets: meets}
}

func (st *apStage) fire() bool {
	st.tick <- time.Now()
	return st.s.autopilot.Done()
}

// enableBoth flips the r_19 双开关全开（＝旧全智能的既有口径——存量
// 用例保持原语义）；拆分后的单开边界由新用例各自钉。
func enableBoth(staff *staffing.Store, key string) {
	staff.SetAutoStock(key, true)
	staff.SetAutoAdvance(key, true)
}

// apWaitFor polls a condition for up to 2s — the broadcast tap drains
// on its own goroutine, so an assertion right after done() races the
// seat channel (the dispatch package's waitFor, same shape).
func apWaitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

func (st *apStage) submitAged(t *testing.T, title string, age time.Duration) *plan.Plan {
	t.Helper()
	// v2.9 指派在册：受派人张三先上 proj-ap 编制——隔离后提案只认本项
	// 目在册（房主/在座/编制中）的指派（幂等：同测多份提案只入册一次）。
	if _, ok := st.staff.Get("proj-ap", "张三"); !ok {
		if _, err := st.staff.Join("proj-ap", "张三", "前端", ""); err != nil {
			t.Fatalf("staff.Join 张三: %v", err)
		}
	}
	stored, _ := st.plans.Submit("", "proj-ap", "小牛", plan.Plan{
		Title: title, ProjectKey: "proj-ap",
		Tasks: []plan.PlanTask{{Title: title + "·单子", Assignee: "张三"}},
	}, time.Now().Add(-age).Unix())
	return stored
}

// TestAutoPilotAcceptsAgedPlanAsAutoPilotActor: 满宽限的待审提案被
// 「自动驾驶」名义接受——同一落库腿（任务入库、PlanLanded 唤醒）、
// 广播带宽限说明、历史另有系统留痕行。
func TestAutoPilotAcceptsAgedPlanAsAutoPilotActor(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{AcceptDelay: time.Minute})
	enableBoth(st.staff, "proj-ap")
	st.submitAged(t, "自动拆解", time.Hour)

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", "proj-ap") != nil {
		t.Fatal("待审槽未弹开")
	}
	if got := st.eng.ListFiltered("", "", "proj-ap", ""); len(got) != 1 {
		t.Fatalf("任务应入库 1 条（got %d）", len(got))
	}
	msg, ok := st.tap.find(chat.MsgPlan, "接受了提案")
	if !ok {
		apWaitFor(t, func() bool { _, ok := st.tap.find(chat.MsgPlan, "接受了提案"); return ok },
			"未见 accepted 广播")
		msg, _ = st.tap.find(chat.MsgPlan, "接受了提案")
	}
	if msg.From != autopilot.AutoPilotActor {
		t.Fatalf("落款应为「自动驾驶」（got %q）", msg.From)
	}
	if !strings.Contains(msg.Text, "代房主放行") || !strings.Contains(msg.Text, "宽限") {
		t.Fatalf("广播应带宽度说明：%q", msg.Text)
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "[自动驾驶]") >= 1 },
		"历史应留一行自动驾驶系统留痕")
	select {
	case n := <-st.landed:
		if n != 1 {
			t.Fatalf("PlanLanded 应带 1 条任务（got %d）", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PlanLanded 未被触发")
	}
}

// TestAutoPilotPresenceHoldsAccept（r_19 修订的引擎腿）: 提案待审已
// 过宽限，但房主近窗内在本房有活动——本拍不代收，槽件留给房主；
// 房主安静后同一提案照常代收（代行只服务缺席）。
func TestAutoPilotPresenceHoldsAccept(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{AcceptDelay: time.Minute})
	enableBoth(st.staff, "proj-ap")
	st.submitAged(t, "在场房主的提案", time.Hour)
	st.hub.NoteOwnerActivity()

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", "proj-ap") == nil {
		t.Fatal("房主在场时代收应让位（提案被抢跑）")
	}
	if got := st.eng.ListFiltered("", "", "proj-ap", ""); len(got) != 0 {
		t.Fatalf("在场时不应入库（got %d）", len(got))
	}

	// 房主安静（在场窗过期）后，下一拍照常代收——让位不是停摆。
	quiet := util.Now()
	prev := util.NowFn()
	util.SetNow(func() int64 { return quiet + int64(chat.OwnerPresenceWindow/time.Second) + 1 })
	defer func() { util.SetNow(prev) }()
	if !st.fire() {
		t.Fatal("第二轮 round 未完成")
	}
	if st.plans.Pending("", "proj-ap") != nil {
		t.Fatal("房主安静后应恢复代收")
	}
}

// TestAutoPilotRespectsGraceWindow: 宽限内的提案不动（房主的否决窗），
// 也不触发选题（提案在槌上＝项目不闲置）。
func TestAutoPilotRespectsGraceWindow(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{AcceptDelay: time.Hour})
	enableBoth(st.staff, "proj-ap")
	st.plans.Submit("", "proj-ap", "小牛", plan.Plan{Title: "新鲜提案", ProjectKey: "proj-ap",
		Tasks: []plan.PlanTask{{Title: "单子"}}}, time.Now().Unix())

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", "proj-ap") == nil {
		t.Fatal("宽限内的提案被抢跑了")
	}
	if got := st.eng.ListFiltered("", "", "proj-ap", ""); len(got) != 0 {
		t.Fatalf("宽限内不应入库（got %d）", len(got))
	}
	if st.pokes.count() != 0 {
		t.Fatal("有待审提案时不该点名选题")
	}
}

// TestAutoPilotPokesIdleProjectOncePerCooldown: 闲置空池 → 补货选题
// 一次，连拍不重复（冷却内只一次）。
func TestAutoPilotPokesIdleProjectOncePerCooldown(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{PokeCooldown: time.Hour})
	enableBoth(st.staff, "proj-ap")

	if !st.fire() || st.pokes.count() != 1 {
		t.Fatalf("闲置首轮应点名一次（got %d）", st.pokes.count())
	}
	if !st.fire() || st.pokes.count() != 1 {
		t.Fatalf("冷却内连拍不应重复点名（got %d）", st.pokes.count())
	}
}

// apFillPool tops the requirement pool to the stocking line so the
// replenish gate closes — the fixture for tests isolating a different
// gate (resume / divert) from the restock's beat.
func (st *apStage) apFillPool(t *testing.T) {
	t.Helper()
	for i := st.openN(); i < agents.WaterTarget; i++ {
		if _, err := st.reqs.Create("", "proj-ap", "蓄水用的既有需求", "", "房主"); err != nil {
			t.Fatal(err)
		}
	}
}

func (st *apStage) openN() int {
	// r_31：蓄水闸口径改净 open（parking 不算可拆粮）——本助手随之
	return st.s.autopilot.NetOpenCount("proj-ap")
}

// apDoing files one doing task for assignee through the public verbs
// (self-create → self-start with the project key, the 领活 discipline)
// — the resume gate's fixture. No nomination pending: a task you file
// for yourself starts clean.
func (st *apStage) apDoing(t *testing.T, title, assignee string) string {
	t.Helper()
	oc := st.eng.Create("", assignee, title, "", assignee)
	if oc.Denied {
		t.Fatalf("Create 被拒：%s", oc.Reason)
	}
	id := oc.Task.ID
	oc = st.eng.Update("", assignee, id, tasks.Patch{Project: "proj-ap", Status: tasks.StatusDoing})
	if oc.Denied {
		t.Fatalf("Update doing 被拒：%s", oc.Reason)
	}
	return id
}

// TestAutoPilotResumesStalledDoingTask: 进行中任务停滞超阈 → Resume
// 钩子点名负责人、房间留一行；随即连拍不重复（再点名窗＝停滞窗，以
// stamp 为锚）。阈值压到 400ms、建单后静默 600ms 保证首轮即停滞，
// 连拍发生在窗内。选题不掺和（池已蓄到目标线——忙房低水位补货另测）。
func TestAutoPilotResumesStalledDoingTask(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{StallDelay: 400 * time.Millisecond})
	enableBoth(st.staff, "proj-ap")
	st.apFillPool(t)
	id := st.apDoing(t, "茶水间实现", "张三")
	time.Sleep(600 * time.Millisecond) // 停滞窗走过：首轮即停滞

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.resumes.count() != 1 {
		t.Fatalf("停滞任务应点名一次（got %d）", st.resumes.count())
	}
	who, ids := st.resumes.last()
	if who != "张三" || len(ids) != 1 || ids[0] != id {
		t.Fatalf("点名对象/任务不对（%s %v）", who, ids)
	}
	if st.pokes.count() != 0 {
		t.Fatal("房不闲置，不该掺和选题")
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "点名 张三 续做") == 1 },
		"房间未见续命留痕")

	if !st.fire() || st.resumes.count() != 1 {
		t.Fatalf("停滞窗内连拍不应重复点名（got %d）", st.resumes.count())
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "点名 张三 续做") == 1 },
		"停滞窗内连拍刷屏了")
}

// TestAutoPilotResumeSkipsFreshTask: 停滞阈内的 doing 任务不动——刚动
// 过账的任务不算停滞，长回合的终态 task_update 自然续命。
func TestAutoPilotResumeSkipsFreshTask(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{StallDelay: time.Hour})
	enableBoth(st.staff, "proj-ap")
	st.apDoing(t, "刚动过账的任务", "张三")

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.resumes.count() != 0 {
		t.Fatalf("阈内任务不该点名（got %d）", st.resumes.count())
	}
}

// TestAutoPilotResumeRefusedSurfacesOnce: 负责人不在册（房主本人/待招
// 岗位）→ 房间一行留痕指路，且停滞窗内连拍不刷屏（拒送也 stamp）。
func TestAutoPilotResumeRefusedSurfacesOnce(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{StallDelay: 400 * time.Millisecond})
	st.resumes.fail = true
	enableBoth(st.staff, "proj-ap")
	st.apDoing(t, "等人处置的任务", "房主")
	time.Sleep(600 * time.Millisecond) // 停滞窗走过：首轮即停滞

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.resumes.count() != 1 {
		t.Fatalf("应尝试点名一次（got %d）", st.resumes.count())
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "等人处置") == 1 },
		"房间未见拒送留痕")
	if !st.fire() || st.resumes.count() != 1 {
		t.Fatalf("停滞窗内连拍不应重试（got %d）", st.resumes.count())
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "等人处置") == 1 },
		"拒送留痕刷屏了")
}

// TestAutoPilotResumeOffWithoutSwitch: 开关关（默认）→ 停滞任务无人
// 点名——续命闸与三个关口一样只属于自动驾驶。
func TestAutoPilotResumeOffWithoutSwitch(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{StallDelay: time.Nanosecond})
	st.apDoing(t, "开关关着", "张三")
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.resumes.count() != 0 {
		t.Fatalf("开关关着不该点名（got %d）", st.resumes.count())
	}
}

// TestAutoPilotSkipsOpenRequirement（r_17 口径）: 闲置＋池有货 ＝
// stalledBacklog——拆解注入走 Divert（自己的路由与冷却戳）；水位 1/3
// 低于目标线，补货并行注入（边拆边补）；连拍两路互不刷屏。
func TestAutoPilotSkipsOpenRequirement(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{PokeCooldown: time.Hour})
	enableBoth(st.staff, "proj-ap")
	if _, err := st.reqs.Create("", "proj-ap", "已有需求", "", "房主"); err != nil {
		t.Fatal(err)
	}
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.diverts.count() != 1 {
		t.Fatalf("有开放需求无人拆应发拆解注入（got %d）", st.diverts.count())
	}
	if st.diverts.lastOpenN() != 1 {
		t.Fatalf("拆解注入应带水位（got open %d）", st.diverts.lastOpenN())
	}
	if st.pokes.count() != 1 {
		t.Fatalf("水位 1/3 低于目标线，补货应并行（got %d）", st.pokes.count())
	}
	if st.s.autopilot == nil || !st.s.autopilot.StalledBacklog("proj-ap") {
		t.Fatal("stalledBacklog 应为真（open>0 且无 todo/doing）")
	}
	if !st.fire() || st.diverts.count() != 1 || st.pokes.count() != 1 {
		t.Fatal("冷却内连拍不应重复注入")
	}
}

// TestAutoPilotReplenishesBusyRoomBelowTarget（r_17 蓄水闸与闲置解耦）:
// 忙房（有 doing）水位低于目标线照样补货——补货是库存动作，不等产线
// 停机；补足到线即停；忙房不走拆解注入。
func TestAutoPilotReplenishesBusyRoomBelowTarget(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{PokeCooldown: time.Hour})
	enableBoth(st.staff, "proj-ap")
	st.apDoing(t, "忙活中的任务", "张三")
	if _, err := st.reqs.Create("", "proj-ap", "池中仅有一条", "", "房主"); err != nil {
		t.Fatal(err)
	}

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.pokes.count() != 1 {
		t.Fatalf("忙房低水位应补货（got %d）", st.pokes.count())
	}
	if st.diverts.count() != 0 {
		t.Fatalf("忙房不走拆解注入（got %d）", st.diverts.count())
	}

	// 编排者立了两条、水位到线——连拍不再补
	for _, title := range []string{"补上的甲", "补上的乙"} {
		if _, err := st.reqs.Create("", "proj-ap", title, "", "小牛"); err != nil {
			t.Fatal(err)
		}
	}
	if !st.fire() || st.pokes.count() != 1 {
		t.Fatalf("水位到线后不应再补（got %d）", st.pokes.count())
	}
}

// TestAutoPilotFullPoolIdleDivertsWithoutRestock: 闲置＋水位已到目标线
// ——不补货（蓄到线即停），拆解照发（消费是拆解/会议钟的地盘）。
func TestAutoPilotFullPoolIdleDivertsWithoutRestock(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{PokeCooldown: time.Hour})
	enableBoth(st.staff, "proj-ap")
	st.apFillPool(t)

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.pokes.count() != 0 {
		t.Fatalf("水位足不应补货（got %d）", st.pokes.count())
	}
	if st.diverts.count() != 1 {
		t.Fatalf("闲置＋池有货应发拆解注入（got %d）", st.diverts.count())
	}
}

// TestAutoPilotOffLeavesHostGate: 开关关（默认）→ 到龄提案原样在槽，
// 等房主；不点名。
func TestAutoPilotOffLeavesHostGate(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{AcceptDelay: time.Minute})
	st.submitAged(t, "等人审", time.Hour)
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", "proj-ap") == nil {
		t.Fatal("开关关着，到龄提案不该被代收")
	}
	if st.pokes.count() != 0 {
		t.Fatal("开关关着不该点名")
	}
}

// TestAutoPilotDailyCapTripsOff: 每日上限 1 份——首份代收，第二份不再
// 代收并熔断自关＋房间播报。
func TestAutoPilotDailyCapTripsOff(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{AcceptDelay: time.Minute, MaxPlans: 1})
	staff := st.staff
	enableBoth(staff, "proj-ap")

	st.submitAged(t, "今日首份", time.Hour)
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", "proj-ap") != nil || len(st.eng.ListFiltered("", "", "proj-ap", "")) != 1 {
		t.Fatal("首份应被代收")
	}

	st.submitAged(t, "超限的第二份", time.Hour)
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", "proj-ap") == nil {
		t.Fatal("超限提案不该再被代收")
	}
	if len(st.eng.ListFiltered("", "", "proj-ap", "")) != 1 {
		t.Fatalf("任务数不该增长（got %d）", len(st.eng.ListFiltered("", "", "proj-ap", "")))
	}
	if staff.SettingsOf("proj-ap").AutoAdvance {
		t.Fatal("熔断后自动推进应已自动关闭")
	}
	if !staff.SettingsOf("proj-ap").AutoStock {
		t.Fatal("熔断只关推进——自动补货必须存活（r_19 拆分语义）")
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "上限") >= 1 },
		"房间未见熔断播报")
}

// TestApDayBookPersistsAcrossRestart: 日代收计数落盘——同日重启回种
// （熔断分子不再随重启白送一整页预算），跨日书页自然翻篇不种。
func TestApDayBookPersistsAcrossRestart(t *testing.T) {
	root := t.TempDir()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, root, "", "")
	s, err := Start(chat.NewHub(), Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
		},
		Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)

	now := time.Now()
	e1 := s.startAutoPilot(AutoPilotConfig{Every: -1})
	e1.BumpAccepted("proj-ap", now)
	e1.BumpAccepted("proj-ap", now)

	e2 := s.startAutoPilot(AutoPilotConfig{Every: -1})
	if got := e2.AcceptedToday("proj-ap", now); got != 2 {
		t.Fatalf("同日重启应回种计数（got %d want 2）", got)
	}

	// 跨日书页：昨天日期的书页种不进来
	yesterday := now.AddDate(0, 0, -1)
	book := autopilot.ApDayBook{Day: yesterday.Format("2006-01-02"), Counts: map[string]int{"proj-ap": 5}}
	raw, err := json.Marshal(book)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "autopilot_day.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	e3 := s.startAutoPilot(AutoPilotConfig{Every: -1})
	if got := e3.AcceptedToday("proj-ap", now); got != 0 {
		t.Fatalf("跨日书页应翻篇（got %d want 0）", got)
	}
}

// TestAutoPilotEndpointFace（r_19 双开关版）: GET 反射三字段；POST 开
// 任一开关须确认令牌（缺一 400）；开补货额外须在编编排者、开推进不
// 须（代收/续命无人可送也不哑火）；空载荷 400；关闭免确认；/projects
// 随行 autopilot（＝两者之或）标志。
func TestAutoPilotEndpointFace(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	base := addrHTTP(st.s)

	resp, err := http.Get(base + "/p/proj-ap/autopilot")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Project   string `json:"project"`
		AutoStock bool   `json:"auto_stock"`
		AutoAdv   bool   `json:"auto_advance"`
		AutoPilot bool   `json:"autopilot"`
		MaxPlans  int    `json:"max_plans"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got.Project != "proj-ap" || got.AutoStock || got.AutoAdv || got.AutoPilot || got.MaxPlans != AutoPilotMaxPlans {
		t.Fatalf("GET 反射不对：%+v", got)
	}

	// 缺确认令牌 → 400（开任一开关都要）
	r, _ := http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"stock":true}`))
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺令牌应 400（got %d）", r.StatusCode)
	}
	r.Body.Close()
	r, _ = http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"advance":true}`))
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("开推进缺令牌同样应 400（got %d）", r.StatusCode)
	}
	r.Body.Close()

	// 空载荷（既无开关也无旋钮）→ 400 无事可做
	r, _ = http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"confirm":"AUTOPILOT"}`))
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("空载荷应 400（got %d）", r.StatusCode)
	}
	r.Body.Close()

	// 有令牌但无编排者 → 开补货 400 带指路
	r, _ = http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"stock":true,"confirm":"AUTOPILOT"}`))
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("无编排者开补货应 400（got %d）", r.StatusCode)
	}
	var deny struct {
		Error string `json:"error"`
	}
	json.NewDecoder(r.Body).Decode(&deny)
	r.Body.Close()
	if !strings.Contains(deny.Error, agents.OrchestratorRole) {
		t.Fatalf("报错应指路补编排岗：%q", deny.Error)
	}

	// 无编排者开推进 → 200（代收/续命不需要编排者——拆分后的新边界）
	r, _ = http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"advance":true,"confirm":"AUTOPILOT"}`))
	if r.StatusCode != http.StatusOK {
		t.Fatalf("无编排者开推进应 200（got %d）", r.StatusCode)
	}
	var advOn struct {
		AutoAdvance bool `json:"auto_advance"`
		AutoPilot   bool `json:"autopilot"`
	}
	json.NewDecoder(r.Body).Decode(&advOn)
	r.Body.Close()
	if !advOn.AutoAdvance || !advOn.AutoPilot || !st.staff.SettingsOf("proj-ap").AutoAdvance {
		t.Fatal("单开推进未生效（auto_advance/autopilot 应回真）")
	}
	if st.staff.SettingsOf("proj-ap").AutoStock {
		t.Fatal("单开推进不该波及补货开关")
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "开启了自动推进") == 1 },
		"开推进应留房间系统行")
	// 关推进免确认（回到双关基线，供后续断言）
	r, _ = http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"advance":false}`))
	if r.StatusCode != http.StatusOK {
		t.Fatalf("关推进应免确认（got %d）", r.StatusCode)
	}
	r.Body.Close()

	// 在编编排者到位 → 目标制补货门先拦一道：开补货缺 goal → 400 人话
	if _, err := st.staff.Join("proj-ap", "小牛", agents.OrchestratorRole, ""); err != nil {
		t.Fatal(err)
	}
	r, _ = http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"stock":true,"confirm":"AUTOPILOT"}`))
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("编排者到位但缺目标开补货应 400（got %d）", r.StatusCode)
	}
	json.NewDecoder(r.Body).Decode(&deny)
	r.Body.Close()
	if !strings.Contains(deny.Error, "本次目标") {
		t.Fatalf("报错应指路填目标：%q", deny.Error)
	}

	// 带目标双开成功（旧全智能口径＋目标制）
	r, _ = http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"stock":true,"advance":true,"confirm":"AUTOPILOT","goal":"交付《合鸣》第二卷"}`))
	if r.StatusCode != http.StatusOK {
		t.Fatalf("具备条件应 200（got %d）", r.StatusCode)
	}
	var flipped struct {
		AutoStock   bool   `json:"auto_stock"`
		AutoAdvance bool   `json:"auto_advance"`
		AutoPilot   bool   `json:"autopilot"`
		StockGoal   string `json:"stock_goal"`
	}
	json.NewDecoder(r.Body).Decode(&flipped)
	r.Body.Close()
	if !flipped.AutoStock || !flipped.AutoAdvance || !flipped.AutoPilot {
		t.Fatal("双开回执不对")
	}
	if flipped.StockGoal != "交付《合鸣》第二卷" {
		t.Fatalf("回执应带本次目标（got %q）", flipped.StockGoal)
	}
	if st.staff.SettingsOf("proj-ap").StockGoal != "交付《合鸣》第二卷" {
		t.Fatal("目标未落 staffing")
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "开启了自动补货") == 1 },
		"开补货应留房间系统行")
	if _, ok := st.tap.find(chat.MsgSystem, "本次目标：交付《合鸣》第二卷"); !ok {
		t.Fatal("开补货系统行应带本次目标")
	}
	// GET 面回显目标（设置卡「本次目标」行的真源）
	g, _ := http.Get(base + "/p/proj-ap/autopilot")
	var gg struct {
		StockGoal string `json:"stock_goal"`
	}
	json.NewDecoder(g.Body).Decode(&gg)
	g.Body.Close()
	if gg.StockGoal != "交付《合鸣》第二卷" {
		t.Fatalf("GET 面应回显 stock_goal（got %q）", gg.StockGoal)
	}

	// /projects 随行标志（房列表 ⚡ 小标的数据源，口径＝任一开关开）
	pr, _ := http.Get(base + "/projects")
	var projs []map[string]any
	json.NewDecoder(pr.Body).Decode(&projs)
	pr.Body.Close()
	found := false
	for _, p := range projs {
		if p["key"] == "proj-ap" {
			found = true
			if p["autopilot"] != true {
				t.Fatalf("/projects 应随行 autopilot 标志：%+v", p)
			}
		}
	}
	if !found {
		t.Fatal("/projects 未见 proj-ap")
	}

	// 双关免确认
	r, _ = http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"stock":false,"advance":false}`))
	if r.StatusCode != http.StatusOK {
		t.Fatalf("关闭应免确认（got %d）", r.StatusCode)
	}
	r.Body.Close()
	if st.staff.SettingsOf("proj-ap").AnyAutoOn() {
		t.Fatal("关闭未生效")
	}
}

// TestAutoPilotDoneWrapsUp（目标制补货收摊）: 本房编排者上报目标达成 →
// r_19 双开关全关＋房内一条系统行（带本次目标与一句话结论）；非编排者
// 被拒（房主的手在设置卡）；全关状态幂等收摊不重复播报。会话路由
// （session.go 的 MsgAutopilotDone case）是薄层，这里直测落点本体。
func TestAutoPilotDoneWrapsUp(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	if _, err := st.staff.Join("proj-ap", "小牛", agents.OrchestratorRole, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.staff.Join("proj-ap", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	st.staff.SetAutoStock("proj-ap", true)
	st.staff.SetAutoAdvance("proj-ap", true)
	st.staff.SetStockGoal("proj-ap", "交付《合鸣》第二卷")

	// 非编排者收摊 → denied，开关不动
	msg := st.s.autoPilotDoneAs(st.hub, "小猿", "", "proj-ap")
	if msg.Event != "denied" {
		t.Fatalf("非编排者收摊应 denied（got %s）", msg.Event)
	}
	if !st.staff.SettingsOf("proj-ap").AnyAutoOn() {
		t.Fatal("被拒的收摊不得关开关")
	}
	if st.tap.count(chat.MsgSystem, "判定本次目标已达成") != 0 {
		t.Fatal("被拒的收摊不得播报")
	}

	// 编排者收摊 → completed，双关＋系统行带目标与结论
	msg = st.s.autoPilotDoneAs(st.hub, "小牛", "三章全部过审", "proj-ap")
	if msg.Event != "completed" {
		t.Fatalf("编排者收摊应 completed（got %s）", msg.Event)
	}
	if st.staff.SettingsOf("proj-ap").AnyAutoOn() {
		t.Fatal("收摊应双关")
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "判定本次目标已达成") == 1 },
		"收摊应留房内系统行")
	if _, ok := st.tap.find(chat.MsgSystem, "本次目标：交付《合鸣》第二卷"); !ok {
		t.Fatal("收摊系统行应带本次目标")
	}
	if _, ok := st.tap.find(chat.MsgSystem, "结论：三章全部过审。"); !ok {
		t.Fatal("收摊系统行应带结论")
	}

	// 全关再收摊 → 幂等 completed，不重复播报
	msg = st.s.autoPilotDoneAs(st.hub, "小牛", "", "proj-ap")
	if msg.Event != "completed" {
		t.Fatalf("全关收摊应幂等 completed（got %s）", msg.Event)
	}
	if st.tap.count(chat.MsgSystem, "判定本次目标已达成") != 1 {
		t.Fatal("幂等收摊不得重复播报")
	}
}

// TestAutoPilotEnableGoalResetsPokeClock（目标制补货）: 重开补货带目标
// （或开着补货换目标）＝新会话——上一段对话的冷却锚不得吞掉开启面的
// 即时 Kick。事故形状：重启后首轮点名落 30min 锚，房主随即带目标重开，
// Kick 落在锚窗内静默空转，半小时无人理房主（2026-10-03 book 房实录）。
func TestAutoPilotEnableGoalResetsPokeClock(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	base := addrHTTP(st.s)
	if _, err := st.staff.Join("proj-ap", "小牛", agents.OrchestratorRole, ""); err != nil {
		t.Fatal(err)
	}
	enableBoth(st.staff, "proj-ap")

	// 首轮点名：落 30min 冷却锚（默认 PokeEvery）
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.pokes.count() != 1 {
		t.Fatalf("首轮应点名一次（got %d）", st.pokes.count())
	}
	// 冷却窗内连拍不再点名（no-nag 基线）
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.pokes.count() != 1 {
		t.Fatalf("冷却窗内不应重复点名（got %d）", st.pokes.count())
	}

	// 带目标重开（off→on）：新会话清锚，Kick 立刻点名
	st.staff.SetAutoStock("proj-ap", false)
	r, _ := http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"stock":true,"advance":true,"confirm":"AUTOPILOT","goal":"交付第二卷"}`))
	if r.StatusCode != http.StatusOK {
		t.Fatalf("带目标重开应 200（got %d）", r.StatusCode)
	}
	r.Body.Close()
	apWaitFor(t, func() bool { return st.pokes.count() == 2 },
		"带目标重开应立刻点名（新会话清锚）")

	// 开着补货热更换目标：同样是新会话，立刻点名
	r, _ = http.Post(base+"/p/proj-ap/autopilot", "application/json",
		strings.NewReader(`{"goal":"交付第三卷"}`))
	if r.StatusCode != http.StatusOK {
		t.Fatalf("热更换目标应 200（got %d）", r.StatusCode)
	}
	r.Body.Close()
	apWaitFor(t, func() bool { return st.pokes.count() == 3 },
		"热更换目标应立刻点名（新会话清锚）")
}

// TestAutoPilotRunsInTheLobby: 大厅是一等作用域——旗舰编排者驻大厅，
// 开关开在大厅（default）时到龄提案照被代收：任务挂 default、广播落
// 在服务端自己的 hub（keeper 同款——Registry.Rooms() 不保证有大厅位）。
func TestAutoPilotRunsInTheLobby(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{AcceptDelay: time.Minute})
	lobbyTap := newServerTap(st.s.hub)
	if _, err := st.staff.Join(chat.LobbyKey, "小牛", agents.OrchestratorRole, ""); err != nil {
		t.Fatal(err)
	}
	// v2.9 指派在册：大厅提案的受派人小马先上大厅编制（同 proj-ap 侧）
	if _, err := st.staff.Join(chat.LobbyKey, "小马", "人事", ""); err != nil {
		t.Fatal(err)
	}
	enableBoth(st.staff, chat.LobbyKey)
	st.plans.Submit("", chat.LobbyKey, "小牛", plan.Plan{
		Title: "Niuma_Studio 自治", ProjectKey: chat.LobbyKey,
		Tasks: []plan.PlanTask{{Title: "工作室级单子", Assignee: "小马"}},
	}, time.Now().Add(-time.Hour).Unix())

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", chat.LobbyKey) != nil {
		t.Fatal("Niuma_Studio 待审槽未弹开")
	}
	if got := st.eng.ListFiltered("", "", chat.LobbyKey, ""); len(got) != 1 {
		t.Fatalf("Niuma_Studio 任务应入库 1 条（got %d）", len(got))
	}
	apWaitFor(t, func() bool {
		msg, ok := lobbyTap.find(chat.MsgPlan, "接受了提案")
		return ok && msg.From == autopilot.AutoPilotActor
	}, "Niuma_Studio 房未见自动驾驶的 accepted 广播")
}

// ── r_13 t_175：巡报钟与水位线 ────────────────────────────────────

type apPatrol struct {
	mu    sync.Mutex
	n     int
	calls []string // 每次注入的 context 快照
}

func (p *apPatrol) Patrol(_ string, ctx string) error {
	p.mu.Lock()
	p.n++
	p.calls = append(p.calls, ctx)
	p.mu.Unlock()
	return nil
}

func (p *apPatrol) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// TestAutoPilotPatrolClock：巡报按间隔注入——首轮即到、窗内不重复、
// 窗后再到；autopilot 关闭零注入。
func TestAutoPilotPatrolClock(t *testing.T) {
	patrol := &apPatrol{}
	st := startAutoPilotStage(t, AutoPilotConfig{
		Patrol:      patrol.Patrol,
		PatrolEvery: 30 * time.Minute,
	})
	enableBoth(st.staff, "proj-ap")
	if !st.fire() {
		t.Fatal("round 1 未完成")
	}
	if patrol.count() != 1 {
		t.Fatalf("首轮应巡报 1 次（got %d）", patrol.count())
	}
	if !st.fire() || !st.fire() {
		t.Fatal("round 2/3 未完成")
	}
	if patrol.count() != 1 {
		t.Fatalf("窗内不应重复巡报（got %d）", patrol.count())
	}
	// 窗后（假钟推进由 fire 管理——这里直接用引擎内部推进模拟到时）
	if st.s.autopilot == nil {
		t.Fatal("引擎未挂载")
	}
	// 手动把下次巡报时刻拨到过去（模拟 30min 窗过）
	st.s.autopilot.PatrolDueNowForTest("proj-ap")
	if !st.fire() {
		t.Fatal("round 4 未完成")
	}
	if patrol.count() != 2 {
		t.Fatalf("窗后应再巡（got %d）", patrol.count())
	}
}

// TestAutoPilotPatrolOffWhenClosed：autopilot 关闭的项目零巡报。
func TestAutoPilotPatrolOffWhenClosed(t *testing.T) {
	patrol := &apPatrol{}
	st := startAutoPilotStage(t, AutoPilotConfig{Patrol: patrol.Patrol})
	// 不开 SetAutoPilot——默认关
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if patrol.count() != 0 {
		t.Fatalf("开关关着不该巡报（got %d）", patrol.count())
	}
}

// TestAutoPilotPatrolContext：巡报上下文带饱和度与水位——open=2 时含
// 「水位偏低」标记、饱和度口径（在办/在座）在文案里。
func TestAutoPilotPatrolContext(t *testing.T) {
	patrol := &apPatrol{}
	st := startAutoPilotStage(t, AutoPilotConfig{Patrol: patrol.Patrol})
	enableBoth(st.staff, "proj-ap")
	// 两条 open（水位偏低档 1–2）
	if _, err := st.reqs.Create("", "proj-ap", "需求甲", "", "房主"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.reqs.Create("", "proj-ap", "需求乙", "", "房主"); err != nil {
		t.Fatal(err)
	}
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if patrol.count() != 1 {
		t.Fatalf("应巡报 1 次（got %d）", patrol.count())
	}
	ctx := patrol.calls[0]
	if !strings.Contains(ctx, "open 2 条") {
		t.Fatalf("上下文应含水位计数: %s", ctx)
	}
	if !strings.Contains(ctx, "水位偏低") {
		t.Fatalf("open=2 应带水位偏低标记: %s", ctx)
	}
	if !strings.Contains(ctx, "饱和度") {
		t.Fatalf("上下文应含饱和度: %s", ctx)
	}
}

// TestAutoPilotOpenCount：水位计数口径（open 行数）。
func TestAutoPilotOpenCount(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	if _, err := st.reqs.Create("", "proj-ap", "需求甲", "", "房主"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.reqs.Create("", "proj-ap", "需求乙", "", "房主"); err != nil {
		t.Fatal(err)
	}
	if got := st.s.autopilot.StockCount("proj-ap"); got != 2 {
		t.Fatalf("stockCount 应 2（got %d）", got)
	}
	// split 一条后仍剩 1
	if _, err := st.reqs.MarkSplit("", "proj-ap", "需求甲"); err == nil {
		if got := st.s.autopilot.StockCount("proj-ap"); got != 1 {
			t.Fatalf("split 后 openCount 应 1（got %d）", got)
		}
	}
}

// TestAutoPilotIdleNewRule：闲置新口径——open 不再挡 idle（有 open 且
// 无 todo/doing 时 idle 为真），stalledBacklog 同场景为真。
func TestAutoPilotIdleNewRule(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	enableBoth(st.staff, "proj-ap")
	if _, err := st.reqs.Create("", "proj-ap", "挂着没人拆", "", "房主"); err != nil {
		t.Fatal(err)
	}
	if !st.s.autopilot.Idle("proj-ap") {
		t.Fatal("r_13 新口径：open 不再挡 idle（无 todo/doing 即闲置）")
	}
	if !st.s.autopilot.StalledBacklog("proj-ap") {
		t.Fatal("open>0 且 idle → stalledBacklog 应为真")
	}
	// 加一条 todo（挂 proj-ap）→ 不再 idle
	oc := st.eng.Create("", "小猿", "在途单", "", "小猿")
	if oc.Denied {
		t.Fatalf("Create 被拒：%s", oc.Reason)
	}
	up := st.eng.Update("", "小猿", oc.Task.ID, tasks.Patch{Project: "proj-ap", Status: tasks.StatusDoing})
	if up.Denied {
		t.Fatalf("Update 被拒：%s", up.Reason)
	}
	if st.s.autopilot.Idle("proj-ap") {
		t.Fatal("有 doing 不该 idle")
	}
	if st.s.autopilot.StalledBacklog("proj-ap") {
		t.Fatal("有 doing 不该 stalledBacklog")
	}
}

// ── r_14 t_171：旋钮四级链与热更 ────────────────────────────────────

// TestAutoPilotKnobsChain：优先级链——设置文件 > cfg(env) > 内置默认。
func TestAutoPilotKnobsChain(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{AcceptDelay: 99 * time.Second})
	ap := st.s.autopilot
	// 无设置时走 cfg（env 档 99s）——链：设置 > cfg > 内置
	if got := ap.AcceptDelay("proj-ap"); got != 99*time.Second {
		t.Fatalf("无设置应走 cfg 99s（got %v）", got)
	}
	// 设置覆盖（staffing 优先于 cfg 的 env 值）
	st.staff.SetAutoPilotKnobs("proj-ap", staffing.AutoPilotKnobs{AcceptDelayS: 60})
	if got := ap.AcceptDelay("proj-ap"); got != 60*time.Second {
		t.Fatalf("设置档应 60s（got %v）", got)
	}
	// 无设置的另一项目走 cfg
	if got := ap.AcceptDelay("proj-other"); got != 99*time.Second {
		t.Fatalf("其他项目应走 env 档 99s（got %v）", got)
	}
}

// TestAutoPilotKnobsClamp：越界夹紧（写入面）。
func TestAutoPilotKnobsClamp(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	set := st.staff.SetAutoPilotKnobs("proj-ap", staffing.AutoPilotKnobs{
		PokeEveryMin: 1, AcceptDelayS: 99999, StallDelayMin: 0, MaxPlans: 500,
	})
	k := set.AutoPilotKnobs
	if k.PokeEveryMin != 5 {
		t.Fatalf("poke 下界夹到 5（got %d）", k.PokeEveryMin)
	}
	if k.AcceptDelayS != 300 {
		t.Fatalf("accept 上界夹到 300（got %d）", k.AcceptDelayS)
	}
	if k.MaxPlans != 99 {
		t.Fatalf("max_plans 上界夹到 99（got %d）", k.MaxPlans)
	}
}

// TestAutoPilotKnobsHotUpdate：旋钮热更——写入后引擎下一拍即用新值
// （无需重启；布尔开关兼容不受影响）。
func TestAutoPilotKnobsHotUpdate(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	ap := st.s.autopilot
	if got := ap.PokeCooldown("proj-ap"); got != AutoPilotPokeEvery {
		t.Fatalf("缺省 poke（got %v）", got)
	}
	st.staff.SetAutoPilotKnobs("proj-ap", staffing.AutoPilotKnobs{PokeEveryMin: 90})
	if got := ap.PokeCooldown("proj-ap"); got != 90*time.Minute {
		t.Fatalf("热更后应 90min（got %v）", got)
	}
	// 旋钮写不动开关（r_19 后开关是双位——两者都不受旋钮写入影响）
	enableBoth(st.staff, "proj-ap")
	if set := st.staff.SettingsOf("proj-ap"); !set.AutoStock || !set.AutoAdvance {
		t.Fatal("双开关不受旋钮写入影响")
	}
}

// TestAutoPilotKnobsZeroMeansUnset：0＝未设置（走链），不是零值生效。
func TestAutoPilotKnobsZeroMeansUnset(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	ap := st.s.autopilot
	st.staff.SetAutoPilotKnobs("proj-ap", staffing.AutoPilotKnobs{PokeEveryMin: 90})
	// 0＝保留已存值（单旋钮写不重置其他）——accept 写入不动 poke 的 90
	st.staff.SetAutoPilotKnobs("proj-ap", staffing.AutoPilotKnobs{AcceptDelayS: 60})
	if got := ap.PokeCooldown("proj-ap"); got != 90*time.Minute {
		t.Fatalf("0 应保留已存 90min（got %v）", got)
	}
	// 正交：无 cfg 无设置的项目走内置默认
	st2 := startAutoPilotStage(t, AutoPilotConfig{})
	if got := st2.s.autopilot.AcceptDelay("proj-2"); got != AutoPilotAcceptDelay {
		t.Fatalf("无 cfg 无设置应内置 %v（got %v）", AutoPilotAcceptDelay, got)
	}
}

// ── r_17 复查修复的钉行为 ──────────────────────────────────────────

// TestAutoPilotDivertYieldsToLiveMeeting（复查#1 引擎半边）：会议室正开
// 着会＝消费在飞——闲置＋池有货也不发拆解注入（plan 单槽，别给主持塞
// 相互冲突的提交指令）；散会后的下一拍照常发。
func TestAutoPilotDivertYieldsToLiveMeeting(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{PokeCooldown: time.Hour})
	enableBoth(st.staff, "proj-ap")
	st.apFillPool(t)
	if _, err := st.meets.Begin("proj-ap", "r_01", "在评审的需求", "小牛", nil); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.diverts.count() != 0 {
		t.Fatalf("会议进行中不该发拆解注入（got %d）", st.diverts.count())
	}
	if _, ok := st.meets.End("proj-ap"); !ok {
		t.Fatal("散会失败")
	}
	if !st.fire() || st.diverts.count() != 1 {
		t.Fatalf("散会后应照常拆解（got %d）", st.diverts.count())
	}
}

// TestAutoPilotClocksRunDuringProposalGrace（复查#10）：提案在槽（宽限
// 窗内）不再饿死巡报与续命——独立闸照跑；蓄水仍留在槽后（消费在飞，
// 不添新货）。
func TestAutoPilotClocksRunDuringProposalGrace(t *testing.T) {
	patrol := &apPatrol{}
	st := startAutoPilotStage(t, AutoPilotConfig{
		AcceptDelay: time.Hour, StallDelay: 400 * time.Millisecond,
		Patrol: patrol.Patrol,
	})
	enableBoth(st.staff, "proj-ap")
	st.apFillPool(t)
	st.apDoing(t, "停滞中的活", "张三")
	time.Sleep(600 * time.Millisecond) // 停滞窗走过：首轮即停滞
	st.plans.Submit("", "proj-ap", "小牛", plan.Plan{Title: "宽限中的提案", ProjectKey: "proj-ap",
		Tasks: []plan.PlanTask{{Title: "单子"}}}, time.Now().Unix())

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", "proj-ap") == nil {
		t.Fatal("宽限内提案不该被抢收")
	}
	if st.resumes.count() != 1 {
		t.Fatalf("提案在槽也该点名停滞任务（got %d）", st.resumes.count())
	}
	if patrol.count() != 1 {
		t.Fatalf("提案在槽也该巡报（got %d）", patrol.count())
	}
	if st.pokes.count() != 0 {
		t.Fatal("提案在槽不补货（消费在飞，不添新货）")
	}
}

// TestAutoPilotDryPokeBackoff（复查#11）：选题注入后水位纹丝不动＝编排
// 者已答「本期无新需求」（引擎听不见 say，只看得见水位）——冷却按 2
// 的幂退避；补到线即清连击。
func TestAutoPilotDryPokeBackoff(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{PokeCooldown: 300 * time.Millisecond})
	enableBoth(st.staff, "proj-ap")

	if !st.fire() || st.pokes.count() != 1 {
		t.Fatalf("首轮应点名一次（got %d）", st.pokes.count())
	}
	time.Sleep(450 * time.Millisecond) // 基础冷却过、水位未动
	if !st.fire() || st.pokes.count() != 2 {
		t.Fatalf("水位未动也应在基础冷却后再点一次（got %d）", st.pokes.count())
	}
	dry := func() int { return st.s.autopilot.PokeDryStreak("proj-ap") }
	if got := dry(); got != 1 {
		t.Fatalf("第二次落在同一水位应记干涸连击 1（got %d）", got)
	}
	time.Sleep(450 * time.Millisecond) // 基础冷却过、但加倍窗 600ms 未过
	if !st.fire() || st.pokes.count() != 2 {
		t.Fatalf("干涸退避窗内不该重复点名（got %d）", st.pokes.count())
	}
	st.apFillPool(t) // 编排者补货到线——连击清零
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if got := dry(); got != 0 {
		t.Fatalf("水位到线应清干涸连击（got %d）", got)
	}
}

// ── r_19：双开关正交边界 ────────────────────────────────────────────

// TestAutoStockOnlyReplenishesWithoutAdvancing（r_19）: 只开补货——
// 蓄水闸照常（低水位 Poke、到线即停），推进四闸全哑：到龄提案不代收
// （回房主关口）、闲置+池有货不拆解、停滞任务不续命。
func TestAutoStockOnlyReplenishesWithoutAdvancing(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{AcceptDelay: time.Minute})
	st.staff.SetAutoStock("proj-ap", true) // 只补货，推进关——不要 enableBoth

	// 到龄提案在槽：推进关 → 不代收，原样留给房主
	pending := st.submitAged(t, "等人审的提案", time.Hour)
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", "proj-ap") == nil {
		t.Fatal("只开补货时到龄提案不该被代收——房主手动审是拆分语义")
	}
	if got := len(st.eng.ListFiltered("", "", "proj-ap", "")); got != 0 {
		t.Fatalf("不该有任何任务入库（got %d）", got)
	}

	// 提案仍在槽（房主没动）——蓄水照旧让位（消费在飞不添新货）
	if st.pokes.count() != 0 {
		t.Fatalf("提案在槽期间蓄水应让位（pokes=%d）", st.pokes.count())
	}

	// 撤掉提案，放空池：补货闸照常开火
	if _, err := st.plans.Take("", "proj-ap", pending.ID); err != nil { // 房主手动收走
		t.Fatalf("Take: %v", err)
	}
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.pokes.count() != 1 {
		t.Fatalf("空池低水位应 Poke 一次（pokes=%d）", st.pokes.count())
	}

	// 闲置+池里有货：拆解是推进侧的——不发
	st.apFillPool(t)
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.diverts.count() != 0 {
		t.Fatalf("只开补货不该拆解（diverts=%d）", st.diverts.count())
	}
}

// TestAutoAdvanceOnlyDrainsWithoutRestocking（r_19）: 只开推进——代收
// 照常（到龄提案落地），蓄水闸全哑（空池零 Poke——不进新货）。
func TestAutoAdvanceOnlyDrainsWithoutRestocking(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{AcceptDelay: time.Minute})
	st.staff.SetAutoAdvance("proj-ap", true) // 只推进，补货关

	// 空池闲置：零补货
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.pokes.count() != 0 {
		t.Fatalf("只开推进不该补货（pokes=%d）", st.pokes.count())
	}

	// 到龄提案：代收照常（推进侧核心职权）
	st.submitAged(t, "推进侧的提案", time.Hour)
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.plans.Pending("", "proj-ap") != nil || len(st.eng.ListFiltered("", "", "proj-ap", "")) != 1 {
		t.Fatal("只开推进时到龄提案应照常代收")
	}
}

// TestAutoPilotWaterTargetHotUpdate（r_19）: 蓄水目标线旋钮——调到 5
// 后，池里 3 条（旧默认线）重新算低水位触发补货；蓄到 5 即停。写即
// 热更（引擎每拍直读 staffing）。
func TestAutoPilotWaterTargetHotUpdate(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	enableBoth(st.staff, "proj-ap")
	st.staff.SetAutoPilotKnobs("proj-ap", staffing.AutoPilotKnobs{WaterTarget: 5})
	st.apFillPool(t) // 蓄到内置线 3——在新目标线 5 之下

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.pokes.count() != 1 {
		t.Fatalf("目标线调 5 后 3 条应算低水位（pokes=%d）", st.pokes.count())
	}

	// 蓄到新线 5：闸闭合，连拍不重复
	for i := st.openN(); i < 5; i++ {
		if _, err := st.reqs.Create("", "proj-ap", "新线蓄水", "", "房主"); err != nil {
			t.Fatal(err)
		}
	}
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.pokes.count() != 1 {
		t.Fatalf("到新目标线应停（pokes=%d）", st.pokes.count())
	}
}

// TestKBCapacity（r_24 t_205）：聚合只读面——饱和度口径与巡报同一份
// （doing/seated/sat）、水位目标线随行、纯读不敲巡报钟（GET 后巡报
// 计数不涨）。
func TestKBCapacity(t *testing.T) {
	patrol := &apPatrol{}
	st := startAutoPilotStage(t, AutoPilotConfig{Patrol: patrol.Patrol})
	enableBoth(st.staff, "proj-ap")
	// 一座一在办：饱和度 100%；一条 open 需求
	if _, err := st.staff.Join("proj-ap", "小牛", "编排者", ""); err != nil {
		t.Fatal(err)
	}
	st.apDoing(t, "驾驶舱数据源", "小牛")
	if _, err := st.reqs.Create("", "proj-ap", "需求甲", "", "房主"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(addrHTTP(st.s) + "/kb/capacity?all=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Rooms []struct {
			Room   string `json:"room"`
			Doing  int    `json:"doing"`
			Seated int    `json:"seated"`
			Sat    int    `json:"sat"`
			Open   int    `json:"open"`
			Water  int    `json:"water"`
		} `json:"rooms"`
		Now int64 `json:"now"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var proj struct{ doing, seated, sat, open, water int }
	for _, r := range body.Rooms {
		switch r.Room {
		case "proj-ap":
			proj = struct{ doing, seated, sat, open, water int }{r.Doing, r.Seated, r.Sat, r.Open, r.Water}
		case "default":
			if r.Doing != 0 || r.Seated != 0 {
				t.Fatalf("Niuma_Studio 应零活零座（got %d/%d）", r.Doing, r.Seated)
			}
		}
	}
	if proj.doing != 1 || proj.seated != 1 || proj.sat != 100 {
		t.Fatalf("proj-ap 饱和度应 1/1=100%%（got doing=%d seated=%d sat=%d）", proj.doing, proj.seated, proj.sat)
	}
	if proj.open != 1 {
		t.Fatalf("open 应 1（got %d）", proj.open)
	}
	if proj.water != agents.WaterTarget {
		t.Fatalf("水位目标线应 %d（got %d）", agents.WaterTarget, proj.water)
	}
	if body.Now == 0 {
		t.Fatal("now 时间戳应非零")
	}
	// 纯读纪律：GET 不敲巡报钟（边界条款 §四——读接口不触发 HR 巡报）
	if patrol.count() != 0 {
		t.Fatalf("GET 不该触发巡报（got %d 次）", patrol.count())
	}
}

// TestKBCapacityLobbyOnce：大厅是 store 里的常驻 active 项目
// （EnsureLobby 首启即播种并强制保活），all=1 的房间清单若不跳过
// LobbyKey 就会把首行手动放入的大厅再追加一遍——驾驶舱「池有多深」
// 渲染成两行一模一样的大厅（生产首启必现，夹具此前不含大厅故漏网）。
func TestKBCapacityLobbyOnce(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})
	if _, err := st.proj.EnsureLobby(filepath.Join(t.TempDir(), "w")); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(addrHTTP(st.s) + "/kb/capacity?all=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Rooms []struct {
			Room string `json:"room"`
		} `json:"rooms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range body.Rooms {
		if r.Room == chat.LobbyKey {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("Niuma_Studio 应恰出现一次（got %d 次）", n)
	}
}

// TestParkingSkipsDivertAndMeetsBothWaterLines（r_31 t_136）：parking
// 双口径——净 open 给蓄水闸/拆解注入（parking 不被捞）、合计给巡报（构成
// 注）。构造 1 open＋2 parking，验证三面。
func TestParkingSkipsDivertAndMeetsBothWaterLines(t *testing.T) {
	patrol := &apPatrol{}
	st := startAutoPilotStage(t, AutoPilotConfig{Patrol: patrol.Patrol})
	enableBoth(st.staff, "proj-ap")
	// 1 open＋2 parking：净 open=1（低于目标线 3——蓄水闸该补货）、合计=3（到线）
	if _, err := st.reqs.Create("", "proj-ap", "活粮", "", "房主"); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"冻鱼甲", "冻鱼乙"} {
		id, err := st.reqs.Create("", "proj-ap", title, "", "房主")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.reqs.Park("", "proj-ap", id.ID, "等房主建第二项目", 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := st.s.autopilot.NetOpenCount("proj-ap"); got != 1 {
		t.Fatalf("净 open 应 1（parking 不算活粮，got %d）", got)
	}
	if got := st.s.autopilot.StockCount("proj-ap"); got != 3 {
		t.Fatalf("合计应 3（蓄着也算库存，got %d）", got)
	}
	// 蓄水闸用净 open：1<3 仍触发补货（冻结的库存不是可拆粮）——
	// replenishIfDue 无返回值，以 poke 计数为证
	before := st.pokes.count()
	st.s.autopilot.ReplenishIfDue("proj-ap", st.hub, time.Now())
	if st.pokes.count() <= before {
		t.Fatal("净 open 1 低于目标线 3，蓄水闸应触发补货（parking 不算可拆粮）")
	}
	// 巡报构成注：合计 3 且 parked=2（文案「open 1 + parking 2」）
	st.s.autopilot.PatrolDueNowForTest("proj-ap")
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	ctx := patrol.calls[len(patrol.calls)-1]
	if !strings.Contains(ctx, "需求池 open 3 条（open 1 + parking 2）") {
		t.Fatalf("巡报应合计＋构成注（got %s）", ctx)
	}
}

// TestPendingPlanRemindsHostOnce（挂审提醒）：待审提案满 30 分钟 → 房间
// 一行落史的 @房主 定向提醒，每份提案只提醒一次（连拍不刷屏）；未满龄
// 不动；同需求修订重提换新 ID 重新武装；双开关全关也照常提醒（卫生面，
// sweepProposals 同款——手动模式下全房对这个决定让位，静默需要一张脸）。
func TestPendingPlanRemindsHostOnce(t *testing.T) {
	st := startAutoPilotStage(t, AutoPilotConfig{})

	// 未满龄（5 分钟）：零提醒
	fresh := st.submitAged(t, "刚提的方案", 5*time.Minute)
	if !st.fire() {
		t.Fatal("round did not finish")
	}
	if n := st.tap.count(chat.MsgSystem, "【挂审提醒】"); n != 0 {
		t.Fatalf("未满 30 分钟的待审提案不应提醒，得到 %d 条", n)
	}

	// 同需求修订、满龄（31 分钟，新 ID 顶掉旧件）：一条提醒点名新 ID
	aged := st.submitAged(t, "等房主的方案", 31*time.Minute)
	if aged.ID == fresh.ID {
		t.Fatal("前置失效：修订重提要换新 ID")
	}
	if !st.fire() {
		t.Fatal("round did not finish")
	}
	var line chat.Message
	apWaitFor(t, func() bool {
		l, ok := st.tap.find(chat.MsgSystem, "【挂审提醒】")
		if ok {
			line = l
		}
		return ok
	}, "满龄待审提案应有一条 @房主 挂审提醒")
	for _, want := range []string{"@房主", aged.ID, "plan accept", "plan reject"} {
		if !strings.Contains(line.Text, want) {
			t.Fatalf("提醒行应含 %q：%s", want, line.Text)
		}
	}

	// 连拍：同一份提案不再重复提醒（等排水稳定后计数）
	if !st.fire() {
		t.Fatal("round did not finish")
	}
	time.Sleep(50 * time.Millisecond)
	if n := st.tap.count(chat.MsgSystem, "【挂审提醒】"); n != 1 {
		t.Fatalf("每份提案只应提醒一次，得到 %d 条", n)
	}
}
