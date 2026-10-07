package dispatch

// deliver 的超时纪律（重放风暴的根因封口）：session/send 的 ctx 超时
// （现值 5 分钟）是「结果未知」——请求已写进 child 的 stdin，回合可能
// 在跑。此时入队重试会让同一条指令在下一轮重放（每轮超时一弹一进，
// 队列永不排空，成员对着旧指令无限复读）。只有协议拒绝码（-32010
// busy）与传输失败才是确定未送达，照旧走持久重试车道。

import (
	"context"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// errBridge lets each Send fail with a scripted error (nil = ack).
type errBridge struct {
	baseBridge
	errs []error // consumed one per Send; exhausted = nil
	sent int
}

func (b *errBridge) Send(ctx context.Context, sid, content, inputID string, deny []string) (*zcode.SendAck, error) {
	defer func() { b.sent++ }()
	if b.sent < len(b.errs) {
		return nil, b.errs[b.sent]
	}
	return &zcode.SendAck{}, nil
}

// newDeliverFixture wires a one-member dispatcher over a fake bridge.
func newDeliverFixture(t *testing.T, errs ...error) (*Dispatcher, *member) {
	t.Helper()
	h := chat.NewHub()
	d := &Dispatcher{
		hub: h, cfg: Config{InboxDir: t.TempDir()},
		projectKey: "default", members: map[string]*member{},
		bridge: &errBridge{errs: errs},
	}
	seat, _ := h.Join("小H", "人事", false)
	m := &member{name: "小H", role: "人事", sessionID: "s_1", seat: seat, status: StatusIdle}
	d.members["小H"] = m
	return d, m
}

// TestDeliverTimeoutDoesNotRequeue: 超时（结果未知）弃单不重试；busy
// （确定未送达）照旧入队——同一条分支表的两半各自守住。
func TestDeliverTimeoutDoesNotRequeue(t *testing.T) {
	d, m := newDeliverFixture(t, context.DeadlineExceeded)
	d.deliver("小H", "【办公室消息】去招牛马", []int64{7}, []int64{7})
	if len(m.lanes.queue) != 0 {
		t.Fatalf("超时（结果未知）竟入了重试队列: %v", m.lanes.queue)
	}

	d2, m2 := newDeliverFixture(t, &zcode.Error{Code: zcode.ErrPromptRunning, Message: "busy"})
	d2.deliver("小H", "【办公室消息】去招牛马", []int64{8}, []int64{8})
	if len(m2.lanes.queue) != 1 || len(m2.lanes.queue[0].read) != 1 || m2.lanes.queue[0].read[0] != 8 {
		t.Fatalf("busy 拒绝应走持久重试车道: queue=%+v", m2.lanes.queue)
	}
}

// TestDeliverTimeoutAfterReactivateDoesNotRequeue: 冷会话梯子
// （-32004 → resume → 重发）末端超时同样弃单——第一条 Send 被协议拒绝
// 确定未送达，重发那笔超时才是结果未知。
func TestDeliverTimeoutAfterReactivateDoesNotRequeue(t *testing.T) {
	d, m := newDeliverFixture(t,
		&zcode.Error{Code: zcode.ErrSessionNotActive, Message: "cold"},
		context.DeadlineExceeded,
	)
	d.deliver("小H", "【办公室消息】去招牛马", nil, nil)
	if len(m.lanes.queue) != 0 {
		t.Fatalf("重发超时（结果未知）竟入了重试队列: %v", m.lanes.queue)
	}
}
