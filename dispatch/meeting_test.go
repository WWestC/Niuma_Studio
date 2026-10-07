package dispatch

// meeting_test.go — the v2.4 convene-order pins: the ORCHESTRATOR
// convenes and chairs the requirement review (编排者拉上相关成员开
// 会); the product manager is the first invitee — the requirement's
// voice at the table, never the chair — busy colleagues are not
// pulled, and no (free) orchestrator means no meeting at all. The
// chair's own turns land as the record's Opening/Conclusion. The
// clock is parked (MeetingEvery: -1); tests drive tryConvene directly
// — deliver is synchronous against the recording bridge, so the
// assertions need no waiting.

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// recBridge records every injection per session — the acks_test stub
// shape plus the by-seat memory these pins need.
type recBridge struct {
	baseBridge
	mu  sync.Mutex
	got map[string][]string
}

func (f *recBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	f.got[sessionID] = append(f.got[sessionID], content)
	f.mu.Unlock()
	return nil, nil
}

// all joins one session's recorded injections ("" = none).
func (f *recBridge) all(sessionID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.got[sessionID], "\n")
}

// meetStage is one dispatcher over fresh stores: the project key is
// "demo", every clock parked, the bridge recording.
type meetStage struct {
	d     *Dispatcher
	rb    *recBridge
	meets meeting.Store
	reqs  requirements.Store
	eng   *tasks.Engine
	plans *plan.Engine
}

func newMeetStage(t *testing.T) *meetStage {
	t.Helper()
	dir := t.TempDir()
	agentStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	meets := meeting.OpenMemory()
	reqs := requirements.OpenMemory()
	eng := tasks.MustOpenMemory("房主")
	plans := plan.OpenMemory()
	rb := &recBridge{got: map[string][]string{}}
	d := Start(chat.NewHub(), agentStore, rb, Config{
		Workspace:    dir,
		InboxDir:     t.TempDir(),
		PatrolEvery:  -1,
		DailyAt:      "off",
		Meetings:     meets,
		Reqs:         reqs,
		Tasks:        eng,
		Plans:        plans,
		MeetingEvery: -1, // tests drive tryConvene directly
		ProjectKey:   "demo",
	})
	t.Cleanup(d.Stop)
	return &meetStage{d: d, rb: rb, meets: meets, reqs: reqs, eng: eng, plans: plans}
}

// seat attaches one member with a deterministic session id.
func (st *meetStage) seat(t *testing.T, name, role, sid string) {
	t.Helper()
	if err := st.d.attach(name, role, sid); err != nil {
		t.Fatal(err)
	}
}

// busy gives name one doing task — hands full, no seat at the table.
func (st *meetStage) busy(t *testing.T, name string) {
	t.Helper()
	out := st.eng.Create("", "房主", "在办的活", "占用双手", name)
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if up := st.eng.Update("", "房主", out.Task.ID, tasks.Patch{Status: tasks.StatusDoing}); up.Denied {
		t.Fatal(up.Reason)
	}
}

func TestOrchestratorConvenesReviewPMAtTheTable(t *testing.T) {
	st := newMeetStage(t)
	st.seat(t, "小牛", agents.OrchestratorRole, "s-orc")
	st.seat(t, "老品", "产品经理", "s-pm")
	st.seat(t, "阿工", "工程师", "s-eng")
	st.seat(t, "阿测", "测试", "s-qa")
	st.seat(t, "阿美", "设计师", "s-des")  // idle but 岗位不合 → 让位
	st.seat(t, "阿忙", "工程师", "s-busy") // 岗位合但忙 → 不拉
	st.busy(t, "阿忙")
	if _, err := st.reqs.Create("", "demo", "新功能：工程师与测试联合攻关", "需要工程师与测试投入；设计师本次不参与", "房主"); err != nil {
		t.Fatal(err)
	}

	st.d.tryConvene()

	m, live := st.meets.Active("demo")
	if !live {
		t.Fatal("会议室应被占用（编排者空闲＋需求在库＋有空闲相关成员）")
	}
	if m.Chair != "小牛" {
		t.Fatalf("主持应是编排者小牛（v2.4：会议归编排者），得到 %q", m.Chair)
	}
	want := map[string]bool{"老品": true, "阿工": true, "阿测": true}
	got := map[string]bool{}
	for _, p := range m.Participants {
		got[p] = true
	}
	if len(got) != len(want) {
		t.Fatalf("参会应恰为 %v（PM 优先＋岗位匹配，忙者不拉、超员裁剪），得到 %v", want, m.Participants)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("参会名单缺 %q：%v", k, m.Participants)
		}
	}
	for _, p := range m.Participants {
		if p == m.Chair {
			t.Fatal("编排者主持，不应同时出现在参会名单里")
		}
	}

	if orc := st.rb.all("s-orc"); !strings.Contains(orc, "【需求评审会·主持】") || !strings.Contains(orc, "你是本会主持（编排者）") {
		t.Fatalf("编排者会话应收到主持简报，得到：%s", orc)
	}
	if pm := st.rb.all("s-pm"); !strings.Contains(pm, "【需求评审会·参会】") || !strings.Contains(pm, "你是需求方（产品）") {
		t.Fatalf("产品经理应以需求方身份收到参会邀约（不是主持），得到：%s", pm)
	}
	if eng := st.rb.all("s-eng"); !strings.Contains(eng, "【需求评审会·参会】") {
		t.Fatalf("岗位匹配的空闲牛马应收到参会邀约，得到：%s", eng)
	}
	if busy := st.rb.all("s-busy"); busy != "" {
		t.Fatalf("手头有活的成员不应被拉会，得到：%s", busy)
	}
}

// TestMeetingYieldsAfterAutoInject（r_17 让拍）: 自动驾驶注入（拆解）
// 刚送达编排者 → tryConvene 让一拍不开会（给直提旁路让路，会议钟不
// 抢跑）；让拍窗过后照常召集。
func TestMeetingYieldsAfterAutoInject(t *testing.T) {
	st := newMeetStage(t)
	st.seat(t, "小牛", agents.OrchestratorRole, "s-orc")
	st.seat(t, "阿工", "工程师", "s-eng")
	if _, err := st.reqs.Create("", "demo", "待处理的需求", "工程师参与", "房主"); err != nil {
		t.Fatal(err)
	}
	if err := st.d.AutoPilotDivert("demo", "小牛", 1); err != nil {
		t.Fatal(err)
	}

	st.d.tryConvene()
	if _, live := st.meets.Active("demo"); live {
		t.Fatal("注入刚送达编排者，会议钟应让拍不开会")
	}
	if st.rb.all("s-orc") == "" || !strings.Contains(st.rb.all("s-orc"), "【自动驾驶·拆解】") {
		t.Fatal("前置失效：拆解注入未送达编排者")
	}

	// 让拍窗拨过去（测试缝：直接清窗）→ 同样条件下照常召集
	st.d.mu.Lock()
	st.d.apYieldUntil = time.Time{}
	st.d.mu.Unlock()
	st.d.tryConvene()
	if _, live := st.meets.Active("demo"); !live {
		t.Fatal("让拍窗过后应照常召集评审会")
	}
}

// TestMeetingHoldsWhilePlanAwaitsHost（阀门闸/待审背压）：提案槽里压着
// 等房主亲断的提案时，会议钟让位不开新评审——评审会的产出就是提案，
// 槽一房一件，再开一场只会把在审件顶成「未审作废」；房主批掉（Take
// 落槽）后下一拍照常召集。与引擎「消费在飞，不添新货」同口径。
func TestMeetingHoldsWhilePlanAwaitsHost(t *testing.T) {
	st := newMeetStage(t)
	st.seat(t, "小牛", agents.OrchestratorRole, "s-orc")
	st.seat(t, "阿工", "工程师", "s-eng")
	req, err := st.reqs.Create("", "demo", "待评审的需求", "工程师参与", "房主")
	if err != nil {
		t.Fatal(err)
	}

	// 前置：槽里压一份待审提案（上一场评审的产出，等房主）
	stored, _ := st.plans.Submit("", "demo", "小牛", plan.Plan{
		Req: req.ID, Title: "上一场的拆解", Tasks: []plan.PlanTask{{Title: "单子一"}},
	}, util.Now())

	st.d.tryConvene()
	if _, live := st.meets.Active("demo"); live {
		t.Fatal("待审提案还压在槽里，会议钟应让位不开新评审")
	}
	if st.rb.all("s-orc") != "" || st.rb.all("s-eng") != "" {
		t.Fatal("未开会就不该有任何参会注入")
	}

	// 房主批掉（落槽清空）→ 下一拍照常召集
	if _, err := st.plans.Take("", "demo", stored.ID); err != nil {
		t.Fatal(err)
	}
	st.d.tryConvene()
	if _, live := st.meets.Active("demo"); !live {
		t.Fatal("待审提案落定后应照常召集评审会")
	}
}

// TestMeetingReconveneCooldown（会议钟死循环）：需求评审过但状态仍
// open——提案滞留单槽等房主亲断的常态——兜底分支不再每拍强选重开；
// 纪要 24h 修订窗过后才允许重审（「过期走需求池重新评审」）。
func TestMeetingReconveneCooldown(t *testing.T) {
	realNow := util.NowFn()
	t.Cleanup(func() { util.SetNow(realNow) })

	st := newMeetStage(t)
	st.seat(t, "小牛", agents.OrchestratorRole, "s-orc")
	st.seat(t, "阿工", "工程师", "s-eng")
	req, err := st.reqs.Create("", "demo", "评审过但未立项的需求", "工程师参与", "房主")
	if err != nil {
		t.Fatal(err)
	}

	// 前置：一场已散会的评审（EndTS=base），需求评审过、仍 open
	base := int64(1_700_000_000)
	util.SetNow(func() int64 { return base })
	if _, err := st.meets.Begin("demo", req.ID, req.Title, "小牛", []string{"阿工"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.meets.End("demo"); !ok {
		t.Fatal("前置失效：会议未能正常入档")
	}

	// 修订窗内（+1h）：不开会、零注入——事故形态（此前每 10 分钟重开
	// 一场，全员参会指令刷屏）
	util.SetNow(func() int64 { return base + 3600 })
	st.d.tryConvene()
	if _, live := st.meets.Active("demo"); live {
		t.Fatal("评审未过重审冷却的需求不应被重开会（死循环形态）")
	}
	if st.rb.all("s-orc") != "" || st.rb.all("s-eng") != "" {
		t.Fatal("未开会就不该有任何参会注入")
	}

	// 修订窗外（+25h）：兜底重审成立
	util.SetNow(func() int64 { return base + 25*3600 })
	st.d.tryConvene()
	if _, live := st.meets.Active("demo"); !live {
		t.Fatal("超过 24h 修订窗的已评审需求应允许重审（过期走需求池重新评审）")
	}
}

func TestNoFreeOrchestratorMeansNoMeeting(t *testing.T) {
	// 没有编排者：产品经理再闲也不开会——v2.4 会议是编排者的职权
	st := newMeetStage(t)
	st.seat(t, "老品", "产品经理", "s-pm")
	st.seat(t, "阿工", "工程师", "s-eng")
	if _, err := st.reqs.Create("", "demo", "要工程师做的需求", "工程师参与", "房主"); err != nil {
		t.Fatal(err)
	}
	st.d.tryConvene()
	if _, live := st.meets.Active("demo"); live {
		t.Fatal("没有编排者在座就不该开会（v2.4：PM 不再主导会议）")
	}
	if st.rb.all("s-pm") != "" || st.rb.all("s-eng") != "" {
		t.Fatal("没有主席就不该有任何注入")
	}

	// 编排在座但手头有活：同样不开
	st2 := newMeetStage(t)
	st2.seat(t, "小牛", agents.OrchestratorRole, "s-orc")
	st2.seat(t, "老品", "产品经理", "s-pm")
	st2.busy(t, "小牛")
	if _, err := st2.reqs.Create("", "demo", "另一个需求", "描述", "房主"); err != nil {
		t.Fatal(err)
	}
	st2.d.tryConvene()
	if _, live := st2.meets.Active("demo"); live {
		t.Fatal("编排者手头有活就不该开会")
	}
}

func TestChairTurnsLandAsOpeningAndConclusion(t *testing.T) {
	st := newMeetStage(t)
	st.seat(t, "小牛", agents.OrchestratorRole, "s-orc")
	st.seat(t, "老品", "产品经理", "s-pm")
	st.seat(t, "阿工", "工程师", "s-eng")
	if _, err := st.reqs.Create("", "demo", "工程师需求", "工程师参与", "房主"); err != nil {
		t.Fatal(err)
	}
	st.d.tryConvene()

	st.d.noteMeetingTurn("小牛", "开场：需求复述与初步拆解草案。")
	st.d.noteMeetingTurn("老品", "补充用户场景与验收口径。")
	st.d.noteMeetingTurn("路人", "我不在会场。")

	m, _ := st.meets.Active("demo")
	if m.Opening != "开场：需求复述与初步拆解草案。" {
		t.Fatalf("主席的首轮发言应记为开场，得到 %q", m.Opening)
	}
	if len(m.Transcript) != 2 {
		t.Fatalf("纪要应恰有主席与产品经理两行（路人不算），得到 %d 行", len(m.Transcript))
	}

	st.meets.SetPhase("demo", meeting.PhaseSummary)
	st.d.noteMeetingTurn("小牛", "最终拆解：三个任务包。")
	if m, _ = st.meets.Active("demo"); m.Conclusion != "最终拆解：三个任务包。" {
		t.Fatalf("主席在总结段的发言应记为方案，得到 %q", m.Conclusion)
	}
}

// meetWait polls cond under a 2s deadline — the agenda goroutine acts
// asynchronously to MeetingCtl (the receipt comes from the window, the
// phase moves right after it returns).
func meetWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("超时等待：%s", what)
}

// agendaParked waits for the live agenda to park in a window (the
// control channel only exists while a window is open).
func (st *meetStage) agendaParked(t *testing.T) {
	t.Helper()
	meetWait(t, "议程协程停进窗口", func() bool {
		st.d.mu.Lock()
		defer st.d.mu.Unlock()
		return st.d.agenda != nil
	})
}

// phaseOf reads the live meeting's phase ("" = the room is free).
func (st *meetStage) phaseOf() string {
	m, live := st.meets.Active("demo")
	if !live {
		return ""
	}
	return m.Phase
}

// conveneDemo seats the v2.4 quorum and convenes one review.
func conveneDemo(t *testing.T) *meetStage {
	t.Helper()
	st := newMeetStage(t)
	st.seat(t, "小牛", agents.OrchestratorRole, "s-orc")
	st.seat(t, "老品", "产品经理", "s-pm")
	st.seat(t, "阿工", "工程师", "s-eng")
	if _, err := st.reqs.Create("", "demo", "工程师需求", "工程师参与", "房主"); err != nil {
		t.Fatal(err)
	}
	st.d.tryConvene()
	if _, live := st.meets.Active("demo"); !live {
		t.Fatal("会议应已开场")
	}
	return st
}

func TestChairMeetingControls(t *testing.T) {
	st := conveneDemo(t)

	// 空闲房间（开场前另起一台）
	st0 := newMeetStage(t)
	if _, err := st0.d.MeetingCtl("小牛", true, 0); err == nil {
		t.Fatal("会议室空闲时不应有可控制的会议")
	}

	// 非主持（参会人/外人）碰钟：拒
	for _, who := range []string{"老品", "路人"} {
		if _, err := st.d.MeetingCtl(who, false, 10); err == nil {
			t.Fatalf("%s 不是主持，不应能延时", who)
		}
	}

	// 主持提前结束讨论窗：直进总结段（phase 翻面 + 总结简报注入）
	st.agendaParked(t)
	receipt, err := st.d.MeetingCtl("小牛", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receipt, "结束") {
		t.Fatalf("end 回执应说明结束了当前议程段，得到 %q", receipt)
	}
	meetWait(t, "总结段", func() bool { return st.phaseOf() == meeting.PhaseSummary })
	meetWait(t, "总结简报注入", func() bool {
		return strings.Contains(st.rb.all("s-orc"), "【需求评审会·总结】")
	})

	// 总结窗内延时：分钟数钳到上限 60，回执带新截止
	st.agendaParked(t)
	receipt, err = st.d.MeetingCtl("小牛", false, 999)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receipt, "延长 60 分钟") {
		t.Fatalf("延时回执应反映钳制后的 60 分钟，得到 %q", receipt)
	}
	if _, err = st.d.MeetingCtl("小牛", false, 0); err == nil {
		t.Fatal("非正分钟数应被拒")
	}

	// 主持再 end：总结窗当场散会（房间释放、入档）
	if _, err = st.d.MeetingCtl("小牛", true, 0); err != nil {
		t.Fatal(err)
	}
	meetWait(t, "散会释放会议室", func() bool { return st.phaseOf() == "" })
	if h := st.meets.History("demo", 1); len(h) != 1 {
		t.Fatalf("散会的会议应入档，得到 %d 条", len(h))
	}
}

func TestAgendaWindowsFireNaturally(t *testing.T) {
	// 议程自然走钟仍然成立：窗口缩短到毫秒级，讨论窗烧完进总结、
	// 总结窗烧完散会（不靠主持控制）。
	st := newMeetStage(t)
	st.d.cfg.MeetingDiscuss = 40 * time.Millisecond
	st.d.cfg.MeetingClose = 40 * time.Millisecond
	st.seat(t, "小牛", agents.OrchestratorRole, "s-orc")
	st.seat(t, "老品", "产品经理", "s-pm")
	if _, err := st.reqs.Create("", "demo", "工程师需求", "工程师参与", "房主"); err != nil {
		t.Fatal(err)
	}
	st.d.tryConvene()
	meetWait(t, "讨论窗烧完进总结", func() bool { return st.phaseOf() == meeting.PhaseSummary })
	meetWait(t, "总结窗烧完散会", func() bool { return st.phaseOf() == "" })
	if !strings.Contains(st.rb.all("s-orc"), "【需求评审会·总结】") {
		t.Fatal("自然走钟也应注入总结简报")
	}
}

// TestCloseMeetingArchivesMinutes（r_25 t_208）：散会即机械归档——
// closeMeeting 后 kb 出现 p/<房>/meetings/<需求号>-1 文档（验收①，v2.9
// 纪要隔离跟房走），主持修订轻提示上墙（§四），同需求第二场会序号递增
// 不覆盖。
func TestCloseMeetingArchivesMinutes(t *testing.T) {
	dir := t.TempDir()
	docs, err := kb.OpenDocs(filepath.Join(dir, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	st := newMeetStage(t)
	st.d.cfg.Docs = docs

	// 假会一场：开锤→发言→散会
	if _, err := st.meets.Begin("demo", "r_99", "纪要归档验证", "小牛", []string{"小猿"}); err != nil {
		t.Fatal(err)
	}
	st.meets.NoteOpening("demo", "第一句议题。第二句目标。")
	st.meets.Line("demo", "小猿", "建议归档走混合模式，先机械后修订。")
	st.meets.NoteConclusion("demo", "结论一句话。")
	st.d.closeMeeting()

	doc, err := docs.Get("p/demo/meetings/r_99-1", 0)
	if err != nil {
		t.Fatalf("散会后 kb 应出现纪要文档: %v", err)
	}
	for _, want := range []string{"评审纪要 · r_99", "主持：小牛", "小猿", "建议归档走混合模式"} {
		if !strings.Contains(doc.Body, want) {
			t.Fatalf("纪要应含 %q:\n%s", want, doc.Body)
		}
	}
	if !strings.HasPrefix(doc.Title, "内部") {
		t.Fatalf("标题应带内部前缀（got %s）", doc.Title)
	}

	// 同需求第二场：序号递增（不覆盖第一场的纪要）
	if _, err := st.meets.Begin("demo", "r_99", "第二场", "小牛", []string{"小狐"}); err != nil {
		t.Fatal(err)
	}
	st.d.closeMeeting()
	if _, err := docs.Get("p/demo/meetings/r_99-2", 0); err != nil {
		t.Fatalf("第二场应落 r_99-2: %v", err)
	}
	first, _ := docs.Get("p/demo/meetings/r_99-1", 0)
	if !strings.Contains(first.Body, "建议归档走混合模式") {
		t.Fatal("第一场纪要不应被第二场覆盖")
	}
}

// TestCloseMeetingDeliversRevisePrompt（r_25 混合归档后半——修订驱动）：
// 纪要落库即把修订窗直递主持本人会话（房间系统行主持看不见——成员只
// 吃车道注入），注入含文档键与 24h 窗、逐人核对清单两项核对点；主持
// 不在调度（房主主持的会）不硬投、不炸。
func TestCloseMeetingDeliversRevisePrompt(t *testing.T) {
	dir := t.TempDir()
	docs, err := kb.OpenDocs(filepath.Join(dir, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	st := newMeetStage(t)
	st.d.cfg.Docs = docs
	st.seat(t, "小牛", agents.OrchestratorRole, "s-orc")

	if _, err := st.meets.Begin("demo", "r_98", "修订驱动验证", "小牛", []string{"小猿"}); err != nil {
		t.Fatal(err)
	}
	st.meets.NoteConclusion("demo", "结论一句话。")
	st.d.closeMeeting()

	orc := st.rb.all("s-orc")
	for _, want := range []string{"【纪要修订】", "p/demo/meetings/r_98-1", "24h", "逐人核对清单", "expect_rev=1"} {
		if !strings.Contains(orc, want) {
			t.Fatalf("主持应收到含 %q 的修订注入:\n%s", want, orc)
		}
	}

	// 房主主持（不在调度）：只有房间行，无注入、无 panic
	if _, err := st.meets.Begin("demo", "r_97", "房主主持场", "房主", []string{"小猿"}); err != nil {
		t.Fatal(err)
	}
	st.d.closeMeeting()
	if _, err := docs.Get("p/demo/meetings/r_97-1", 0); err != nil {
		t.Fatalf("房主主持场的纪要照常归档: %v", err)
	}
}
