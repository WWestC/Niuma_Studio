package dispatch

// twins_test.go — the「两个小马」regression pins (v2.2 hotfix): a
// member whose binding moves to a NEW session must never leave the
// old session's pinned sidebar row live. The register script's
// title-match fold is the safety net (pinned in zcode's own tests);
// these pin the dispatcher's precise strikes — the unstaffed rebirth
// archiving the displaced agents binding, the staffed create branch
// folding a stale v1 binding, and the unbind (Bind "") folding the
// row watchSeat never gets a chance to fold.

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// twinStage is one dispatcher over fresh stores with both sidebar
// seams capturing instead of touching the real desktop database.
type twinStageOut struct {
	d      *Dispatcher
	bridge *stubBridge
	mu     sync.Mutex
	pins   []zcode.TaskIndexEntry
	folds  []string
}

func twinStage(t *testing.T, staffed bool) *twinStageOut {
	t.Helper()
	dir := t.TempDir()
	agentStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Workspace: dir}
	if staffed {
		staffStore, err := staffing.Open(filepath.Join(dir, "staffing.json"))
		if err != nil {
			t.Fatal(err)
		}
		cfg.StaffStore = staffStore
	}
	bridge := &stubBridge{}
	d := Start(chat.NewHub(), agentStore, bridge, cfg)
	t.Cleanup(d.Stop)
	st := &twinStageOut{d: d, bridge: bridge}
	d.registerIndex = func(e zcode.TaskIndexEntry) error {
		st.mu.Lock()
		st.pins = append(st.pins, e)
		st.mu.Unlock()
		return nil
	}
	d.archiveIndex = func(sid string) error {
		st.mu.Lock()
		st.folds = append(st.folds, sid)
		st.mu.Unlock()
		return nil
	}
	return st
}

func (st *twinStageOut) snapshot() (pins []zcode.TaskIndexEntry, folds []string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]zcode.TaskIndexEntry(nil), st.pins...), append([]string(nil), st.folds...)
}

// TestUnstaffedRebirthFoldsDisplacedBinding: the v1 shape's second
// Birth swaps the agents binding to a fresh session — the old
// session's row must fold in the same pass (the attach refusal that
// follows is the expected second-birth shape, not the assertion).
func TestUnstaffedRebirthFoldsDisplacedBinding(t *testing.T) {
	st := twinStage(t, false)
	if _, err := st.d.Birth("小马", "人事", "", "", ""); err != nil {
		t.Fatalf("初次出生不应失败：%v", err)
	}
	_, foldsBefore := st.snapshot()
	if len(foldsBefore) != 0 {
		t.Fatalf("初次出生不该有归档：%v", foldsBefore)
	}

	_, err := st.d.Birth("小马", "人事", "", "", "") // second birth: swap binding
	if err == nil || !strings.Contains(err.Error(), "已在调度中") {
		t.Fatalf("二次出生应在入座处被拒（受管成员无需重复出生），得：%v", err)
	}
	// the rebirth's pin rides registerTaskIndexAsync's goroutine — the
	// attach refusal returns before it must have landed, so poll.
	deadline := time.Now().Add(2 * time.Second)
	var pins []zcode.TaskIndexEntry
	var folds []string
	for {
		pins, folds = st.snapshot()
		if len(pins) >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(folds) != 1 || folds[0] != "sess-stub-1" {
		t.Fatalf("被顶替的旧会话行应恰好折叠一次（sess-stub-1）：%v", folds)
	}
	if len(pins) != 2 || pins[1].SessionID != "sess-stub-2" {
		t.Fatalf("新会话应照常注册置顶：%+v", pins)
	}
}

// TestStaffedBirthFoldsStaleV1Binding: the staffing-driven create
// branch meets a profile still carrying a v1-era agents binding —
// the stale id's row folds before the fresh session pins.
func TestStaffedBirthFoldsStaleV1Binding(t *testing.T) {
	st := twinStage(t, true)
	st.d.store.Upsert("小马", "人事", "", "")
	st.d.store.BindSession("小马", "sess-v1-stale")
	// 前置：staffing 是全新空表（twinStage 自建），小马无行——出生必走 create 分支。

	if _, err := st.d.Birth("小马", "人事", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	pins, folds := st.snapshot()
	if len(folds) != 1 || folds[0] != "sess-v1-stale" {
		t.Fatalf("v1 残留绑定应折叠一次：%v", folds)
	}
	if len(pins) != 1 || pins[0].SessionID != "sess-stub-1" {
		t.Fatalf("新会话应注册置顶：%+v", pins)
	}
}

// TestUnbindFoldsSidebarRow: Bind "" retires the member without a
// seat death — the pinned row must fold with the cleared binding.
func TestUnbindFoldsSidebarRow(t *testing.T) {
	st := twinStage(t, false)
	if _, err := st.d.Birth("小马", "人事", "", "", ""); err != nil {
		t.Fatalf("初次出生不应失败：%v", err)
	}
	if err := st.d.Bind("小马", ""); err != nil {
		t.Fatalf("解绑不应失败：%v", err)
	}
	_, folds := st.snapshot()
	if len(folds) != 1 || folds[0] != "sess-stub-1" {
		t.Fatalf("解绑应折叠置顶行：%v", folds)
	}
	if cfg, ok := st.d.store.Get("小马"); !ok || cfg.SessionID != "" {
		t.Fatalf("解绑后档案不应再带会话：%+v", cfg)
	}
}
