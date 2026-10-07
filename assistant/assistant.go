// Package assistant is the workbench's 小助手: the host's usage
// advisor, living in the app and answering through the SAME zcode
// app-server child the member dispatchers ride (the deep ZCode
// binding — no separate AI key onboarding, the session drinks from
// the operator's existing provider config).
//
// Shape: ONE yolo session for the assistant, born lazily on the first
// question and then KEPT ACROSS BOOTS — the binding file names it,
// the next boot adopts it (subscribe → resume ladder), so ZCode's
// left sidebar grows no second row per restart; only an unadoptable
// binding (session deleted) births fresh and folds the dead row. The
// birth carries the system prompt plus the embedded manual;
// later questions ride a compact live digest of the office. Turn
// replies arrive over the shared bridge's observer fan-out (dispatch.
// Observer): streamed text_delta builds a poll-visible partial, the
// stopReason envelope finalizes the answer. The assistant is an
// advisor by contract — reverse requests fail closed immediately
// (deny/decline), so a tool-happy model can never touch the office
// nor leak permission asks into the room.
package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/dispatch"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// Config shapes the assistant.
type Config struct {
	// Workspace is the assistant session's home (the office's own
	// workspace — the lobby project's folder, like the fleet's
	// IndexWorkspace).
	Workspace string
	// Model/Reasoning select what the session runs on (the process
	// defaults DISPATCH_MODEL/GLM-5.3 + high; empty Model keeps the
	// CLI default).
	Model     string
	Reasoning string
	// Manual is the usage knowledge base injected with the first
	// question (main passes the embedded kb manual).
	Manual string
	// Digest, when set, renders the compact live office state
	// (members, active projects) prefixed onto every non-first
	// question — background only, the model is told not to answer it.
	Digest func() string
	// Title is the ZCode sidebar row's label (registered pinned, the
	// member-row contract); "" keeps the built-in default.
	Title string
	// AskCap bounds one question's length (bytes).
	AskCap int
	// RegisterIndex overrides the sidebar-row registration (nil =
	// zcode.RegisterTaskIndex; tests inject a no-op — the real one
	// writes the operator's live sqlite, which tests must never touch).
	RegisterIndex func(zcode.TaskIndexEntry) error
	// ArchiveIndex overrides the sidebar-row fold (nil =
	// zcode.ArchiveTaskIndex; tests inject a no-op, same reason).
	ArchiveIndex func(workspace, sessionID string) error
	// SessionFile, when set (~/.niuma/assistant.json), is THE
	// assistant's stable identity: it carries the session id across
	// boots, so the first ask of every boot adopts the SAME session
	// (subscribe → -32004 resume ladder) instead of opening another
	// ZCode sidebar row — the row is re-pinned in place by task_id. A
	// binding that proves unadoptable (session deleted) births fresh
	// and folds the dead row, so exactly one live row survives either
	// way. "" = no adoption, nothing read or written (tests).
	SessionFile string
	// Warmup fires one HIDDEN minimal turn at bridge-ready (see
	// Warmup): security middleboxes (corporate per-app VPNs & kin)
	// often drop an unknown process's FIRST outbound connects for
	// minutes, so the warm-up burns that window before the host asks
	// anything real. The probe rides its OWN throwaway session —
	// bare text, no charter, low reasoning — so the real session is
	// never held by it. The app default is on (NIUMA_WARMUP=off
	// disables); the package zero-value is off (embeds/tests opt in).
	Warmup bool
	// WarmupPrompt overrides the warm-up question (tests; the default
	// asks for a one-word reply).
	WarmupPrompt string
	// WarmupTimeout is the probe's own watchdog budget: how long the
	// hidden warm-up may run before it is abandoned as a failed link.
	// Isolation makes an early abandon safe (a late envelope lands on
	// the throwaway session and is ignored), so this can be far
	// shorter than turnTimeout — the app pins it to the door's hold
	// (NIUMA_WARMUP_GATE) so "door waits" == "probe lives". Zero
	// falls back to turnTimeout.
	WarmupTimeout time.Duration
	// TurnTimeout overrides the stale-turn backstop; 0 keeps the
	// default (NIUMA_TURN_TIMEOUT minutes at the app layer).
	TurnTimeout time.Duration
	// Term, when set, lands the assistant's transcript rows (r_17 终端
	// 转录)：每一问的注入原文（input 行，src=房主——预热回合不记，它
	// 是通道探针不是问答）、回合终点的完整回答（turn 行）与失败
	// （error 行）。main 把它接到大厅 hub 的 AppendTerm——小助手与
	// 成员同册同视图；nil = 未接线（测试）。
	Term func(chat.TraceEntry)
}

func (c Config) askCap() int {
	if c.AskCap > 0 {
		return c.AskCap
	}
	return 16 << 10
}

func (c Config) title() string {
	if c.Title != "" {
		return c.Title
	}
	return i18n.S("【Niuma_Studio】小助手-使用顾问")
}

// tt is the stale-turn backstop for this assistant (see turnTimeout).
func (c Config) tt() time.Duration {
	if c.TurnTimeout > 0 {
		return c.TurnTimeout
	}
	return turnTimeout
}

// warmTt is the probe's watchdog budget (see WarmupTimeout): with the
// probe on its own throwaway session, an early abandon is safe, so
// this can be far shorter than the real turn's backstop.
func (c Config) warmTt() time.Duration {
	if c.WarmupTimeout > 0 {
		return c.WarmupTimeout
	}
	return c.tt()
}

// warmupText is the hidden warm-up question: one word back, so the
// round-trip measures the LINK, not the model's prose.
func (c Config) warmupText() string {
	if c.WarmupPrompt != "" {
		return c.WarmupPrompt
	}
	return "【通道预热】请只回复两个字：就绪"
}

// EnvTurnTimeout parses NIUMA_TURN_TIMEOUT: whole minutes (1–60),
// anything else (empty, garbage, out of range) = 0 = the default. The
// knob exists for hostile networks where the operator wants the
// backstop looser or tighter than the default budget.
func EnvTurnTimeout(v string) time.Duration {
	min, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || min < 1 || min > 60 {
		return 0
	}
	return time.Duration(min) * time.Minute
}

// EnvWarmGate parses NIUMA_WARMUP_GATE: whole seconds (5–600),
// anything else (empty, garbage, out of range) = 0 = the caller's
// default. The knob retunes the boot door's hold on the warm-up
// (main's warmGateMax): a network whose cold-start penalty is known
// to run long can hold the door until the probe really lands (进门
// 即热通道), a fast one can open sooner and let the panel's health
// line take over. Past the cap — or on a failed warm-up — the door
// opens all the same; the warm-up never welds it shut.
func EnvWarmGate(v string) time.Duration {
	sec, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || sec < 5 || sec > 600 {
		return 0
	}
	return time.Duration(sec) * time.Second
}

// Msg is one transcript row (role user | assistant | error).
type Msg struct {
	Role string `json:"role"`
	Text string `json:"text"`
	TS   int64  `json:"ts"`
}

// Turn states.
const (
	StatusIdle    = "idle"
	StatusRunning = "running"
)

// transcriptCap keeps the poll payload bounded (the model-side context
// is the session's own; this is the UI mirror).
const transcriptCap = 200

// turnTimeout is the stale-turn backstop, NOT the model budget: the
// app-server retries a dead model link itself (up to 11 attempts, ~1
// minute apart — observed on a per-app VPN where an unsigned host's
// connects time out 10s each for minutes before one lands), so the
// watchdog must OUTLIVE that budget and let the terminal envelope
// resolve the turn naturally. It only covers true wedges (bridge died,
// events lost) — and a late envelope after expiry still lands via the
// awaitLate salvage below.
const turnTimeout = 12 * time.Minute

// unavailableReason is what the panel shows when dispatch is off — the
// assistant rides the same bridge, no bridge means no assistant.
const unavailableReason = "小助手随成员调度一起运行：当前调度未启用（--no-dispatch 或 ZCode 不可用）。启用调度后即可提问。"

// DefaultSessionFile resolves the per-boot binding file
// (~/.niuma/assistant.json) under the v2 root.
func DefaultSessionFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".niuma", "assistant.json")
}

// Assistant drives the one helper session. Safe for concurrent use.
type Assistant struct {
	// driverFn resolves the CURRENT bridge on every use — the bridge
	// can appear after boot (ZCode installed while the splash door
	// waits), and a statically-bound nil driver would keep the
	// assistant dead until a restart.
	driverFn func() dispatch.SessionDriver
	observe  func(*dispatch.Observer)
	cfg      Config

	mu        sync.Mutex
	sessionID string
	status    string // idle | running
	partial   string // streamed text_delta buffer of the running turn
	// pendingReply（复读根治）：本回合最近一条请求边界帧的收尾话——
	// 边界帧只暂存不落册（真终点另有 turn.completed，其 response 缺形
	// 时以它兜底），turn.started 时作废。
	pendingReply string
	msgs         []Msg
	turnAt       time.Time // the running turn's send time (watchdog anchor)
	registered   bool      // observer hooked into the live bridge
	// awaitLate marks "a server-side turn for our session may still
	// deliver its terminal envelope" — set when the watchdog expires or
	// a deliver fails mid-flight, cleared when a new turn starts or a
	// terminal lands. While set, a late TurnDone/state terminal still
	// finalizes (the model answered minutes late through a reconnecting
	// link; dropping the answer was the original panel bug).
	awaitLate bool
	// The boot warm-up (Warmup): the probe turn runs on its OWN
	// throwaway session — probeSessionID. Isolation is the point: the
	// host's real session is never held by the warm-up (an ask
	// mid-warm sends immediately instead of queuing), a late probe
	// envelope lands on a session id nothing else listens to, and
	// abandoning a wedged probe early has zero collateral. warm is
	// the health verdict for the panel ("" never run/disabled,
	// "warming", "ok", "fail").
	warmInFlight   bool
	warm           string
	warmAt         time.Time
	warmPhase      string // warming 的当前腿：创建会话 vs 等模型首响应（门面分腿显示，慢在哪一腿看得见）
	warmMS         int64
	warmNote       string
	probeSessionID string
}

// The warm-up's two legs — the boot door shows which one is burning
// the wait, telling link slowness (await) apart from bridge/app-server
// slowness (create).
const (
	warmPhaseCreating = "创建预热会话中"
	warmPhaseAwaiting = "等待模型首个响应中"
)

// New builds the assistant. driver resolves the bridge lazily (nil =
// dispatch off → valid-but-unavailable assistant, the HTTP face still
// answers with available:false); observe registers the event fan-out
// (called once when a driver first exists — a no-op observeFn is fine).
func New(driver func() dispatch.SessionDriver, observe func(*dispatch.Observer), cfg Config) *Assistant {
	a := &Assistant{driverFn: driver, observe: observe, cfg: cfg, status: StatusIdle}
	if driver != nil && driver() != nil {
		a.hook() // the bridge is already up (the normal boot path)
	}
	return a
}

// hook registers the observer on the bridge exactly once. Called from
// New (bridge already alive) and lazily from ensure/Warmup (bridge
// arrived late) — the guard makes both paths safe.
func (a *Assistant) hook() {
	a.mu.Lock()
	if a.registered {
		a.mu.Unlock()
		return
	}
	a.registered = true
	a.mu.Unlock()
	if a.observe == nil {
		return
	}
	a.observe(&dispatch.Observer{
		OnEvent:   a.onEvent,
		OnState:   a.onState,
		Owns:      a.owns,
		OnReverse: a.onReverse,
	})
}

// driverNow resolves the bridge, tolerating a nil provider.

// term lands one transcript row (r_17): nil-safe (tests embed without
// the hook), always stamped 小助手 — the assistant's journal rides the
// LOBBY hub's term store under that one name.
func (a *Assistant) term(e chat.TraceEntry) {
	if a.cfg.Term == nil {
		return
	}
	e.From = i18n.S("小助手")
	a.cfg.Term(e)
}

func (a *Assistant) driverNow() dispatch.SessionDriver {
	if a.driverFn == nil {
		return nil
	}
	return a.driverFn()
}

// --- the cross-boot session binding (~/.niuma/assistant.json) -------------
//
// ONE session for the assistant's whole life: the file names it, each
// boot's first ask adopts it, and the sidebar row (keyed by the
// session's task_id) is re-pinned in place — restarts reuse, they
// never duplicate. The panel's transcript stays per-boot (an in-memory
// mirror), while the model keeps the session's accumulated context —
// the continuity is the point.

func (a *Assistant) readSessionFile() string {
	b, err := os.ReadFile(a.cfg.SessionFile)
	if err != nil {
		return ""
	}
	var f struct {
		Session string `json:"session"`
	}
	if json.Unmarshal(b, &f) != nil {
		return ""
	}
	return f.Session
}

func (a *Assistant) writeSessionFile(sid string) {
	b, err := json.Marshal(map[string]string{"session": sid})
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(a.cfg.SessionFile), 0o755)
	_ = os.WriteFile(a.cfg.SessionFile, b, 0o600)
}

// adopt takes the previous boot's session back — the stable identity:
// the binding file names it, the subscribe → -32004 → resume ladder
// (the dispatcher's room-restart adoption habit) proves it alive, and
// success re-registers the SAME sidebar row (keyed by task_id, the
// row re-pins in place — even one the desktop's timeline sync
// rewrote) and re-asserts the configured model (it may have changed
// between boots). The session keeps its accumulated context, manual
// included, so no system prompt repeat. "" = nothing to adopt (no
// binding, or the session is gone); the caller births fresh and folds
// the dead row.
func (a *Assistant) adopt(ctx context.Context) string {
	if a.cfg.SessionFile == "" {
		return ""
	}
	prev := a.readSessionFile()
	if prev == "" {
		return ""
	}
	if _, err := a.driverNow().Subscribe(ctx, prev); err != nil {
		if !zcode.IsCode(err, zcode.ErrSessionNotActive) {
			return ""
		}
		if _, rerr := a.driverNow().ResumeSession(ctx, prev); rerr != nil {
			return ""
		}
		if _, serr := a.driverNow().Subscribe(ctx, prev); serr != nil {
			return ""
		}
	}
	a.registerIndex(prev)
	a.applyModel(ctx, prev)
	a.mu.Lock()
	a.sessionID = prev
	a.mu.Unlock()
	return prev
}

// registerIndex mirrors the session into the ZCode sidebar (pinned):
// birth pins a fresh row, adoption re-pins THE row in place — one live
// 小助手 row, ever. Best-effort by contract: visibility only, never
// the turn.

// The sidebar's two real desktop writes ride package-level seams
// below the per-instance cfg ones: the assistant test binary's
// TestMain stubs both, so a test that forgot its cfg.RegisterIndex
// no-op can never touch the operator's LIVE ZCode database (the
// phantom-member 实录 the dispatch package fixed the same way).
// Production keeps the real verbs.
var (
	assistantIndexRegister = zcode.RegisterTaskIndex
	assistantIndexFold     = zcode.ArchiveTaskIndexForce
)

func (a *Assistant) registerIndex(sid string) {
	register := a.cfg.RegisterIndex
	if register == nil {
		register = assistantIndexRegister
	}
	_ = register(zcode.TaskIndexEntry{
		WorkspacePath: a.cfg.Workspace,
		SessionID:     sid,
		Title:         a.cfg.title(),
		Mode:          "yolo",
		Provider:      zcode.DesktopListProvider,
		Model:         a.cfg.Model,
		Status:        "running",
	})
}

// applyModel selects the configured model on the session — the birth
// formula's SetModel step (create WITHOUT a model, then set), re-run
// by adoption because the config may have changed between boots.
// Tolerated: a failure keeps whatever model the session already runs
// and says so once.
func (a *Assistant) applyModel(ctx context.Context, sid string) {
	if a.cfg.Model == "" {
		return
	}
	provider, _ := zcode.ResolveModelProvider(a.cfg.Model)
	if provider == "" {
		return
	}
	sel := zcode.ModelSelection{ProviderID: provider, ModelID: a.cfg.Model}
	if a.cfg.Reasoning != "" {
		sel.Options = &zcode.ModelOptions{ReasoningLevel: a.cfg.Reasoning}
	}
	if err := a.driverNow().SetModel(ctx, sid, sel); err != nil {
		a.append(Msg{Role: "error",
			Text: i18n.Sf("模型 %s 切换失败（%v），沿用会话现有模型作答", a.cfg.Model, err),
			TS:   time.Now().Unix()})
	}
}

// foldIndex archives a session's sidebar row — the fallback half of
// the stable identity: a binding that proved unadoptable folds so the
// fresh birth leaves exactly one live row (and Archive's close half).
// Force-fold: the desktop's timeline sync drops the dhEmployee marker
// from born rows, so the guarded variant no-ops.
func (a *Assistant) foldIndex(sid string) {
	archive := a.cfg.ArchiveIndex
	if archive == nil {
		_ = assistantIndexFold(sid)
		return
	}
	_ = archive(a.cfg.Workspace, sid)
}

// --- the ask path --------------------------------------------------------

// ErrBusy reports a turn still in flight (the panel disables send, so
// this is the race guard, not the flow).
var ErrBusy = fmt.Errorf("小助手正在回答上一个问题，稍候")

// Ask appends the question and starts (or queues behind) a turn.
// The HTTP answer returns immediately; the reply lands on the poll.
func (a *Assistant) Ask(question string) error {
	question = strings.TrimSpace(question)
	if question == "" {
		return errors.New(i18n.S("问题不能为空"))
	}
	if len(question) > a.cfg.askCap() {
		return errors.New(i18n.Sf("问题太长（上限 %d 字节）", a.cfg.askCap()))
	}
	if a.driverNow() == nil {
		return errors.New(i18n.S(unavailableReason))
	}
	a.mu.Lock()
	if a.status != StatusIdle {
		// the warm-up never holds the machine (its probe rides a
		// separate throwaway session) — a busy here is a REAL turn
		a.mu.Unlock()
		return ErrBusy
	}
	a.msgs = append(a.msgs, Msg{Role: "user", Text: question, TS: time.Now().Unix()})
	a.status = StatusRunning
	a.partial = ""
	a.pendingReply = "" // 新回合开工：上一回合的边界口信作废
	a.turnAt = time.Now()
	a.awaitLate = false
	a.mu.Unlock()
	go a.turn(question)
	return nil
}

// Warmup burns the cold-start window: one hidden minimal turn on its
// OWN throwaway session at bridge-ready, so the security middleboxes
// that drop an unknown process's first connects (minutes, observed)
// spend their anger before the host asks anything real. Isolation is
// the design: the probe is bare text (no charter, no manual), runs at
// low reasoning (it measures the LINK, not the model's thinking), and
// the real session is never held by it — an ask mid-warm sends
// immediately, a late probe envelope lands on a session id nothing
// else listens to, and a wedged probe can be abandoned on a short
// budget with zero collateral. Its round-trip becomes the panel's
// channel health (Link in State). Once per process; a no-op when
// disabled, driverless, or already resolved.
func (a *Assistant) Warmup() {
	if !a.cfg.Warmup {
		return
	}
	if a.driverNow() == nil {
		return // no bridge yet; a later call (bridge attach) retries
	}
	a.hook()
	a.mu.Lock()
	if a.warm != "" || a.warmInFlight {
		a.mu.Unlock()
		return // resolved (ok/fail) or already warming — once per process
	}
	a.warm = "warming"
	a.warmInFlight = true
	a.warmAt = time.Now()
	a.warmPhase = warmPhaseCreating
	a.mu.Unlock()
	go func() {
		if err := a.probeTurn(); err != nil {
			a.endWarm(false, err.Error())
		}
	}()
}

// WarmGate reports the boot warm-up's flight for the splash door:
// main's boot gate holds the app entry on it while the hidden probe
// burns the cold-start window, so the host walks in to an ALREADY warm
// channel. expireStale runs first — the probe's own watchdog applies
// (short by design; isolation makes the early abandon safe), so a
// wedged probe resolves (and the door opens) on its own; any verdict
// other than "warming" (disabled, landed, failed) opens the door.
func (a *Assistant) WarmGate() (warming bool, elapsed time.Duration, phase string) {
	a.expireStale()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.warm != "warming" {
		return false, 0, ""
	}
	return true, time.Since(a.warmAt), a.warmPhase
}

// probeMCP is the probe session's MCP fleet override: session/create's
// mcpServers param REPLACES the whole fleet when non-empty (empty falls
// back to the user config), so the probe names one dead-port http
// server — connection refused is instant, and the turn stops parking
// its first model request while the fleet's tool surface connects
// (measured ~4s for two http servers, ~5.6s for one npx stdio server).
// The probe's turn calls no tools; the fleet is dead weight it would
// otherwise make the door wait for. timeoutMs is the backstop for
// hosts where port 9 somehow answers.
var probeMCP = []zcode.MCPServerSpec{{
	Name: "niuma-probe-none", Type: "http",
	URL:       "http://127.0.0.1:9/mcp",
	Headers:   []zcode.MCPHeader{},
	Isolation: "session", TimeoutMs: 2000,
}}

// mcpFleetDriver is SessionDriver's optional per-session MCP-fleet
// override (the fleet's sharedBridge passes it through to the
// zcode.Client). A driver without it (test fakes) probes with the
// default fleet — the pre-override behavior.
type mcpFleetDriver interface {
	CreateSessionMCP(ctx context.Context, workspace, mode string, servers []zcode.MCPServerSpec) (string, error)
}

// ensureProbe seats the probe on a fresh throwaway session — created
// with a BARE MCP fleet (probeMCP above: the probe measures the LINK,
// not the fleet), recorded (events route by session id, so the id must
// exist before the send), modelled (same provider/model as real asks,
// low reasoning), subscribed. No sidebar row, no binding file: one
// invisible session per boot in the server's history is the price of
// never colliding with the real session's cross-boot adoption.
func (a *Assistant) ensureProbe(ctx context.Context) (string, error) {
	a.hook() // the bridge may have arrived after boot — fan out from now
	if a.driverNow() == nil {
		return "", errors.New(i18n.S(unavailableReason))
	}
	sid, err := a.probeCreate(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.S("创建会话失败"), err)
	}
	a.mu.Lock()
	a.probeSessionID = sid
	a.mu.Unlock()
	a.applyProbeModel(ctx, sid)
	if _, err := a.driverNow().Subscribe(ctx, sid); err != nil {
		a.mu.Lock()
		a.probeSessionID = ""
		a.mu.Unlock()
		return "", fmt.Errorf("%s: %w", i18n.S("订阅会话失败"), err)
	}
	return sid, nil
}

// probeCreate births the throwaway session with the bare fleet when
// the driver and the CLI both take the override, falling back to a
// plain create otherwise: an older CLI's strict schema rejects the
// mcpServers param wholesale, and a create error there would kill the
// warm-up entirely — better to probe with the full fleet (the
// pre-override cost) than not to probe.
func (a *Assistant) probeCreate(ctx context.Context) (string, error) {
	d := a.driverNow()
	if mb, ok := d.(mcpFleetDriver); ok {
		if sid, err := mb.CreateSessionMCP(ctx, a.cfg.Workspace, "yolo", probeMCP); err == nil {
			return sid, nil
		}
		// fall through: param rejected or the create itself failed —
		// the plain retry below re-errors (or retries) on its own terms
	}
	return d.CreateSession(ctx, a.cfg.Workspace, "yolo")
}

// applyProbeModel selects the configured model at LOW reasoning on the
// probe session: the probe measures the link's round-trip, not the
// model's thinking, and "就绪" needs no deliberation. Tolerated
// silently — a probe is invisible (no transcript row, no error row);
// whatever model the session defaults to still exercises the link.
func (a *Assistant) applyProbeModel(ctx context.Context, sid string) {
	if a.cfg.Model == "" {
		return
	}
	provider, _ := zcode.ResolveModelProvider(a.cfg.Model)
	if provider == "" {
		return
	}
	sel := zcode.ModelSelection{ProviderID: provider, ModelID: a.cfg.Model,
		Options: &zcode.ModelOptions{ReasoningLevel: "low"}}
	_ = a.driverNow().SetModel(ctx, sid, sel)
}

// probeTurn delivers the bare probe question on the throwaway session;
// everything after the send is the event stream's business (the
// watchdog and the terminal envelope both resolve via endWarm).
func (a *Assistant) probeTurn() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sid, err := a.ensureProbe(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.warmInFlight { // 还在飞才翻腿——看门狗先放弃时别多此一举
		a.warmPhase = warmPhaseAwaiting
	}
	a.mu.Unlock()
	inputID := fmt.Sprintf("assistant-probe-%d", time.Now().UnixNano())
	if _, err := a.driverNow().Send(ctx, sid, a.cfg.warmupText(), inputID, nil); err != nil {
		return fmt.Errorf("%s: %w", i18n.S("发送失败"), err)
	}
	return nil
}

// turn births the real session (first ask) and delivers the question;
// everything after the send is the event stream's business. The probe
// never rides this path — it has its own (probeTurn).
func (a *Assistant) turn(question string) {
	sid, first, err := a.ensure()
	if err == nil {
		err = a.deliver(sid, first, question)
	}
	if err == nil {
		return
	}
	a.failTurn(err)
}

// ensure seats the session once: adoption first (the stable identity
// — the previous boot's binding, when alive, is taken back whole), and
// only an unadoptable binding falls through to a fresh birth (the
// dispatcher's birth formula: create WITHOUT a model, then SetModel —
// the CLI persistence bug; then the sidebar row and the subscribe),
// folding the dead binding's row so one live row remains. first
// reports whether the caller owes the system prompt with the next
// send (birth yes, adoption no — that context already sits in the
// session).
func (a *Assistant) ensure() (sid string, first bool, err error) {
	a.hook() // the bridge may have arrived after boot — fan out from now
	a.mu.Lock()
	sid, booted := a.sessionID, a.sessionID != ""
	a.mu.Unlock()
	if booted {
		return sid, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if adopted := a.adopt(ctx); adopted != "" {
		return adopted, false, nil
	}
	if a.cfg.SessionFile != "" {
		if prev := a.readSessionFile(); prev != "" {
			a.foldIndex(prev) // unadoptable: its row folds, the birth's pins
		}
	}
	sid, err = a.driverNow().CreateSession(ctx, a.cfg.Workspace, "yolo")
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", i18n.S("创建会话失败"), err)
	}
	// the id is recorded the moment the session exists: subscribe-time
	// state patches (and any early event) must already match us
	a.mu.Lock()
	a.sessionID = sid
	a.mu.Unlock()
	a.applyModel(ctx, sid)
	// the sidebar row: pinned like the members', so the operator sees
	// the assistant in ZCode's list too (best-effort by contract:
	// visibility only, never the birth)
	a.registerIndex(sid)
	if _, err := a.driverNow().Subscribe(ctx, sid); err != nil {
		// a session that cannot be subscribed is not usable — drop the
		// binding so the next ask births fresh instead of limping
		a.mu.Lock()
		a.sessionID = ""
		a.mu.Unlock()
		return "", false, fmt.Errorf("%s: %w", i18n.S("订阅会话失败"), err)
	}
	if a.cfg.SessionFile != "" {
		a.writeSessionFile(sid) // the binding the next boots adopt
	}
	return sid, true, nil
}

// deliver injects one question: the first send carries the system
// prompt plus the manual; later sends prefix the live digest.
func (a *Assistant) deliver(sid string, first bool, question string) error {
	content := question
	if first {
		content = systemPrompt(a.cfg.Manual) + "\n\n【房主的第一个问题】\n" + question
	} else if a.cfg.Digest != nil {
		if d := a.cfg.Digest(); d != "" {
			content = "【当前工作室状态（背景，无需回应）】\n" + d + "\n\n【房主问】\n" + question
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	inputID := fmt.Sprintf("assistant-%d", time.Now().UnixNano())
	_, err := a.driverNow().Send(ctx, sid, content, inputID, nil)
	if zcode.IsCode(err, zcode.ErrSessionNotActive) {
		// the -32004 → resume → subscribe ladder, the dispatcher's
		// cold-session habit; a ladder failure drops the binding so the
		// next ask births a fresh session instead of limping forever
		if _, rerr := a.driverNow().ResumeSession(ctx, sid); rerr == nil {
			if _, serr := a.driverNow().Subscribe(ctx, sid); serr == nil {
				_, err = a.driverNow().Send(ctx, sid, content, inputID, nil)
			}
		}
		if zcode.IsCode(err, zcode.ErrSessionNotActive) {
			a.mu.Lock()
			if a.sessionID == sid {
				a.sessionID = ""
			}
			a.mu.Unlock()
		}
	}
	if zcode.IsCode(err, zcode.ErrPromptRunning) {
		// -32010: the previous (maybe watchdog-expired) turn is still
		// alive server-side; this question was never taken. Keep the
		// binding and keep listening — the old turn's late envelope
		// still lands via the salvage path.
		return errStillRunning
	}
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.S("发送失败"), err)
	}
	// r_17：注入原文落 input 行——content 是本问的最终合成文（首问带
	// system prompt、后续带活体摘要，会话看到什么记什么）。通道探针
	// 不走这条路（它有自己的 probeTurn），到达这里的都是真问。
	a.term(chat.TraceEntry{Kind: chat.TraceInput, Src: i18n.S("房主"),
		Text: chat.TermClipOut(content)})
	return nil
}

// errStillRunning is deliver's -32010 translation: the server never
// took this question because the previous turn is still alive in
// there (the reconnect-retry reality above). The wording must tell the
// host their text did NOT go out.
var errStillRunning = fmt.Errorf("上一轮回答还在进行（模型通道可能在重连重试），这条没有送出——稍等片刻再发一次")

// Archive folds the assistant's sidebar row — the app-close half of
// the sidebar contract: on duty = pinned, app closed = folded (the
// next boot's first ask adopts the SAME session and re-pins THE row
// in place — reuse, not a fresh row per restart). Idempotent,
// best-effort; a never-born session is a silent no-op. Force-fold by
// task_id: the desktop's timeline sync rewrites born rows (dropping
// the dhEmployee marker), so the guarded variant would no-op.
func (a *Assistant) Archive() {
	a.mu.Lock()
	sid := a.sessionID
	a.mu.Unlock()
	if sid == "" {
		return
	}
	a.foldIndex(sid)
}

// --- the event path (the observer callbacks) -----------------------------

func (a *Assistant) owns(sid string) bool {
	if sid == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessionID == sid
}

// onEvent finalizes the running turn on its terminal and mirrors the
// streamed deltas into the poll-visible partial. The success terminal
// is the typed turn.completed — authoritative even past our own
// watchdog (awaitLate): a model link that finally reconnected answers
// minutes after expiry, and that late answer must still land. The
// no-kind stopReason=="stop" frames fire at EVERY model-request
// boundary inside the turn and only feed pendingReply (the model often
// restates its pending closing words verbatim at each one — finalizing
// per frame appended the same answer twice-plus; 房主实录 2026-10-03);
// failure-reason frames still finalize on the spot. The probe's events
// route FIRST, by its own session id: its terminal is channel health,
// never a transcript row — and a LATE probe envelope (after the
// watchdog abandoned it) falls through to the session-id mismatch and
// is dropped, which is exactly why the probe rides its own throwaway
// session.
func (a *Assistant) onEvent(ev zcode.Event) {
	a.mu.Lock()
	if a.warmInFlight && ev.SessionID == a.probeSessionID {
		if _, resultType, ok := ev.TurnCompleted(); ok {
			warm := resultType == "" || resultType == "success"
			note := ""
			if !warm {
				note = i18n.Sf("预热异常中止：%s", resultType)
			}
			a.mu.Unlock()
			a.endWarm(warm, note)
			return
		}
		if _, stopReason, done := ev.TurnDone(); done {
			a.mu.Unlock()
			a.endWarm(false, i18n.Sf("预热异常中止：%s", stopReason))
			return
		}
		a.mu.Unlock()
		return
	}
	if ev.SessionID != a.sessionID {
		a.mu.Unlock()
		return
	}
	if ev.Type == "turn.started" {
		a.pendingReply = "" // 上一回合的边界口信不得漏进这一回
		a.mu.Unlock()
		return
	}
	if delta, ok := ev.TextDelta(); ok {
		if a.status == StatusRunning {
			if len(a.partial) < 1<<16 {
				a.partial += delta
			}
			a.turnAt = time.Now() // streaming counts as life for the watchdog
		}
		a.mu.Unlock()
		return
	}
	if content, ok := ev.TurnStopped(); ok {
		if a.status == StatusRunning || a.awaitLate {
			a.pendingReply = content
		}
		a.mu.Unlock()
		return
	}
	if content, resultType, ok := ev.TurnCompleted(); ok {
		accept := a.status == StatusRunning || a.awaitLate
		a.partial = ""
		stash := a.pendingReply
		a.pendingReply = ""
		a.status = StatusIdle
		a.awaitLate = false
		a.mu.Unlock()
		if !accept {
			return // a replayed terminal for an already-closed turn
		}
		// r_17：回合终点整行落册（回答全文或异常 resultType）。
		a.term(chat.TraceEntry{Kind: chat.TraceTurn, State: "done",
			Text: chat.TermClipOut(content), Stop: resultType})
		switch {
		case content != "":
			a.append(Msg{Role: "assistant", Text: content, TS: time.Now().Unix()})
		case stash != "":
			// response 缺形：以最后一条边界帧的口信兜底，回答不丢。
			a.append(Msg{Role: "assistant", Text: stash, TS: time.Now().Unix()})
		case resultType != "" && resultType != "success":
			a.append(Msg{Role: "error", Text: i18n.Sf("回答异常中止：%s", resultType), TS: time.Now().Unix()})
		}
		return
	}
	content, stopReason, done := ev.TurnDone()
	if !done {
		a.mu.Unlock()
		return
	}
	accept := a.status == StatusRunning || a.awaitLate
	a.partial = ""
	a.pendingReply = ""
	a.status = StatusIdle
	a.awaitLate = false
	a.mu.Unlock()
	if !accept {
		return // a replayed envelope for an already-closed turn
	}

	// r_17：回合终点整行落册（回答全文或异常 stop）。
	a.term(chat.TraceEntry{Kind: chat.TraceTurn, State: "done",
		Text: chat.TermClipOut(content), Stop: stopReason})
	switch {
	case content != "":
		a.append(Msg{Role: "assistant", Text: content, TS: time.Now().Unix()})
	case stopReason != "stop":
		a.append(Msg{Role: "error", Text: i18n.Sf("回答异常中止：%s", stopReason), TS: time.Now().Unix()})
	}
}

// onState is the failure terminal a hung provider never emits events
// for (prompt_failed & kin — the dispatcher's onState lesson). It also
// covers a late failure for an expired turn (awaitLate): once the
// server says the turn died, no envelope is coming.
func (a *Assistant) onState(sessionID, status, reason string) {
	if status == "running" {
		return // the send already flipped us; nothing to learn
	}
	if !strings.Contains(reason, "fail") && !strings.Contains(reason, "error") {
		return
	}
	a.mu.Lock()
	if a.warmInFlight && sessionID == a.probeSessionID {
		// the probe's fail-state patch: channel health, never a row
		a.mu.Unlock()
		a.endWarm(false, i18n.Sf("回答失败中止：%s", zcode.TurnFailHint(reason)))
		return
	}
	mine := sessionID == a.sessionID && (a.status == StatusRunning || a.awaitLate)
	if !mine {
		a.mu.Unlock()
		return
	}
	a.status = StatusIdle
	a.partial = ""
	a.awaitLate = false
	a.mu.Unlock()
	failText := i18n.Sf("回答失败中止：%s", zcode.TurnFailHint(reason))
	a.term(chat.TraceEntry{Kind: chat.TraceError, Text: failText})
	a.append(Msg{Role: "error", Text: failText, TS: time.Now().Unix()})
}

// endWarm is the probe's every terminal (envelope, fail state, send
// error, watchdog): it records the channel-health verdict and that's
// all — the real session was never held by the warm-up, so there is
// nothing to flush and no transcript row to write. Idempotent: the
// envelope, a state patch and the watchdog may all fire.
func (a *Assistant) endWarm(ok bool, note string) {
	a.mu.Lock()
	if !a.warmInFlight {
		a.mu.Unlock()
		return
	}
	a.warmInFlight = false
	a.warmMS = time.Since(a.warmAt).Milliseconds()
	a.warmPhase = ""
	if ok {
		a.warm = "ok"
		a.warmNote = ""
	} else {
		a.warm = "fail"
		a.warmNote = note
	}
	a.mu.Unlock()
}

// onReverse is the advisor contract, fail-closed and IMMEDIATE: the
// assistant never executes anything and never asks the room — deny or
// decline on the spot, with reasons that read in the transcript.
func (a *Assistant) onReverse(method string, _ map[string]any) (any, error) {
	switch method {
	case "interaction/requestPermission":
		return map[string]any{"decision": "deny", "reason": i18n.S("小助手只做顾问，不执行操作")}, nil
	case "interaction/requestUserInput":
		return map[string]any{"action": "decline", "reason": i18n.S("小助手不受理会话内提问——请在小助手面板里输入")}, nil
	}
	return nil, nil // requestRuntimePreferences & unknown: the client's fail-safe defaults
}

// --- state & HTTP face -----------------------------------------------------

// State is the panel's one-poll answer.
type State struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Status    string `json:"status"` // idle | running
	// Since is the running turn's age in seconds — the panel's
	// waiting feedback (a provider outage keeps a turn "running" with
	// zero output; the number tells the host it's stalled, not busy).
	Since    int    `json:"since,omitempty"`
	Partial  string `json:"partial,omitempty"`
	Messages []Msg  `json:"messages"`
	// Link is the boot warm-up's channel health: "" = not run or
	// disabled, "warming" = in flight, "ok" = round-tripped
	// (LinkMS carries the latency), "fail" = LinkNote says why. The
	// hostile-network tell: a slow warm-up means the first real
	// question will be slow too, and now the panel can say so.
	Link     string `json:"link,omitempty"`
	LinkMS   int64  `json:"linkMs,omitempty"`
	LinkNote string `json:"linkNote,omitempty"`
}

// State snapshots the transcript (plus the stale-turn watchdog).
func (a *Assistant) State() State {
	a.expireStale()
	a.mu.Lock()
	defer a.mu.Unlock()
	msgs := make([]Msg, len(a.msgs))
	copy(msgs, a.msgs)
	st := State{Status: a.status, Messages: msgs}
	if a.driverNow() == nil {
		st.Reason = i18n.S(unavailableReason)
		return st
	}
	st.Available = true
	if a.status == StatusRunning {
		st.Partial = a.partial
		st.Since = int(time.Since(a.turnAt).Round(time.Second) / time.Second)
	}
	st.Link = a.warm
	switch a.warm {
	case "ok":
		st.LinkMS = a.warmMS
	case "fail":
		st.LinkNote = a.warmNote
	}
	return st
}

// expireStale finalizes a turn that neither ended nor streamed within
// turnTimeout (lazy check — reads are frequent, timers are not). The
// turn only goes to STANDBY: awaitLate stays armed so the late
// envelope still lands (see onEvent), and the composer frees up for a
// re-ask (a -32010 answer explains if the server turn is still busy).
func (a *Assistant) expireStale() {
	a.mu.Lock()
	// the probe has its OWN, far shorter budget (warmTt): it rides a
	// throwaway session, so abandoning it early is safe — a late
	// envelope lands on a session id nothing listens to. The real
	// turn keeps the long turnTimeout backstop.
	warm := a.warmInFlight && time.Since(a.warmAt) > a.cfg.warmTt()
	stale := !warm && a.status == StatusRunning && time.Since(a.turnAt) > a.cfg.tt()
	if stale {
		a.status = StatusIdle
		a.partial = ""
		a.awaitLate = true
	}
	a.mu.Unlock()
	switch {
	case warm:
		a.endWarm(false, i18n.S("预热超时（通道长时间无响应）——首个问题可能也会慢"))
	case stale:
		mins := int(a.cfg.tt().Minutes())
		a.append(Msg{Role: "error",
			Text: i18n.Sf("回答超时（%d 分钟无进展），本轮挂起——迟到的回复送达后会自动补记；可重新提问", mins),
			TS:   time.Now().Unix()})
	}
}

// append records one transcript row (capped).
func (a *Assistant) append(m Msg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.msgs = append(a.msgs, m)
	if len(a.msgs) > transcriptCap {
		a.msgs = a.msgs[len(a.msgs)-transcriptCap:]
	}
}

// failTurn lands a birth/deliver error as an error row and re-arms.
// The server turn (when one got that far) may still be alive — e.g.
// the -32010 still-running refusal — so awaitLate stays armed for the
// salvage path instead of assuming the silence is final. The probe
// never takes this path — its failures resolve via endWarm.
func (a *Assistant) failTurn(err error) {
	a.mu.Lock()
	a.status = StatusIdle
	a.partial = ""
	a.awaitLate = a.sessionID != ""
	a.mu.Unlock()
	// the panel/term mirror reads err.Error() — static sentinel texts
	// (errStillRunning) ride the dictionary throat here, exactly like
	// the HTTP face's writeErr.
	text := i18n.S(err.Error())
	a.term(chat.TraceEntry{Kind: chat.TraceError, Text: text})
	a.append(Msg{Role: "error", Text: text, TS: time.Now().Unix()})
}

// HTTPHandler serves the panel's face (mounted under /assistant/):
// GET /assistant = State; POST /assistant/ask {"text":...} = start a
// turn. JSON in, JSON out — the writeJSONErr convention.
func (a *Assistant) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimSuffix(r.URL.Path, "/") {
		case "/assistant":
			if r.Method != http.MethodGet {
				writeJSONErr(w, http.StatusMethodNotAllowed, "GET only")
				return
			}
			writeJSON(w, a.State())
		case "/assistant/ask":
			if r.Method != http.MethodPost {
				writeJSONErr(w, http.StatusMethodNotAllowed, "POST only")
				return
			}
			var body struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil {
				writeJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
				return
			}
			if err := a.Ask(body.Text); err != nil {
				status := http.StatusBadRequest
				if err == ErrBusy {
					status = http.StatusConflict
				} else if !a.available() {
					status = http.StatusServiceUnavailable
				}
				writeJSONErr(w, status, err.Error())
				return
			}
			writeJSON(w, a.State())
		default:
			http.NotFound(w, r)
		}
	})
}

func (a *Assistant) available() bool { return a.driverNow() != nil }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONErr(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": i18n.S(reason)}) // 静态中文错误走词典（server/http.go 同款咽喉）
}

// --- the prompt -----------------------------------------------------------

// systemPrompt is the assistant's charter: identity, behavior rules,
// and the manual as the authority. Chinese-first (the room's tongue).
func systemPrompt(manual string) string {
	var b strings.Builder
	b.WriteString(`你是「牛马工作室 niuma」里的小助手，房主身边的使用顾问。这间工作室是一个运行在本机的像素办公室：人类房主加上一群由 ZCode 驱动的 AI 牛马，在 Niuma_Studio 与各项目房里对话、派活、管理任务、排期与能力装配。

你的职责：回答房主的任何问题——工作室的功能怎么用、命令怎么写、某个概念是什么意思，也可以聊点别的。回答要求：
- 中文，先给答案，再给必要的补充；
- 涉及操作时给出具体路径或示例（在哪个板块、@谁、用什么命令）；
- 你只是顾问，不执行操作：不要调用工具、不要改文件、不要替房主派活——需要动作时，告诉房主怎么做；
- 说明书里没有的功能不要编造；不确定就明说。`)
	if manual != "" {
		b.WriteString("\n\n【工作室说明书（权威资料）】\n")
		b.WriteString(manual)
	}
	return b.String()
}
