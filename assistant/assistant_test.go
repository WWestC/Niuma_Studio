package assistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/dispatch"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// fakeDriver is the in-test SessionDriver: one canned session, every
// send recorded, no child process anywhere near the operator's ZCode.
type fakeDriver struct {
	mu      sync.Mutex
	sends   []string // the delivered contents, in order
	sendErr error    // when set, Send fails with this (cleared per call)
	onSend  func(content string)

	created    int                    // CreateSession calls — adoption must not birth
	createIDs  []string               // session ids to hand out, in order (probe first, then the real session); default sess-assistant
	setModels  []zcode.ModelSelection // SetModel calls, in order (the probe's low-reasoning pin)
	subErr     error                  // one-shot: the next Subscribe fails with this
	resumeErr  error                  // one-shot: the next ResumeSession fails with this
	subscribed []string               // Subscribe attempts, in order (the ladder trace)

	// the probe's MCP-fleet override face (mcpFleetDriver): CreateSessionMCP
	// records the fleets it was handed; mcpErr (one-shot) simulates an old
	// CLI whose strict schema rejects the mcpServers param wholesale.
	mcpOverrides [][]zcode.MCPServerSpec
	mcpErr       error
}

func (f *fakeDriver) CreateSession(ctx context.Context, workspace, mode string) (string, error) {
	f.mu.Lock()
	f.created++
	id := "sess-assistant"
	if len(f.createIDs) > 0 {
		id = f.createIDs[0]
		f.createIDs = f.createIDs[1:]
	}
	f.mu.Unlock()
	return id, nil
}

// CreateSessionMCP is the fake's mcpFleetDriver face: records the
// override, honors the one-shot rejection (an old CLI), else behaves
// exactly like CreateSession.
func (f *fakeDriver) CreateSessionMCP(ctx context.Context, workspace, mode string, servers []zcode.MCPServerSpec) (string, error) {
	f.mu.Lock()
	f.mcpOverrides = append(f.mcpOverrides, servers)
	err := f.mcpErr
	f.mcpErr = nil
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	return f.CreateSession(ctx, workspace, mode)
}
func (f *fakeDriver) SetModel(ctx context.Context, sessionID string, sel zcode.ModelSelection) error {
	f.mu.Lock()
	f.setModels = append(f.setModels, sel)
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) ResumeSession(ctx context.Context, sessionID string) (*zcode.ResumeResult, error) {
	f.mu.Lock()
	err := f.resumeErr
	f.resumeErr = nil
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return nil, nil
}
func (f *fakeDriver) Subscribe(ctx context.Context, sessionID string) (*zcode.SubscribeResult, error) {
	f.mu.Lock()
	f.subscribed = append(f.subscribed, sessionID)
	err := f.subErr
	f.subErr = nil
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &zcode.SubscribeResult{SessionID: sessionID}, nil
}
func (f *fakeDriver) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	f.sends = append(f.sends, content)
	hook := f.onSend
	err := f.sendErr
	f.sendErr = nil // one-shot: the next send succeeds unless re-armed
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if hook != nil {
		hook(content)
	}
	return &zcode.SendAck{SessionID: sessionID, Accepted: true}, nil
}

// failNextSend arms the next Send with err (the -32010 simulation and
// kin).
func (f *fakeDriver) failNextSend(err error) {
	f.mu.Lock()
	f.sendErr = err
	f.mu.Unlock()
}

// failNextSubscribe arms the next Subscribe with err (the cold -32004
// the adoption ladder rides).
func (f *fakeDriver) failNextSubscribe(err error) {
	f.mu.Lock()
	f.subErr = err
	f.mu.Unlock()
}

// failNextResume arms the next ResumeSession with err (the dead
// binding: session files gone).
func (f *fakeDriver) failNextResume(err error) {
	f.mu.Lock()
	f.resumeErr = err
	f.mu.Unlock()
}

// births reports the CreateSession count (read after the turn lands).
func (f *fakeDriver) births() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created
}

// modelSelections returns the SetModel calls seen so far (the probe's
// low-reasoning pin reads this).
func (f *fakeDriver) modelSelections() []zcode.ModelSelection {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]zcode.ModelSelection(nil), f.setModels...)
}

// mcpFleets returns the per-create MCP fleet overrides seen so far.
func (f *fakeDriver) mcpFleets() [][]zcode.MCPServerSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]zcode.MCPServerSpec(nil), f.mcpOverrides...)
}

// recorded returns the sends seen so far (the turn goroutine writes,
// the test reads — guarded like the real bridge would be).
func (f *fakeDriver) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sends...)
}

// newTest builds the assistant over a fake driver and hands back the
// observer the fleet would have registered.
func newTest(t *testing.T, cfg Config) (*Assistant, *fakeDriver, *dispatch.Observer) {
	t.Helper()
	driver := &fakeDriver{}
	var obs *dispatch.Observer
	a := New(func() dispatch.SessionDriver { return driver },
		func(o *dispatch.Observer) { obs = o }, cfg)
	if obs == nil || obs.OnEvent == nil || obs.Owns == nil {
		t.Fatal("New did not wire the observer callbacks")
	}
	return a, driver, obs
}

func testConfig() Config {
	return Config{
		Workspace: "/tmp/niuma-test-ws",
		Manual:    "手册正文（测试用）",
		Digest:    func() string { return "在线成员：房主（人类玩家）" },
		// tests must never write the operator's live task-index sqlite
		RegisterIndex: func(zcode.TaskIndexEntry) error { return nil },
	}
}

func waitStatus(t *testing.T, a *Assistant, want string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if a.State().Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status never reached %q (now %q)", want, a.State().Status)
}

// waitSends waits until the driver recorded want sends — the honest
// sync point for "the turn goroutine delivered" (Ask flips the status
// synchronously, the delivery lands a beat later).
func waitSends(t *testing.T, d *fakeDriver, want int) []string {
	t.Helper()
	for i := 0; i < 400; i++ {
		if got := d.recorded(); len(got) >= want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("driver saw %d sends, want %d", len(d.recorded()), want)
	return nil
}

// ev builds one session/event notification payload for the observer.
func ev(sessionID string, payload map[string]any) zcode.Event {
	return zcode.Event{SessionID: sessionID, Payload: payload}
}

// turnCompleted 造类型化 turn.completed（回合真终点：每回合恰一次、
// payload.response 带最终全文——落册只认它）。
func turnCompleted(sessionID, content string) zcode.Event {
	return zcode.Event{SessionID: sessionID, Type: "turn.completed",
		Payload: map[string]any{"response": content, "resultType": "success"}}
}

// TestBoundaryFramesDoNotDoubleAppend pins the 复读根治 (房主实录
// 2026-10-03): a turn's tool loop emits a stopReason=="stop" frame per
// model request and the model often restates its closing words
// verbatim — only the typed turn.completed may land a row.
func TestBoundaryFramesDoNotDoubleAppend(t *testing.T) {
	a, driver, obs := newTest(t, testConfig())

	driver.onSend = func(content string) {
		go func() {
			obs.OnEvent(ev("sess-assistant", map[string]any{
				"querySource": "main_turn", "stopReason": "stop", "content": "收尾话"}))
			obs.OnEvent(ev("sess-assistant", map[string]any{
				"querySource": "main_turn", "stopReason": "stop", "content": "收尾话"}))
			obs.OnEvent(turnCompleted("sess-assistant", "收尾话"))
		}()
	}
	if err := a.Ask("核验一下"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitStatus(t, a, StatusIdle)

	st := a.State()
	if len(st.Messages) != 2 { // user question + ONE assistant answer
		t.Fatalf("transcript has %d rows, want 2: %+v", len(st.Messages), st.Messages)
	}
	if last := st.Messages[1]; last.Role != "assistant" || last.Text != "收尾话" {
		t.Errorf("final row = %+v", last)
	}
}

// TestAskLifecycle pins the happy path: the first ask births the
// session carrying the system prompt plus the manual and the question;
// streamed deltas build the partial; the typed turn.completed
// finalizes the answer and re-arms the idle state; the second ask
// rides the digest prefix instead.
func TestAskLifecycle(t *testing.T) {
	a, driver, obs := newTest(t, testConfig())

	driver.onSend = func(content string) {
		go func() {
			obs.OnEvent(ev("sess-assistant", map[string]any{"kind": "text_delta", "delta": "第一"}))
			obs.OnEvent(ev("sess-assistant", map[string]any{"kind": "text_delta", "delta": "句"}))
			obs.OnEvent(turnCompleted("sess-assistant", "第一句完整回答"))
		}()
	}
	if err := a.Ask("这是什么工作室？"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitStatus(t, a, StatusIdle)

	sends := waitSends(t, driver, 1)
	if len(sends) != 1 {
		t.Fatalf("first turn sent %d contents, want 1", len(sends))
	}
	first := sends[0]
	for _, want := range []string{"使用顾问", "手册正文（测试用）", "这是什么工作室？"} {
		if !strings.Contains(first, want) {
			t.Errorf("first send missing %q", want)
		}
	}

	st := a.State()
	if len(st.Messages) != 2 { // user question + assistant answer
		t.Fatalf("transcript has %d rows, want 2: %+v", len(st.Messages), st.Messages)
	}
	if last := st.Messages[1]; last.Role != "assistant" || last.Text != "第一句完整回答" {
		t.Errorf("final row = %+v", last)
	}
	if st.Partial != "" {
		t.Errorf("partial survived the terminal: %q", st.Partial)
	}

	// second ask: digest background, no system prompt repeat (the auto
	// finale hook was turn 1's only — turn 2's events are ours to drive)
	driver.onSend = nil
	if err := a.Ask("现在有谁在线？"); err != nil {
		t.Fatalf("ask 2: %v", err)
	}
	sends2 := waitSends(t, driver, 2)
	second := sends2[len(sends2)-1]
	if !strings.Contains(second, "在线成员：房主") {
		t.Errorf("second send lacks the digest: %q", second)
	}
	if strings.Contains(second, "手册正文（测试用）") {
		t.Errorf("second send re-carries the manual (bloat)")
	}
	if !strings.Contains(second, "现在有谁在线？") {
		t.Errorf("second send lacks the question")
	}
	// unrelated sessions never reach us
	obs.OnEvent(turnCompleted("sess-someone-else", "别家的回答"))
	if strings.Contains(a.State().Partial, "别家") || len(a.State().Messages) != 3 {
		t.Errorf("foreign session leaked into the transcript")
	}
	// finish the second turn for cleanliness
	obs.OnEvent(turnCompleted("sess-assistant", "房主在线"))
	waitStatus(t, a, StatusIdle)
}

// TestBusyGuard pins the one-turn-at-a-time rule and the ErrBusy race
// guard.
func TestBusyGuard(t *testing.T) {
	a, _, _ := newTest(t, testConfig())
	if err := a.Ask("第一问"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if err := a.Ask("第二问"); err != ErrBusy {
		t.Fatalf("second concurrent ask = %v, want ErrBusy", err)
	}
	// the empty and the oversized questions never start a turn
	if err := a.Ask("   "); err == nil {
		t.Error("blank question accepted")
	}
	if err := a.Ask(strings.Repeat("长", 20_000)); err == nil {
		t.Error("oversized question accepted")
	}
}

// TestFailTerminals pins both failure terminals: the provider-side
// state patch (prompt_failed never emits a stopReason envelope) and
// the abnormal stop reason both land as error rows, never wedges.
func TestFailTerminals(t *testing.T) {
	a, driver, obs := newTest(t, testConfig())
	if err := a.Ask("会失败的问题"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitSends(t, driver, 1) // delivered — the session id is stored, the turn armed
	obs.OnState("sess-assistant", "error", "turn prompt_failed: provider 500")
	waitStatus(t, a, StatusIdle)
	st := a.State()
	if n := len(st.Messages); n != 2 {
		t.Fatalf("transcript rows = %d, want 2 (question + error)", n)
	}
	if last := st.Messages[1]; last.Role != "error" || !strings.Contains(last.Text, "prompt_failed") {
		t.Errorf("error row = %+v", last)
	}

	// abnormal stop reason on a healthy turn
	if err := a.Ask("再来一问"); err != nil {
		t.Fatalf("ask 2: %v", err)
	}
	waitSends(t, driver, 2)
	obs.OnEvent(ev("sess-assistant", map[string]any{
		"querySource": "main_turn", "stopReason": "max_tokens", "content": ""}))
	waitStatus(t, a, StatusIdle)
	if last := a.State().Messages[len(a.State().Messages)-1]; last.Role != "error" || !strings.Contains(last.Text, "max_tokens") {
		t.Errorf("abnormal-stop row = %+v", last)
	}
}

// TestReverseFailClosed pins the advisor contract: every permission
// ask and in-session question is denied/declined IMMEDIATELY (no
// window, no room leak) and runtime-preference/unknown methods fall
// to the client defaults.
func TestReverseFailClosed(t *testing.T) {
	a, _, _ := newTest(t, testConfig())
	res, err := a.onReverse("interaction/requestPermission", map[string]any{"sessionId": "sess-assistant"})
	if err != nil {
		t.Fatalf("permission: %v", err)
	}
	if m, _ := res.(map[string]any); m["decision"] != "deny" {
		t.Errorf("permission answer = %v, want deny", res)
	}
	res, err = a.onReverse("interaction/requestUserInput", nil)
	if err != nil {
		t.Fatalf("user-input: %v", err)
	}
	if m, _ := res.(map[string]any); m["action"] != "decline" {
		t.Errorf("user-input answer = %v, want decline", res)
	}
	if res, err := a.onReverse("session/requestRuntimePreferences", nil); res != nil || err != nil {
		t.Errorf("runtime preferences = (%v, %v), want the client defaults", res, err)
	}
}

// TestStaleWatchdog pins the turn-stuck backstop and the LATE-ANSWER
// salvage: a turn that neither ends nor streams past turnTimeout is
// parked with an error row (the composer frees up), but the terminal
// envelope that lands afterwards — the model link finally reconnected
// minutes later — still records the answer. A replayed envelope after
// that must not duplicate it.
func TestStaleWatchdog(t *testing.T) {
	a, driver, obs := newTest(t, testConfig())
	if err := a.Ask("会卡住的问题"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitSends(t, driver, 1)
	a.mu.Lock()
	a.turnAt = time.Now().Add(-turnTimeout - time.Second)
	a.mu.Unlock()
	st := a.State() // expireStale fires inside
	if st.Status != StatusIdle {
		t.Fatalf("stale turn survived the watchdog: %+v", st)
	}
	if st.Partial != "" {
		t.Errorf("partial survived the watchdog: %q", st.Partial)
	}
	last := st.Messages[len(st.Messages)-1]
	if last.Role != "error" || !strings.Contains(last.Text, "超时") {
		t.Errorf("watchdog row = %+v", last)
	}
	if a.State().Since != 0 {
		t.Errorf("idle state carries a since age")
	}

	// the late terminal lands even though the panel already gave up
	obs.OnEvent(turnCompleted("sess-assistant", "迟到但完整的回答"))
	st = a.State()
	if got := st.Messages[len(st.Messages)-1]; got.Role != "assistant" || got.Text != "迟到但完整的回答" {
		t.Errorf("late answer dropped: %+v", got)
	}
	n := len(st.Messages)
	// …but only once — a replayed envelope (re-subscribe from cursor 0)
	// must not duplicate the row
	obs.OnEvent(turnCompleted("sess-assistant", "迟到但完整的回答"))
	if got := len(a.State().Messages); got != n {
		t.Errorf("replayed terminal duplicated the answer (%d rows → %d)", n, got)
	}
}

// TestPromptRunningRefusal pins the -32010 path: re-asking while the
// (expired) server turn still churns refuses with wording that says
// the question did NOT go out, yet the old turn's late answer still
// lands afterwards.
func TestPromptRunningRefusal(t *testing.T) {
	a, driver, obs := newTest(t, testConfig())
	if err := a.Ask("第一问"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitSends(t, driver, 1)
	// park the first turn past the watchdog
	a.mu.Lock()
	a.turnAt = time.Now().Add(-turnTimeout - time.Second)
	a.mu.Unlock()
	_ = a.State()

	driver.failNextSend(&zcode.Error{Code: zcode.ErrPromptRunning, Message: "A prompt is already running"})
	if err := a.Ask("补一问"); err != nil {
		t.Fatalf("ask 2: %v", err) // accepted locally; the refusal is a row
	}
	waitSends(t, driver, 2)
	waitStatus(t, a, StatusIdle)
	last := a.State().Messages[len(a.State().Messages)-1]
	if last.Role != "error" || !strings.Contains(last.Text, "没有送出") {
		t.Errorf("refusal row = %+v", last)
	}
	// the still-running first turn finishes late — the answer lands
	obs.OnEvent(turnCompleted("sess-assistant", "第一问的迟到回答"))
	if got := a.State().Messages[len(a.State().Messages)-1]; got.Role != "assistant" {
		t.Errorf("late answer after refusal dropped: %+v", got)
	}
}

// TestArchiveOnClose pins the sidebar contract's close half: Archive
// folds the born session's row; a never-born assistant is a no-op.
func TestArchiveOnClose(t *testing.T) {
	var archived []string
	cfg := testConfig()
	cfg.ArchiveIndex = func(workspace, sessionID string) error {
		archived = append(archived, workspace+"|"+sessionID)
		return nil
	}
	a, driver, _ := newTest(t, cfg)
	a.Archive() // never born: silent no-op
	if len(archived) != 0 {
		t.Fatalf("unborn assistant archived %+v", archived)
	}
	if err := a.Ask("问"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitSends(t, driver, 1)
	a.Archive()
	if len(archived) != 1 || archived[0] != "/tmp/niuma-test-ws|sess-assistant" {
		t.Fatalf("archive calls = %+v", archived)
	}
	a.Archive() // idempotent at the contract level (fold is own-row guarded)
	if len(archived) != 2 {
		t.Fatalf("second archive refused: %+v", archived)
	}
}

// TestAdoptAcrossBoots pins the stable identity: the first ask of a
// boot ADOPTS the previous boot's session (the binding file names it,
// the subscribe ladder proves it) — no fresh birth, the SAME sidebar
// row re-pinned, no system prompt repeat (the session carries its
// context), the digest prefix instead.
func TestAdoptAcrossBoots(t *testing.T) {
	file := filepath.Join(t.TempDir(), "assistant.json")
	if err := os.WriteFile(file, []byte(`{"session":"sess-prev"}`), 0o600); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	var registered []string
	cfg := testConfig()
	cfg.SessionFile = file
	cfg.RegisterIndex = func(e zcode.TaskIndexEntry) error {
		registered = append(registered, e.SessionID)
		return nil
	}
	a, driver, obs := newTest(t, cfg)

	driver.onSend = func(content string) {
		go obs.OnEvent(turnCompleted("sess-prev", "复用会话的回答"))
	}
	if err := a.Ask("重启后第一问"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitStatus(t, a, StatusIdle)

	if n := driver.births(); n != 0 {
		t.Errorf("adoption birthed %d fresh sessions, want 0", n)
	}
	if len(registered) != 1 || registered[0] != "sess-prev" {
		t.Errorf("sidebar rows = %+v, want the SAME session re-pinned in place", registered)
	}
	sends := driver.recorded()
	if len(sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(sends))
	}
	if strings.Contains(sends[0], "手册正文（测试用）") {
		t.Errorf("adopted session re-carries the system prompt (bloat)")
	}
	if !strings.Contains(sends[0], "在线成员：房主") || !strings.Contains(sends[0], "重启后第一问") {
		t.Errorf("adopted first send = %q", sends[0])
	}
	// the binding survives for the next boot
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "sess-prev") {
		t.Errorf("binding file = %q (%v), want it to keep naming the session", string(b), err)
	}
}

// TestAdoptFailsBirthsFresh pins the fallback half: a binding whose
// session is gone (cold subscribe, dead resume) must NOT be reused —
// the dead row folds, a fresh session births carrying the system
// prompt, and the binding is rewritten to it.
func TestAdoptFailsBirthsFresh(t *testing.T) {
	file := filepath.Join(t.TempDir(), "assistant.json")
	if err := os.WriteFile(file, []byte(`{"session":"sess-gone"}`), 0o600); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	var registered, folded []string
	cfg := testConfig()
	cfg.SessionFile = file
	cfg.RegisterIndex = func(e zcode.TaskIndexEntry) error {
		registered = append(registered, e.SessionID)
		return nil
	}
	cfg.ArchiveIndex = func(workspace, sessionID string) error {
		folded = append(folded, sessionID)
		return nil
	}
	a, driver, obs := newTest(t, cfg)

	// the ladder dies on the resume step: the session files are gone
	driver.failNextSubscribe(&zcode.Error{Code: zcode.ErrSessionNotActive, Message: "cold"})
	driver.failNextResume(errors.New("session file not found"))

	driver.onSend = func(content string) {
		go obs.OnEvent(turnCompleted("sess-assistant", "新会话的回答"))
	}
	if err := a.Ask("识别不了就新建"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitStatus(t, a, StatusIdle)

	if n := driver.births(); n != 1 {
		t.Errorf("births = %d, want 1 (fallback)", n)
	}
	if len(folded) != 1 || folded[0] != "sess-gone" {
		t.Errorf("folded rows = %+v, want the dead binding folded", folded)
	}
	if len(registered) != 1 || registered[0] != "sess-assistant" {
		t.Errorf("sidebar rows = %+v, want the fresh session pinned", registered)
	}
	sends := driver.recorded()
	if len(sends) != 1 || !strings.Contains(sends[0], "手册正文（测试用）") {
		t.Fatalf("fresh birth's first send = %+v, want the system prompt + manual", sends)
	}
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "sess-assistant") {
		t.Errorf("binding file = %q (%v), want it rewritten to the fresh session", string(b), err)
	}
}

// TestUnavailable pins the dispatch-off shape: the panel's face still
// answers, with available:false and the reason.
func TestUnavailable(t *testing.T) {
	a := New(nil, nil, Config{})
	st := a.State()
	if st.Available || st.Reason == "" || st.Status != StatusIdle {
		t.Errorf("unavailable state = %+v", st)
	}
	if err := a.Ask("你好"); err == nil || !strings.Contains(err.Error(), "调度") {
		t.Errorf("ask while unavailable = %v", err)
	}
}

// --- the boot warm-up -------------------------------------------------------

// waitWarm polls State until the warm-up reaches the wanted verdict —
// the async arm's honest sync point.
func waitWarm(t *testing.T, a *Assistant, want string) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if a.State().Link == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("warm-up never reached %q (now %q)", want, a.State().Link)
}

// TestWarmupLifecycle pins the isolated warm-up probe: it runs on its
// OWN throwaway session — bare text (no charter, no manual) at LOW
// reasoning — its reply NEVER reaches the transcript, and the channel
// health lands ok with a latency. A second Warmup is a no-op. The
// real first ask then births the real session and DOES carry the
// charter + manual (a fresh birth, as always — the probe never did).
func TestWarmupLifecycle(t *testing.T) {
	cfg := testConfig()
	cfg.Warmup = true
	a, driver, obs := newTest(t, cfg)
	driver.createIDs = []string{"sess-probe", "sess-assistant"}
	a.Warmup()
	waitWarm(t, a, "warming")
	sends := waitSends(t, driver, 1) // the probe is out (and its session recorded)
	if !strings.Contains(sends[0], "通道预热") {
		t.Errorf("probe send = %q", sends[0])
	}
	if strings.Contains(sends[0], "手册正文（测试用）") {
		t.Errorf("probe carries the manual (want bare): %q", sends[0])
	}
	for _, sel := range driver.modelSelections() {
		if sel.Options == nil || sel.Options.ReasoningLevel != "low" {
			t.Errorf("probe SetModel = %+v, want low reasoning", sel)
		}
	}

	obs.OnEvent(turnCompleted("sess-probe", "就绪"))
	waitWarm(t, a, "ok")
	waitStatus(t, a, StatusIdle)

	st := a.State()
	if len(st.Messages) != 0 {
		t.Fatalf("warm-up leaked into the transcript: %+v", st.Messages)
	}
	if st.LinkMS < 0 {
		t.Errorf("warm latency = %d", st.LinkMS)
	}
	// once per process
	a.Warmup()
	if a.State().Link != "ok" || a.State().Status != StatusIdle {
		t.Errorf("second Warmup re-armed the turn")
	}
	// the real first ask births the real session — fresh birth, so the
	// charter + manual ride it (the probe is bare by design)
	if err := a.Ask("真问题"); err != nil {
		t.Fatalf("ask after warm-up: %v", err)
	}
	sends2 := waitSends(t, driver, 2)
	if !strings.Contains(sends2[1], "手册正文（测试用）") || !strings.Contains(sends2[1], "真问题") {
		t.Errorf("first ask = %q, want charter + question", sends2[1])
	}
	obs.OnEvent(turnCompleted("sess-assistant", "真答案"))
	waitStatus(t, a, StatusIdle)
}

// TestWarmupBareMCP pins the probe's bare-fleet birth: the throwaway
// session is created through the MCP-override verb carrying the
// dead-port fleet (probeMCP — the probe measures the LINK, the fleet's
// tool-surface wait is dead weight), and a driver that rejects the
// verb (an old CLI's strict schema) falls back to the plain create so
// the warm-up still runs, just with the full fleet.
func TestWarmupBareMCP(t *testing.T) {
	cfg := testConfig()
	cfg.Warmup = true

	a, driver, obs := newTest(t, cfg)
	driver.createIDs = []string{"sess-probe"}
	a.Warmup()
	waitWarm(t, a, "warming")
	waitSends(t, driver, 1)
	fleets := driver.mcpFleets()
	if len(fleets) != 1 || len(fleets[0]) != 1 {
		t.Fatalf("probe fleet overrides = %+v, want exactly one override of one entry", fleets)
	}
	spec := fleets[0][0]
	if spec.Name != probeMCP[0].Name || spec.URL != probeMCP[0].URL || spec.Isolation != "session" {
		t.Fatalf("probe fleet spec = %+v, want the dead-port probeMCP entry", spec)
	}
	obs.OnEvent(turnCompleted("sess-probe", "就绪"))
	waitWarm(t, a, "ok")

	// 回退面：带 override 的 create 被拒 → 退回普通 create，预热照常落地
	a2, driver2, obs2 := newTest(t, cfg)
	driver2.createIDs = []string{"sess-probe2"}
	driver2.mcpErr = errors.New("strict schema rejects mcpServers")
	a2.Warmup()
	waitWarm(t, a2, "warming")
	waitSends(t, driver2, 1)
	if fleets2 := driver2.mcpFleets(); len(fleets2) != 1 {
		t.Fatalf("override attempt count = %d, want exactly one before the fallback", len(fleets2))
	}
	if n := driver2.births(); n != 1 {
		t.Fatalf("plain-create fallback count = %d, want 1", n)
	}
	obs2.OnEvent(turnCompleted("sess-probe2", "就绪"))
	waitWarm(t, a2, "ok")
}

// TestWarmGate pins the splash door's read on the warm-up: disabled by
// default (the zero-value config never holds the door), "warming" with
// a running clock while the hidden probe is in flight, and closed the
// moment the verdict lands — the boot gate opens on ok and fail alike.
func TestWarmGate(t *testing.T) {
	a := New(nil, nil, Config{})
	if warming, _, _ := a.WarmGate(); warming {
		t.Errorf("disabled warm-up holds the gate")
	}

	cfg := testConfig()
	cfg.Warmup = true
	a, driver, obs := newTest(t, cfg)
	driver.createIDs = []string{"sess-probe", "sess-assistant"}
	a.Warmup()
	waitWarm(t, a, "warming")
	if warming, el, _ := a.WarmGate(); !warming || el < 0 {
		t.Errorf("mid-warm WarmGate = (%v, %v), want (true, ≥0)", warming, el)
	}
	waitSends(t, driver, 1) // the probe session id must be recorded before events match
	obs.OnEvent(turnCompleted("sess-probe", "就绪"))
	waitWarm(t, a, "ok")
	if warming, _, _ := a.WarmGate(); warming {
		t.Errorf("landed warm-up still holds the gate")
	}
}

// TestWarmupParallel pins the isolation headline: a question that
// arrives mid-warm-up goes out IMMEDIATELY on the real session (no
// queue, no waiting behind the probe), the probe terminal and the
// ask's terminal land independently by session id, and the probe's
// one-word reply never shows in the transcript.
func TestWarmupParallel(t *testing.T) {
	cfg := testConfig()
	cfg.Warmup = true
	a, driver, obs := newTest(t, cfg)
	driver.createIDs = []string{"sess-probe", "sess-assistant"}
	a.Warmup()
	waitWarm(t, a, "warming")
	waitSends(t, driver, 1) // probe session created first — serializes the id handout

	if err := a.Ask("并行的问题"); err != nil {
		t.Fatalf("ask during warm-up: %v", err)
	}
	sends := waitSends(t, driver, 2) // probe AND ask both out, nothing queued
	if !strings.Contains(sends[1], "并行的问题") {
		t.Errorf("ask send = %q", sends[1])
	}
	if err := a.Ask("第二个"); err != ErrBusy {
		t.Errorf("ask while the real turn runs = %v, want ErrBusy", err)
	}

	// the probe terminal lands on ITS session: health flips ok while
	// the real turn keeps running — the two never touch
	obs.OnEvent(turnCompleted("sess-probe", "就绪"))
	waitWarm(t, a, "ok")
	waitStatus(t, a, StatusRunning) // the real ask is still the turn

	obs.OnEvent(turnCompleted("sess-assistant", "并行问题的回答"))
	waitStatus(t, a, StatusIdle)
	st := a.State()
	if len(st.Messages) != 2 || st.Messages[0].Text != "并行的问题" || st.Messages[1].Text != "并行问题的回答" {
		t.Fatalf("transcript = %+v, want the Q&A only (no probe leak)", st.Messages)
	}
}

// TestWarmupFailPaths pins the failure verdicts: a send error and a
// fail state patch on the probe session both resolve the warm-up as
// channel fail (health only, no transcript row) — and the machine was
// never held, so a real ask runs to its terminal untouched throughout.
func TestWarmupFailPaths(t *testing.T) {
	cfg := testConfig()
	cfg.Warmup = true

	// 1) the send itself fails (probe birth ok, deliver not)
	a, driver, _ := newTest(t, cfg)
	driver.createIDs = []string{"sess-probe", "sess-assistant"}
	driver.failNextSend(fmt.Errorf("boom"))
	a.Warmup()
	waitWarm(t, a, "fail")
	if st := a.State(); !strings.Contains(st.LinkNote, "boom") {
		t.Errorf("fail verdict carries no note: %+v", st)
	}
	waitStatus(t, a, StatusIdle) // the machine was never held

	// 2) a fail state patch on the probe session (send ok, the turn
	// died server-side)
	b, bdriver, bobs := newTest(t, cfg)
	bdriver.createIDs = []string{"sess-probe", "sess-assistant"}
	b.Warmup()
	waitWarm(t, b, "warming")
	waitSends(t, bdriver, 1)
	bobs.OnState("sess-probe", "error", "turn prompt_failed: provider 500")
	waitWarm(t, b, "fail")
	if st := b.State(); len(st.Messages) != 0 {
		t.Fatalf("probe failure leaked into the transcript: %+v", st.Messages)
	}

	// and a real ask still runs to its terminal untouched
	if err := b.Ask("真问题"); err != nil {
		t.Fatalf("ask after warm fail: %v", err)
	}
	sends := waitSends(t, bdriver, 2)
	if !strings.Contains(sends[1], "真问题") {
		t.Errorf("ask send = %q", sends[1])
	}
	bobs.OnEvent(turnCompleted("sess-assistant", "真答案"))
	waitStatus(t, b, StatusIdle)
	if st := b.State(); len(st.Messages) != 2 {
		t.Fatalf("transcript = %+v, want the Q&A only", st.Messages)
	}
}

// TestWarmupTimeout pins the probe's OWN short watchdog: a probe that
// never hears back resolves as fail (预热超时) once WarmupTimeout
// lapses — far shorter than the real turn's backstop — and the
// machine stays idle (the warm-up never held it).
func TestWarmupTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.Warmup = true
	cfg.WarmupTimeout = 30 * time.Millisecond
	a, driver, _ := newTest(t, cfg)
	driver.createIDs = []string{"sess-probe", "sess-assistant"}
	a.Warmup()
	waitSends(t, driver, 1)
	time.Sleep(60 * time.Millisecond)
	st := a.State() // expireStale fires inside
	if st.Link != "fail" || !strings.Contains(st.LinkNote, "预热超时") {
		t.Fatalf("timed-out warm-up = link %q note %q", st.Link, st.LinkNote)
	}
	if st.Status != StatusIdle {
		t.Errorf("timed-out warm-up left the machine %q", st.Status)
	}
}

// TestProbeLateEnvelopeIgnored pins the isolation payoff: a probe
// envelope that arrives AFTER the warm-up resolved (the watchdog
// abandoned the probe, or a duplicate terminal) lands on a session id
// nothing listens to — no panic, no transcript row, no verdict flip.
// This is the correctness property the shared-session design could
// never offer (no turn id in the envelope to tell a late probe from
// the queued ask's answer).
func TestProbeLateEnvelopeIgnored(t *testing.T) {
	cfg := testConfig()
	cfg.Warmup = true
	cfg.WarmupTimeout = 20 * time.Millisecond
	a, driver, obs := newTest(t, cfg)
	driver.createIDs = []string{"sess-probe", "sess-assistant"}
	a.Warmup()
	waitWarm(t, a, "fail") // the watchdog resolves first (State polls expireStale)

	obs.OnEvent(turnCompleted("sess-probe", "就绪"))
	if st := a.State(); st.Link != "fail" || len(st.Messages) != 0 {
		t.Fatalf("late probe envelope flipped state: link %q msgs %+v", st.Link, st.Messages)
	}
}

// TestWarmupDisabled pins the opt-out: Warmup zero-value (the package
// default) and a driverless boot are both silent no-ops.
func TestWarmupDisabled(t *testing.T) {
	a, driver, _ := newTest(t, testConfig()) // Warmup not set
	a.Warmup()
	if st := a.State(); st.Status != StatusIdle || st.Link != "" || len(driver.recorded()) != 0 {
		t.Fatalf("disabled warm-up ran: %+v", st)
	}
	b := New(func() dispatch.SessionDriver { return nil }, nil, Config{Warmup: true})
	b.Warmup() // no bridge: no-op, never panics
	if st := b.State(); st.Link != "" || st.Status != StatusIdle {
		t.Fatalf("driverless warm-up ran: %+v", st)
	}
}

// TestEnvTurnTimeout pins the NIUMA_TURN_TIMEOUT parser: whole minutes
// in range pass, everything else falls back to the default (0).
func TestEnvTurnTimeout(t *testing.T) {
	cases := map[string]time.Duration{
		"":      0,
		"20":    20 * time.Minute,
		"1":     time.Minute,
		"60":    time.Hour,
		"0":     0,
		"61":    0,
		"-5":    0,
		"abc":   0,
		" 15 ":  15 * time.Minute,
		"1.5":   0,
		"15min": 0,
	}
	for in, want := range cases {
		if got := EnvTurnTimeout(in); got != want {
			t.Errorf("EnvTurnTimeout(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestEnvWarmGate pins the NIUMA_WARMUP_GATE parser: whole seconds in
// range pass, everything else falls back to the caller's default (0).
func TestEnvWarmGate(t *testing.T) {
	cases := map[string]time.Duration{
		"":     0,
		"120":  120 * time.Second,
		"5":    5 * time.Second,
		"600":  600 * time.Second,
		"0":    0,
		"4":    0,
		"601":  0,
		"-90":  0,
		"abc":  0,
		" 45 ": 45 * time.Second,
		"1.5":  0,
		"90s":  0,
	}
	for in, want := range cases {
		if got := EnvWarmGate(in); got != want {
			t.Errorf("EnvWarmGate(%q) = %v, want %v", in, got, want)
		}
	}
}
