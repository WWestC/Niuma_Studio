package dispatch

// coalesce_test.go — v2.11 车道优化的行为面（L1–L6）。第一性口径：回合
// 是唯一消化单位，批量决策的真信号是「交互性是否还在」——
//   - L1 汇聚窗：近同时到达的点名并成一轮（同窗兄弟折入，队头延迟
//     发车），FIFO 与归因不破；
//   - L2 同发送方连发合并：同人短窗连发并进尾节，队龄不洗新，异发
//     送方/超窗各占一位；
//   - L3 自适应并车：深度退化阈值（闲道保逐条节奏，深道大口吞）＋
//     foldCapDeep 阶梯＋字数预算（装不下留下轮，不丢线）；
//   - L4 背压：深度越坎给发送方递拥塞注（成员走背景车道零房间噪
//     音，非成员落系统行），水位阶梯防刷屏；QueueCap 溢出从静默丢
//     改为「系统行＋发送方投递失败注」；
//   - L5 收到纪律 footer 挪到投递面：单车单线教一遍，并车多节教
//     「一轮一收到、回执挂每条」；
//   - L6 度量：等待/深度/并车三分布＋溢出计数，LaneStats 可查。
// 汇聚窗的行为面用显式小窗考（TestMain 的 seam 把默认窗在整个测试
// 二进制内关掉，老套件的 3s waitFor 死线不受 2s 窗拖累）。

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/wire"
)

// startCfgRoom — startKickRoom 的可配变体（汇聚窗等旋钮按测注入）。
func startCfgRoom(t *testing.T, mutate func(*Config)) (*chat.Hub, *Dispatcher, *gateBridge) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	gb := newGateBridge()
	cfg := Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	d := Start(hub, store, gb, cfg)
	t.Cleanup(d.Stop)
	if err := d.attach("甲", "工程师", "s-kick-a"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("乙", "工程师", "s-kick-b"); err != nil {
		t.Fatal(err)
	}
	return hub, d, gb
}

// waitSlow — 汇聚窗测试自己的宽死线等待（秒级戳截断让窗的实际等待
// 在窗长 ±1s 内抖动，3s 的 waitFor 不够用）。
func waitSlow(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

// TestSenderFoldMergesConsecutiveSameSender — L2：同人短窗连发并进尾
// 节（读/回执货随节合并、队龄保持首条戳），异发送方与超窗不并。
func TestSenderFoldMergesConsecutiveSameSender(t *testing.T) {
	_, d, _ := startCfgRoom(t, nil)
	m := d.lookup("甲")
	now := time.Now().Unix()
	d.enqueueFrom(m, "乙", "第一条：接口定稿", []int64{1}, []int64{1}, now, nil)
	d.enqueueFrom(m, "乙", "第二条：补充说明", []int64{2}, []int64{2}, now+5, nil)
	d.mu.Lock()
	qlen, text, stamp := len(m.lanes.queue), m.lanes.queue[0].text, m.lanes.queue[0].at
	reads := append([]int64(nil), m.lanes.queue[0].read...)
	d.mu.Unlock()
	if qlen != 1 {
		t.Fatalf("同人短窗连发应并成一条，车道 %d 条", qlen)
	}
	for _, want := range []string{"第一条", "第二条", senderFoldMarker} {
		if !strings.Contains(text, want) {
			t.Fatalf("合并条缺 %q：%s", want, text)
		}
	}
	for _, want := range []int64{1, 2} {
		if !containsSeq(reads, want) {
			t.Fatalf("已读货未随节合并（缺 %d）：%v", want, reads)
		}
	}
	if stamp != now {
		t.Fatalf("合并不得洗新队龄：戳 %d，期望 %d", stamp, now)
	}
	// 异发送方不并；同人超窗不并——各占一位，逐条节奏只让给「同一个
	// 人的一条思路」。
	d.enqueueFrom(m, "丙", "第三条：别人插话", []int64{3}, []int64{3}, now+6, nil)
	d.enqueueFrom(m, "乙", "第四条：隔了一阵", []int64{4}, []int64{4}, now+61, nil)
	d.mu.Lock()
	qlen = len(m.lanes.queue)
	d.mu.Unlock()
	if qlen != 3 {
		t.Fatalf("异发送方/超窗不应合并，车道应 3 条，实 %d", qlen)
	}
}

// TestCoalesceWindowBatchesSiblings — L1：窗内先后到达的两条点名并成
// 一车（队头延迟发车，兄弟折入〔N〕节），车道清空、footer 只教一遍。
func TestCoalesceWindowBatchesSiblings(t *testing.T) {
	hub, d, gb := startCfgRoom(t, func(c *Config) { c.CoalesceWindow = 2 * time.Second })
	speaker, _ := hub.Join("房主", "", false)
	if _, ok := hub.Say(speaker, "@甲 窗内的第一条"); !ok {
		t.Fatal("say 未入史")
	}
	// 延迟证明：窗没满不发车（秒级戳截断下实际等待 ≥ 窗长-1s，150ms
	// 的观测窗稳在「未发车」侧）。
	time.Sleep(150 * time.Millisecond)
	if n := gb.sendCount("s-kick-a"); n != 0 {
		t.Fatalf("汇聚窗未满不该发车，实投 %d 次", n)
	}
	if _, ok := hub.Say(speaker, "@甲 窗内的第二条"); !ok {
		t.Fatal("say 未入史")
	}
	waitSlow(t, func() bool {
		texts := gb.sendTexts("s-kick-a")
		return len(texts) == 1 && strings.Contains(texts[0], "窗内的第一条") && strings.Contains(texts[0], "窗内的第二条")
	}, "近同时到达的两条点名应并成一车送达")
	d.mu.Lock()
	left := len(d.members["甲"].lanes.queue)
	d.mu.Unlock()
	if left != 0 {
		t.Fatalf("第二条应随车折走，车道剩 %d 条", left)
	}
	text := gb.sendTexts("s-kick-a")[0]
	if n := strings.Count(text, ackNoteMark); n != 1 {
		t.Fatalf("收到纪律一轮只教一遍，实 %d 遍", n)
	}
}

// TestAdaptiveFoldThresholdByDepth — L3：同一「30 秒半陈」的线，浅道
// （深度 3，阈值 52s）不折——逐条节奏；深道（深度 8，阈值退化到
// 10s）全折——交互性早没了，消化率优先。
func TestAdaptiveFoldThresholdByDepth(t *testing.T) {
	_, d, gb := startCfgRoom(t, nil)
	mid := time.Now().Add(-30 * time.Second).Unix()

	// 深道：8 条 30s 线全折（foldCapFor(8)=8）
	m := d.lookup("甲")
	for i := 0; i < 8; i++ {
		d.enqueueAt(m, fmt.Sprintf("半陈线%d", i), []int64{int64(100 + i)}, []int64{int64(100 + i)}, mid, nil)
	}
	d.deliver("甲", "深道头令", nil, nil)
	text := gb.sendTexts("s-kick-a")[0]
	if !strings.Contains(text, "〔8〕") || !strings.Contains(text, "半陈线7") {
		t.Fatalf("深度 8 阈值应退化到 10s，30s 线全折：%s", text)
	}
	d.mu.Lock()
	left := len(m.lanes.queue)
	d.mu.Unlock()
	if left != 0 {
		t.Fatalf("深道 8 条应全并走，剩 %d", left)
	}

	// 浅道：深度 3 阈值 52s，30s 线不动——各等各的回合
	m2 := d.lookup("乙")
	for i := 0; i < 3; i++ {
		d.enqueueAt(m2, fmt.Sprintf("浅道线%d", i), nil, nil, mid, nil)
	}
	d.deliver("乙", "浅道头令", nil, nil)
	t2 := gb.sendTexts("s-kick-b")[0]
	if strings.Contains(t2, "排队积压") {
		t.Fatalf("浅道 30s 线未过阈不该并车：%s", t2)
	}
	d.mu.Lock()
	left2 := len(m2.lanes.queue)
	d.mu.Unlock()
	if left2 != 3 {
		t.Fatalf("浅道 3 条应原地等待，剩 %d", left2)
	}
}

// TestFoldRuneBudgetStopsBatch — L3：折叠段字数预算封顶——超预算的线
// 留给自己的回合（欠回复的线只准延迟，不准丢）。
func TestFoldRuneBudgetStopsBatch(t *testing.T) {
	_, d, gb := startCfgRoom(t, nil)
	m := d.lookup("甲")
	old := time.Now().Add(-2 * time.Hour).Unix()
	filler := strings.Repeat("长", 2900) // 两条 5806 ≤ 6000，第三条超
	for i := 0; i < 3; i++ {
		d.enqueueAt(m, fmt.Sprintf("长线%d：%s", i, filler), []int64{int64(10 + i)}, []int64{int64(10 + i)}, old, nil)
	}
	d.deliver("甲", "头令", nil, nil)
	text := gb.sendTexts("s-kick-a")[0]
	if !strings.Contains(text, "〔1〕") || !strings.Contains(text, "〔2〕") {
		t.Fatal("预算内两条应折入")
	}
	if strings.Contains(text, "〔3〕") {
		t.Fatal("预算外第三条应停折")
	}
	d.mu.Lock()
	left := len(m.lanes.queue)
	d.mu.Unlock()
	if left != 1 {
		t.Fatalf("超预算线应留车道等自己的回合，剩 %d", left)
	}
}

// TestFoldRuneBudgetGuardsFirstLine — 预算从首条起算：怪物头线（单条已
// 超 foldBudget）不因「首条免检」独占一轮——整段不折，留它自己的回合。
func TestFoldRuneBudgetGuardsFirstLine(t *testing.T) {
	_, d, gb := startCfgRoom(t, nil)
	m := d.lookup("甲")
	old := time.Now().Add(-2 * time.Hour).Unix()
	monster := "怪物头线：" + strings.Repeat("巨", 6100)
	d.enqueueAt(m, monster, []int64{10}, []int64{10}, old, nil)
	d.enqueueAt(m, "跟班短线", []int64{11}, []int64{11}, old, nil)
	d.deliver("甲", "头令", nil, nil)
	text := gb.sendTexts("s-kick-a")[0]
	if strings.Contains(text, "排队积压") {
		t.Fatal("怪物头线超预算，本轮一条都不该折")
	}
	d.mu.Lock()
	left := len(m.lanes.queue)
	d.mu.Unlock()
	if left != 2 {
		t.Fatalf("两条都应留车道（怪物头线走自己的回合），剩 %d", left)
	}
}

// TestBackpressureNotifiesSenders — L4：深度越坎（4）发送方得拥塞注——
// 成员走背景车道（零房间噪音），非成员（房主）落系统行；水位阶梯
// （+4 再开口）防刷屏。
func TestBackpressureNotifiesSenders(t *testing.T) {
	hub, d, _ := startCfgRoom(t, nil)
	m := d.lookup("甲")
	now := time.Now().Unix()
	// 深度 1–3：安静
	for i, from := range []string{"丙", "乙", "丙"} {
		d.enqueueFrom(m, from, fmt.Sprintf("线%d", i+1), nil, nil, now+int64(i), nil)
	}
	d.mu.Lock()
	inj := len(d.members["乙"].lanes.inject)
	d.mu.Unlock()
	if inj != 0 {
		t.Fatalf("深度未越坎不该递注（乙背景道 %d 条）", inj)
	}
	// 深度 4：越坎，成员乙得背景注
	d.enqueueFrom(m, "乙", "线4", nil, nil, now+3, nil)
	d.mu.Lock()
	note := ""
	if s := d.members["乙"].lanes.inject; len(s) > 0 {
		note = s[len(s)-1].text
	}
	d.mu.Unlock()
	if !strings.Contains(note, "拥塞提示") || !strings.Contains(note, "甲") {
		t.Fatalf("成员发送方应收到背景拥塞注：%q", note)
	}
	for _, msg := range hub.History() {
		if msg.Type == chat.MsgSystem && strings.Contains(msg.Text, "拥塞提示") {
			t.Fatalf("成员发送方的注不该进房间系统行：%s", msg.Text)
		}
	}
	// 深度 5–7：水位阶梯内安静；深度 8：非成员发送方落系统行
	for i, from := range []string{"房主", "戊", "房主", "戊"} {
		d.enqueueFrom(m, from, fmt.Sprintf("线%d", 5+i), nil, nil, now+4+int64(i), nil)
	}
	found := false
	for _, msg := range hub.History() {
		if msg.Type == chat.MsgSystem && strings.Contains(msg.Text, "拥塞提示") && strings.Contains(msg.Text, "甲") {
			found = true
		}
	}
	if !found {
		t.Fatal("深度 8 再越坎，非成员发送方应见系统行拥塞注")
	}
}

// TestQueueCapOverflowDropsAloud — L4：车道满 32 条后裁旧不再静默——
// 房间落积压溢出系统行（点名被裁的线），被裁线的成员发送方背景道
// 得投递失败注，L6 溢出计数入快照。
func TestQueueCapOverflowDropsAloud(t *testing.T) {
	hub, d, _ := startCfgRoom(t, nil)
	m := d.lookup("甲")
	// 占住成员（Running 只进不出），车道纯积压
	d.deliver("甲", "占位令（保持忙碌，车道只进不出）", nil, nil)
	now := time.Now().Unix()
	for i := 0; i < QueueCap+1; i++ {
		from := "乙"
		if i%2 == 1 {
			from = "丙"
		}
		d.enqueueFrom(m, from, fmt.Sprintf("溢出线%02d", i), []int64{int64(1000 + i)}, []int64{int64(1000 + i)}, now+int64(i), nil)
	}
	d.mu.Lock()
	qlen := len(m.lanes.queue)
	d.mu.Unlock()
	if qlen != QueueCap {
		t.Fatalf("溢出应裁到 %d 条，剩 %d", QueueCap, qlen)
	}
	found := false
	for _, msg := range hub.History() {
		if msg.Type == chat.MsgSystem && strings.Contains(msg.Text, "积压溢出") && strings.Contains(msg.Text, "溢出线00") {
			found = true
		}
	}
	if !found {
		t.Fatal("溢出裁线应落系统行（点名最早被裁的线）")
	}
	d.mu.Lock()
	var note string
	for _, e := range d.members["乙"].lanes.inject {
		if strings.Contains(e.text, "投递失败") {
			note = e.text
		}
	}
	d.mu.Unlock()
	if note == "" {
		t.Fatal("被裁线的成员发送方应在背景道收到投递失败注")
	}
	if got := d.LaneStats().OverflowDropped; got != 1 {
		t.Fatalf("溢出计数应 1，实 %d", got)
	}
}

// TestAckFooterPerDelivery — L5：收到纪律按投递面出——单车单线教单条
// 口径一遍；并车多节教「一轮一收到、回执挂每条、分节作答」。
func TestAckFooterPerDelivery(t *testing.T) {
	hub, d, gb := startCfgRoom(t, nil)
	speaker, _ := hub.Join("房主", "", false)
	if _, ok := hub.Say(speaker, "@甲 单条点名"); !ok {
		t.Fatal("say 未入史")
	}
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") == 1 }, "点名应注入")
	text := gb.sendTexts("s-kick-a")[0]
	if !strings.Contains(text, "整轮只回「收到」二字即可") {
		t.Fatalf("单线投递应教收到协议：%s", text)
	}
	if n := strings.Count(text, ackNoteMark); n != 1 {
		t.Fatalf("收到纪律一轮只教一遍，实 %d", n)
	}

	m := d.lookup("乙")
	old := time.Now().Add(-2 * time.Hour).Unix()
	d.enqueueAt(m, "陈线一条", []int64{11}, []int64{11}, old, nil)
	d.deliver("乙", "头线", []int64{20}, []int64{20})
	t2 := gb.sendTexts("s-kick-b")[0]
	if !strings.Contains(t2, "整轮只回一次") || !strings.Contains(t2, "分节作答") {
		t.Fatalf("并车多节应教一轮一收到、分节作答：%s", t2)
	}
}

// TestLaneStatsRecordsDeliveries — L6：送达记等待（头线＋折线各一
// 条）、入队记深度、投递记并车条数；快照可查。
func TestLaneStatsRecordsDeliveries(t *testing.T) {
	_, d, _ := startCfgRoom(t, nil)
	m := d.lookup("甲")
	old := time.Now().Add(-2 * time.Hour).Unix()
	d.enqueueAt(m, "陈线一", []int64{11}, []int64{11}, old, nil)
	d.enqueueAt(m, "陈线二", []int64{12}, []int64{12}, old, nil)
	d.deliver("甲", "头", []int64{20}, []int64{20})

	st := d.LaneStats()
	if len(st.FoldedPerDelivery) == 0 || st.FoldedPerDelivery[len(st.FoldedPerDelivery)-1] != 2 {
		t.Fatalf("并车条数应记 2：%v", st.FoldedPerDelivery)
	}
	if len(st.WaitSeconds) < 3 {
		t.Fatalf("等待应记头线＋两条折线共 3 条：%v", st.WaitSeconds)
	}
	if len(st.QueueDepth) < 2 {
		t.Fatalf("入队深度应有记录：%v", st.QueueDepth)
	}
	if st.OverflowDropped != 0 {
		t.Fatalf("无溢出应记 0，实 %d", st.OverflowDropped)
	}
}

// TestAckFooterSessionLatch — L5 会话闩：协议出生已教，投递面头 3 轮
// 带 footer 立规矩，此后不带（指令噪音不逐轮重复），每 25 轮复习一
// 遍；并车多节不受闩约束（那是本轮回复结构的操作指引）。
func TestAckFooterSessionLatch(t *testing.T) {
	_, d, gb := startCfgRoom(t, nil)
	for i := 1; i <= 26; i++ {
		d.deliver("甲", fmt.Sprintf("第%d条点名", i), []int64{int64(i)}, []int64{int64(i)})
	}
	texts := gb.sendTexts("s-kick-a")
	if len(texts) != 26 {
		t.Fatalf("应 26 次投递，实 %d", len(texts))
	}
	have := func(i int) bool { return strings.Contains(texts[i], ackNoteMark) }
	for _, i := range []int{0, 1, 2} {
		if !have(i) {
			t.Fatalf("第 %d 轮应带 footer（头 3 轮立规矩）", i+1)
		}
	}
	for i := 3; i < 24; i++ {
		if have(i) {
			t.Fatalf("第 %d 轮不该带 footer（会话闩内）", i+1)
		}
	}
	if !have(24) {
		t.Fatal("第 25 轮应复习 footer")
	}
	if have(25) {
		t.Fatal("第 26 轮不该带 footer")
	}
}

// TestImageCapPerDelivery — P3 图片单轮预算：一次投递至多 3 张，超出
// 诚实注记「未随本轮送达」；解析面只出现留下的前 3 张（nil 媒体库下
// 的不可用注记不点名被裁的图）。
func TestImageCapPerDelivery(t *testing.T) {
	_, d, gb := startCfgRoom(t, nil)
	imgs := make([]wire.Image, 5)
	for i := range imgs {
		imgs[i] = wire.Image{ID: fmt.Sprintf("img-%d", i), Name: fmt.Sprintf("图%d", i+1)}
	}
	d.deliverImg("甲", "带五张图的点名", []int64{7}, []int64{7}, imgs)
	text := gb.sendTexts("s-kick-a")[0]
	if !strings.Contains(text, "共随附 5 张图片") || !strings.Contains(text, "其余 2 张未随本轮送达") {
		t.Fatalf("图片预算注记缺失：%s", text)
	}
	if !strings.Contains(text, "图1") || !strings.Contains(text, "图3") {
		t.Fatal("留下的前 3 张应在解析面")
	}
	if strings.Contains(text, "图4") || strings.Contains(text, "图5") {
		t.Fatalf("被裁的图不该出现在解析面：%s", text)
	}
}
