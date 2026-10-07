package server

// reset_test.go — the factory-reset face's contract: the typed
// confirmation on the wire ("RESET"), the one-signal buffered channel
// (double-fire answers 409 instead of queueing a second exit), and the
// 404 for embeds that never wired the choreography. The wipe and the
// relaunch themselves live in main's choreography (util.WipeStudioData
// has its own tests) — here we only pin the door's behavior.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
)

func startResettable(t *testing.T) (*Server, chan struct{}) {
	t.Helper()
	hub := chat.NewHub()
	hub.Join("房主", "boss", false)
	ch := make(chan struct{}, 1)
	s, err := Start(hub, Options{Endpoint: EndpointConfig{PreferredPort: -1}, ResetCh: ch})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, ch
}

func postResetBody(t *testing.T, s *Server, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(addrHTTP(s)+"/reset", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /reset: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestResetRequiresConfirmToken(t *testing.T) {
	s, ch := startResettable(t)
	for _, body := range []string{
		`{}`,
		`{"confirm":"reset"}`, // 大小写敏感
		`{"confirm":"重置"}`,    // 前端口令不是线口令
		`not json`,
	} {
		code, out := postResetBody(t, s, body)
		if code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, code)
		}
		if out["ok"] == true {
			t.Errorf("body %q: 拒绝时不得回 ok", body)
		}
	}
	select {
	case <-ch:
		t.Fatal("口令不符不得投信号")
	default:
	}
}

func TestResetSignalsOnceThenConflicts(t *testing.T) {
	s, ch := startResettable(t)
	code, out := postResetBody(t, s, `{"confirm":"RESET"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body: %v）", code, out)
	}
	if out["ok"] != true || out["restarting"] != true {
		t.Fatalf("回执形状不符: %v", out)
	}
	select {
	case <-ch: // 信号已投（断言的接收会把令牌取走——放回去，保持占位）
		ch <- struct{}{}
	default:
		t.Fatal("确认口令后必须投出一枚信号")
	}
	// 缓冲 1 已占：第二发是 409，不是排队
	if code, _ := postResetBody(t, s, `{"confirm":"RESET"}`); code != http.StatusConflict {
		t.Errorf("double-fire status = %d, want 409", code)
	}
	if r, err := http.Get(addrHTTP(s) + "/reset"); err != nil {
		t.Fatalf("GET /reset: %v", err)
	} else {
		r.Body.Close()
		if r.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET status = %d, want 405", r.StatusCode)
		}
	}
}

func TestResetAbsentWithoutChannel(t *testing.T) {
	hub := chat.NewHub()
	s, err := Start(hub, Options{Endpoint: EndpointConfig{PreferredPort: -1}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	if code, _ := postResetBody(t, s, `{"confirm":"RESET"}`); code != http.StatusNotFound {
		t.Errorf("未接线 status = %d, want 404", code)
	}
}
