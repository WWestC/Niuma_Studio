package dispatch

// asks_test.go — 向房主提问的调度侧（v2.5）：AskUserQuestion 的反向
// 请求要变成房里的选项卡（结构化问题如实入账、成员名下落提问行），
// 房主的 Answer 要当场把悬着的调用以 accept(answers) 收口；空答案
// 不消费、陌生 qid 拒绝、二次应答拒绝；超时路径 decline 且卡片过期。

import (
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
)

// revBridge 在 ackBridge 之上捕获调度器装上的反向处理器——ackBridge
// 把 SetReverse 丢了，本组要直接驱动它。
type revBridge struct {
	ackBridge
	reverse func(method string, params map[string]any) (any, error)
}

func (f *revBridge) SetReverse(fn func(method string, params map[string]any) (any, error)) {
	f.reverse = fn
}

func startAskDispatcher(t *testing.T) (*Dispatcher, *chat.Hub, *revBridge) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	rb := &revBridge{}
	d := Start(hub, store, rb, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("小牛", agents.OrchestratorRole, "s-q-1"); err != nil {
		t.Fatal(err)
	}
	return d, hub, rb
}

func askParams() map[string]any {
	return map[string]any{
		"sessionId": "s-q-1",
		"prompt":    "标签视觉方向",
		"questions": []any{map[string]any{
			"question":    "主打哪种风格？",
			"header":      "风格",
			"multiSelect": false,
			"options": []any{
				map[string]any{"label": "像素复古", "description": "8-bit 办公室"},
				map[string]any{"label": "终端极客", "description": "命令行绿字"},
			},
		}},
	}
}

func TestRequestUserInputBecomesCardAndAnswerResolves(t *testing.T) {
	d, hub, rb := startAskDispatcher(t)
	old := askWindow
	askWindow = 5 * time.Second
	t.Cleanup(func() { askWindow = old })

	type outcome struct {
		v   any
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		v, err := rb.reverse("interaction/requestUserInput", askParams())
		done <- outcome{v, err}
	}()
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 1 }, "问题卡应已登记进开放集")

	q := hub.OpenQuestions()[0]
	if q.From != "小牛" || q.Prompt != "标签视觉方向" {
		t.Fatalf("卡片丢身份：%+v", q)
	}
	if len(q.Asks) != 1 || len(q.Asks[0].Options) != 2 ||
		q.Asks[0].Options[0].Label != "像素复古" || q.Asks[0].Options[0].Desc != "8-bit 办公室" {
		t.Fatalf("结构化问题未如实入账：%+v", q.Asks)
	}
	if q.Due <= q.TS {
		t.Fatalf("due 应晚于 ts（窗口签名）：ts=%d due=%d", q.TS, q.Due)
	}

	// 成员名下的提问行（长期留痕）要随卡落地
	waitFor(t, func() bool {
		for _, m := range hub.History() {
			if m.From == "小牛" && strings.Contains(m.Text, "向房主提问") &&
				strings.Contains(m.Text, "像素复古 / 终端极客") {
				return true
			}
		}
		return false
	}, "提问 say 行应随卡落进历史")

	// 陌生 qid 拒绝；全空答案不消费（卡片继续可点）
	if d.Answer("q_nope", "房主", map[string]string{"主打哪种风格？": "像素复古"}) {
		t.Fatal("陌生 qid 应拒绝")
	}
	if d.Answer(q.ID, "房主", map[string]string{"主打哪种风格？": "   "}) {
		t.Fatal("空答案不应消费提问")
	}
	if len(hub.OpenQuestions()) != 1 {
		t.Fatal("被拒的应答不应收口卡片")
	}

	// 正确应答：调用以 accept + answers 当场返回，开放集清空
	if !d.Answer(q.ID, "房主", map[string]string{"主打哪种风格？": "终端极客"}) {
		t.Fatal("合法应答应成功")
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("应答路径不应出错：%v", r.err)
		}
		m, ok := r.v.(map[string]any)
		if !ok || m["action"] != "accept" {
			t.Fatalf("应答后应 accept，得到 %#v", r.v)
		}
		content, _ := m["content"].(map[string]any)
		answers, _ := content["answers"].(map[string]string)
		if answers["主打哪种风格？"] != "终端极客" {
			t.Fatalf("answers 应按键回传所选：%#v", content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("应答后悬着的调用应立即返回")
	}
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 0 }, "收口后开放集应清空")
	if d.Answer(q.ID, "房主", map[string]string{"主打哪种风格？": "像素复古"}) {
		t.Fatal("二次应答应拒绝（先到先得）")
	}
}

func TestRequestUserInputTimesOut(t *testing.T) {
	_, hub, rb := startAskDispatcher(t)
	old := askWindow
	askWindow = 40 * time.Millisecond
	t.Cleanup(func() { askWindow = old })

	res, err := rb.reverse("interaction/requestUserInput", askParams()) // 无人应答，同步等超时
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.(map[string]any)
	if !ok || m["action"] != "decline" {
		t.Fatalf("超时应 decline，得到 %#v", res)
	}
	if reason, _ := m["reason"].(string); !strings.Contains(reason, "超时") {
		t.Fatalf("拒绝理由应说明超时：%q", reason)
	}
	if got := hub.OpenQuestions(); len(got) != 0 {
		t.Fatalf("超时后开放集应清空，仍有 %d", len(got))
	}
}

// askLineCount 统计成员名下含这段提问行指纹的历史行数——重播
// 去重的「一行话」断言靠它。
func askLineCount(hub *chat.Hub) int {
	n := 0
	for _, m := range hub.History() {
		if m.From == "小牛" && strings.Contains(m.Text, "像素复古 / 终端极客") {
			n++
		}
	}
	return n
}

// TestRequestUserInputReannounceKeepsOneCard：ZCode ≥3.14 的
// app-server 会把未应答的提问以新请求 id 重播（1s→10s 退避）直到
// 有一个 id 被应答。重播不是新问题——一张卡、一行提问行；房主
// 一次应答要让所有泊着的调用（原请求＋重播）以同一份 accept 收口。
func TestRequestUserInputReannounceKeepsOneCard(t *testing.T) {
	d, hub, rb := startAskDispatcher(t)
	old := askWindow
	askWindow = 10 * time.Second
	t.Cleanup(func() { askWindow = old })

	type outcome struct {
		v   any
		err error
	}
	orig := make(chan outcome, 1)
	go func() {
		v, err := rb.reverse("interaction/requestUserInput", askParams())
		orig <- outcome{v, err}
	}()
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 1 }, "原提问应登记一张卡")

	// 重播：同参数、同会话、新请求 id（线上是 server-N 自增，这里
	// 直驱同参数的第二次调用即可）
	reen := make(chan outcome, 1)
	go func() {
		v, err := rb.reverse("interaction/requestUserInput", askParams())
		reen <- outcome{v, err}
	}()
	time.Sleep(100 * time.Millisecond) // 让重播跑完它的静默登记路径
	if got := hub.OpenQuestions(); len(got) != 1 {
		t.Fatalf("重播不应再落卡，开放集却有 %d 张", len(got))
	}
	if n := askLineCount(hub); n != 1 {
		t.Fatalf("重播不应再说提问行，历史里已有 %d 行", n)
	}

	// 一次应答：原请求与重播都要以 accept + 同一份 answers 收口
	q := hub.OpenQuestions()[0]
	if !d.Answer(q.ID, "房主", map[string]string{"主打哪种风格？": "终端极客"}) {
		t.Fatal("合法应答应成功")
	}
	for name, ch := range map[string]chan outcome{"原请求": orig, "重播": reen} {
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("%s 路径不应出错：%v", name, r.err)
			}
			m, ok := r.v.(map[string]any)
			if !ok || m["action"] != "accept" {
				t.Fatalf("%s 应同样 accept，得到 %#v", name, r.v)
			}
			content, _ := m["content"].(map[string]any)
			answers, _ := content["answers"].(map[string]string)
			if answers["主打哪种风格？"] != "终端极客" {
				t.Fatalf("%s 的 answers 应回传所选：%#v", name, content)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s 在应答后应立即收口", name)
		}
	}
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 0 }, "收口后开放集应清空")
}

// TestRequestUserInputReannounceExpiryDeclinesBoth：无人应答直至
// 窗口耗尽——原请求与重播都要以同一理由 decline；且过期之后同形
// 提问再来是真正的新问题（新卡），去重不许去过头。
func TestRequestUserInputReannounceExpiryDeclinesBoth(t *testing.T) {
	d, hub, rb := startAskDispatcher(t)
	old := askWindow
	askWindow = 250 * time.Millisecond
	t.Cleanup(func() { askWindow = old })

	type outcome struct {
		v   any
		err error
	}
	orig := make(chan outcome, 1)
	go func() {
		v, err := rb.reverse("interaction/requestUserInput", askParams())
		orig <- outcome{v, err}
	}()
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 1 }, "原提问应登记一张卡")

	reen := make(chan outcome, 1)
	go func() {
		v, err := rb.reverse("interaction/requestUserInput", askParams())
		reen <- outcome{v, err}
	}()
	for name, ch := range map[string]chan outcome{"原请求": orig, "重播": reen} {
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("%s 路径不应出错：%v", name, r.err)
			}
			m, ok := r.v.(map[string]any)
			if !ok || m["action"] != "decline" || !strings.Contains(m["reason"].(string), "超时") {
				t.Fatalf("%s 应同样以超时 decline，得到 %#v", name, r.v)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s 在窗口耗尽后应收口", name)
		}
	}
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 0 }, "过期后开放集应清空")

	// 过期后的同形提问是模型真切的第二次提问——必须照常落新卡
	askWindow = 10 * time.Second
	fresh := make(chan outcome, 1)
	go func() {
		v, err := rb.reverse("interaction/requestUserInput", askParams())
		fresh <- outcome{v, err}
	}()
	waitFor(t, func() bool { return len(hub.OpenQuestions()) == 1 }, "过期后的同形提问应落新卡")
	if n := askLineCount(hub); n != 2 {
		t.Fatalf("第二次真提问应再说一行（共 2 行），实际 %d 行", n)
	}
	// 收摊：把新卡答掉，别让 goroutine 悬到测试结束
	d.Answer(hub.OpenQuestions()[0].ID, "房主", map[string]string{"主打哪种风格？": "像素复古"})
	select {
	case <-fresh:
	case <-time.After(2 * time.Second):
		t.Fatal("新卡应答后调用应收口")
	}
}

// TestRequestPermissionReannounceKeepsOneLine：requestPermission 同样
// 被 ZCode ≥3.14 重播（1s→10s 退避）——泊等的 ask 策略下一次房主
// 提示一行、一个倒计时，重播者陪着等同一裁决（deny）。（System 行
// 只广播不进历史，用观察员座位收。）
func TestRequestPermissionReannounceKeepsOneLine(t *testing.T) {
	_, hub, rb := startAskDispatcher(t)
	tap := newSystemTap(hub)
	old := PermWindow
	PermWindow = 250 * time.Millisecond
	t.Cleanup(func() { PermWindow = old })

	permLine := func() int { return tap.count("请求权限：Bash") }

	type outcome struct {
		v   any
		err error
	}
	orig := make(chan outcome, 1)
	go func() {
		v, err := rb.reverse("interaction/requestPermission", map[string]any{
			"sessionId": "s-q-1", "toolName": "Bash"})
		orig <- outcome{v, err}
	}()
	waitFor(t, func() bool { return permLine() == 1 }, "原权限请求应落一行提示")

	// 同请求重播（线上是 1s 后新 id 重发，这里直驱同参数）
	reen := make(chan outcome, 1)
	go func() {
		v, err := rb.reverse("interaction/requestPermission", map[string]any{
			"sessionId": "s-q-1", "toolName": "Bash"})
		reen <- outcome{v, err}
	}()
	time.Sleep(80 * time.Millisecond)
	if n := permLine(); n != 1 {
		t.Fatalf("重播不应再落提示行，已有 %d 行", n)
	}

	for name, ch := range map[string]chan outcome{"原请求": orig, "重播": reen} {
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("%s 路径不应出错：%v", name, r.err)
			}
			m, ok := r.v.(map[string]any)
			if !ok || m["decision"] != "deny" {
				t.Fatalf("%s 应同样 deny，得到 %#v", name, r.v)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s 在窗口耗尽后应收口", name)
		}
	}

	// 窗口收口后的同形请求是真请求——重新落行
	after := make(chan outcome, 1)
	go func() {
		v, err := rb.reverse("interaction/requestPermission", map[string]any{
			"sessionId": "s-q-1", "toolName": "Bash"})
		after <- outcome{v, err}
	}()
	waitFor(t, func() bool { return permLine() == 2 }, "窗口后的新请求应再落一行")
	select {
	case <-after:
	case <-time.After(2 * time.Second):
		t.Fatal("新请求在窗口耗尽后应收口")
	}
}
