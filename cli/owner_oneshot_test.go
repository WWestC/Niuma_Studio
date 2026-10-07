package cli

// dialTask's anti-twin probe must never block the room's owner: while
// the GUI seat is live the server routes the owner's same-name dial to
// the seatless owner write face (no "-2" twin, no top-seat), so the
// probe's refusal only locked the host out of their own one-shot CLI —
// the 「房主改不动自己的编制表」lockout. A held MANAGED member keeps
// the refusal: identity binds to the exact name there.

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/server"

	"github.com/coder/websocket/wsjson"
)

// startOwnerStudio boots one server whose owner holds a live GUI-style
// seat (an in-process join), plus a held managed-style member. The seat
// credentials point at a scratch dir — the developer machine's live
// studio may hold REAL tokens under $HOME, and loadSeatToken picking
// one up would skip the very probe under test.
func startOwnerStudio(t *testing.T) (*server.Server, *kb.DocsStore, string) {
	t.Helper()
	restore := chat.OverrideSeatTokenHome(t.TempDir())
	t.Cleanup(restore)
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	lobby.Join("小马", "人事", false)
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	s, err := server.Start(lobby, server.Options{Endpoint: server.EndpointConfig{
		PreferredPort: -1,
	},
		Stores: server.StoreSet{
			Docs: docs,
		},
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, docs, "ws://127.0.0.1:" + strconv.Itoa(s.Port) + "/ws"
}

func TestDialTaskOwnerBypassesHeldSeatProbe(t *testing.T) {
	_, _, url := startOwnerStudio(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// the owner dials their own held name: through the seatless face,
	// never the probe's refusal.
	conn, me, err := dialTask(ctx, url, "房主", "kb append")
	if err != nil {
		t.Fatalf("房主一次性写入应放行，got %v", err)
	}
	if me.Name != "房主" {
		t.Fatalf("房主应保名（无座通道回显本人），got %q", me.Name)
	}
	_ = wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgBye})
	_ = conn.CloseNow()

	// a held managed member keeps the refusal — no dial, no side effects.
	if _, _, err := dialTask(ctx, url, "小马", "task update"); err == nil ||
		!strings.Contains(err.Error(), "已有在座连接") {
		t.Fatalf("受管成员在座仍应拒绝，got %v", err)
	}
}

func TestOwnerKbAppendWhileGUISeatLive(t *testing.T) {
	s, docs, _ := startOwnerStudio(t)
	if _, err := docs.Write("ops/room-log", "办公室日志", "# 办公室日志", "seed"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rc := runKbAppend([]string{"ops/room-log", "房主亲笔一行",
		"--name", "房主", "--port", strconv.Itoa(s.Port)})
	if rc != 0 {
		t.Fatalf("runKbAppend rc = %d, want 0（房主 GUI 在座也应写通）", rc)
	}
	doc, err := docs.Get("ops/room-log", 0)
	if err != nil || !strings.Contains(doc.Body, "房主亲笔一行") || doc.UpdatedBy != "房主" {
		t.Fatalf("追加应落盘且作者为房主，got by=%q err=%v body=%q", doc.UpdatedBy, err, doc.Body)
	}
}
