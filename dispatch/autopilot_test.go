package dispatch

// autopilot_test.go — 自动补货/自动推进的调度器侧行为面（v2.7→r_19）：
//   - 【自动驾驶·选题】注入的冻结契约（提示词关键片段钉死；r_19 带
//     活目标线参数与「已开启自动补货」措辞）；
//   - AutoPilotPoke 只送在岗编排者，无编排者时如实报错；
//   - 【自动驾驶·续命】注入的冻结契约＋只送在册成员（房主/待招
//     如实报错指路）——续命是 v2.9 的第四闸；
//   - onReverse 两分支：提问卡代行宽限（r_19 修订：缺席照常出卡、
//     只给 accept_delay 宽限，满窗按假设放行＋房间一行系统留痕；
//     房主在场则让位全窗）、权限请求直接 allow——r_19 起只认自动
//     推进开关（只开补货时房主在场，照旧出卡/35 秒窗）。
//
// 开关真源是 staffing 的项目级设置（SettingsOf/SetAutoAdvance）——与
// 服务端引擎读同一位；关闭路径的行为（照旧出卡、35 秒权限窗）由
// asks/perm 既有测试钉住，这里不重复。

import (
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// startAutoPilotStage wires one project dispatcher over the recording
// bridge with a staffing store attached (the switch's truth); the
// orchestrator seat is optional (the no-driver refusal has its own
// test). All clocks parked — nothing fires on its own.
func startAutoPilotStage(t *testing.T, withOrchestrator bool) (*Dispatcher, *chat.Hub, *watchBridge, *staffing.Store) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staff, err := staffing.Open("")
	if err != nil {
		t.Fatal(err)
	}
	wb := &watchBridge{}
	d := Start(hub, store, wb, Config{
		Workspace:    t.TempDir(),
		InboxDir:     t.TempDir(),
		PatrolEvery:  -1,
		DailyAt:      "off",
		MeetingEvery: -1,
		WatchdogTick: make(chan time.Time), // 停摆：探针永不自发
		ProjectKey:   "proj-ap",
		StaffStore:   staff,
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "后端开发", "s-ap-1"); err != nil {
		t.Fatal(err)
	}
	if withOrchestrator {
		if err := d.attach("小牛", agents.OrchestratorRole, "s-ap-2"); err != nil {
			t.Fatal(err)
		}
	}
	return d, hub, wb, staff
}

// TestAutoPilotTopicPromptContract pins the frozen injection: the verb
// (req create 带编排者名与项目 key)、验收标准纪律、蓄水纪律（留在
// 池里不推进提案）与「不硬造需求」底线——提示词即契约。
func TestAutoPilotTopicPromptContract(t *testing.T) {
	p := autoPilotTopicPrompt("proj-ap", "小牛", 0, 3, "")
	for _, want := range []string{
		"【自动驾驶·选题】",
		"项目=proj-ap",
		"房主已开启自动补货",
		"niuma req create --name 小牛 --project proj-ap",
		"验收标准",
		"留在池里",
		"不随手推进提案",
		"自行决策",
		"不硬造需求",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("选题提示词缺约：%q", want)
		}
	}
}

// TestAutoPilotTopicPromptStocking（r_17 蓄水版）: 选题＝补货——头部带
// 当前水位与目标线、立到目标线即停、立完留在池里不推进提案；旧版
// 「推进到提案」话头必须摘除。
func TestAutoPilotTopicPromptStocking(t *testing.T) {
	empty := autoPilotTopicPrompt("proj-ap", "小牛", 0, 3, "")
	if !strings.Contains(empty, "open 0 条、低于目标线 3 条") {
		t.Errorf("空池头部应带水位/目标线: %s", empty)
	}
	low := autoPilotTopicPrompt("proj-ap", "小牛", 2, 3, "")
	if !strings.Contains(low, "open 2 条、低于目标线 3 条") {
		t.Errorf("头部应带当前水位: %s", low)
	}
	for _, p := range []string{empty, low} {
		if !strings.Contains(p, "把 open 补足到 ≥3 条即停") {
			t.Errorf("应钉「立到目标线即停」: %s", p)
		}
		if !strings.Contains(p, "留在池里") || !strings.Contains(p, "不随手推进提案") {
			t.Errorf("应钉「留在池里不推进提案」: %s", p)
		}
		if !strings.Contains(p, "不为凑数立低价值需求") {
			t.Errorf("质量铁律不能丢: %s", p)
		}
		if strings.Contains(p, "推进到提案") {
			t.Errorf("旧版「推进到提案」话头必须摘除: %s", p)
		}
	}
}

// TestAutoPilotTopicPromptGoal（目标制补货）: 带本次目标的选题注入——
// 目标上头部、达成判定协议（未达成不得拿「本期无新需求」应付）、收摊
// 动词（autopilot complete 带编排者名与项目 key）；无目标模式独有的
// 「本期无新需求」出路在目标版必须摘除（那是死循环的门）。
func TestAutoPilotTopicPromptGoal(t *testing.T) {
	p := autoPilotTopicPrompt("proj-ap", "小牛", 0, 3, "写完第二卷前三章并过一审")
	for _, want := range []string{
		"【自动驾驶·选题】",
		"项目=proj-ap",
		"房主已开启自动补货（本次目标：写完第二卷前三章并过一审）",
		"open 0 条、低于目标线 3 条",
		"niuma req create --name 小牛 --project proj-ap",
		"本次目标达成判定（每轮自查）",
		"不得以「环境零变化/本期无新需求」应付",
		"niuma autopilot complete --name 小牛 --project proj-ap",
		"自动关闭",
		"收摊总结",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("目标版选题提示词缺约：%q", want)
		}
	}
	if strings.Contains(p, "确无可做的下一步就明说") {
		t.Errorf("目标版不得保留无目标模式的「本期无新需求」出路: %s", p)
	}
	// 无目标模式（存量已开的房/直连 SetAutoStock）措辞原样：旧出路在，
	// 目标协议与收摊动词不在。
	legacy := autoPilotTopicPrompt("proj-ap", "小牛", 0, 3, "")
	if !strings.Contains(legacy, "确无可做的下一步就明说「本期无新需求」") {
		t.Errorf("无目标模式应保留旧出路: %s", legacy)
	}
	if strings.Contains(legacy, "autopilot complete") || strings.Contains(legacy, "本次目标") {
		t.Errorf("无目标模式不得掺入目标协议: %s", legacy)
	}
}

// TestAutoPilotDivertPromptContract pins the frozen 拆解 injection
// (r_17 直提旁路版): 消费既存需求、小型清晰需求直接 plan submit 落提
// 案（不必等评审会——不一定一直开需求会）、大需求不要直提留在池里
// 等会议钟（编排者没有自己开会的动词，话术不得许诺）、水位纪律。
func TestAutoPilotDivertPromptContract(t *testing.T) {
	p := autoPilotDivertPrompt("proj-ap", 2)
	for _, want := range []string{
		"【自动驾驶·拆解】",
		"项目=proj-ap",
		"2 条开放需求",
		"直接 plan submit 落提案",
		"不必等评审会",
		"不要直提",
		"留在池里，会议钟稍后会召集评审",
		"拆解既存需求优先于立新需求",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("拆解提示词缺约：%q", want)
		}
	}
}

// TestAutoPilotPokeDeliversToOrchestrator: 有编排者 → 注入送到编排者
// 会话（内容含冻结头）；普通成员不被打扰。
func TestAutoPilotPokeDeliversToOrchestrator(t *testing.T) {
	d, _, wb, _ := startAutoPilotStage(t, true)
	if err := d.AutoPilotPoke(0); err != nil {
		t.Fatalf("有编排者时 Poke 不应报错：%v", err)
	}
	sent := wb.sentText()
	if !strings.Contains(sent, "【自动驾驶·选题】") {
		t.Fatalf("未见选题注入（sends=%q）", sent)
	}
	if strings.Count(sent, "【自动驾驶·选题】") != 1 {
		t.Fatalf("注入应只发一枚（sends=%q）", sent)
	}
}

// TestAutoPilotPokeRefusesWithoutOrchestrator: 无编排者 → 如实报错并
// 指路（服务端引擎把这行转成房间提示）。
func TestAutoPilotPokeRefusesWithoutOrchestrator(t *testing.T) {
	d, _, wb, _ := startAutoPilotStage(t, false)
	err := d.AutoPilotPoke(0)
	if err == nil {
		t.Fatal("无编排者时应报错")
	}
	if !strings.Contains(err.Error(), agents.OrchestratorRole) {
		t.Fatalf("报错应点名缺失的编排岗：%v", err)
	}
	if wb.sentText() != "" {
		t.Fatalf("无编排者时不应有任何注入（sends=%q）", wb.sentText())
	}
}

// TestAutoPilotResumePromptContract pins the frozen 续命 injection: the
// header, every stalled task id＋title, the ledger verbs (task update
// 落账) and the mode's 自行决策 discipline — the prompt is the
// contract.
func TestAutoPilotResumePromptContract(t *testing.T) {
	p := autoPilotResumePrompt("proj-ap", []*tasks.Task{
		{ID: "t_103", Title: "茶水间区域与空闲成员前往实现"},
		{ID: "t_117", Title: "触发联动：出任务→编排者打印"},
	})
	for _, want := range []string{
		"【自动驾驶·续命】",
		"项目=proj-ap",
		"自动推进巡检",
		"房主已开启自动推进、不介入流程确认",
		"长时间没有台账更新",
		"t_103「茶水间区域与空闲成员前往实现」",
		"t_117「触发联动：出任务→编排者打印」",
		"task update",
		"自行决策",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("续命提示词缺约：%q", want)
		}
	}
}

// TestAutoPilotResumeDeliversToAssignee: 负责人在册 → 注入送到其会话
// （内容含冻结头与任务行），只此一枚。
func TestAutoPilotResumeDeliversToAssignee(t *testing.T) {
	d, _, wb, _ := startAutoPilotStage(t, false)
	err := d.AutoPilotResume("张三", []*tasks.Task{{ID: "t_103", Title: "茶水间实现"}})
	if err != nil {
		t.Fatalf("在册成员续命不应报错：%v", err)
	}
	sent := wb.sentText()
	if !strings.Contains(sent, "【自动驾驶·续命】") {
		t.Fatalf("未见续命注入（sends=%q）", sent)
	}
	if !strings.Contains(sent, "t_103") {
		t.Fatalf("注入应点名停滞任务（sends=%q）", sent)
	}
	if strings.Count(sent, "【自动驾驶·续命】") != 1 {
		t.Fatalf("注入应只发一枚（sends=%q）", sent)
	}
}

// TestAutoPilotResumeRefusesNonMember: 负责人不在调度成员（房主本人/
// 待招岗位）→ 如实报错指路、零注入（服务端引擎把这行转成房间提示）。
func TestAutoPilotResumeRefusesNonMember(t *testing.T) {
	d, _, wb, _ := startAutoPilotStage(t, false)
	err := d.AutoPilotResume("房主", []*tasks.Task{{ID: "t_106", Title: "验收发布"}})
	if err == nil {
		t.Fatal("负责人不在册时应报错")
	}
	for _, want := range []string{"房主", "t_106", "不在本房调度成员"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错缺 %q：%v", want, err)
		}
	}
	if wb.sentText() != "" {
		t.Fatalf("无人可送时不应有任何注入（sends=%q）", wb.sentText())
	}
}

// askParamsAP is the autopilot tests' ask shape (规格二选一).
func askParamsAP(m *member) map[string]any {
	return map[string]any{
		"sessionId": m.sessionID,
		"prompt":    "规格二选一",
		"questions": []any{map[string]any{
			"question": "选 A 还是 B？",
			"options":  []any{map[string]any{"label": "A"}, map[string]any{"label": "B"}},
		}},
	}
}

// TestOnReverseAskGraceCardAutoReleasesUnderAutoPilot（r_19 修订）：
// 开关开＋房主缺席 → 提问照常出卡，但 Due 写的是代行宽限；满窗未点
// 按合理假设放行——decline 理由带「勿停工候复」，房间落一行带摘要
// 的系统留痕，pending 不残留。旧 r_19 的「不出卡当场放行」已被修订
// 取代（代行不失聪：卡先出面，宽限给房主点选机会）。
func TestOnReverseAskGraceCardAutoReleasesUnderAutoPilot(t *testing.T) {
	old := autoAskGraceDefault
	autoAskGraceDefault = 1 * time.Second
	t.Cleanup(func() { autoAskGraceDefault = old })
	d, hub, _, staff := startAutoPilotStage(t, true)
	staff.SetAutoAdvance("proj-ap", true)
	tap := newSystemTap(hub)
	m := d.lookup("小牛")

	done := make(chan any, 1)
	go func() {
		v, err := d.onReverse("interaction/requestUserInput", askParamsAP(m))
		if err != nil {
			t.Error(err)
		}
		done <- v
	}()
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 1 },
		"缺席房主也应出卡（代行宽限）")
	q := hub.OpenQuestions()[0]
	if q.Due-q.TS != 1 {
		t.Fatalf("宽限卡的 due-ts 应恰为宽限秒数（got %d）", q.Due-q.TS)
	}
	waitFor(t, func() bool {
		for _, hm := range hub.History() {
			if hm.From == "小牛" && strings.Contains(hm.Text, "自动推进中，逾期按合理假设放行") &&
				strings.Contains(hm.Text, "A / B") {
				return true
			}
		}
		return false
	}, "提问 say 行应注明代行宽限与后果")

	res := <-done
	r, ok := res.(map[string]any)
	if !ok || r["action"] != "decline" {
		t.Fatalf("宽限满应 decline（got %+v）", res)
	}
	if reason, _ := r["reason"].(string); !strings.Contains(reason, "合理假设") || !strings.Contains(reason, "勿停工候复") {
		t.Fatalf("decline 理由应是假设口径＋勿候复：%q", reason)
	}
	waitFor(t, func() bool { return tap.count("宽限满未点选") == 1 },
		"房间未见放行系统行")
	// 行内要带提问摘要——放行了什么必须看得见（首问文本＋选项标签）。
	waitFor(t, func() bool {
		return tap.count("「选 A 还是 B？」") == 1 && tap.count("可选：A / B") == 1
	}, "放行系统行应带提问摘要")
	d.mu.Lock()
	n := len(d.asks)
	d.mu.Unlock()
	if n != 0 {
		t.Fatalf("宽限满后不应残留注册卡（残留 %d 张）", n)
	}
}

// TestOnReverseAskGraceFoldsRepeats: 同一问（同签名）反复到达——宽限内
// 的重宣告挂同一张卡（不另开卡、不另落行、转录不重记）；放行后的同问
// 重达在折叠窗内当场放行（不再开窗）；换一个真问题则另起一卡一行。
func TestOnReverseAskGraceFoldsRepeats(t *testing.T) {
	old := autoAskGraceDefault
	autoAskGraceDefault = 400 * time.Millisecond
	t.Cleanup(func() { autoAskGraceDefault = old })
	d, hub, _, staff := startAutoPilotStage(t, true)
	staff.SetAutoAdvance("proj-ap", true)
	tap := newSystemTap(hub)
	m := d.lookup("小牛")

	run := func(params map[string]any) chan any {
		ch := make(chan any, 1)
		go func() {
			v, err := d.onReverse("interaction/requestUserInput", params)
			if err != nil {
				t.Error(err)
			}
			ch <- v
		}()
		return ch
	}
	ask := askParamsAP(m)
	first := run(ask)
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 1 }, "首问应出卡")
	time.Sleep(50 * time.Millisecond) // mint 收尾余量：重宣告须挂上已注册的 pa
	re1, re2 := run(ask), run(ask)
	<-first
	<-re1
	<-re2
	waitFor(t, func() bool { return tap.count("宽限满未点选") == 1 },
		"三重宣告应只落一行放行留痕")
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 0 }, "卡应收口")

	// 放行后的同问重达（折叠窗内）：当场按假设放行，不再开卡不再落行。
	res, err := d.onReverse("interaction/requestUserInput", ask)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := res.(map[string]any)
	if !ok || r["action"] != "decline" {
		t.Fatalf("折叠窗内的同问重达应当场 decline（got %+v）", res)
	}
	if reason, _ := r["reason"].(string); !strings.Contains(reason, "刚按合理假设放行") {
		t.Fatalf("重达的 decline 理由应指明刚放行：%q", reason)
	}
	waitFor(t, func() bool {
		return tap.count("宽限满未点选") == 1 && len(hub.OpenQuestions()) == 0
	}, "重达不应另起一行或一卡")

	// 窗内换真问题：新签名，另起一卡一行、转录再记一对。
	other := map[string]any{
		"sessionId": m.sessionID,
		"prompt":    "分支策略",
		"questions": []any{map[string]any{
			"question": "要不要拆两步走？",
			"options":  []any{map[string]any{"label": "拆"}, map[string]any{"label": "不拆"}},
		}},
	}
	second := run(other)
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 1 }, "新问题应另起一卡")
	<-second
	waitFor(t, func() bool { return tap.count("宽限满未点选") == 2 },
		"不同的新问题应另起一行")
	waitFor(t, func() bool { return termPairs(t, hub, "小牛") == 4 },
		"转录应恰好两对（重宣告不重记）")
}

// TestOnReverseAskOwnerPresentFallsToFullCard（r_19 修订的在场腿）：
// 开关开着，但房主近窗内在场（点过卡）→ 提问走正常全窗出卡，宽限
// 不适用：卡在宽限时长过后仍开放、房间无代行放行行，点选答案直达。
func TestOnReverseAskOwnerPresentFallsToFullCard(t *testing.T) {
	oldG := autoAskGraceDefault
	autoAskGraceDefault = 200 * time.Millisecond
	t.Cleanup(func() { autoAskGraceDefault = oldG })
	oldW := askWindow
	askWindow = 4 * time.Second
	t.Cleanup(func() { askWindow = oldW })
	d, hub, _, staff := startAutoPilotStage(t, true)
	staff.SetAutoAdvance("proj-ap", true)
	tap := newSystemTap(hub)
	m := d.lookup("小牛")
	hub.NoteOwnerActivity()

	done := make(chan any, 1)
	go func() {
		v, err := d.onReverse("interaction/requestUserInput", askParamsAP(m))
		if err != nil {
			t.Error(err)
		}
		done <- v
	}()
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 1 }, "应出卡")
	q := hub.OpenQuestions()[0]
	if q.Due-q.TS != 4 {
		t.Fatalf("在场房主应走全窗（due-ts 应为 4，got %d）", q.Due-q.TS)
	}
	time.Sleep(350 * time.Millisecond) // 越过宽限时长仍开放＝确系全窗
	if len(hub.OpenQuestions()) != 1 {
		t.Fatal("在场房主的卡不应被宽限放行")
	}
	if tap.count("宽限满未点选") != 0 || tap.count("自动推进") != 0 {
		t.Fatal("在场时不应有代行放行行")
	}
	if !d.Answer(q.ID, "房主", map[string]string{"选 A 还是 B？": "B"}) {
		t.Fatal("应答应落卡")
	}
	res := <-done
	r, ok := res.(map[string]any)
	if !ok || r["action"] != "accept" {
		t.Fatalf("点选后应 accept（got %+v）", res)
	}
	answers, _ := r["content"].(map[string]any)["answers"].(map[string]string)
	if answers["选 A 还是 B？"] != "B" {
		t.Fatalf("答案应原样回送：%+v", answers)
	}
}

// TestOnReverseAskGraceClickDeliversAnswer: 缺席开窗的宽限卡，房主
// 宽限内赶到点选——答案直达成员当轮（代行宽限不是假窗，点了真算）。
func TestOnReverseAskGraceClickDeliversAnswer(t *testing.T) {
	old := autoAskGraceDefault
	autoAskGraceDefault = 8 * time.Second
	t.Cleanup(func() { autoAskGraceDefault = old })
	d, hub, _, staff := startAutoPilotStage(t, true)
	staff.SetAutoAdvance("proj-ap", true)
	m := d.lookup("小牛")

	done := make(chan any, 1)
	go func() {
		v, err := d.onReverse("interaction/requestUserInput", askParamsAP(m))
		if err != nil {
			t.Error(err)
		}
		done <- v
	}()
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 1 }, "宽限卡应出面")
	q := hub.OpenQuestions()[0]
	if !d.Answer(q.ID, "房主", map[string]string{"选 A 还是 B？": "A"}) {
		t.Fatal("宽限内点选应落卡")
	}
	res := <-done
	r, ok := res.(map[string]any)
	if !ok || r["action"] != "accept" {
		t.Fatalf("宽限内点选应 accept（got %+v）", res)
	}
	answers, _ := r["content"].(map[string]any)["answers"].(map[string]string)
	if answers["选 A 还是 B？"] != "A" {
		t.Fatalf("答案应原样回送：%+v", answers)
	}
	if len(hub.OpenQuestions()) != 0 {
		t.Fatal("点选后卡应收口")
	}
}

// TestDispatcherAutoAskGraceChain: 代行宽限的旋钮链——staffing 的
// accept_delay 优先，无旋钮走默认（与提案代收同一个旋钮、同一个数）。
func TestDispatcherAutoAskGraceChain(t *testing.T) {
	d, _, _, staff := startAutoPilotStage(t, false)
	if got := d.autoAskGrace(); got != autoAskGraceDefault {
		t.Fatalf("无旋钮应走默认（got %s）", got)
	}
	staff.SetAutoPilotKnobs("proj-ap", staffing.AutoPilotKnobs{AcceptDelayS: 200})
	if got := d.autoAskGrace(); got != 200*time.Second {
		t.Fatalf("旋钮应生效（got %s）", got)
	}
}

// TestAutoAskFirstExpires: 折叠窗过期后同签名重新算首见（懒清理口径
// 同 resumeMark）——时钟缝拨过 autoAskFold 验证。
func TestAutoAskFirstExpires(t *testing.T) {
	d, _, _, _ := startAutoPilotStage(t, false)
	base := util.Now()
	prev := util.NowFn()
	util.SetNow(func() int64 { return base })
	t.Cleanup(func() { util.SetNow(prev) })

	sig := "sig-x"
	if !d.autoAskFirst(sig) {
		t.Fatal("首见应返回 true")
	}
	if d.autoAskFirst(sig) {
		t.Fatal("窗内重复应返回 false")
	}
	util.SetNow(func() int64 { return base + autoAskFold + 1 })
	if !d.autoAskFirst(sig) {
		t.Fatal("窗过期后同签名应重新算首见")
	}
}

// termPairs counts the ask+answer transcript rows one member carries —
// the r_17 一问一答成对纪律的观测面（fold 重复不重记）。
func termPairs(t *testing.T, hub *chat.Hub, name string) int {
	t.Helper()
	rows := hub.TermSnapshot(name, 0)[name]
	ask, answer := 0, 0
	for _, e := range rows {
		switch e.Kind {
		case chat.TraceAsk:
			ask++
		case chat.TraceAnswer:
			answer++
		}
	}
	if ask != answer {
		t.Fatalf("终端转录 ask/answer 应成对（ask=%d answer=%d）", ask, answer)
	}
	return ask * 2
}

// TestOnReversePermAllowedUnderAutoPilot: 开关开 → 权限请求直接放行
// （reason 标注来源），成员回合不被 35 秒窗拖住、房间无系统行。
func TestOnReversePermAllowedUnderAutoPilot(t *testing.T) {
	d, hub, _, staff := startAutoPilotStage(t, true)
	staff.SetAutoAdvance("proj-ap", true)
	tap := newSystemTap(hub)
	m := d.lookup("小牛")

	res, err := d.onReverse("interaction/requestPermission", map[string]any{
		"sessionId": m.sessionID, "toolName": "Bash",
	})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := res.(map[string]any)
	if !ok || r["decision"] != "allow" {
		t.Fatalf("应直接放行（got %+v）", res)
	}
	if reason, _ := r["reason"].(string); reason != "autopilot-allow" {
		t.Fatalf("reason 应标注来源：%q", reason)
	}
	if n := tap.count(""); n != 0 {
		t.Fatalf("自动驾驶放行不该打扰房间（%d 条系统行）", n)
	}
}

// TestAutoAdvanceGateReadsAdvanceNotStock（r_19）: 放行门只认自动推进
// ——只开补货时房主在场，权限/提问照旧走人审窗；这一条是拆分的
// dispatcher 半边全部意义。
func TestAutoAdvanceGateReadsAdvanceNotStock(t *testing.T) {
	d, _, _, staff := startAutoPilotStage(t, true)
	if d.autoAdvanceOn() {
		t.Fatal("默认必须关")
	}
	staff.SetAutoStock("proj-ap", true)
	if d.autoAdvanceOn() {
		t.Fatal("只开补货不得触发放行门（房主在场）")
	}
	staff.SetAutoAdvance("proj-ap", true)
	if !d.autoAdvanceOn() {
		t.Fatal("开推进应打开放行门")
	}
}

// TestDispatcherWaterTargetChain（r_19）: 选题注入的目标线两级链——
// staffing 旋钮（water_target）> 内置默认 3；旋钮写即热更（每次
// 注入直读）。
func TestDispatcherWaterTargetChain(t *testing.T) {
	d, _, _, staff := startAutoPilotStage(t, true)
	if got := d.waterTarget(); got != agents.WaterTarget {
		t.Fatalf("未设置应走默认链: got %d want %d", got, agents.WaterTarget)
	}
	staff.SetAutoPilotKnobs("proj-ap", staffing.AutoPilotKnobs{WaterTarget: 5})
	if got := d.waterTarget(); got != 5 {
		t.Fatalf("设置档未生效: got %d want 5", got)
	}
}

// TestAutoPilotTopicPromptCustomTarget（r_19）: 目标线调到 5 时，措辞
// 全程带活口径——头部「低于目标线 5 条」、做法「补足到 ≥5 条即停」。
func TestAutoPilotTopicPromptCustomTarget(t *testing.T) {
	p := autoPilotTopicPrompt("proj-ap", "小牛", 2, 5, "")
	for _, want := range []string{
		"open 2 条、低于目标线 5 条",
		"把 open 补足到 ≥5 条即停",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("自定义目标线措辞缺约 %q: %s", want, p)
		}
	}
}
