package server

// 观察者心跳必须以应用层数据帧喂前端看门狗：浏览器对 JS 隐藏协议级
// ping/pong，工作台（web/wire/conn.js）的 60s 入站看门狗只认数据帧——
// 修复前安静房间每分钟被前端自己掐断一次，每掐一次聊天流里就落一条
// 「断线补窗」分隔线。帧走 SendTo（writer pump 单写者纪律），所以同时
// 断言三件事：帧到达观察者、不带 seq（补窗游标契约）、不广播给在座
// 成员（SendTo 单收件人）。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestObserverHeartbeatFeedsWatchdog(t *testing.T) {
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false) // the live GUI seat
	tap := newServerTap(lobby)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1, Heartbeat: 80 * time.Millisecond}, LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	replay := false
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgHello, Observer: true, Replay: &replay}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	var w chat.Message
	if err := wsjson.Read(ctx, conn, &w); err != nil {
		t.Fatalf("welcome: %v", err)
	}
	if w.Type != chat.MsgWelcome {
		t.Fatalf("观察者首帧应为 welcome，got %+v", w)
	}

	// 房间全程零动静，两个心跳数据帧仍须到达——这是看门狗的口粮
	var got chat.Message
	pings := 0
	for pings < 2 {
		if err := wsjson.Read(ctx, conn, &got); err != nil {
			t.Fatalf("read: %v", err)
		}
		if got.Type != chat.MsgPing {
			continue
		}
		pings++
		if got.Seq != 0 {
			t.Fatalf("ping 帧不得携带 seq（会污染补窗游标）：%+v", got)
		}
	}

	// SendTo 单收件人：心跳只喂观察者，在座成员一条都不见
	time.Sleep(120 * time.Millisecond)
	if n := tap.count(chat.MsgPing, ""); n != 0 {
		t.Fatalf("心跳帧不得广播给在座成员，tap 收到 %d 条", n)
	}
}
