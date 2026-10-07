package server

// auth_test.go — the session layer's security pins: the four-row
// identity resolution (loopback operator / session role / service
// token / anonymous), the dial gate (multi-user refuses a bare remote
// dial; the visitor channel's own token stays exempt), the per-verb
// role floors over a live WS, and the hello protocol negotiation.
//
// Remote-source behavior is pinned through crafted RemoteAddr values
// (the resolver is the choke point; a real socket would need a second
// interface) — the loopback paths ride real dials.

import (
	"context"
	"fmt"
	"github.com/WWestC/Niuma_Studio/server/roomops"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/identity"
	"github.com/WWestC/Niuma_Studio/wire"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// memAccounts is identity.Store's in-memory twin for these pins.
type memAccounts struct {
	accounts map[string]identity.Account
	grants   map[string]map[string]bool
}

func newMemAccounts() *memAccounts {
	return &memAccounts{accounts: map[string]identity.Account{}, grants: map[string]map[string]bool{}}
}
func (m *memAccounts) Create(a identity.Account) error {
	if _, dup := m.accounts[a.Username]; dup {
		return identity.ErrAccountExists(a.Username)
	}
	m.accounts[a.Username] = a
	return nil
}
func (m *memAccounts) Get(u string) (identity.Account, bool) { a, ok := m.accounts[u]; return a, ok }
func (m *memAccounts) List() []identity.Account {
	out := []identity.Account{}
	for _, a := range m.accounts {
		out = append(out, identity.Account{Username: a.Username, Role: a.Role, DisplayName: a.DisplayName, CreatedTS: a.CreatedTS})
	}
	return out
}
func (m *memAccounts) SetRole(u string, r identity.Role) error {
	a, ok := m.accounts[u]
	if !ok {
		return identity.ErrAccountNotFound(u)
	}
	a.Role = r
	m.accounts[u] = a
	return nil
}
func (m *memAccounts) SetPassword(u string, salt, hash []byte) error { return nil }
func (m *memAccounts) Delete(u string) error                         { delete(m.accounts, u); return nil }
func (m *memAccounts) Projects(u string) []string {
	var out []string
	for k := range m.grants[u] {
		out = append(out, k)
	}
	return out
}
func (m *memAccounts) GrantProject(u, p string) error {
	if m.grants[u] == nil {
		m.grants[u] = map[string]bool{}
	}
	m.grants[u][p] = true
	return nil
}
func (m *memAccounts) RevokeProject(u, p string) error { delete(m.grants[u], p); return nil }

// armedServer boots a multi-user server over an in-memory account
// store with one admin and one member pre-seeded, returning the
// handles the assertions need.
func armedServer(t *testing.T) (*Server, *identity.Service, string, string) {
	t.Helper()
	lobby := chat.NewHub()
	store := newMemAccounts()
	svc := identity.NewService(store, time.Hour, identity.ThrottleConfig{})
	// 令牌必须从服务器持有的同一个 Service 铸（会话表是进程内的）。
	mkAcc(t, store, "root", identity.RoleAdmin, "adminpw")
	mkAcc(t, store, "alice", identity.RoleMember, "memberpw")
	adminSess, err := svc.Login("test", "root", "adminpw")
	if err != nil {
		t.Fatal(err)
	}
	memberSess, err := svc.Login("test", "alice", "memberpw")
	if err != nil {
		t.Fatal(err)
	}
	adminTok, memberTok := adminSess.Token, memberSess.Token
	_ = err
	s, err := Start(lobby, Options{
		Endpoint:      EndpointConfig{PreferredPort: -1},
		LocalName:     "root",
		Auth:          svc,
		OperatorToken: "svc-tok-0000000000000001",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, svc, adminTok, memberTok
}

func mkAcc(t *testing.T, store identity.Store, name string, role identity.Role, pw string) {
	t.Helper()
	salt, hash, err := identity.NewCredential(pw)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(identity.Account{Username: name, Role: role, Salt: salt, Hash: hash}); err != nil {
		t.Fatal(err)
	}
}

func reqAt(remote string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://studio/ws", nil)
	r.RemoteAddr = remote
	return r
}

func TestAuthResolutionMatrix(t *testing.T) {
	s, svc, adminTok, memberTok := armedServer(t)

	// 武装态确认；单用户裸 Server：nil 身份服务＝历史零摩擦。
	if !s.multiuser() {
		t.Fatal("armed server should be multi-user")
	}
	bare := &Server{}
	if bare.multiuser() {
		t.Fatal("bare server misread as multi-user")
	}

	// 匿名远端：无身份。
	if a := s.authOf(reqAt("192.0.2.9:40000")); a != nil {
		t.Fatalf("remote anonymous should resolve nil: %+v", a)
	}
	// 回环：操作者（admin）。
	if a := s.authOf(reqAt("127.0.0.1:40000")); a == nil || a.Role != identity.RoleAdmin || !a.Operator {
		t.Fatalf("loopback should resolve operator: %+v", a)
	}
	// 会话令牌：各自角色（远端来源）。
	if a := s.authOf(bearerReq(reqAt("192.0.2.9:40000"), adminTok)); a == nil || a.Role != identity.RoleAdmin || a.Username != "root" {
		t.Fatalf("admin session should resolve: %+v", a)
	}
	if a := s.authOf(bearerReq(reqAt("192.0.2.9:40000"), memberTok)); a == nil || a.Role != identity.RoleMember || a.Username != "alice" {
		t.Fatalf("member session should resolve: %+v", a)
	}
	// 服务令牌：仅回环受纳（机器凭证不在机器之外生效——文件外泄
	// 不得成为远程 root 钥匙）；远端出示解析为匿名。
	if a := s.authOf(bearerReq(reqAt("192.0.2.9:40000"), "svc-tok-0000000000000001")); a != nil {
		t.Fatalf("service token from beyond the loopback must refuse: %+v", a)
	}
	if a := s.authOf(bearerReq(reqAt("127.0.0.1:40000"), "svc-tok-0000000000000001")); a == nil || !a.Operator {
		t.Fatalf("service token on the loopback should resolve operator: %+v", a)
	}
	// 坏令牌：终局拒绝——回环也不赦免（显式出示即按裁决）。
	if a := s.authOf(bearerReq(reqAt("127.0.0.1:40000"), "garbage")); a != nil {
		t.Fatalf("bad token must refuse (even loopback): %+v", a)
	}
	if a := s.dialAuth(reqAt("192.0.2.9:40000"), "garbage"); a != nil {
		t.Fatalf("bad hello token must refuse: %+v", a)
	}
	// 过期会话：nil（Sessions 表的 TTL 语义在这些钉子里不复测，见
	// identity 包；此处钉「失效令牌解析为匿名」）。
	svc.Sessions().Revoke(adminTok)
	if a := s.authOf(bearerReq(reqAt("192.0.2.9:40000"), adminTok)); a != nil {
		t.Fatalf("revoked session must resolve nil: %+v", a)
	}
}

func bearerReq(r *http.Request, tok string) *http.Request {
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

// dialWS opens one WS to the server and speaks hello; returns the
// welcome (or the refusal frame before the close).
func dialWS(t *testing.T, s *Server, hello any) (chat.Message, *websocket.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), nil)
	if err != nil {
		return chat.Message{}, nil, err
	}
	if err := wsjson.Write(ctx, conn, hello); err != nil {
		_ = conn.CloseNow()
		return chat.Message{}, nil, err
	}
	var w chat.Message
	if err := wsjson.Read(ctx, conn, &w); err != nil {
		_ = conn.CloseNow()
		return chat.Message{}, nil, err
	}
	return w, conn, nil
}

func TestMultiUserDialGateAndVerbRoles(t *testing.T) {
	s, _, _, memberTok := armedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1) 回环裸拨号：操作者自动入座（本机零摩擦），welcome 带协商版本。
	w, conn, err := dialWS(t, s, map[string]any{"type": chat.MsgHello, "name": "root", "replay": true})
	if err != nil {
		t.Fatalf("loopback bare dial: %v", err)
	}
	if w.Type != chat.MsgWelcome || w.Proto != wire.ProtoVersion {
		t.Fatalf("welcome should carry proto: %+v", w)
	}
	_ = conn.CloseNow()

	// 2) member 会话拨号：入座；say 过（member 面），kick 私拒（admin 面）。
	w, conn, err = dialWS(t, s, map[string]any{"type": chat.MsgHello, "name": "alice", "auth": memberTok, "replay": true})
	if err != nil || w.Type != chat.MsgWelcome {
		t.Fatalf("member dial: %v %+v", err, w)
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgSay, Text: "成员发言"}); err != nil {
		t.Fatal(err)
	}
	var got chat.Message
	for {
		if err := wsjson.Read(ctx, conn, &got); err != nil {
			t.Fatalf("read: %v", err)
		}
		if got.Type == chat.MsgSay && got.From == "alice" {
			break // 广播回流：member 的对话写面在
		}
		if got.Type == chat.MsgSystem && strings.Contains(got.Text, "say") {
			t.Fatalf("say 被角色门误拒: %s", got.Text)
		}
	}
	// kick：member 门槛不足 → 私拒一行，且连接不断（下一帧照常可读）。
	if err := wsjson.Write(ctx, conn, chat.Message{Type: roomops.MsgKick, Name: "someone"}); err != nil {
		t.Fatal(err)
	}
	refused := false
	readDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(readDeadline) {
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
		if err := wsjson.Read(rctx, conn, &got); err != nil {
			rcancel()
			break
		}
		rcancel()
		if got.Type == chat.MsgSystem && strings.Contains(got.Text, "kick") && strings.Contains(got.Text, "权限") {
			refused = true
			break
		}
	}
	if !refused {
		t.Fatal("member 的 kick 应被角色门私拒")
	}
	_ = conn.CloseNow()

	// 3) 单用户形态（无身份服务）：裸拨号照旧、kick 不设门槛——历史
	// 行为逐字节不变（既有测试全量承载这条回归，这里钉门本身不在）。
	lobby := chat.NewHub()
	legacy, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, LocalName: "root"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(legacy.Close)
	if legacy.multiuser() {
		t.Fatal("legacy server misread as multi-user")
	}
	w, conn, err = dialWS(t, legacy, map[string]any{"type": chat.MsgHello, "name": "root", "replay": true})
	if err != nil || w.Type != chat.MsgWelcome {
		t.Fatalf("legacy dial: %v %+v", err, w)
	}
	_ = conn.CloseNow()
}

func TestHelloProtoNegotiation(t *testing.T) {
	s, _, _, _ := armedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	// 客户端版本高于服务端：一条可读的 system 拒绝＋策略关闭。
	if err := wsjson.Write(ctx, conn, map[string]any{
		"type": "hello", "name": "future", "proto": wire.ProtoVersion + 1,
	}); err != nil {
		t.Fatal(err)
	}
	var got chat.Message
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("refusal frame: %v", err)
	}
	if got.Type != chat.MsgSystem || !strings.Contains(got.Text, "协议版本") {
		t.Fatalf("proto refusal should be readable: %+v", got)
	}
	// 旧客户端（无 proto 字段＝0）：照常服务。
	conn2, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn2.CloseNow() })
	if err := wsjson.Write(ctx, conn2, map[string]any{"type": "hello", "name": "oldclient"}); err != nil {
		t.Fatal(err)
	}
	var w chat.Message
	if err := wsjson.Read(ctx, conn2, &w); err != nil || w.Type != chat.MsgWelcome || w.Proto != wire.ProtoVersion {
		t.Fatalf("legacy client should be served: %v %+v", err, w)
	}
}

func TestAuthEndpoints(t *testing.T) {
	s, _, _, _ := armedServer(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", s.Port)

	// 未启用多用户时面如实自报。
	lobby := chat.NewHub()
	legacy, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, LocalName: "root"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(legacy.Close)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/auth/login", legacy.Port), "application/json",
		strings.NewReader(`{"username":"x","password":"y"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("单用户 /auth/login 应 404: %d", resp.StatusCode)
	}

	// 登录换会话；错密码 401 统一口径。
	resp, err = http.Post(base+"/auth/login", "application/json",
		strings.NewReader(`{"username":"alice","password":"memberpw"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("member login: %d", resp.StatusCode)
	}
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("login should set the session cookie")
	}
	resp2, err := http.Post(base+"/auth/login", "application/json",
		strings.NewReader(`{"username":"alice","password":"wrong"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", resp2.StatusCode)
	}

	// /auth/me：cookie 会话自认 member。
	req, _ := http.NewRequest(http.MethodGet, base+"/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	me, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me: %d", me.StatusCode)
	}

	// 账户管理面：member 会话被 403（admin 专属）；匿名远端 403。
	req, _ = http.NewRequest(http.MethodGet, base+"/auth/users", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	r3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r3.Body.Close()
	if r3.StatusCode != http.StatusForbidden {
		t.Fatalf("member on admin face: %d", r3.StatusCode)
	}
}

// TestStrictLoopbackOptsOut — NIUMA_STRICT_LOOPBACK 的形态：回环不再
// 自动操作者——本机裸请求/裸拨号与远端同拒；持服务令牌的本机 CLI
// 照旧零摩擦入座。
func TestStrictLoopbackOptsOut(t *testing.T) {
	lobby := chat.NewHub()
	store := newMemAccounts()
	svc := identity.NewService(store, time.Hour, identity.ThrottleConfig{})
	mkAcc(t, store, "root", identity.RoleAdmin, "adminpw")
	s, err := Start(lobby, Options{
		Endpoint:       EndpointConfig{PreferredPort: -1},
		LocalName:      "root",
		Auth:           svc,
		OperatorToken:  "svc-tok-0000000000000001",
		StrictLoopback: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	// HTTP：回环裸请求不再解析为操作者（管理面 403）。
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/auth/users", nil)
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("strict 模式回环裸请求应 403: %d", rec.Code)
	}
	// 持服务令牌（回环出示）：操作者照旧。
	req2 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/auth/users", nil)
	req2.Host = "127.0.0.1"
	req2.RemoteAddr = "127.0.0.1:40000"
	req2.Header.Set("Authorization", "Bearer svc-tok-0000000000000001")
	rec2 := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("strict 模式本机服务令牌应照旧: %d", rec2.Code)
	}

	// WS：回环裸拨号被拒（error 帧可读）；带服务令牌入座。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	if err := wsjson.Write(ctx, conn, map[string]any{"type": "hello", "name": "root"}); err != nil {
		t.Fatal(err)
	}
	var got chat.Message
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "error" || !strings.Contains(got.Text, "多用户") {
		t.Fatalf("strict 模式裸拨号应拒: %+v", got)
	}

	conn2, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn2.CloseNow() })
	if err := wsjson.Write(ctx, conn2, map[string]any{"type": "hello", "name": "root", "auth": "svc-tok-0000000000000001"}); err != nil {
		t.Fatal(err)
	}
	var w chat.Message
	if err := wsjson.Read(ctx, conn2, &w); err != nil || w.Type != chat.MsgWelcome {
		t.Fatalf("strict 模式服务令牌拨号应入座: %v %+v", err, w)
	}
}
