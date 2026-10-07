package server

// 引用回复的 WS 全链路回归：房主写面发出的 say 带 quote 字段（结构化
// 引用，chat.Quote），广播帧必须原样带回——此前线上曾出现「客户端字段
// 齐全、服务端历史落账却无 quote」的事故面，这条测试钉住 handleWS 的
// say 分支真的路由进 SayQuoted（而不是静默回落普通 Say 把引用吞掉）。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestSayQuoteRidesBroadcastOverWS(t *testing.T) {
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, LocalName: "房主"})
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
		Type: chat.MsgHello, Name: "小马", Role: "人事", Replay: &replay}); err != nil {
		t.Fatalf("hello: %v", err)
	}

	q := &chat.Quote{Seq: 42, From: "小牛", At: true, Snip: "方案 B 定稿"}
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgSay, Text: "B", Quote: q}); err != nil {
		t.Fatalf("say: %v", err)
	}

	// 逐帧读直到 say 广播：welcome/join 等先行帧按需跳过
	var got chat.Message
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := wsjson.Read(ctx, conn, &got); err != nil {
			t.Fatalf("read: %v", err)
		}
		if got.Type != chat.MsgSay {
			continue
		}
		if got.Quote == nil {
			t.Fatalf("say 广播丢了 quote 字段：%+v", got)
		}
		if got.Quote.From != "小牛" || !got.Quote.At || got.Quote.Seq != 42 || got.Quote.Snip != "方案 B 定稿" {
			t.Fatalf("quote 字段走样：%+v", got.Quote)
		}
		if len(got.Mentions) == 0 || got.Mentions[0] != "小牛" {
			t.Fatalf("引用即点名：被引人应进 mentions：%v", got.Mentions)
		}
		return
	}
	t.Fatal("3 秒内没等到 say 广播")
}
