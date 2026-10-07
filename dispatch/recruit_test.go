package dispatch

// Boot flagship recruiter tests (v2.2): the gate's shapes — an inert
// fleet (--no-dispatch's shape) never gates the door, a fleet without
// room memory never starts the loop, a live loop over a refusing
// bridge parks the reason in Blocked while the post rows still land
// in the lobby's REAL table (ops/establishment, never a p/default
// shadow), and a working bridge drives the FULL normal-birth flow —
// real seat, staffing row, agents config with the post's manual,
// ZCode sidebar pin, Lv.8 stamp — before Done may ever flip.

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// refusedBridge answers every driving verb with an error — the
// recruiter's retry shape (a bridge that cannot carry a session).
type refusedBridge struct{}

func (refusedBridge) ListSessions(ctx context.Context) ([]zcode.SessionInfo, error) {
	return nil, errors.New("refused")
}
func (refusedBridge) CreateSession(ctx context.Context, workspace, mode string) (string, error) {
	return "", errors.New("refused")
}
func (refusedBridge) SetModel(ctx context.Context, sessionID string, sel zcode.ModelSelection) error {
	return errors.New("refused")
}
func (refusedBridge) ResumeSession(ctx context.Context, sessionID string) (*zcode.ResumeResult, error) {
	return nil, errors.New("refused")
}
func (refusedBridge) Subscribe(ctx context.Context, sessionID string) (*zcode.SubscribeResult, error) {
	return nil, errors.New("refused")
}
func (refusedBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	return nil, errors.New("refused")
}
func (refusedBridge) Steer(ctx context.Context, sessionID, content string) error {
	return errors.New("refused")
}
func (refusedBridge) ProbeSession(ctx context.Context, sessionID string) error {
	return nil
}
func (refusedBridge) SetHooks(hooks zcode.Hooks)                              {}
func (refusedBridge) SetReverse(fn func(string, map[string]any) (any, error)) {}

// stubBridge is the working bridge: every driving verb succeeds with
// synthesized ids — the full birth path runs against it.
type stubBridge struct {
	baseBridge
	mu  sync.Mutex
	n   int
	sid []string               // created session ids, in order
	set []zcode.ModelSelection // setModel selections, in order
}

func (b *stubBridge) CreateSession(ctx context.Context, workspace, mode string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.n++
	id := "sess-stub-" + strconv.Itoa(b.n)
	b.sid = append(b.sid, id)
	return id, nil
}
func (b *stubBridge) SetModel(ctx context.Context, sessionID string, sel zcode.ModelSelection) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.set = append(b.set, sel)
	return nil
}

// lastModel hands back the newest recorded setModel selection (the
// zero ModelSelection when none landed) — the seat-tier tests' probe.
func (b *stubBridge) lastModel() zcode.ModelSelection {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.set) == 0 {
		return zcode.ModelSelection{}
	}
	return b.set[len(b.set)-1]
}
func (b *stubBridge) ResumeSession(ctx context.Context, sessionID string) (*zcode.ResumeResult, error) {
	return &zcode.ResumeResult{}, nil
}
func (b *stubBridge) Subscribe(ctx context.Context, sessionID string) (*zcode.SubscribeResult, error) {
	return &zcode.SubscribeResult{}, nil
}
func (b *stubBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	return &zcode.SendAck{}, nil
}

// recruitStage is one lobby fleet over fresh stores: the recruiter's
// stage. The beat tightens for the test and restores on cleanup.
type recruitStageOut struct {
	f     *Fleet
	docs  *kb.DocsStore
	store *agents.Store
	hub   *chat.Hub
	ranks map[string]int
}

func recruitStage(t *testing.T, bridge Bridge, withDocs bool) recruitStageOut {
	return recruitStageCfg(t, bridge, withDocs, nil)
}

// recruitStageCfg is recruitStage with a Config hook — the seat-tier
// tests inject a default-chain model (Model + hermetic ProviderResolve)
// so reasoning-only picks have a real model to ride.
func recruitStageCfg(t *testing.T, bridge Bridge, withDocs bool, extra func(*Config)) recruitStageOut {
	t.Helper()
	dir := t.TempDir()
	projs, err := projects.Open(filepath.Join(dir, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projs.EnsureLobby(dir); err != nil {
		t.Fatal(err)
	}
	staff, err := staffing.Open(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	agentStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	var docs *kb.DocsStore
	cfg := Config{Workspace: dir}
	if extra != nil {
		extra(&cfg)
	}
	ranks := map[string]int{}
	if withDocs {
		if docs, err = kb.OpenDocs(filepath.Join(dir, "kb")); err != nil {
			t.Fatal(err)
		}
		cfg.Docs = docs
	}
	cfg.SetRank = func(name string, lv int) { ranks[name] = lv }
	// tick 换挡的恢复清理必须先于 f.Stop 注册（cleanup LIFO 后跑）：
	// Stop 收拢 recruitLoop 之后才许写回包级 tick，否则循环还在读它
	// ——race 实录（换挡写在 StartFleet 前同样成立，循环出生即新挡）。
	oldTick := recruitTick
	recruitTick = 20 * time.Millisecond
	t.Cleanup(func() { recruitTick = oldTick })
	hub := chat.NewHub()
	f := StartFleet(bridge, agentStore, projs, staff,
		map[string]*chat.Hub{chat.LobbyKey: hub}, cfg)
	t.Cleanup(f.Stop)
	return recruitStageOut{f: f, docs: docs, store: agentStore, hub: hub, ranks: ranks}
}

// waitRecruit polls the progress until cond flips or the deadline
// dies (the loop is a goroutine; every assertion synchronizes here).
func waitRecruit(t *testing.T, f *Fleet, cond func(RecruitProgress) bool) RecruitProgress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	rp := f.FlagshipRecruit()
	for time.Now().Before(deadline) && !cond(rp) {
		time.Sleep(10 * time.Millisecond)
		rp = f.FlagshipRecruit()
	}
	if !cond(rp) {
		t.Fatalf("招聘进度未达预期：%+v", rp)
	}
	return rp
}

// TestInertFleetNeverGates pins the default: an inert fleet (the
// --no-dispatch shape) reports the establishment half satisfied — the
// ZCode state alone governs that door.
func TestInertFleetNeverGates(t *testing.T) {
	f := inertFleet(t)
	if rp := f.FlagshipRecruit(); !rp.Done {
		t.Fatalf("惰性 fleet 不应把门：%+v", rp)
	}
}

// TestRecruitSkippedWithoutDocs pins the docs guard: no room memory,
// no loop — the progress stays satisfied (the door cannot wait on a
// table nobody can read).
func TestRecruitSkippedWithoutDocs(t *testing.T) {
	st := recruitStage(t, refusedBridge{}, false)
	st.f.KickFlagshipRecruit() // no docs: an explicit kick stays a no-op
	if rp := st.f.FlagshipRecruit(); !rp.Done {
		t.Fatalf("无 docs 的 fleet 不应开始招聘：%+v", rp)
	}
}

// TestRecruitRefusedBirthNeverSatisfied pins the phantom-presence
// regression: the recruiter's own config upsert must NEVER satisfy
// its gate — with every birth refused the loop stays short (Done
// false, Blocked naming the hire) across many ticks, because
// presence counts LIVE SEATS only, never stored configs.
func TestRecruitRefusedBirthNeverSatisfied(t *testing.T) {
	st := recruitStage(t, refusedBridge{}, true)
	st.f.KickFlagshipRecruit()

	rp := waitRecruit(t, st.f, func(p RecruitProgress) bool { return p.Blocked != "" })
	if rp.Done {
		t.Fatalf("被拒的桥不可能满编：%+v", rp)
	}
	if !strings.Contains(rp.Blocked, "小牛") {
		t.Fatalf("受阻原因应点名小牛的出生未成：%+v", rp)
	}
	// the pre-birth upsert already wrote the configs: the very trap.
	// Many ticks later the gate must STILL hold — a config is not a hire.
	if _, ok := st.store.Get("小牛"); !ok {
		t.Fatal("前置建档应已发生（这正是幽灵满编的诱因）")
	}
	time.Sleep(150 * time.Millisecond)
	rp = st.f.FlagshipRecruit()
	if rp.Done || rp.Blocked == "" {
		t.Fatalf("档案不得顶替在岗：门应仍在等真实座位：%+v", rp)
	}

	// the post rows still landed in the lobby's REAL table — the two
	// flagship posts plus the advisor row (v2.4: a system post the pass
	// ensures without ever birthing; the gate doesn't count it).
	doc, err := st.docs.Get("ops/establishment", 0)
	if err != nil {
		t.Fatalf("Niuma_Studio 编制表不可读：%v", err)
	}
	rows, perr := kb.EstablishmentRows(doc.Body)
	if perr != "" {
		t.Fatalf("Niuma_Studio 编制表解析失败：%s", perr)
	}
	if len(rows) != len(kb.FlagshipPosts)+1 {
		t.Fatalf("表行应落齐 %d 个系统岗（旗舰×%d＋小助手），得 %d：%s",
			len(kb.FlagshipPosts)+1, len(kb.FlagshipPosts), len(rows), doc.Body)
	}
	if _, err := st.docs.Get("p/default/establishment", 0); err == nil {
		t.Fatal("Niuma_Studio 不应长出无人看护的 p/default 影子表")
	}
}

// TestRecruitBirthsFullNormalFlow is the parity pin: a working bridge
// hires both flagship posts through the EXACT normal birth path —
// live seats in the lobby, HR's config carrying roles/hr, the ZCode
// sidebar pin registered for both, Lv.8 stamped, the org-memory
// ledger line landed — and only then flips Done.
func TestRecruitBirthsFullNormalFlow(t *testing.T) {
	bridge := &stubBridge{}
	st := recruitStage(t, bridge, true)

	// capture the sidebar pins BEFORE the loop can birth (the kick is
	// manual precisely so this seam lands first)
	var pinMu sync.Mutex
	pins := map[string]zcode.TaskIndexEntry{}
	d := st.f.Lobby()
	if d == nil {
		t.Fatal("lobby 调度器缺席，无法装侧栏桩")
	}
	d.registerIndex = func(e zcode.TaskIndexEntry) error {
		pinMu.Lock()
		pins[e.SessionID] = e
		pinMu.Unlock()
		return nil
	}

	st.f.KickFlagshipRecruit()
	rp := waitRecruit(t, st.f, func(p RecruitProgress) bool { return p.Done })
	if rp.Filled != rp.Total || rp.Total != len(kb.FlagshipPosts) {
		t.Fatalf("满编计数不符：%+v", rp)
	}

	// live seats by exact role — the strict presence the gate demanded
	seats := map[string]string{}
	for _, m := range st.hub.Members() {
		seats[m.Name] = m.Role
	}
	if seats["小牛"] != agents.OrchestratorRole {
		t.Fatalf("小牛应有活座位（%s）：%v", agents.OrchestratorRole, seats)
	}
	if seats["小马"] != agents.HRRole {
		t.Fatalf("小马应有活座位（%s）：%v", agents.HRRole, seats)
	}

	// the sessions were really created and pinned in the sidebar
	bridge.mu.Lock()
	created := len(bridge.sid)
	bridge.mu.Unlock()
	if created < len(kb.FlagshipPosts) {
		t.Fatalf("应真实创建 %d 个会话，得 %d", len(kb.FlagshipPosts), created)
	}
	pinMu.Lock()
	if len(pins) < len(kb.FlagshipPosts) {
		t.Fatalf("两个旗舰岗位都应置顶进 ZCode 侧栏，得 %d 条：%+v", len(pins), pins)
	}
	var pinTitles []string
	for _, e := range pins {
		pinTitles = append(pinTitles, e.Title)
	}
	pinMu.Unlock()
	joined := strings.Join(pinTitles, " ")
	if !strings.Contains(joined, "小牛") || !strings.Contains(joined, "小马") {
		t.Fatalf("侧栏行应点名小牛与小马：%v", pinTitles)
	}

	// HR's config carries the manual (her handoff points at roles/hr)
	if cfg, ok := st.store.Get("小马"); !ok || cfg.Manual != "roles/hr" {
		t.Fatalf("小马档案应带 roles/hr 手册：%+v", cfg)
	}
	// both Lv.8 — stamped at birth, refreshed on the done pass
	if st.ranks["小牛"] != 8 || st.ranks["小马"] != 8 {
		t.Fatalf("两位旗舰应 Lv.8：%v", st.ranks)
	}
	// the org-memory ledger line landed (niuma recruit 步骤⑥ parity)
	logDoc, err := st.docs.Get("ops/room-log", 0)
	if err != nil || !strings.Contains(logDoc.Body, "已上岗") {
		t.Fatalf("上岗台账应落 ops/room-log：%v %q", err, logDoc.Body)
	}
}
