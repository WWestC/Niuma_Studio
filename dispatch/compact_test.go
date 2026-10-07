package dispatch

// compact_test.go — P0 会话折叠的行为面：
//   - 阈值链：NIUMA_COMPACT_CTX 生效、缺省关、低于下限按关读；
//   - 越线的空闲成员折一次（instructions 带保留纪律），冷却 1 小时内
//     不重折；Running 成员不折；
//   - 折叠回合的终点被吞：摘要不进房、成员翻回 Idle。

import (
	"context"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/zcode"
)

// Compact 让 gateBridge 说折叠动词（compactorBridge 的可选面）。
func (b *gateBridge) Compact(ctx context.Context, sessionID, instructions string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.compacts++
	b.compactText = instructions
	return nil
}

func (b *gateBridge) compactCount() (int, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.compacts, b.compactText
}

func memberStatus(d *Dispatcher, name string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if m := d.members[name]; m != nil {
		return m.status
	}
	return ""
}

func TestCompactOffByDefaultAndFloor(t *testing.T) {
	// 无旋钮无环境变量：仪表再大也不折
	_, d, gb := startCfgRoom(t, func(c *Config) {
		c.CtxGauge = func(ids []string) (map[string]zcode.SessionGauge, error) {
			return map[string]zcode.SessionGauge{"s-kick-a": {CtxEstimate: 9_000_000}}, nil
		}
	})
	d.CompactNow()
	if n, _ := gb.compactCount(); n != 0 {
		t.Fatalf("缺省关着不该折叠（got %d）", n)
	}
	// 低于下限（5 万）的环境值按未设置读
	t.Setenv("NIUMA_COMPACT_CTX", "1000")
	d.CompactNow()
	if n, _ := gb.compactCount(); n != 0 {
		t.Fatalf("低于下限的阈值不该点亮折叠（got %d）", n)
	}
}

func TestCompactFoldsIdleMemberOverLine(t *testing.T) {
	t.Setenv("NIUMA_COMPACT_CTX", "600000")
	_, d, gb := startCfgRoom(t, func(c *Config) {
		c.CtxGauge = func(ids []string) (map[string]zcode.SessionGauge, error) {
			return map[string]zcode.SessionGauge{
				"s-kick-a": {CtxEstimate: 800_000, Turns: 42},
				"s-kick-b": {CtxEstimate: 599_999, Turns: 3},
			}, nil
		}
	})
	d.CompactNow()
	n, instr := gb.compactCount()
	if n != 1 {
		t.Fatalf("恰甲越线应折一次（got %d）", n)
	}
	if !containsAll(instr, "在办任务", "关键", "整理完成后") {
		t.Fatalf("折叠指令应带保留纪律：%q", instr)
	}
	if s := memberStatus(d, "甲"); s != StatusRunning {
		t.Fatalf("折叠回合应把成员翻 Running（got %s）", s)
	}
	if s := memberStatus(d, "乙"); s != StatusIdle {
		t.Fatalf("未越线的乙不该被动（got %s）", s)
	}
	// 冷却 1 小时：同拍再巡不重折
	d.CompactNow()
	if n, _ = gb.compactCount(); n != 1 {
		t.Fatalf("冷却内不该重折（got %d）", n)
	}
}

func TestCompactRunningMemberSkipped(t *testing.T) {
	t.Setenv("NIUMA_COMPACT_CTX", "600000")
	_, d, gb := startCfgRoom(t, func(c *Config) {
		c.CtxGauge = func(ids []string) (map[string]zcode.SessionGauge, error) {
			return map[string]zcode.SessionGauge{"s-kick-a": {CtxEstimate: 800_000}}, nil
		}
	})
	d.mu.Lock()
	if m := d.members["甲"]; m != nil {
		m.status = StatusRunning
	}
	d.mu.Unlock()
	d.CompactNow()
	if n, _ := gb.compactCount(); n != 0 {
		t.Fatalf("Running 成员不该折（got %d）", n)
	}
}

func TestCompactTerminalSwallowed(t *testing.T) {
	t.Setenv("NIUMA_COMPACT_CTX", "600000")
	hub, d, gb := startCfgRoom(t, func(c *Config) {
		c.CtxGauge = func(ids []string) (map[string]zcode.SessionGauge, error) {
			return map[string]zcode.SessionGauge{"s-kick-a": {CtxEstimate: 800_000}}, nil
		}
	})
	d.CompactNow()
	if n, _ := gb.compactCount(); n != 1 {
		t.Fatalf("应折一次（got %d）", n)
	}
	// 折叠回合的终点：摘要不镜像进房、成员翻回 Idle
	d.onEvent(turnCompletedEvent("s-kick-a", "整理后的摘要：当前在办 t_05……"))
	for _, m := range hub.History() {
		if m.From == "甲" && (len(m.Text) > 0) {
			t.Fatalf("折叠摘要不该进房：%+v", m)
		}
	}
	if s := memberStatus(d, "甲"); s != StatusIdle {
		t.Fatalf("折叠终点应翻回 Idle（got %s）", s)
	}
	d.mu.Lock()
	folding := d.members["甲"].watch.compacting
	d.mu.Unlock()
	if folding {
		t.Fatal("终点应清掉 compacting 标记")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
