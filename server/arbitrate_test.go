package server

// task_arbitrate — the host's bypass verdict on a pending proposal. The
// bug this pins: the web workbench (the host's face) drew 接单/婉拒 on
// every pending card, but Confirm/Decline only accept the people in
// pending.Need — the host clicking 接单 earned a green「已发出」toast
// next to the red「你不是该变更的需确认人」denial (the double popup).
// Arbitrate is the host's protocol verb: approve applies the patch at
// once (skipping remaining confirmations), reject discards it (an
// assign proposal's rejection cancels the task). Members borrowing the
// verb are refused — the engine logs the verdict as the host.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/tasks"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// newArbitrateServer boots a server whose engine already holds the
// screenshot's shape: 小牛 (Lv.8) proposed a task onto same-rank 小马 —
// an assign proposal waiting on 小马 alone, so the host sits OUTSIDE
// Need. Returns the server and a function creating one more such task.
func newArbitrateServer(t *testing.T) (*Server, func() string) {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false) // the live GUI seat
	eng := tasks.MustOpenMemory("房主")
	eng.SetRank("小牛", 8)
	eng.SetRank("小马", 8)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{Engine: eng}, LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, func() string {
		out := eng.Create("", "小牛", "招聘美术设计师", "按 r_02 与任务1结论定画像", "小马")
		if out.Denied || out.Task.Pending == nil || out.Task.Pending.Kind != tasks.ProposalAssign {
			t.Fatalf("前置提案未成立: %+v", out)
		}
		return out.Task.ID
	}
}

func readUntil(t *testing.T, conn *websocket.Conn, pred func(chat.Message) bool, what string) chat.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("read %s: %v", what, err)
		}
		if pred(m) {
			return m
		}
	}
}

func TestTaskArbitrateSeatlessApprove(t *testing.T) {
	s, mk := newArbitrateServer(t)
	id := mk()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialOwnerSeatless(t, s)

	// The bug's exact line first: the host's confirm on someone else's
	// Need list must deny — arbitration is the way out, not a workaround.
	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgTaskConfirm, TaskID: id}); err != nil {
		t.Fatalf("send task_confirm: %v", err)
	}
	got := readUntil(t, conn, func(m chat.Message) bool { return m.Type == chat.MsgTask }, "confirm receipt")
	if got.Event != "denied" || !strings.Contains(got.Text, "你不是该变更的需确认人") {
		t.Fatalf("房主 confirm 应被拒且点名需确认人，got %+v", got)
	}

	approve := true
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgTaskArbitrate, TaskID: id, Approve: &approve}); err != nil {
		t.Fatalf("send task_arbitrate: %v", err)
	}
	got = readUntil(t, conn, func(m chat.Message) bool { return m.Type == chat.MsgTask }, "arbitrate receipt")
	if got.Event != "confirmed" || got.Task == nil || got.Task.Pending != nil || got.Task.Assignee != "小马" {
		t.Fatalf("仲裁批准应立即生效（pending 清空、指派落小马），got %+v", got)
	}
	if !strings.Contains(got.Text, "房主仲裁") || got.TaskID != id {
		t.Fatalf("仲裁回执应带 id 与房主仲裁字样，got %+v", got)
	}
}

func TestTaskArbitrateSeatlessRejectCancelsAssign(t *testing.T) {
	s, mk := newArbitrateServer(t)
	id := mk()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialOwnerSeatless(t, s)

	reject := false
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgTaskArbitrate, TaskID: id, Approve: &reject}); err != nil {
		t.Fatalf("send task_arbitrate: %v", err)
	}
	got := readUntil(t, conn, func(m chat.Message) bool { return m.Type == chat.MsgTask }, "arbitrate receipt")
	if got.Event != "declined" || got.Task == nil || got.Task.Status != tasks.StatusCancelled {
		t.Fatalf("驳回 assign 提案应作废并取消任务，got %+v", got)
	}
	if !strings.Contains(got.Text, "任务已取消") {
		t.Fatalf("驳回回执应说明任务取消，got %+v", got)
	}
}

func TestTaskArbitrateMemberRefused(t *testing.T) {
	s, mk := newArbitrateServer(t)
	id := mk()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A seated member borrowing the arbitration verb: private denial —
	// the engine would log the verdict as the host, so lending it out
	// is impersonation, not delegation.
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	replay := false
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgHello, Name: "小马", Replay: &replay}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	approve := true
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgTaskArbitrate, TaskID: id, Approve: &approve}); err != nil {
		t.Fatalf("send task_arbitrate: %v", err)
	}
	got := readUntil(t, conn, func(m chat.Message) bool {
		return m.Type == chat.MsgTask && m.Event == "denied"
	}, "member arbitrate denial")
	if !strings.Contains(got.Text, "仲裁仅限房主") || got.TaskID != id {
		t.Fatalf("成员仲裁应私拒且带 id，got %+v", got)
	}

	// The proposal itself must survive the refused borrow untouched.
	if task, ok := s.opts.Stores.Engine.Get(id); !ok || task.Pending == nil {
		t.Fatalf("被拒的仲裁不应动提案，got %+v", task)
	}
}
