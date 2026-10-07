package dispatch

// 收到回执的调度器半边（飞书式收到，chat/acks.go 拥有语义）：点名唤
// 醒的回合若整轮只回「收到」（尾标点除外），转成原消息下的回执、不
// 镜像回房——FYI 的 @ 不再回声成新的 @，点名循环从根上断掉；有话
// 要说的回合、以及没有房间货运的回合（公告/巡检类注入）照常广播。

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// ackBridge 记录注入文本，永不失败——本组只考回合终端的转换规则。
// 账本带锁：车道 kick 把 Send 搬上了自己的 goroutine（laneKick），
// waitFor 的读与 kick 的写跨协程（wakeBridge 的既有纪律）。
type ackBridge struct {
	baseBridge
	mu    sync.Mutex
	sends []string
}

func (f *ackBridge) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

func (f *ackBridge) allSends() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sends...)
}

func (f *ackBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	f.sends = append(f.sends, content)
	f.mu.Unlock()
	return nil, nil
}

// turnCompletedEvent 造一个合法的回合真终点（类型化 turn.completed
// 通知：每回合恰一次、payload.response 带最终全文——镜像只认它）。
func turnCompletedEvent(sessionID, content string) zcode.Event {
	return zcode.Event{SessionID: sessionID, Type: "turn.completed",
		Payload: map[string]any{
			"response": content, "resultType": "success",
		}}
}

// turnStoppedEvent 造一条请求边界帧（stopReason=="stop"——回合内每
// 次模型请求流式收尾都会发一条，不是回合终点；复读事故的元凶）。
func turnStoppedEvent(sessionID, content string) zcode.Event {
	return zcode.Event{SessionID: sessionID, Payload: map[string]any{
		"querySource": "main_turn", "stopReason": "stop", "content": content,
	}}
}

func TestBareAckReplyBecomesReceiptNotSay(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	ab := &ackBridge{}
	d := Start(hub, store, ab, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-ack-1"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := hub.Join("李四", "", false)
	msg, ok := hub.Say(speaker, "@张三 知会一下：明天十点发布")
	if !ok {
		t.Fatal("say 未入史")
	}
	waitFor(t, func() bool { return ab.sendCount() > 0 }, "点名注入应发生")

	// 整轮只回「收到」（带尾标点也算）：转回执，不镜像
	d.onEvent(turnCompletedEvent("s-ack-1", "收到。"))
	waitFor(t, func() bool {
		got := ackersOfHub(hub, msg.Seq)
		return len(got) == 1 && got[0] == "张三"
	}, "纯「收到」应记为原消息的回执")
	for _, m := range hub.History() {
		if m.From == "张三" {
			t.Fatalf("「收到」被镜像成了聊天消息: %+v", m)
		}
	}
}

func TestRealReplyStillMirrors(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	ab := &ackBridge{}
	d := Start(hub, store, ab, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-ack-2"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := hub.Join("李四", "", false)
	if _, ok := hub.Say(speaker, "@张三 发布前把检查单跑一遍"); !ok {
		t.Fatal("say 未入史")
	}
	waitFor(t, func() bool { return ab.sendCount() > 0 }, "点名注入应发生")

	// 有实质内容的回复照常镜像（「收到，我马上跑」不是回执）
	d.onEvent(turnCompletedEvent("s-ack-2", "收到，我马上跑检查单"))
	waitFor(t, func() bool {
		for _, m := range hub.History() {
			if m.From == "张三" && m.Origin == chat.OriginBridge {
				return true
			}
		}
		return false
	}, "实质回复应镜像回房")
}

func TestAckWithoutCargoStillSpeaks(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	ab := &ackBridge{}
	d := Start(hub, store, ab, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-ack-3"); err != nil {
		t.Fatal(err)
	}

	// 无房间货运的注入（公告/巡检类）：没有可挂回执的原消息，纯「收到」
	// 不能凭空消失——照常广播，保住可见的确认
	d.deliver("张三", "【公告｜来自 房主】内容", nil, nil)
	waitFor(t, func() bool { return ab.sendCount() > 0 }, "注入应发生")
	d.onEvent(turnCompletedEvent("s-ack-3", "收到"))
	waitFor(t, func() bool {
		for _, m := range hub.History() {
			if m.From == "张三" && m.Text == "收到" {
				return true
			}
		}
		return false
	}, "无货运回合的「收到」应照常广播")
}

// ackersOfHub snapshots one seq's acker set (chat 包同名助手的本包副本)。
func ackersOfHub(h *chat.Hub, seq int64) []string {
	_, rows := h.AckReceipts()
	for _, row := range rows {
		if row.Seq == seq {
			return row.Ackers
		}
	}
	return nil
}

// isAckReply 的形态宽限表：尾标点与空白不破约；多一个字就不算。
func TestIsAckReplyForms(t *testing.T) {
	for _, s := range []string{"收到", "收到。", " 收到！ ", "收到～", "收到；"} {
		if !isAckReply(s) {
			t.Fatalf("%q 应视为纯收到", s)
		}
	}
	for _, s := range []string{"收到，我马上跑", "已收到", "收到收到收到点名循环", ""} {
		if isAckReply(s) {
			t.Fatalf("%q 不应视为纯收到", s)
		}
	}
}

// 复读根治（房主实录 2026-10-03 book 房）：一个回合内每个模型请求的
// 边界帧（stopReason=="stop"）都发一条，模型在工具调用后常把待发的
// 结束语**原样重说**——小苗一回合落了三条同义回复、两条逐字相同。
// 镜像只认 turn.completed：边界帧发几条、内容多雷同，房里只落一条。
func TestBoundaryFramesNeverEcho(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	ab := &ackBridge{}
	d := Start(hub, store, ab, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-dup-1"); err != nil {
		t.Fatal(err)
	}
	d.deliver("张三", "【公告｜来自 房主】核验一下病句修正", nil, nil)
	waitFor(t, func() bool { return ab.sendCount() > 0 }, "注入应发生")

	d.onEvent(turnStoppedEvent("s-dup-1", "【核验通过】三处文件零残留。"))
	d.onEvent(turnStoppedEvent("s-dup-1", "【核验通过】三处文件零残留。"))
	d.onEvent(turnCompletedEvent("s-dup-1", "【核验通过】三处文件零残留。"))
	waitFor(t, func() bool {
		n := 0
		for _, m := range hub.History() {
			if m.From == "张三" {
				n++
			}
		}
		return n == 1
	}, "三条事件（两条边界帧＋一条真终点）应只镜像一条回复")
}

// 真终点的 response 是权威文本：边界帧暂存的口信只在 response 缺形
// 时兜底；新回合开工（turn.started）即作废上一回合的残留口信。
func TestCompletedResponseWinsAndStashExpires(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	ab := &ackBridge{}
	d := Start(hub, store, ab, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-dup-2"); err != nil {
		t.Fatal(err)
	}
	d.deliver("张三", "【公告｜来自 房主】内容甲", nil, nil)
	waitFor(t, func() bool { return ab.sendCount() > 0 }, "注入应发生")

	d.onEvent(turnStoppedEvent("s-dup-2", "初版收尾"))
	d.onEvent(turnCompletedEvent("s-dup-2", "终版收尾"))
	waitFor(t, func() bool {
		for _, m := range hub.History() {
			if m.From == "张三" && m.Text == "终版收尾" {
				return true
			}
		}
		return false
	}, "response 全文应胜过边界帧的暂存口信")
	for _, m := range hub.History() {
		if m.From == "张三" && m.Text == "初版收尾" {
			t.Fatalf("边界帧口信不该进房: %+v", m)
		}
	}

	// 新回合：turn.started 作废残留；response 缺形的真终点不得把上
	// 一回合的口信漏进来。
	before := len(hub.History())
	d.onEvent(zcode.Event{SessionID: "s-dup-2", Type: "turn.started"})
	d.onEvent(zcode.Event{SessionID: "s-dup-2", Type: "turn.completed",
		Payload: map[string]any{"resultType": "success"}})
	for _, m := range hub.History()[before:] {
		if m.From == "张三" {
			t.Fatalf("残留口信漏进了新回合: %+v", m)
		}
	}
}

// 失败终点（error 等非 stop/tool-calls 的 stopReason）照旧当场落房。
func TestFailureEnvelopeStillSpeaks(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	ab := &ackBridge{}
	d := Start(hub, store, ab, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-dup-3"); err != nil {
		t.Fatal(err)
	}
	d.deliver("张三", "【公告｜来自 房主】内容", nil, nil)
	waitFor(t, func() bool { return ab.sendCount() > 0 }, "注入应发生")

	d.onEvent(zcode.Event{SessionID: "s-dup-3", Payload: map[string]any{
		"querySource": "main_turn", "stopReason": "error", "content": ""}})
	waitFor(t, func() bool {
		for _, m := range hub.History() {
			if m.From == "张三" && strings.Contains(m.Text, "本轮异常中止") {
				return true
			}
		}
		return false
	}, "失败终点应照旧报异常中止")
}
