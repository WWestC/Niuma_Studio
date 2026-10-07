package zcode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestReadyOnFirstProtocolLine pins the boot gate's truth: readiness
// flips on the child's first protocol-shaped line (answer, request or
// notification) — never on the bootstrap debug lines that fail the
// JSON unmarshal (GET /zcode keeps answering booting until then).
func TestReadyOnFirstProtocolLine(t *testing.T) {
	pr, pw := io.Pipe()
	c := &Client{
		stdout:  pr,
		waitSem: make(chan struct{}),
		ready:   make(chan struct{}),
		pending: map[int64]chan rpcResult{},
	}
	defer pw.Close()
	go c.readLoop()

	if c.ReadyNow() {
		t.Fatal("a client that heard nothing reports ready")
	}
	// bootstrap noise: unparseable lines must not trip the gate
	if _, err := pw.Write([]byte("zcode bootstrap debug line\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if c.ReadyNow() {
		t.Fatal("a debug line tripped readiness")
	}
	// first notification: the app-server is speaking protocol
	if _, err := pw.Write([]byte(`{"method":"startup/done","params":{}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.ReadyDone():
	case <-time.After(time.Second):
		t.Fatal("first protocol line did not close the ready channel")
	}
	if !c.ReadyNow() {
		t.Fatal("ReadyNow stays false after ReadyDone closed")
	}
	if !c.Alive() {
		t.Fatal("a child that was never reaped reports dead")
	}
}

// TestAliveAfterReap pins the boot gate's death branch: a reaped child
// never becomes ready, so the gate must stop waiting on it.
func TestAliveAfterReap(t *testing.T) {
	c := &Client{waitSem: make(chan struct{}), ready: make(chan struct{})}
	close(c.waitSem) // cmd.Wait returned
	if c.Alive() {
		t.Fatal("a reaped child reports alive")
	}
	if c.ReadyNow() {
		t.Fatal("a dead client must not report ready")
	}
	var zero Client
	if zero.Alive() || zero.ReadyNow() {
		t.Fatal("a zero-value Client must be neither alive nor ready")
	}
}

// fakeAppLayout builds a temp .app skeleton and returns the bundle
// path (zcode.cjs) plus a helper to drop the provider config at a
// relative spot inside the fake app.
func fakeAppLayout(t *testing.T) (bundle string, place func(rel string) string) {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "Fake.app")
	bundle = filepath.Join(app, "Contents", "Resources", "glm", "zcode.cjs")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle, func(rel string) string {
		p := filepath.Join(app, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
}

// TestFindProviderConfig pins the discovery escalation that keeps the
// bridge self-healing across desktop updates: the env override, the
// two shipped layouts (3.13- glm/provider, 3.14+ Resources/config/
// provider), and the bounded walk that catches an unknown future move.
func TestFindProviderConfig(t *testing.T) {
	t.Run("env override wins", func(t *testing.T) {
		bundle, _ := fakeAppLayout(t)
		p := filepath.Join(t.TempDir(), "mine.json")
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("NIUMA_ZCODE_PROVIDER", p)
		if got := findProviderConfig(bundle); got != p {
			t.Fatalf("override ignored: %q", got)
		}
	})
	t.Run("old layout glm/provider", func(t *testing.T) {
		bundle, place := fakeAppLayout(t)
		want := place(filepath.Join("Contents", "Resources", "glm", "provider", "zcode-builtin.json"))
		if got := findProviderConfig(bundle); got != want {
			t.Fatalf("old layout miss: %q", got)
		}
	})
	t.Run("3.14 layout Resources/config/provider", func(t *testing.T) {
		bundle, place := fakeAppLayout(t)
		want := place(filepath.Join("Contents", "Resources", "config", "provider", "zcode-builtin.json"))
		if got := findProviderConfig(bundle); got != want {
			t.Fatalf("3.14 layout miss: %q", got)
		}
	})
	t.Run("unknown layout found by the walk", func(t *testing.T) {
		bundle, place := fakeAppLayout(t)
		want := place(filepath.Join("Contents", "Resources", "somewhere", "new", "zcode-builtin.json"))
		if got := findProviderConfig(bundle); got != want {
			t.Fatalf("walk did not self-heal: %q", got)
		}
	})
	t.Run("nothing anywhere", func(t *testing.T) {
		bundle, _ := fakeAppLayout(t)
		if got := findProviderConfig(bundle); got != "" {
			t.Fatalf("expected no find, got %q", got)
		}
	})
}

// TestProbeSessionWire pins the keepalive probe's wire contract: the
// minimal session/events read (limit 1) carrying the sessionId — the
// request shape the app-server's resident pool counts as a touch, and
// the -32004 answer the dispatcher's resurrect ladder keys on.
func TestProbeSessionWire(t *testing.T) {
	stdinR, stdinW := io.Pipe() // the client's writes
	stdoutR, stdoutW := io.Pipe()
	c := &Client{
		stdin:   stdinW,
		stdout:  stdoutR,
		waitSem: make(chan struct{}),
		ready:   make(chan struct{}),
		pending: map[int64]chan rpcResult{},
	}
	defer stdinW.Close()
	defer stdoutW.Close()
	go c.readLoop()

	type req struct {
		ID     int64          `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	got := make(chan req, 1)
	go func() {
		line, err := bufio.NewReader(stdinR).ReadBytes('\n')
		if err != nil {
			return
		}
		var r req
		if json.Unmarshal(line, &r) == nil {
			got <- r
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.ProbeSession(context.Background(), "sess-probe-1")
	}()

	select {
	case r := <-got:
		if r.Method != "session/events" {
			t.Fatalf("method: want session/events, got %s", r.Method)
		}
		if r.Params["sessionId"] != "sess-probe-1" {
			t.Fatalf("sessionId not carried: %v", r.Params)
		}
		if lim, _ := r.Params["limit"].(float64); lim != 1 {
			t.Fatalf("limit: want 1, got %v", r.Params["limit"])
		}
		_, _ = stdoutW.Write([]byte(
			fmt.Sprintf(`{"id":%d,"result":{"events":[]}}`+"\n", r.ID)))
	case <-time.After(time.Second):
		t.Fatal("probe request never hit the wire")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("probe against a healthy session must ack nil: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("probe never resolved")
	}
}

// TestFindBundleEnvOverride pins the escape hatch: NIUMA_ZCODE_BUNDLE
// names the bundle to drive, and a stale value falls through to the
// standard homes instead of poisoning every spawn.
func TestFindBundleEnvOverride(t *testing.T) {
	bundle, _ := fakeAppLayout(t)
	t.Setenv("NIUMA_ZCODE_BUNDLE", bundle)
	if got := FindBundle(); got != bundle {
		t.Fatalf("override ignored: %q", got)
	}
	t.Setenv("NIUMA_ZCODE_BUNDLE", filepath.Join(t.TempDir(), "gone.cjs"))
	if got := FindBundle(); got == "gone.cjs" {
		t.Fatal("a stale override must fall through to the standard homes")
	}
}

// TestBundleHomes pins the per-platform fixed homes: the list is never
// empty or blank (the boot gate's missing panel indexes [0] verbatim),
// and each platform's first home is the install the platform actually
// uses — the .app on darwin, the Squirrel per-user install on windows,
// electron-builder's /opt default on linux.
func TestBundleHomes(t *testing.T) {
	homes := BundleHomes()
	if len(homes) == 0 {
		t.Fatal("BundleHomes must never be empty — the boot gate shows homes[0]")
	}
	for _, h := range homes {
		if strings.TrimSpace(h) == "" {
			t.Fatal("a blank home in the list would show as an empty path on the door")
		}
	}
	switch runtime.GOOS {
	case "darwin":
		if homes[0] != DefaultBundle {
			t.Fatalf("darwin first home: want %q, got %q", DefaultBundle, homes[0])
		}
	case "linux":
		if !strings.HasPrefix(homes[0], "/usr/lib/") {
			t.Fatalf("linux first home should live under /usr/lib: %q", homes[0])
		}
	case "windows":
		if !strings.HasSuffix(strings.ToLower(homes[0]), `zcode\resources\glm\zcode.cjs`) {
			t.Fatalf("windows first home should be the Squirrel install layout: %q", homes[0])
		}
	}
}

// TestWalkForBundle pins the drift insurance behind the fixed homes: a
// zcode.cjs at the CLI's known nesting inside a walk root is found, a
// root that doesn't exist is silently skipped, and nesting beyond the
// depth cap is pruned instead of swept.
func TestWalkForBundle(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "resources", "glm", "zcode.cjs")
	if err := os.MkdirAll(filepath.Dir(deep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deep, []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := walkForBundle(root); got != deep {
		t.Fatalf("known-depth layout missed: %q", got)
	}
	if got := walkForBundle(filepath.Join(root, "absent")); got != "" {
		t.Fatalf("a missing root must be skipped, got %q", got)
	}
	capped := t.TempDir()
	beyond := filepath.Join(capped, "a", "b", "c", "d", "e", "f", "g", "zcode.cjs")
	if err := os.MkdirAll(filepath.Dir(beyond), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beyond, []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := walkForBundle(capped); got != "" {
		t.Fatalf("nesting beyond the depth cap must be pruned, got %q", got)
	}
}

// TestCreateSessionMCPWire pins the per-session MCP fleet override's
// wire contract: a NON-EMPTY override rides session/create as
// mcpServers (a non-empty list REPLACES the session's fleet — the
// server reads an empty one as "unspecified"), and the plain
// CreateSession omits the key entirely (the config default).
func TestCreateSessionMCPWire(t *testing.T) {
	stdinR, stdinW := io.Pipe() // the client's writes
	stdoutR, stdoutW := io.Pipe()
	c := &Client{
		stdin:   stdinW,
		stdout:  stdoutR,
		waitSem: make(chan struct{}),
		ready:   make(chan struct{}),
		pending: map[int64]chan rpcResult{},
	}
	defer stdinW.Close()
	defer stdoutW.Close()
	go c.readLoop()

	type req struct {
		ID     int64 `json:"id"`
		Method string
		Params struct {
			Workspace struct {
				WorkspacePath string `json:"workspacePath"`
			} `json:"workspace"`
			Mode       string           `json:"mode"`
			MCPServers []map[string]any `json:"mcpServers"`
		}
	}
	got := make(chan req, 2)
	go func() {
		sc := bufio.NewScanner(stdinR)
		for sc.Scan() {
			var r req
			if json.Unmarshal(sc.Bytes(), &r) == nil {
				got <- r
			}
		}
	}()
	answer := func(r req) {
		_, _ = stdoutW.Write([]byte(
			fmt.Sprintf(`{"id":%d,"result":{"session":{"sessionId":"sess-wire"}}}`+"\n", r.ID)))
	}

	specs := []MCPServerSpec{{
		Name: "probe-none", Type: "http", URL: "http://127.0.0.1:9/mcp",
		Headers: []MCPHeader{{Name: "x", Value: "y"}}, Isolation: "session", TimeoutMs: 2000,
	}}
	errCh := make(chan error, 2)
	go func() {
		_, err := c.CreateSessionMCP(context.Background(), "/ws/probe", "yolo", specs)
		errCh <- err
		_, err = c.CreateSession(context.Background(), "/ws/probe", "yolo")
		errCh <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case r := <-got:
			if r.Method != "session/create" {
				t.Fatalf("method: want session/create, got %s", r.Method)
			}
			if r.Params.Workspace.WorkspacePath != "/ws/probe" || r.Params.Mode != "yolo" {
				t.Fatalf("base params not carried: %+v", r.Params)
			}
			answer(r)
			if i == 0 {
				if len(r.Params.MCPServers) != 1 {
					t.Fatalf("override not on the wire: %+v", r.Params.MCPServers)
				}
				s := r.Params.MCPServers[0]
				if s["name"] != "probe-none" || s["type"] != "http" ||
					s["url"] != "http://127.0.0.1:9/mcp" || s["isolation"] != "session" {
					t.Fatalf("override spec = %+v", s)
				}
				if _, ok := s["timeoutMs"].(float64); !ok {
					t.Fatalf("timeoutMs = %v (%T), want number", s["timeoutMs"], s["timeoutMs"])
				}
			} else if r.Params.MCPServers != nil {
				t.Fatalf("plain create must omit mcpServers: %+v", r.Params.MCPServers)
			}
		case <-time.After(time.Second):
			t.Fatal("create request never hit the wire")
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("create %d: %v", i, err)
			}
		case <-time.After(time.Second):
			t.Fatal("create never resolved")
		}
	}
}
