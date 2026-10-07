package server

// /dispatch-wait (消息注入等待): the face's defaults (300s, bounds
// 10–3600), its validation, the atomic policy file, and the live-apply
// sink (main wires it to Fleet.SetSendWait — the write must reach the
// dispatchers without a restart). Studios here boot with a plain
// registry root; the sink is a spy that records the pushed duration.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

// startSendWaitStudio boots a studio persisting under a temp root; the
// sink (nil = face persists only) records pushed durations.
func startSendWaitStudio(t *testing.T, sink func(time.Duration)) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	registry := chat.NewRegistry(projStore, root, "", "")
	lobby, err := registry.Lobby()
	if err != nil {
		t.Fatalf("Lobby: %v", err)
	}
	lobby.Join("房主", "boss", false)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Registry: registry, Fleet: FleetFuncs{SendWaitFn: sink}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, root
}

func postSendWait(t *testing.T, s *Server, seconds int) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(addrHTTP(s)+"/dispatch-wait", "application/json",
		bytes.NewReader([]byte(fmt.Sprintf(`{"seconds":%d}`, seconds))))
	if err != nil {
		t.Fatalf("POST /dispatch-wait: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// TestSendWaitFaceDefaultsWriteAndSink: an unseeded studio answers the
// 300s default with bounds; a write round-trips (file + receipt) and
// reaches the sink as a duration; out-of-range values refuse.
func TestSendWaitFaceDefaultsWriteAndSink(t *testing.T) {
	var mu sync.Mutex
	var pushed []time.Duration
	s, root := startSendWaitStudio(t, func(dt time.Duration) {
		mu.Lock()
		pushed = append(pushed, dt)
		mu.Unlock()
	})

	resp, err := http.Get(addrHTTP(s) + "/dispatch-wait")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got["seconds"] != float64(SendWaitDefaultSeconds) ||
		got["default_seconds"] != float64(SendWaitDefaultSeconds) ||
		got["min_seconds"] != float64(10) || got["max_seconds"] != float64(3600) {
		t.Fatalf("缺省 GET = %v", got)
	}

	if code, body := postSendWait(t, s, 120); code != 200 || body["seconds"] != float64(120) {
		t.Fatalf("POST 120 = %d %v", code, body)
	}
	if secs := LoadSendWait(SendWaitPath(root)); secs != 120 {
		t.Fatalf("落盘读回 = %d, want 120", secs)
	}
	mu.Lock()
	if len(pushed) != 1 || pushed[0] != 2*time.Minute {
		mu.Unlock()
		t.Fatalf("sink 收到 %v, want [2m]", pushed)
	}
	mu.Unlock()

	// 回读面跟落盘走：不是回显请求，是新的 GET 真读到 120
	resp2, _ := http.Get(addrHTTP(s) + "/dispatch-wait")
	var got2 map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&got2)
	resp2.Body.Close()
	if got2["seconds"] != float64(120) {
		t.Fatalf("写后 GET = %v, want 120", got2["seconds"])
	}

	if code, _ := postSendWait(t, s, 5); code != 400 {
		t.Fatalf("POST 5 = %d, want 400", code)
	}
	if code, _ := postSendWait(t, s, 4000); code != 400 {
		t.Fatalf("POST 4000 = %d, want 400", code)
	}
	if _, err := os.Stat(SendWaitPath(root)); err != nil {
		// 拒绝值不落盘：上一次合法写入的 120 应该还在
		t.Fatalf("拒绝后策略文件失踪: %v", err)
	}
}

// TestSendWaitLoadFallsBackOnCorrupt: 坏文件/越界文件回缺省——设置
// 面永远拿到一个可用的值，而不是把畸形落盘带进调度器。
func TestSendWaitLoadFallsBackOnCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := SendWaitPath(dir)
	if err := os.WriteFile(path, []byte(`{"seconds":"很快"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if secs := LoadSendWait(path); secs != SendWaitDefaultSeconds {
		t.Fatalf("坏文件读回 = %d, want 缺省 %d", secs, SendWaitDefaultSeconds)
	}
	if err := os.WriteFile(path, []byte(`{"seconds":99999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if secs := LoadSendWait(path); secs != SendWaitDefaultSeconds {
		t.Fatalf("越界文件读回 = %d, want 缺省 %d", secs, SendWaitDefaultSeconds)
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.json")); err == nil {
		t.Fatal("unexpected file")
	}
	if secs := LoadSendWait(filepath.Join(dir, "nope.json")); secs != SendWaitDefaultSeconds {
		t.Fatalf("缺文件读回 = %d, want 缺省 %d", secs, SendWaitDefaultSeconds)
	}
}
