package dispatch

// 车道 kick 的行为面（房主实录 14:21 解卡）：
//   - 一条挂死的注入（冷会话的 Send 既不 ack 也不报错）只冻住该成员
//     自己的车道——同一条消息里对其他成员的点名照常送达，泵不被拖住；
//   - kick 在飞时后续点名线停车排队（FIFO，不与在飞的 Send 竞争），
//     回合终点弹线照旧一帧一条；
//   - 看护拍补扫「非 Running 却压着停车线」的僵尸车道（回合无声死
//     掉、永远等不到终点弹线的线），一脚 kick 让它重新上路；
//   - Stop 收拢在飞的 kick：注入 ctx 感知停机，立刻撤销而不是等完
//     整个注入等待窗，未送达的线回停车道随车道持久到下一轮生命。

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// gateBridge 在 errBridge 之上把 Send 做成按会话可闸的门：进了门先
// 记账（完成账——过闸后才记），闸不开就挂到 ctx 撤销。测试用它扮演
// 「冷会话的挂死注入」。
type gateBridge struct {
	errBridge
	mu           sync.Mutex
	gates        map[string]chan struct{}
	sends        []string // 过闸完成的注入（sid|content）——完成序即送达序
	modelGates   map[string]chan struct{}
	modelsWaited int    // 已进门等闸/过闸的 setModel 次数（进门即计——在飞可等）
	modelsDone   int    // 过闸完成的 setModel 次数
	compacts     int    // 折叠动词到账次数——compact_test 的记录面
	compactText  string // 最近一次折叠的 instructions
}

func newGateBridge() *gateBridge {
	return &gateBridge{gates: map[string]chan struct{}{}, modelGates: map[string]chan struct{}{}}
}

// hold 给 sid 装一道关闭的闸，返回开闸器。
func (b *gateBridge) hold(sid string) func() {
	gate := make(chan struct{})
	b.mu.Lock()
	b.gates[sid] = gate
	b.mu.Unlock()
	return func() { close(gate) }
}

func (b *gateBridge) sendCount(sid string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, s := range b.sends {
		if strings.HasPrefix(s, sid+"|") {
			n++
		}
	}
	return n
}

func (b *gateBridge) sendTexts(sid string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, s := range b.sends {
		if strings.HasPrefix(s, sid+"|") {
			out = append(out, strings.TrimPrefix(s, sid+"|"))
		}
	}
	return out
}

func (b *gateBridge) Send(ctx context.Context, sid, content, inputID string, deny []string) (*zcode.SendAck, error) {
	b.mu.Lock()
	gate := b.gates[sid]
	b.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	b.mu.Lock()
	b.sends = append(b.sends, sid+"|"+content)
	b.mu.Unlock()
	return &zcode.SendAck{}, nil
}

// holdModel 给 sid 的 setModel 装一道关闭的闸（Send 闸的同款），返回
// 开闸器——终局延迟改档的测试用它把 SetModel 挂在半空。
func (b *gateBridge) holdModel(sid string) func() {
	gate := make(chan struct{})
	b.mu.Lock()
	b.modelGates[sid] = gate
	b.mu.Unlock()
	return func() { close(gate) }
}

func (b *gateBridge) modelWaiting() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.modelsWaited
}

func (b *gateBridge) modelDone() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.modelsDone
}

func (b *gateBridge) SetModel(ctx context.Context, sessionID string, sel zcode.ModelSelection) error {
	b.mu.Lock()
	gate := b.modelGates[sessionID]
	b.modelsWaited++
	b.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.mu.Lock()
	b.modelsDone++
	b.mu.Unlock()
	return nil
}

// memberDraining — 测试读「该成员的车道 kick 是否在飞」（draining
// 的持锁读面；生产代码只写不读）。
func (d *Dispatcher) memberDraining(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.members[name]
	return m != nil && m.lanes.draining
}

// startKickRoom 起一间带两位在编成员（甲 s-kick-a、乙 s-kick-b）的
// 房间，走真 Start——onSay 的泵接线、wg、停机面全真。
func startKickRoom(t *testing.T) (*chat.Hub, *Dispatcher, *gateBridge) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	gb := newGateBridge()
	d := Start(hub, store, gb, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("甲", "工程师", "s-kick-a"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("乙", "工程师", "s-kick-b"); err != nil {
		t.Fatal(err)
	}
	return hub, d, gb
}

// TestHungSendDoesNotFreezeRouting — 本修的主断言：甲的注入挂死期
// 间，同一条消息对乙的点名照常送达（房主实录：一条挂死注入曾把全
// 房间 @路由冻到注入等待超时）。
func TestHungSendDoesNotFreezeRouting(t *testing.T) {
	hub, d, gb := startKickRoom(t)
	release := gb.hold("s-kick-a")

	speaker, _ := hub.Join("房主", "", false)
	if _, ok := hub.Say(speaker, "@甲 @乙 都看下这条"); !ok {
		t.Fatal("say 未入史")
	}
	// 甲的 Send 还挂着：乙的必须已经送达
	waitFor(t, func() bool { return gb.sendCount("s-kick-b") == 1 }, "挂死甲的同时乙应照常送达")
	if n := gb.sendCount("s-kick-a"); n != 0 {
		t.Fatalf("闸未开甲竟完成了注入: %d", n)
	}

	release()
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") == 1 }, "开闸后甲应送达")
	d.mu.Lock()
	st := d.members["甲"].status
	d.mu.Unlock()
	if st != StatusRunning {
		t.Fatalf("甲送达后应 Running: %s", st)
	}
}

// TestLaneKickKeepsFIFO — kick 在飞时后续点名线停车不抢发；开闸完
// 成首条后，回合终点弹出的第二条仍是后到的线（车道 FIFO）。
func TestLaneKickKeepsFIFO(t *testing.T) {
	hub, d, gb := startKickRoom(t)
	release := gb.hold("s-kick-a")

	speaker, _ := hub.Join("房主", "", false)
	hub.Say(speaker, "@甲 第一条")
	waitFor(t, func() bool { return d.memberDraining("甲") }, "首条的 kick 应在飞")
	hub.Say(speaker, "@甲 第二条")
	// 第二条必须停车（不与在飞的 Send 并发抢同一会话）
	waitFor(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.members["甲"].lanes.queue) == 1
	}, "在飞期间第二条应停车")
	if got := gb.sendCount("s-kick-a"); got > 0 {
		t.Fatalf("闸未开竟有完成注入: %d", got)
	}

	release()
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") == 1 }, "开闸后首条应送达")
	// 首条回合终点弹第二条——内容顺序不倒
	d.onEvent(turnCompletedEvent("s-kick-a", "第一条收到。"))
	waitFor(t, func() bool { return gb.sendCount("s-kick-a") == 2 }, "终点应弹出第二条")
	texts := gb.sendTexts("s-kick-a")
	if !strings.Contains(texts[0], "第一条") || !strings.Contains(texts[1], "第二条") {
		t.Fatalf("车道 FIFO 被倒置: %v", texts)
	}
}

// TestWatchdogSweepsStalledLane — 僵尸车道（成员非 Running 却压着停
// 车线——停进车道所赖的那个回合已无声死掉）：看护拍补一脚 kick，
// 线重新上路。
func TestWatchdogSweepsStalledLane(t *testing.T) {
	_, d, gb := startKickRoom(t)
	m := d.lookup("乙")
	d.enqueueAt(m, "【办公室消息｜来自 房主】僵尸车道里的一条点名", []int64{9}, []int64{9}, time.Now().Unix(), nil)
	d.mu.Lock()
	idle := m.status != StatusRunning
	d.mu.Unlock()
	if !idle {
		t.Fatal("前提走样：乙不应在回合中")
	}
	d.WatchdogNow()
	waitFor(t, func() bool { return gb.sendCount("s-kick-b") == 1 }, "看护拍应把停滞车道的线送出去")
}

// TestStopCancelsInFlightSend — 停机收拢：挂死中的注入被停机立刻撤
// 销（不等注入等待窗），未送达的线回停车道持久化。
func TestStopCancelsInFlightSend(t *testing.T) {
	hub, d, gb := startKickRoom(t)
	gb.hold("s-kick-a") // 永不开闸：只有停机能解

	speaker, _ := hub.Join("房主", "", false)
	hub.Say(speaker, "@甲 停机前最后一令")
	waitFor(t, func() bool { return d.memberDraining("甲") }, "kick 应在飞")

	done := make(chan struct{})
	go func() { d.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop 被挂死的注入拖过 5s——停机解卡没接上")
	}
	waitFor(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.members["甲"].lanes.queue) == 1
	}, "停机撤销的线应回停车道持久")
}
