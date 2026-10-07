package server

// owner-M1 的门面测试：无座房主通道上一切带署名的动词（say 镜像/普通、
// 私信、收到、表情）逐帧验凭——凭名字发言已被拒绝；管理帧不设门（回环
// 信任根不变，mirror_kb_test 的管理用例即回归）。/owner/token 读面：
// 回环给凭、非回环 404 不确认存在性、无 registry 404。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"

	"github.com/coder/websocket/wsjson"
)

// ownerGateFixture spins the lobby with a live owner seat, returns the
// server, the hub and the credential.
func ownerGateFixture(t *testing.T) (*Server, *chat.Hub, string) {
	t.Helper()
	lobby := chat.NewHub()
	seat, _ := lobby.Join("房主", "boss", false)
	lobby.SetOwnerSeat(seat)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	tok, live := lobby.SeatHolderToken("房主")
	if !live || tok == "" {
		t.Fatal("夹具房主座位应带凭证")
	}
	return s, lobby, tok
}

func timedCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func TestOwnerSpeechGate(t *testing.T) {
	s, hub, _ := ownerGateFixture(t)

	t.Run("无凭镜像 say 拒绝且不进史", func(t *testing.T) {
		before := len(hub.History())
		conn := dialOwnerSeatless(t, s)
		ctx, cancel := timedCtx()
		defer cancel()
		if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgSay, Text: "假的", Origin: chat.OriginMirror}); err != nil {
			t.Fatalf("write: %v", err)
		}
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.Type != chat.MsgSystem || !strings.Contains(m.Text, "凭证") {
			t.Fatalf("应回凭证拒绝提示，got %+v", m)
		}
		if got := len(hub.History()); got != before {
			t.Fatalf("无凭发言不得进史：before=%d after=%d", before, got)
		}
	})

	t.Run("错凭普通 say 同拒", func(t *testing.T) {
		conn := dialOwnerSeatlessHello(t, s, true, "wrong-token")
		ctx, cancel := timedCtx()
		defer cancel()
		if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgSay, Text: "也是假的"}); err != nil {
			t.Fatalf("write: %v", err)
		}
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.Type != chat.MsgSystem || !strings.Contains(m.Text, "凭证") {
			t.Fatalf("应回凭证拒绝提示，got %+v", m)
		}
	})

	t.Run("对凭镜像 say 落史带 mirror 标记", func(t *testing.T) {
		conn := dialOwnerSeatlessCred(t, s, hub)
		ctx, cancel := timedCtx()
		defer cancel()
		if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgSay, Text: "真房主", Origin: chat.OriginMirror}); err != nil {
			t.Fatalf("write: %v", err)
		}
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.Type != chat.MsgSay || m.From != "房主" || m.Origin != chat.OriginMirror {
			t.Fatalf("对凭发言应回显带 mirror 标记，got %+v", m)
		}
		found := false
		for _, h := range hub.History() {
			if h.Seq == m.Seq && h.From == "房主" && h.Origin == chat.OriginMirror {
				found = true
			}
		}
		if !found {
			t.Fatal("对凭发言应进历史")
		}
	})

	t.Run("错凭收到回执同拒", func(t *testing.T) {
		conn := dialOwnerSeatless(t, s)
		ctx, cancel := timedCtx()
		defer cancel()
		if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgAck, Seq: 1}); err != nil {
			t.Fatalf("write: %v", err)
		}
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.Type != chat.MsgSystem || !strings.Contains(m.Text, "凭证") {
			t.Fatalf("应回凭证拒绝提示，got %+v", m)
		}
	})

	t.Run("管理帧无凭仍通行", func(t *testing.T) {
		// notice_save（撤下，silent）画最便宜的收据（不依赖引擎）：
		// 回执帧而非凭证拒绝——门的边界就在署名动词上。
		conn := dialOwnerSeatless(t, s)
		ctx, cancel := timedCtx()
		defer cancel()
		if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgNoticeSave, Silent: true}); err != nil {
			t.Fatalf("write: %v", err)
		}
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.Type == chat.MsgSystem && strings.Contains(m.Text, "凭证") {
			t.Fatalf("管理帧不应被凭证门拦下，got %+v", m)
		}
	})
}

func TestOwnerTokenEndpoint(t *testing.T) {
	s, _, _ := ownerGateFixture(t)

	t.Run("回环给凭", func(t *testing.T) {
		// 带 registry 的服务：OwnerCredential 才有值可发
		projStore, err := projects.Open("")
		if err != nil {
			t.Fatalf("projects.Open: %v", err)
		}
		registry := chat.NewRegistry(projStore, "", "房主", "房主")
		if registry.OwnerCredential() == "" {
			t.Fatal("registry 应铸造房主凭证")
		}
		sr, err := Start(chat.NewHub(), Options{Endpoint: EndpointConfig{PreferredPort: -1}, LocalName: "房主", Registry: registry})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		t.Cleanup(sr.Close)
		req := httptest.NewRequest(http.MethodGet, "/owner/token", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		w := httptest.NewRecorder()
		sr.authFace().HandleOwnerToken(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("回环应 200，got %d", w.Code)
		}
		var out struct {
			Token string `json:"token"`
			Name  string `json:"name"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("json: %v", err)
		}
		if out.Token != registry.OwnerCredential() || out.Name != "房主" {
			t.Fatalf("凭证/名字不符，got %+v", out)
		}
	})

	t.Run("非回环 404 不确认存在性", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/owner/token", nil)
		req.RemoteAddr = "203.0.113.9:443"
		w := httptest.NewRecorder()
		s.authFace().HandleOwnerToken(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("非回环应 404，got %d", w.Code)
		}
	})

	t.Run("无 registry 404", func(t *testing.T) {
		bare := chat.NewHub()
		seat, _ := bare.Join("房主", "boss", false)
		bare.SetOwnerSeat(seat)
		s2, err := Start(bare, Options{Endpoint: EndpointConfig{PreferredPort: -1}, LocalName: "房主"})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		t.Cleanup(s2.Close)
		req := httptest.NewRequest(http.MethodGet, "/owner/token", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		w := httptest.NewRecorder()
		s2.authFace().HandleOwnerToken(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("无 registry 应 404，got %d", w.Code)
		}
	})
}
