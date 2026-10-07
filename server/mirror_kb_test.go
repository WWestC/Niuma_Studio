package server

// The seatless owner write face must carry the CLI's kb trio complete:
// kb_write / kb_append already ride it (t_39); kb_restore joins them so
// the host's one-shot CLI works while the GUI seat is live — the router
// sends every owner-name dial here precisely then. Restore keeps the
// seated path's permission rule (kbRestoreAs) and receipt shape: a
// denial carries the reason, a success the new rev.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// dialOwnerSeatless opens the owner's second connection while the GUI
// seat is live — the router must send it to the seatless write face
// (the welcome's You stays the owner's own member, never a "-2" dedup).
// Management frames are name-acted (loopback trust root), so the bare
// dial still serves them; speech frames need the credential — use
// dialOwnerSeatlessCred for those (owner-M1).
func dialOwnerSeatless(t *testing.T, s *Server) *websocket.Conn {
	return dialOwnerSeatlessHello(t, s, false)
}

// dialOwnerSeatlessCred is dialOwnerSeatless presenting the live owner
// seat's credential — the shape the GUI OwnerChannel and the mirror CLI
// speak (speech frames pass the frame gate).
func dialOwnerSeatlessCred(t *testing.T, s *Server, hub *chat.Hub) *websocket.Conn {
	tok, live := hub.SeatHolderToken("房主")
	if !live || tok == "" {
		t.Fatal("房主座位未在座或无凭证——测试夹具应先 Join+SetOwnerSeat")
	}
	conn := dialOwnerSeatlessHello(t, s, true, tok)
	return conn
}

func dialOwnerSeatlessHello(t *testing.T, s *Server, withToken bool, token ...string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	replay := false
	hello := chat.Message{Type: chat.MsgHello, Name: "房主", Replay: &replay}
	if withToken && len(token) > 0 {
		hello.Token = token[0]
	}
	if err := wsjson.Write(ctx, conn, hello); err != nil {
		t.Fatalf("hello: %v", err)
	}
	var w chat.Message
	if err := wsjson.Read(ctx, conn, &w); err != nil {
		t.Fatalf("welcome: %v", err)
	}
	if w.Type != chat.MsgWelcome || w.You == nil || w.You.Name != "房主" {
		t.Fatalf("房主二次拨号应走无座写通道并回显本人，got %+v", w)
	}
	return conn
}

func TestMirrorKbRestore(t *testing.T) {
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false) // the live GUI seat
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{Docs: docs}, LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	if _, err := docs.Write("ops/establishment", "编制表", "rev-one", "seed"); err != nil {
		t.Fatalf("seed rev1: %v", err)
	}
	if _, err := docs.Append("ops/establishment", "rev-two", "seed"); err != nil {
		t.Fatalf("seed rev2: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialOwnerSeatless(t, s)

	// restore rev 1 over the seatless face: the receipt carries
	// "restored" and rev 3; the doc reads back as rev-one's body.
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgKbRestore, Key: "ops/establishment", Rev: 1}); err != nil {
		t.Fatalf("send kb_restore: %v", err)
	}
	var got chat.Message
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if got.Type != chat.MsgKb || got.From != "房主" || got.Event != "restored" ||
		got.KbDoc == nil || got.KbDoc.Rev != 3 {
		t.Fatalf("kb_restore 回执应为 restored/rev3，got %+v", got)
	}
	doc, err := docs.Get("ops/establishment", 0)
	if err != nil || doc.Body != "rev-one" {
		t.Fatalf("恢复后正文应为 rev-one，got %q err %v", doc.Body, err)
	}

	// an unknown revision denies with the reason — never a fake echo.
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgKbRestore, Key: "ops/establishment", Rev: 99}); err != nil {
		t.Fatalf("send kb_restore(99): %v", err)
	}
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("receipt(99): %v", err)
	}
	if got.Event != "denied" || !strings.Contains(got.Text, "未知版本") {
		t.Fatalf("未知版本应 denied 且说明缘由，got %+v", got)
	}
}
