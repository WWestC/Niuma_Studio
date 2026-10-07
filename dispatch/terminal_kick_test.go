package dispatch

// 回合终局的排队线续投不得跑在调用者栈上（读循环 goroutine 的
// Hooks 契约，zcode/client.go）：onEvent/onState 由 app-server 读循环
// 同步驱动，终点若同步 deliverQuoted→bridge.Send 等 ack，ack 恰恰要
// 靠这条被占住的读循环才能读到——自锁烧完整个注入等待窗。房主实录
// 的事故形状：反向请求连读都读不到、子进程 1s→10s 退避重宣告攒 13
// 条、积压事件末了一秒冲刷（「一秒 17 轮模型」的假象）、注入 ack
// 「结果未知」弃单。两处终局路径（onEvent 尾部、onState 失败分支）
// 都必须走 laneKick 纪律——闸门把注入挂在半空，断言终点照样立刻
// 返回、注入在别的 goroutine 上等待放行。

import (
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/capability"
)

// parkLineWhileRunning 把一条点名线停进活回合成员的停车道（等价于
// 忙时入队：成员在回合中，线进车道等终点弹出）。
func parkLineWhileRunning(t *testing.T, d *Dispatcher, name string) {
	t.Helper()
	m := d.lookup(name)
	if m == nil {
		t.Fatalf("%s 不在调度中", name)
	}
	d.mu.Lock()
	m.status = StatusRunning
	m.watch.runningSince = time.Now()
	d.mu.Unlock()
	d.enqueueAt(m, "【办公室消息｜来自 房主】终局排队的一条点名", []int64{9}, []int64{9}, time.Now().Unix(), nil)
}

// terminalReturnsWhileSendHeld 是两条终局路径共用的主断言：注入被
// 闸门挂死时，终点必须立刻返回给它的调用者（真身是 app-server 读
// 循环——多等一秒，应答/事件/反向请求就多停摆一秒）。
func terminalReturnsWhileSendHeld(t *testing.T, fire func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fire()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("回合终点在调用者栈上同步等注入 ack——读循环会被冻住：应答/事件/反向请求全部停摆（Hooks 契约违例）")
	}
}

// 正常终点（stopReason 信封）：onEvent 返回不等注入；排队线随后在
// 自己的 goroutine 上等待放行，开闸即送达。
func TestTurnTerminalDrainsOffCallerStack(t *testing.T) {
	_, d, gb := startKickRoom(t)
	release := gb.hold("s-kick-a")
	parkLineWhileRunning(t, d, "甲")

	terminalReturnsWhileSendHeld(t, func() {
		d.onEvent(turnCompletedEvent("s-kick-a", "上一轮忙完了"))
	})
	waitFor(t, func() bool { return d.memberDraining("甲") }, "终局的排队线应已在自己的 goroutine 上等注入")
	if n := gb.sendCount("s-kick-a"); n != 0 {
		t.Fatalf("闸未开竟完成了注入: %d", n)
	}

	release()
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") == 1 }, "开闸后排队线应送达")
}

// 失败终点（provider 端 prompt_failed，onState 的失败分支——这类
// 回合没有 stopReason 信封）：同纪律。
func TestFailureTerminalDrainsOffCallerStack(t *testing.T) {
	_, d, gb := startKickRoom(t)
	release := gb.hold("s-kick-a")
	parkLineWhileRunning(t, d, "甲")

	terminalReturnsWhileSendHeld(t, func() {
		d.onState("s-kick-a", "", "prompt_failed: provider 529")
	})
	waitFor(t, func() bool { return d.memberDraining("甲") }, "失败终局的排队线应已在自己的 goroutine 上等注入")

	release()
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") == 1 }, "开闸后排队线应送达")
}

// 终局的续投意图不得被一记正在收尾的 kick 吞掉：终点的 laneKick 撞上
// draining（kick 的 Send 已归、退出中）时把意图记在 kickAfter 账上，
// 收尾的 kick 替它补脚——否则下一条线滞留车道，等一个永远不会来的
// 外来触发（满载实测的 img 桩偶发即此形状）。
func TestTerminalKickIntentSurvivesWindDown(t *testing.T) {
	hub, d, gb := startKickRoom(t)
	release := gb.hold("s-kick-a")
	m := d.lookup("甲")

	speaker, _ := hub.Join("房主", "", false)
	hub.Say(speaker, "@甲 第一条")
	waitFor(t, func() bool { return d.memberDraining("甲") }, "首条的 kick 应在飞")
	hub.Say(speaker, "@甲 第二条")
	waitFor(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(m.lanes.queue) == 1
	}, "在飞期间第二条应停车")

	// 首条回合的终局到达：laneKick 撞 draining——意图必须记账
	d.onEvent(turnCompletedEvent("s-kick-a", "第一条完成"))
	d.mu.Lock()
	ka := m.lanes.kickAfter
	d.mu.Unlock()
	if !ka {
		t.Fatal("终局续投撞上收尾中的 kick 时未记账 kickAfter——意图会随竞态蒸发")
	}

	// 放行：首条注入归账、kick 收尾，补脚把第二条送出路——不需要
	// 任何再来的外来触发。（首断言用 ≥1：补脚紧贴归账，0→1→2 在
	// 微秒内完成，==1 是抓不到的瞬态。）
	release()
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") >= 1 }, "开闸后首条应送达")
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") == 2 }, "收尾补脚应把第二条送出车道")
}

// 终局落定的延迟改档同样不得冻读循环：flushModel→SetModel 与 Send
// 同一条读循环 ack 契约（30s 窗的同族事故）；且切换必须先于排队线的
// 注入落地——Assemble 延迟口径的承诺是「下一回合就跑新档」。
func TestTerminalModelSwitchHeldOffCallerStack(t *testing.T) {
	_, d, gb := startKickRoom(t)
	m := d.lookup("甲")
	d.mu.Lock()
	m.status = StatusRunning
	m.fab.wantModel = &capability.ModelTier{ID: "prov/m-tier"} // 显式 ref：不走 ProviderResolve，测试不依赖本机配置
	d.mu.Unlock()
	parkLineWhileRunning(t, d, "甲")

	relModel := gb.holdModel("s-kick-a")
	relSend := gb.hold("s-kick-a")
	terminalReturnsWhileSendHeld(t, func() {
		d.onEvent(turnCompletedEvent("s-kick-a", "上一轮忙完了"))
	})
	waitFor(t, func() bool { return gb.modelWaiting() >= 1 },
		"延迟改档应在终局自己的 goroutine 上等 setModel ack")

	relSend() // 注入的闸先开：切换未落定，注入不得先行
	if n := gb.sendCount("s-kick-a"); n != 0 {
		t.Fatalf("SetModel 未落竟已注入（%d）——切换必须先于下一回合的注入", n)
	}
	relModel()
	waitFor(t, func() bool { return gb.modelDone() >= 1 }, "开闸后 setModel 应完成")
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") == 1 }, "切换落定后排队线应送达")
	d.mu.Lock()
	applied := m.fab.appliedModel
	d.mu.Unlock()
	if applied != "prov/m-tier" {
		t.Fatalf("终局应落定新档（got %q）", applied)
	}
}
