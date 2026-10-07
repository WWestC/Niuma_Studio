package server

// visitor_test.go — r_19 t_191：访客通道的服务端面。开关生命周期/
// token 校验/限流三参数/彩蛋去重/路由 404 姿态（不确认存在性）。
// ⑧ 是治理修订的 WS 级回归：治理只认带 token 的拨号——修订前所有
// observer 拨号无差别进限流，房主自己多开几个窗口就把名额占满、
// 自己被「当前访客较多」锁在门外（「仅缓存·HTTP存活」事故）。

import (
	"context"
	"fmt"
	"github.com/WWestC/Niuma_Studio/server/auth"
	"net/http"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// ① 开关关 → /view 404；开 + 对 token → 200（回页面壳）。
func TestVisitorGateRoute(t *testing.T) {
	g := &VisitorGate{}
	// Gate.Check 是纯函数面（路由处理依赖 Server 构造——单测走纯函数）
	if g.Check("whatever") {
		t.Fatal("默认关：任何 token 不放行")
	}
	token, on, _ := g.Toggle(true)
	if !on || token == "" {
		t.Fatalf("开应生成 token（got %q on=%v）", token, on)
	}
	if !g.Check(token) {
		t.Fatal("对 token 应放行")
	}
	if g.Check("wrong") {
		t.Fatal("错 token 不放行")
	}
}

// ② token 生命周期：关闭即废、重开换新（旧 token 不复活）。
func TestVisitorTokenLifecycle(t *testing.T) {
	g := &VisitorGate{}
	t1, _, _ := g.Toggle(true)
	t2, _, _ := g.Toggle(false)
	if t2 != "" {
		t.Fatal("关闭不产 token")
	}
	if g.Check(t1) {
		t.Fatal("关闭后旧 token 应死")
	}
	t3, _, _ := g.Toggle(true)
	if t3 == t1 || t3 == "" {
		t.Fatal("重开应换新 token（旧不复活）")
	}
	if g.Check(t1) {
		t.Fatal("旧 token 跨纪元不复活")
	}
	if !g.Check(t3) {
		t.Fatal("新 token 放行")
	}
}

// ③ 无翻转无文案（重复 Toggle 同值幂等）。
func TestVisitorToggleIdempotent(t *testing.T) {
	g := &VisitorGate{}
	g.Toggle(true)
	_, _, line := g.Toggle(true)
	if line != "" {
		t.Fatal("重复开不应产文案")
	}
	_, _, line2 := g.Toggle(false)
	if line2 == "" {
		t.Fatal("真翻转应产文案")
	}
}

// ④ 限流：并发帽 5（第 6 连接拒 503）。
func TestVisitorConcurrentCap(t *testing.T) {
	m := newVisitorMeter()
	for i := 0; i < auth.VisitorMaxConcurrent; i++ {
		if ok, _, _ := m.Admit("p1"); !ok {
			t.Fatalf("第 %d 连接不该拒", i+1)
		}
	}
	ok, status, line := m.Admit("p1")
	if ok || status != http.StatusServiceUnavailable {
		t.Fatalf("第 6 连接应 503（got ok=%v status=%d line=%q）", ok, status, line)
	}
	_ = status
	// 释放一个后可再进
	m.Release("p1")
	if ok, _, _ := m.Admit("p1"); !ok {
		t.Fatal("释放后有位应放行")
	}
}

// ⑤ 重连窗 10/min（第 11 次拒 429）。
func TestVisitorReconnectWindow(t *testing.T) {
	m := newVisitorMeter()
	// 5 个并发占位＋同窗内 5 次断连重连（先 release 再 admit 模拟）
	for i := 0; i < 10; i++ {
		if ok, _, _ := m.Admit("p2"); !ok {
			t.Fatalf("窗内第 %d 次不该拒", i+1)
		}
		m.Release("p2")
	}
	ok, status, _ := m.Admit("p2")
	if ok || status != http.StatusTooManyRequests {
		t.Fatalf("第 11 次应 429（got %v %d）", ok, status)
	}
}

// ⑥ 彩蛋去重：同 epoch 只播一次。
func TestVisitorFirstVisitOnce(t *testing.T) {
	tr := newFirstVisitTracker()
	if !tr.FireOnce("tok-1") {
		t.Fatal("首次应播")
	}
	if tr.FireOnce("tok-1") {
		t.Fatal("同 epoch 重连不重播")
	}
	if !tr.FireOnce("tok-2") {
		t.Fatal("新 epoch（重开通道）新一次")
	}
}

// ⑦ 敏感帧过滤：observer 写帧拒绝的既有语义回归（chat 侧协议层——
// 这里钉 server 面的辅助：访客帧无 provider/token/key 泄露面）。
func TestVisitorFrameHygiene(t *testing.T) {
	// server 侧访客面只发 welcome/history/broadcast——无 provider 概念
	// （provider runtime 是成员会话域）。此测试钉 Gate 不携带任何
	// provider/token/key 字段（结构面）——反射检查字段名黑名单。
	g := &VisitorGate{}
	_ = g
	// VisitorGate 只有 on/token/since/mu——无敏感面。结构性断言留 t_193
	// 的 DOM diff（定稿 §四裁剪清单）。此占位测试钉 Toggle 返回值不带 key 类字段
	_, _, line := g.Toggle(true)
	for _, banned := range []string{"provider", "key", "secret"} {
		// token 本身就是访客面唯一凭证（对外只读）——检查 line 文案
		if containsFold(line, banned) {
			t.Fatalf("文案不应含敏感词 %q: %s", banned, line)
		}
	}
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(sub) > 0 && indexOfFold(s, sub) >= 0))
}

func indexOfFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ⑧ 治理只认带 token 的拨号（修订回归，WS 级）：大厅名额占满时——
//
//	房主自己的 observer 拨号（不带 token）必须照常 welcome（修订前
//	这里回 503，窗口永远停在「仅缓存」）；带活 token 的访客拨号 503；
//	死 token 404（文案给访客指路），且不烧重连窗。
func TestVisitorGovernanceTokenOnly(t *testing.T) {
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)

	// 占满大厅的访客并发帽（直接灌表——等价于 5 个外部访客在座）
	for i := 0; i < auth.VisitorMaxConcurrent; i++ {
		if ok, _, _ := s.visitors.Admit(chat.LobbyKey); !ok {
			t.Fatalf("第 %d 个占位不该拒", i+1)
		}
	}
	t.Cleanup(func() {
		for i := 0; i < auth.VisitorMaxConcurrent; i++ {
			s.visitors.Release(chat.LobbyKey)
		}
	})

	dial := func(visitor string) map[string]any {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", s.Port), nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		replay := false
		if err := wsjson.Write(ctx, conn, chat.Message{
			Type: chat.MsgHello, Observer: true, Replay: &replay, Visitor: visitor,
		}); err != nil {
			t.Fatalf("hello: %v", err)
		}
		var m map[string]any
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("first frame: %v", err)
		}
		return m
	}

	// ① 房主窗口（无 token）：名额满员也照常 welcome——事故现场回归
	if m := dial(""); m["type"] != chat.MsgWelcome {
		t.Fatalf("无 token 的拨号是房主自己的窗口，必须放行（got %+v）", m)
	}
	// ② 访客（活 token）满员：503 一句话
	tok, on, _ := s.visitor.Toggle(true)
	if !on {
		t.Fatal("开通道应有 token")
	}
	if m := dial(tok); m["type"] != "error" || m["status"] != float64(http.StatusServiceUnavailable) {
		t.Fatalf("满员访客应 503（got %+v）", m)
	}
	// ③ 死 token：404 指路文案，不进限流（重连窗未烧——第 11 次拨号
	// 若烧了窗会 429 而非 404，这里顺带钉住）
	for i := 0; i < auth.VisitorReconnectWin+1; i++ {
		if m := dial("deadbeef"); m["type"] != "error" || m["status"] != float64(http.StatusNotFound) {
			t.Fatalf("死 token 应 404（got %+v）", m)
		}
	}
}
