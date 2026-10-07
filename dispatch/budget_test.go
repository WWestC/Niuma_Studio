package dispatch

// 背景车道回看预算（injectBudget）的钉子：纯函数 clipBackground 的
// 对齐与算术（最旧让位、最新保住、read cargo 跟行不走），以及端到端
// 一发——超预算的房内闲聊在注入面上留计数与房史指针、让位计数投递
// 后清零（InjectCap 截断与预算裁剪都记账，不再静默丢）。

import (
	"fmt"
	"strings"
	"testing"
)

func TestClipBackground(t *testing.T) {
	// 装得下：原样返回，一条不动
	k, d := clipBackground([]injectEntry{{text: "a", read: 1}, {text: "b", read: 2}})
	if d != 0 || len(k) != 2 || k[0].text != "a" || k[1].read != 2 {
		t.Fatalf("预算内应原样: kept=%v dropped=%d", k, d)
	}
	// 超预算：最旧让位、read cargo 跟行走（被让位的行没送达，不带走它的 seq）
	big := strings.Repeat("字", injectBudget/3+10) // 三条必超
	k, d = clipBackground([]injectEntry{{text: big, read: 10}, {text: big, read: 11}, {text: big, read: 12}})
	if d != 1 {
		t.Fatalf("应让位最旧 1 条, dropped=%d", d)
	}
	if len(k) != 2 || k[0].read != 11 || k[1].read != 12 {
		t.Fatalf("read cargo 应与留下的行对齐: kept=%d", len(k))
	}
	// 单条就超预算：至少保最新一条，块不空
	huge := strings.Repeat("字", injectBudget*2)
	k, d = clipBackground([]injectEntry{{text: huge, read: 5}})
	if d != 0 || len(k) != 1 || k[0].read != 5 {
		t.Fatalf("末条保底被破: kept=%d dropped=%d", len(k), d)
	}
	// 空车道：无操作
	if k, d = clipBackground(nil); d != 0 || len(k) != 0 {
		t.Fatalf("空车道应原样: %v %d", k, d)
	}
}

func TestBackgroundBudgetLeavesPointer(t *testing.T) {
	hub, d, gb := startKickRoom(t)
	speaker, _ := hub.Join("房主", "", false)
	// 12 条长背景（每条约 700 rune）：InjectCap 截到 8（让位 4 记账），
	// 8×700=5600 又超预算 → 预算再裁最旧 3 条（5600→4900→4200→3500 止）。
	// 送达的应是最新的 7..11 五条，注记计 7 条（4 截断＋3 让位）。
	for i := 0; i < 12; i++ {
		if _, ok := hub.Say(speaker, fmt.Sprintf("背景线%02d %s", i, strings.Repeat("字", 690))); !ok {
			t.Fatalf("背景线 %d 未入史", i)
		}
	}
	// 12 条经泵异步落位：等到「截到 8 条且记满 4 条让位」再点名（只等
	// 长度会在第 8 条入道、第 9 条未到时抢跑——计数还没涨）。
	waitFor(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		m := d.members["甲"]
		return len(m.lanes.inject) == InjectCap && m.lanes.injDropped == 4
	}, "12 条背景应截到 8 条且记 4 条让位")

	hub.Say(speaker, "@甲 开工")
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") >= 1 }, "点名应送达")
	text := gb.sendTexts("s-kick-a")[0]
	for _, want := range []string{
		"7 条背景超本轮回看预算未附",
		"/p/default/history",
		"背景线11", // 最新保住
		"背景线07", // 预算裁剪后最旧的存活线
	} {
		if !strings.Contains(text, want) {
			t.Errorf("注入缺 %q:\n%s", want, text[:min(400, len(text))])
		}
	}
	for _, gone := range []string{"背景线04", "背景线05", "背景线06"} {
		if strings.Contains(text, gone) {
			t.Errorf("被让位的 %q 不应仍在注入中", gone)
		}
	}
	// 计数投递即清零（下轮若无让位则无注记）
	d.mu.Lock()
	left := d.members["甲"].lanes.injDropped
	d.mu.Unlock()
	if left != 0 {
		t.Fatalf("让位计数应在投递后清零, got %d", left)
	}
}
