package server

// gate_test.go — 令牌门的钉子：回环零摩擦（走整条 hostgate 历史
// 栈）、远端无令牌/错令牌裸 404、远端持令牌（Bearer 或 ?token=）
// 直达 mux、非回环监听无令牌 fail-close 拒绝起服务。

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testGateStack(t *testing.T, token string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("room"))
	})
	local := gateBrowserBorne(mux)
	if token == "" {
		return local
	}
	return newTokenGate(token, local, mux)
}

func gateRequest(t *testing.T, h http.Handler, remote, host, bearer, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://"+host+"/"+rawQuery, nil)
	r.RemoteAddr = remote
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGateLoopbackZeroFriction(t *testing.T) {
	h := testGateStack(t, "sekrit")
	// 回环请求（无令牌、Host 是回环字面量）照旧过 hostgate 进房间。
	if w := gateRequest(t, h, "127.0.0.1:55555", "127.0.0.1:7777", "", ""); w.Code != http.StatusOK || w.Body.String() != "room" {
		t.Fatalf("回环请求应零摩擦进房：code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestGateRemoteNeedsToken(t *testing.T) {
	h := testGateStack(t, "sekrit")
	// 远端无令牌：裸 404（不确认端点存在性）。
	if w := gateRequest(t, h, "192.168.1.9:51514", "192.168.1.5:7777", "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("远端无令牌应 404：code=%d", w.Code)
	}
	// 错令牌同样 404。
	if w := gateRequest(t, h, "192.168.1.9:51514", "192.168.1.5:7777", "wrong", ""); w.Code != http.StatusNotFound {
		t.Fatalf("远端错令牌应 404：code=%d", w.Code)
	}
	// 对令牌：Bearer 直达 mux（Host 非回环不再是障碍——令牌即边界）。
	if w := gateRequest(t, h, "192.168.1.9:51514", "192.168.1.5:7777", "sekrit", ""); w.Code != http.StatusOK || w.Body.String() != "room" {
		t.Fatalf("远端 Bearer 应进房：code=%d body=%q", w.Code, w.Body.String())
	}
	// 对令牌：?token=（WS 拨号与直链的形态）。
	if w := gateRequest(t, h, "192.168.1.9:51514", "192.168.1.5:7777", "", "?token=sekrit"); w.Code != http.StatusOK {
		t.Fatalf("远端 ?token= 应进房：code=%d", w.Code)
	}
	// 大小写混乱的 Bearer 方案名不认。
	r := httptest.NewRequest(http.MethodGet, "http://192.168.1.5:7777/", nil)
	r.RemoteAddr = "192.168.1.9:51514"
	r.Header.Set("Authorization", "basic sekrit")
	w := httptest.NewRecorder()
	testGateStack(t, "sekrit").ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("非 Bearer 方案应 404：code=%d", w.Code)
	}
}

func TestGateLoopbackKeepsHostgateDiscipline(t *testing.T) {
	h := testGateStack(t, "sekrit")
	// 回环来源但 Host 非回环字面量（DNS rebinding 形状）：hostgate
	// 的历史纪律不因令牌门而松——本地浏览器流量仍受三查。
	if w := gateRequest(t, h, "127.0.0.1:55555", "evil.example:7777", "sekrit", ""); w.Code != http.StatusNotFound {
		t.Fatalf("回环来源的 rebinding 形状仍应 404：code=%d", w.Code)
	}
	// 回环来源带跨站 Origin：同上。
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7777/", nil)
	r.RemoteAddr = "127.0.0.1:55555"
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Authorization", "Bearer sekrit")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("回环来源的跨站 Origin 仍应 404：code=%d", w.Code)
	}
}

func TestGateRefusalFailClose(t *testing.T) {
	// 非回环监听（用本机一块真实非回环地址或合成 IPv4）无令牌 → 拒绝。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()
	if err := gateRefusal(ln, ""); err != nil {
		t.Fatalf("回环监听不该拒绝：%v", err)
	}
	// 合成一个非回环 TCP 地址的假 listener 只测判定逻辑。
	fake := &fakeAddrListener{addr: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 7777}}
	if err := gateRefusal(fake, ""); err == nil || !strings.Contains(err.Error(), "拒绝裸奔") {
		t.Fatalf("非回环无令牌应 fail-close 拒绝：got %v", err)
	}
	if err := gateRefusal(fake, "sekrit"); err != nil {
		t.Fatalf("非回环带令牌应放行：%v", err)
	}
}

type fakeAddrListener struct {
	addr net.Addr
}

func (f *fakeAddrListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (f *fakeAddrListener) Close() error              { return nil }
func (f *fakeAddrListener) Addr() net.Addr            { return f.addr }
