package server

// 座位门（seat door）：同名在座且凭据不匹配的裸 hello 必须在门口被拒，
// 而不是静默顶成「-2」分身——小鸟-2 事故的形态：成员会话为了跑一次
// agent_save 手写裸 WS 程序以自己的名字拨号，而调度器正持着它的正座，
// 加入广播＋改名广播＋10 分钟宽限幽灵就都落进了房间。CLI 各面（say/
// listen/wait/task）早已在拨号前自拒，调度器走 JoinFree 的拒绝——裸拨
// 是最后一条漏网路。门的纪律：同 token 仍走顶替（成员自己的一次性写）、
// 空闲/宽限座位仍按名收回、显式 force（CLI --force-seat）保留文档化的
// 分身路径、无 token 在座座位维持旧例（两次 guest 拨号仍有 guest-2）。

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// dialSeatDoor dials one hello and returns the handshake's first frame.
func dialSeatDoor(t *testing.T, url string, hello chat.Message) chat.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	if err := wsjson.Write(ctx, conn, hello); err != nil {
		t.Fatalf("hello: %v", err)
	}
	var w chat.Message
	if err := wsjson.Read(ctx, conn, &w); err != nil {
		t.Fatalf("handshake read: %v", err)
	}
	return w
}

func TestSeatDoorRefusesMismatchedTwinDial(t *testing.T) {
	lobby := chat.NewHub()
	holder, holderMember := lobby.JoinWithToken("小鸟", "人事", false, "tok-A")
	if holderMember.Name != "小鸟" {
		t.Fatalf("holder seat: %+v", holderMember)
	}
	t.Cleanup(func() { lobby.Leave(holder) })
	tap := newServerTap(lobby)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port)
	replay := false

	// 裸拨（无 token）与错 token 拨：system 拒绝帧，房间无分身痕迹
	for name, token := range map[string]string{"bare": "", "wrong-token": "tok-B"} {
		w := dialSeatDoor(t, url, chat.Message{
			Type: chat.MsgHello, Name: "小鸟", Role: "人事", Replay: &replay, Token: token})
		if w.Type != chat.MsgSystem || !strings.Contains(w.Text, "已拒绝拨号") {
			t.Fatalf("%s 拨号应被座位门拒绝，got %+v", name, w)
		}
	}
	if lobby.HasLiveMember("小鸟-2") {
		t.Fatalf("被拒拨号不得留下 -2 分身座位")
	}
	if n := tap.count(chat.MsgRename, "小鸟-2"); n != 0 {
		t.Fatalf("被拒拨号不得广播改名（分身痕迹）：%d 条", n)
	}

	// 同 token：顶替不动名字（成员自己的一次性写通道）
	w := dialSeatDoor(t, url, chat.Message{
		Type: chat.MsgHello, Name: "小鸟", Role: "人事", Replay: &replay, Token: "tok-A"})
	if w.Type != chat.MsgWelcome || w.You == nil || w.You.Name != "小鸟" {
		t.Fatalf("同 token 拨号应顶替原座，got %+v", w)
	}

	// 显式 force：文档化分身路径保留（CLI --force-seat）
	w = dialSeatDoor(t, url, chat.Message{
		Type: chat.MsgHello, Name: "小鸟", Role: "人事", Replay: &replay, Force: true})
	if w.Type != chat.MsgWelcome || w.You == nil || w.You.Name != "小鸟-2" {
		t.Fatalf("force 拨号应落文档化分身 小鸟-2，got %+v", w)
	}
}

func TestSeatDoorLeavesUnrelatedJoinsAlone(t *testing.T) {
	lobby := chat.NewHub()
	holder, _ := lobby.JoinWithToken("小鸟", "人事", false, "tok-A")
	t.Cleanup(func() { lobby.Leave(holder) })
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	replay := false
	w := dialSeatDoor(t, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), chat.Message{
		Type: chat.MsgHello, Name: "小简", Role: "写手", Replay: &replay})
	if w.Type != chat.MsgWelcome || w.You == nil || w.You.Name != "小简" {
		t.Fatalf("无关名字的正常入座不受门影响，got %+v", w)
	}
}

// 小苗-2 事故形态：成员迁房后旧房快照留下的宽限幽灵（被暂停房的
// 冻结钟撑成永生），成员的 CLI 带着新房 token 拨旧房——门必须拒绝
// （带指引的拒绝帧，而不是顶成 -2 分身）；免凭据拨入仍按名收回。
func TestSeatDoorRefusesGhostMismatchDial(t *testing.T) {
	lobby := chat.NewHub()
	// 旧房幽灵：token 是迁房前的 e5d0。
	lobby.RestoreSeats([]chat.SeatSnapshot{{Name: "小苗", Role: "排期编排", Token: "e5d0-old"}})
	tap := newServerTap(lobby)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port)
	replay := false

	// 带着新房 token（8d06）拨旧房：拒绝帧点名宽限占用，无分身痕迹。
	w := dialSeatDoor(t, url, chat.Message{
		Type: chat.MsgHello, Name: "小苗", Role: "排期编排", Replay: &replay, Token: "8d06-new"})
	if w.Type != chat.MsgSystem || !strings.Contains(w.Text, "宽限占用") || !strings.Contains(w.Text, "已拒绝拨号") {
		t.Fatalf("错 token 拨幽灵应被座位门拒绝（点名宽限），got %+v", w)
	}
	if lobby.HasLiveMember("小苗-2") || lobby.HasLiveMember("小苗") {
		t.Fatalf("被拒拨号不得留下座位（含 -2 分身）")
	}
	if n := tap.count(chat.MsgRename, "小苗-2"); n != 0 {
		t.Fatalf("被拒拨号不得广播改名（分身痕迹）：%d 条", n)
	}

	// 幽灵自己的 token：收回放行。
	w = dialSeatDoor(t, url, chat.Message{
		Type: chat.MsgHello, Name: "小苗", Role: "排期编排", Replay: &replay, Token: "e5d0-old"})
	if w.Type != chat.MsgWelcome || w.You == nil || w.You.Name != "小苗" {
		t.Fatalf("幽灵自己的 token 拨入应收回原座，got %+v", w)
	}
}
