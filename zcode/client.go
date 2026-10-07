// Package zcode drives a ZCode app-server child process over its
// newline-delimited JSON-RPC protocol, so the room can inject user
// messages into ZCode sessions (v1.0 dispatcher architecture).
//
// Protocol contract (verified against ZCode CLI 0.16.9 source —
// apps/zcode-cli packages/shared/src/zcode-protocol — and by the M1
// spike):
//   - one JSON object per line on the child's stdin/stdout, UTF-8;
//     NO jsonrpc field (sending one is rejected with -32600);
//   - client→server request {id,method,params}; server answers
//     {id,result} or {id,error:{code,message}};
//   - notifications {method,params} (no id) — session events arrive
//     as "session/event" and "state.updated";
//   - server→client requests {id,method,params} (permission asks,
//     user-input asks) MUST be answered with {id,result} — unknown
//     methods get a -32601 error, never an empty result, and absent
//     handlers fail closed (permission deny / question decline).
package zcode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
)

// DefaultBundle is the macOS app-bundle location of the CLI
// (the only platform this project drives, matching recruit).
const DefaultBundle = "/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs"

// Options configures a Client.
type Options struct {
	// Node binary (default "node").
	Node string
	// Path to zcode.cjs (default DefaultBundle).
	Bundle string
	// Workspace directory the app-server runs for (--cwd).
	Cwd string
	// Env appended to the child's environment. Provider config is
	// NOT env-driven on CLI 0.16.9 — it lives in ~/.zcode/v2; the M1
	// spike confirmed zero-config creation works.
	Env []string
	// Stderr sinks the child's stderr (nil = discard).
	Stderr io.Writer
	// OnReverse, when set, answers server→client requests
	// (interaction/requestPermission, interaction/requestUserInput).
	// Returning (nil, nil) falls back to the fail-close defaults.
	// Settable after Start too (SetReverse) — the dispatcher wires its
	// room-forwarding policy once it exists.
	OnReverse func(method string, params map[string]any) (any, error)
}

// SetReverse installs (or replaces, nil removes) the reverse-request
// policy after Start.
func (c *Client) SetReverse(fn func(method string, params map[string]any) (any, error)) {
	c.mu.Lock()
	c.opts.OnReverse = fn
	c.mu.Unlock()
}

// Client is one running app-server session-driver. Zero seats in the
// room, one child process driving every member session.
type Client struct {
	opts    Options
	cmd     *exec.Cmd // nil when driving a pipe (tests)
	stdin   io.WriteCloser
	stdout  io.Reader
	waitSem chan struct{} // closed when the child is reaped

	// ready closes on the child's first protocol-shaped line (answer,
	// request or notification — the boot-gate truth behind GET /zcode:
	// "the app-server is up and speaking"). Bootstrap debug lines that
	// fail the JSON unmarshal never trip it.
	ready     chan struct{}
	readyOnce sync.Once

	mu      sync.Mutex
	closed  bool
	writeMu sync.Mutex
	nextID  int64
	pending map[int64]chan rpcResult
	hooks   Hooks
}

// ReadyDone returns the channel closed when the child first spoke
// protocol (see Client.ready). Never closes if the child dies before
// speaking — pair with Alive for that branch.
func (c *Client) ReadyDone() <-chan struct{} { return c.ready }

// ReadyNow is ReadyDone's non-blocking probe (false on a zero-value
// Client and until the first protocol line).
func (c *Client) ReadyNow() bool {
	if c.ready == nil {
		return false
	}
	select {
	case <-c.ready:
		return true
	default:
		return false
	}
}

// Alive reports whether the child has not been reaped yet. A child
// that died before speaking never becomes ready — the boot gate shows
// the failure instead of waiting forever.
func (c *Client) Alive() bool {
	if c.waitSem == nil {
		return false // never Started
	}
	select {
	case <-c.waitSem:
		return false
	default:
		return true
	}
}

// rpcResult carries one call's answer back from the read loop.
type rpcResult struct {
	result json.RawMessage
	code   int
	msg    string
}

// Error is a protocol-level rejection (code -32004 "Session is not
// active", -32010 "A prompt is already running", …).
type Error struct {
	Code    int
	Message string
}

// codeGloss 已知协议码的中文释义。zcode.Error 会经 %w 一路裸奔到聊天
// 流、小助手面板与 400 应答——光「[-32004] Session is not active」
// 用户无从下手；原样保留 CLI 的英文原文（协议真相），释义附在后面。
// 语义依据：本文件头部协议契约 + dispatcher 的分诊分支注释。
var codeGloss = map[int]string{
	-32700: "请求不是合法 JSON（CLI 版本不匹配？）",
	-32600: "请求不符合协议形状（CLI 版本不匹配？）",
	-32601: "该方法 CLI 不认识（CLI 版本过旧？请更新 ZCode）",
	-32602: "请求参数不合法（CLI 版本不匹配？）",
	-32001: "会话已被宿主回收（对话记忆仍在，可自动或手动 resume 找回）",
	-32004: "会话不在活动状态——冷会话需先 resume 再用（工作室会自动走这一步，反复出现请反馈）",
	-32010: "上一回合还在运行——等它结束再注入（工作室会自动排队，无需操作）",
	-32022: "会话启动询问超时（app-server 15 秒窗口无人应答——机器过载或 CLI 卡住，稍后重试）",
}

func (e *Error) Error() string {
	if gloss, ok := codeGloss[e.Code]; ok {
		return i18n.Sf("zcode: [%d] %s（%s）", e.Code, e.Message, i18n.S(gloss))
	}
	return fmt.Sprintf("zcode: [%d] %s", e.Code, e.Message)
}

// Sentinel error codes the dispatcher branches on.
const (
	ErrSessionNotActive = -32004 // cold session: resume, then retry
	ErrPromptRunning    = -32010 // busy turn: queue locally, retry on turn end
)

// IsCode reports whether err carries the given protocol code.
func IsCode(err error, code int) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// resolveNode finds the runtime that executes the CLI. First choice is
// the install's own Electron main binary — the desktop's own pairing:
// glm/zcode.cjs is built for exactly that Node (Electron 41 = Node
// 24), and a machine with ZCode installed but no Node.js still works.
// A GUI launch (double-clicked .app / `open`) inherits the bare system
// PATH on macOS, so plain-node fallbacks walk PATH → the homebrew
// homes → a login shell's own resolution (covers nvm/volta/mise
// setups, once, bounded). The trailing "node" keeps Start's exec error
// truthful when nothing resolves.
func resolveNode(bundle string) string {
	if p := util.Env("ZCODE_NODE"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if exe := electronExeIn(installRootOf(bundle)); exe != "" {
		return exe
	}
	if p, err := exec.LookPath("node"); err == nil {
		return p
	}
	for _, cand := range []string{"/opt/homebrew/bin/node", "/usr/local/bin/node"} {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/bin/zsh", "-lc", "command -v node").Output()
		if p := strings.TrimSpace(string(out)); err == nil && p != "" && !strings.ContainsAny(p, "\n\r ") {
			if _, serr := os.Stat(p); serr == nil {
				return p
			}
		}
	}
	return "node"
}

// providerEnv hands the CLI its bundled provider config when the
// launching environment doesn't already carry the pair. The CLI's own
// search walks two paths RELATIVE to the bundle — <glm>/provider/ and
// five levels up — but the desktop's 3.14 update moved the file to
// Resources/config/provider/, which matches neither, so a plain
// terminal or GUI launch (no ZCode-injected env) dies at boot with
// 「无法定位 CLI ZCode Built-in Provider Config」. findProviderConfig
// covers the known layouts AND a bounded walk of the app bundle, so a
// future move keeps the bridge self-healing; the personal config stays
// at its fixed ~/.zcode/v2 home (the CLI's own default, made explicit).
func providerEnv(bundle string) []string {
	if os.Getenv("ZCODE_BUILTIN_PROVIDER_CONFIG_FILE") != "" &&
		os.Getenv("ZCODE_PERSONAL_PROVIDER_CONFIG_FILE") != "" {
		return nil
	}
	// The ACTIVE builtin config (runtime-refreshed copy first, bundled
	// fallback — see activeBuiltinPath): the child's registry revision
	// must equal the one the account push is keyed to, or the push
	// silently never applies and every SetModel(account:…) is rejected.
	builtin := activeBuiltinPath()
	if builtin == "" {
		builtin = findProviderConfig(bundle)
	}
	if builtin == "" {
		return nil // nothing to hand in; the CLI's own error tells the rest
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		"ZCODE_BUILTIN_PROVIDER_CONFIG_FILE=" + builtin,
		"ZCODE_PERSONAL_PROVIDER_CONFIG_FILE=" + filepath.Join(home, ".zcode", "v2", "provider_config.json"),
	}
}

// findProviderConfig locates the CLI's bundled provider config with
// escalation: the NIUMA_ZCODE_PROVIDER override first, then the two
// layouts the CLI itself has shipped (3.13-: <glm>/provider/, 3.14+:
// Resources/config/provider/), then a depth-bounded walk of the app
// bundle — the self-healing tier that keeps a future desktop update
// from breaking the bridge again. "" = nothing found.
func findProviderConfig(bundle string) string {
	if p := util.Env("ZCODE_PROVIDER"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	dir := filepath.Dir(bundle)
	for _, cand := range []string{
		filepath.Join(dir, "provider", "zcode-builtin.json"),
		filepath.Join(dir, "..", "config", "provider", "zcode-builtin.json"),
	} {
		if _, err := os.Stat(cand); err == nil {
			return filepath.Clean(cand)
		}
	}
	// walk base: the .app root when the bundle lives in one, else the
	// bundle's own directory (a bare CLI install)
	base := dir
	if app := darwinAppBundleOf(dir); app != "" {
		base = app
	}
	found := ""
	_ = filepath.WalkDir(base, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtrees are skipped, not fatal
		}
		if found != "" {
			return fs.SkipAll
		}
		if e.IsDir() {
			if rel, rerr := filepath.Rel(base, p); rerr == nil && strings.Count(rel, string(filepath.Separator)) >= 6 {
				return fs.SkipDir
			}
			return nil
		}
		if e.Name() == "zcode-builtin.json" {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// Start spawns the app-server child. The read loop runs until Close
// or the child dies; calls in flight when that happens fail.
func Start(opts Options) (*Client, error) {
	if opts.Node == "" {
		opts.Node = resolveNode(opts.Bundle)
	}
	if opts.Bundle == "" {
		opts.Bundle = DefaultBundle
	}
	if opts.Cwd == "" {
		dir, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		opts.Cwd = dir
	}
	cmd := util.HideConsole(exec.Command(opts.Node, opts.Bundle, "app-server", "--json", "--cwd", opts.Cwd))
	// parity env last: the desktop's settings-page proxy/CA wins over
	// inherited shell values, exactly as the desktop itself orders it
	env := append(append(append(os.Environ(), opts.Env...), providerEnv(opts.Bundle)...), desktopParityEnv()...)
	if electronMainExe(opts.Node) {
		// glm/zcode.cjs runs under the install's Electron main binary
		// in plain-Node mode — the desktop's own pairing; without this
		// the child comes up as a Chromium app and wedges at GPU init.
		env = append(env, "ELECTRON_RUN_AS_NODE=1")
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	cmd.Stderr = opts.Stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New(i18n.Sf("zcode: 启动 app-server 失败：找不到 %s——请安装 Node.js（brew install node），或用环境变量 NIUMA_ZCODE_NODE 指定 node 路径", opts.Node))
		}
		return nil, fmt.Errorf("zcode: 启动 app-server 失败: %w", err)
	}
	c := &Client{
		opts: opts, cmd: cmd, stdin: stdin, stdout: stdout,
		waitSem: make(chan struct{}),
		ready:   make(chan struct{}),
		pending: map[int64]chan rpcResult{},
	}
	// cmd.Wait is reaped at the readLoop's tail (NOT here in a racing
	// goroutine): Wait closes the pipe's read side, so running it before
	// the scan loop drains the buffer truncates the child's dying words
	// — the tail events land first, then EOF, then Wait.
	go c.readLoop()
	// Readiness probe: a one-shot session/list whose answer — success
	// OR error — carries our id, so the ready marker flips
	// deterministically even on a server that stays silent until
	// asked. The result is dropped; callers' own lists are unaffected.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = c.ListSessions(ctx)
	}()
	return c, nil
}

// Close stops the child: close stdin → SIGTERM → 3s → SIGKILL
// (the Z-Deck-verified graceful ladder). Pipe-backed clients just
// tear the transport down.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.stdin.Close()
	if c.cmd != nil {
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-c.waitSem:
		case <-time.After(3 * time.Second):
			_ = c.cmd.Process.Kill()
			select {
			case <-c.waitSem:
			case <-time.After(3 * time.Second):
				// waitSem now closes at the readLoop tail; a grandchild
				// holding the stdout write-end keeps EOF (and thus the
				// reap) away — bounded, never hang Close on it.
			}
		}
	}
	return nil
}

func (c *Client) write(obj any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return errors.New(i18n.S("zcode: 通道已关闭（app-server 未在运行）——等待自动重启后重试"))
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// call issues a request and waits for its answer.
func (c *Client) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New(i18n.S("zcode: 通道已关闭（app-server 未在运行）——等待自动重启后重试"))
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	res, ok := <-ch
	if !ok {
		return nil, errors.New(i18n.Sf("zcode: 通道断开（app-server 进程退出），%s 的调用未完成——工作室会自动重启 app-server 并拉回成员会话，稍后重试", method))
	}
	if res.code != 0 {
		return nil, &Error{Code: res.code, Message: res.msg}
	}
	return res.result, nil
}

// readLoop dispatches every stdout line: answers, server requests,
// notifications.
func (c *Client) readLoop() {
	sc := bufio.NewScanner(c.stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20) // events can be large
	for sc.Scan() {
		line := sc.Bytes()
		var env struct {
			// ID stays raw: OUR ids are ints, but server→client
			// requests carry STRING ids ("server-1") — a typed field
			// would fail the whole unmarshal and silently drop the
			// request (the requestRuntimePreferences -32022 timeout).
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			continue // bootstrap debug lines are tolerated
		}
		hasID := len(env.ID) > 0 && string(env.ID) != "null"
		if hasID || env.Method != "" {
			// first protocol-shaped line: the app-server is up — open
			// the boot gate (GET /zcode flips booting → ready)
			c.readyOnce.Do(func() { close(c.ready) })
		}
		switch {
		case hasID && (env.Result != nil || env.Error != nil):
			var id int64
			if err := json.Unmarshal(env.ID, &id); err != nil {
				continue // not one of ours (string ids answer below)
			}
			c.mu.Lock()
			ch, ok := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if !ok {
				continue
			}
			if env.Error != nil {
				ch <- rpcResult{code: env.Error.Code, msg: env.Error.Message}
			} else {
				ch <- rpcResult{result: env.Result}
			}
		case hasID && env.Method != "":
			go c.answerServerRequest(env.ID, env.Method, env.Params)
		case env.Method != "":
			c.deliverNotification(env.Method, env.Params)
		}
	}
	// Child died or stdout closed: fail every pending call.
	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	// Reap here, after the last read: Wait closes the stdout pipe, so
	// racing it against the scan loop drops the death-time tail (in-fly
	// events lost, pending RPCs mis-blamed as channel breaks).
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
	close(c.waitSem)
}

// Hooks are the client's notification sinks; handlers run on the
// read loop's goroutine and must not call back into the client
// synchronously (hand work to a channel instead).
type Hooks struct {
	// Session receives every session/event notification.
	Session func(Event)
	// State receives every state.updated patch (earliest busy/idle
	// signal: patch.status "running" arrives before any event).
	State func(sessionID, status, reason string)
}

// SetHooks wires the notification sinks (safe any time).
func (c *Client) SetHooks(h Hooks) {
	c.mu.Lock()
	c.hooks = h
	c.mu.Unlock()
}

// answerServerRequest applies the fail-close policy: an explicit
// OnReverse first, then deny/decline defaults, and a -32601 error for
// anything unhandled — never an empty result (an empty result can be
// read as consent). id is echoed verbatim (the server's request ids
// are strings; ours are ints — both ride RawMessage).
func (c *Client) answerServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	var p map[string]any
	_ = json.Unmarshal(params, &p)
	// OnReverse is swapped under c.mu (SetReverse); this runs on its own
	// goroutine per request — take the func under the lock, call it out.
	c.mu.Lock()
	onRev := c.opts.OnReverse
	c.mu.Unlock()
	if onRev != nil {
		if res, err := onRev(method, p); err != nil || res != nil {
			if err != nil {
				_ = c.write(map[string]any{"id": id, "error": map[string]any{
					"code": -32601, "message": err.Error()}})
				return
			}
			_ = c.write(map[string]any{"id": id, "result": res})
			return
		}
	}
	switch method {
	case "interaction/requestPermission":
		_ = c.write(map[string]any{"id": id, "result": map[string]any{
			"decision": "deny", "reason": "niuma: fail-close default"}})
	case "interaction/requestUserInput":
		_ = c.write(map[string]any{"id": id, "result": map[string]any{
			"action": "decline", "reason": "niuma: fail-close default"}})
	case "session/requestRuntimePreferences":
		// The app-server asks its host for runtime toggles while a
		// session boots; an error here is not an answer — the server
		// waits out its 15s window and fails the create (-32022). The
		// schema is strict: exactly these keys, optional ones omitted
		// fall to the server's own defaults. Headless member host =
		// the fail-safe set (no native-search side effects, no
		// auto-loaded memory).
		_ = c.write(map[string]any{"id": id, "result": map[string]any{
			"nativeSearchEnhancementsEnabled": false,
			"memoryEnabled":                   false,
		}})
	default:
		_ = c.write(map[string]any{"id": id, "error": map[string]any{
			"code": -32601, "message": "niuma: unhandled server request"}})
	}
}

// hookStallWarn is the tripwire for Hooks-contract violations: handlers
// run on the read loop's goroutine (see Hooks), so one that blocks
// stalls the whole wire — answers, events and reverse requests all sit
// unread while the child times out and re-announces. A handler
// outstaying this budget earns one loud log line per event, naming the
// stall the next forensic session would otherwise have to rediscover.
const hookStallWarn = 5 * time.Second

// hookRan logs a contract-violation tripwire when a hook call that
// started at start outstayed hookStallWarn.
func hookRan(start time.Time, what string) {
	if d := time.Since(start); d > hookStallWarn {
		log.Printf("[zcode] %s 钩子阻塞读循环 %s（违反 Hooks 契约：钩子内不得同步回调 client 或等待注入——期间应答、事件、反向请求全部停摆，子进程会退避重宣告）", what, d)
	}
}

func (c *Client) deliverNotification(method string, params json.RawMessage) {
	switch method {
	case "session/event":
		ev, ok := parseEvent(params)
		if !ok {
			return
		}
		c.mu.Lock()
		f := c.hooks.Session
		c.mu.Unlock()
		if f != nil {
			t0 := time.Now()
			f(ev)
			hookRan(t0, "session/event")
		}
	case "state.updated":
		var p struct {
			SessionID string `json:"sessionId"`
			Patch     struct {
				Status string `json:"status"`
			} `json:"patch"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		c.mu.Lock()
		f := c.hooks.State
		c.mu.Unlock()
		if f != nil {
			t0 := time.Now()
			f(p.SessionID, p.Patch.Status, p.Reason)
			hookRan(t0, "state.updated")
		}
	default:
		// v4/telemetry/event, process/*, computer-use/*, startup/*:
		// informational, ignored by contract.
	}
}

// HomePath resolves a path under ~/.zcode (helper for callers that
// need to inspect session storage).
func HomePath(rel string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zcode", rel), nil
}
