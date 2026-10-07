package dispatch

// seatmodel_test — 思考强度（ZCode 的 reasoningLevel）随座位模型档
// 流转的定格：出生带档（表单 pick → setModel options + staffing 行
// 落账 + Snapshot 回显）、改档带档（写面 → 立即切换）、清档（两槽
// 皆空才清——档位是一对可单飞的槽，强度不跟模型殉葬）、强度单飞
//（空模型挂默认链模型——ZCode 拾取器口径）。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// postDispatch drives one fleet POST and refuses anything but 200.
func postDispatch(t *testing.T, f *Fleet, path, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s = %d: %s", path, rec.Code, rec.Body.String())
	}
}

// snapshotOf reads the lobby dispatcher's status snapshot for one
// member (missing member = zero Status, the callers fail their own
// assertions on it).
func snapshotOf(f *Fleet, name string) Status {
	d := f.Get(chat.LobbyKey)
	if d == nil {
		return Status{}
	}
	for _, s := range d.Snapshot() {
		if s.Name == name {
			return s
		}
	}
	return Status{}
}

// TestBirthCarriesReasoning pins the birth pick's tier shape: an
// explicit model + thinking intensity rides the setModel options
// (providerId/modelId + options.reasoningLevel — the CLI's own
// triple), lands in the staffing row, and echoes in the snapshot.
func TestBirthCarriesReasoning(t *testing.T) {
	b := &stubBridge{}
	st := recruitStage(t, b, false)
	postDispatch(t, st.f, "/dispatch/birth",
		`{"name":"小思","role":"后端","model":"prov/GLM-X","reasoning":"low"}`)
	sel := b.lastModel()
	if sel.ProviderID != "prov" || sel.ModelID != "GLM-X" ||
		sel.Options == nil || sel.Options.ReasoningLevel != "low" {
		t.Fatalf("出生档应带思考强度下发: %+v", sel)
	}
	row, ok := st.f.staff.Get(chat.LobbyKey, "小思")
	if !ok || row.Model != "prov/GLM-X" || row.Reasoning != "low" {
		t.Fatalf("出生档应落 staffing 行: %+v", row)
	}
	if s := snapshotOf(st.f, "小思"); s.Model != "prov/GLM-X" || s.Reasoning != "low" {
		t.Fatalf("Snapshot 应回显座位档（模型+思考强度）: %+v", s)
	}
}

// TestSetSeatModelReasoning pins the 改档 write face: a later pick
// with an intensity switches the live session through setModel's
// options, and re-picking the SAME model with a different intensity
// still switches (the matrix compares the whole triple).
func TestSetSeatModelReasoning(t *testing.T) {
	b := &stubBridge{}
	st := recruitStage(t, b, false)
	postDispatch(t, st.f, "/dispatch/birth", `{"name":"小思","role":"后端"}`)
	postDispatch(t, st.f, "/dispatch/model",
		`{"name":"小思","model":"prov/GLM-Y","reasoning":"max"}`)
	if sel := b.lastModel(); sel.ModelID != "GLM-Y" ||
		sel.Options == nil || sel.Options.ReasoningLevel != "max" {
		t.Fatalf("改档应带思考强度切换: %+v", sel)
	}
	if s := snapshotOf(st.f, "小思"); s.Model != "prov/GLM-Y" || s.Reasoning != "max" {
		t.Fatalf("Snapshot 应回显改档: %+v", s)
	}
	// 同模型换强度也要切（三元组的比较，不是只看模型 id）
	postDispatch(t, st.f, "/dispatch/model",
		`{"name":"小思","model":"prov/GLM-Y","reasoning":"low"}`)
	if sel := b.lastModel(); sel.Options == nil || sel.Options.ReasoningLevel != "low" {
		t.Fatalf("同模型换强度应再切: %+v", sel)
	}
}

// TestSetSeatModelClearDropsReasoning pins the clear rule: emptying
// BOTH slots clears the tier (a lone reasoning on an empty pick is a
// reasoning-only tier now, not a clear — see below). The row loses
// the pick and the live session parks back on the process default
// (GLM-DEF, no intensity) through the same effect matrix as any
// pick — mid-turn it lands at the turn's end (flushModel), hence the
// explicit flush here — and the snapshot stops echoing the old tier.
func TestSetSeatModelClearDropsReasoning(t *testing.T) {
	b := &stubBridge{}
	st := modelStage(t, b)
	postDispatch(t, st.f, "/dispatch/birth",
		`{"name":"小思","role":"后端","model":"prov/GLM-X","reasoning":"low"}`)
	postDispatch(t, st.f, "/dispatch/model", `{"name":"小思","model":"","reasoning":""}`)
	row, ok := st.f.staff.Get(chat.LobbyKey, "小思")
	if !ok || row.Model != "" || row.Reasoning != "" {
		t.Fatalf("两槽皆空才是清档: %+v", row)
	}
	// stub 世界成员出生后常驻 running，回默认的停靠要等回合末——
	// 手动落闸后座位才真停回默认链。
	d := st.f.Get(chat.LobbyKey)
	m := d.lookup("小思")
	d.setStatus(m, StatusIdle)
	d.flushModel(m)
	if s := snapshotOf(st.f, "小思"); s.Model != "GLM-DEF" || s.Reasoning != "" {
		t.Fatalf("清档后座位应停回默认链（无强度）: %+v", s)
	}
}

// modelStage is recruitStage with a default chain the seat can hang a
// reasoning-only pick on: cfg.Model "GLM-DEF" resolves to a stub
// personal provider (the hermetic ProviderResolve seam — the real
// ~/.zcode lookup never runs in tests).
func modelStage(t *testing.T, b *stubBridge) recruitStageOut {
	return recruitStageCfg(t, b, false, func(c *Config) {
		c.Model = "GLM-DEF"
		c.ProviderResolve = func(string) (string, bool) { return "prov", true }
	})
}

// TestReasoningOnlyRidesDefaultChain pins the ZCode-picker shape: a
// thinking intensity with NO model pick rides the default-chain model
// — the staffing row keeps the lone reasoning, the live session
// switches to the default model at that intensity, and the snapshot
// echoes the intensity over the default model. Both at birth and
// through the 改档 write face.
func TestReasoningOnlyRidesDefaultChain(t *testing.T) {
	b := &stubBridge{}
	st := modelStage(t, b)
	// 出生即单飞：空模型＋max → 默认链 GLM-DEF 挂 max
	postDispatch(t, st.f, "/dispatch/birth",
		`{"name":"小默","role":"后端","reasoning":"max"}`)
	if sel := b.lastModel(); sel.ProviderID != "prov" || sel.ModelID != "GLM-DEF" ||
		sel.Options == nil || sel.Options.ReasoningLevel != "max" {
		t.Fatalf("强度单飞应落默认链模型: %+v", sel)
	}
	row, ok := st.f.staff.Get(chat.LobbyKey, "小默")
	if !ok || row.Model != "" || row.Reasoning != "max" {
		t.Fatalf("出生的单飞强度应落 staffing 行: %+v", row)
	}
	if s := snapshotOf(st.f, "小默"); s.Model != "GLM-DEF" || s.Reasoning != "max" {
		t.Fatalf("Snapshot 应回显默认链模型+强度: %+v", s)
	}

	// 改档面单飞：清模型留强度 → 默认链模型换档，模型回显跟默认链
	postDispatch(t, st.f, "/dispatch/birth",
		`{"name":"小响","role":"测试","model":"prov/GLM-X","reasoning":"low"}`)
	postDispatch(t, st.f, "/dispatch/model", `{"name":"小响","model":"","reasoning":"high"}`)
	if sel := b.lastModel(); sel.ModelID != "GLM-DEF" ||
		sel.Options == nil || sel.Options.ReasoningLevel != "high" {
		t.Fatalf("清模型留强度应切到默认链模型: %+v", sel)
	}
	row, ok = st.f.staff.Get(chat.LobbyKey, "小响")
	if !ok || row.Model != "" || row.Reasoning != "high" {
		t.Fatalf("改档的单飞强度应落 staffing 行: %+v", row)
	}
	if s := snapshotOf(st.f, "小响"); s.Model != "GLM-DEF" || s.Reasoning != "high" {
		t.Fatalf("Snapshot 应回显默认链模型+新强度: %+v", s)
	}
}

// pickyBridge takes every verb but rejects a setModel pick carrying
// no thinking intensity — the GLM-5.3-personal-relay shape behind the
// -32603 rejections (a bare pick dies, an intensity-tagged one lands).
type pickyBridge struct {
	stubBridge
}

func (b *pickyBridge) SetModel(ctx context.Context, sessionID string, sel zcode.ModelSelection) error {
	if sel.Options == nil || sel.Options.ReasoningLevel == "" {
		return fmt.Errorf("zcode: [-32603] Reasoning level is required for %s/%s", sel.ProviderID, sel.ModelID)
	}
	b.stubBridge.SetModel(ctx, sessionID, sel)
	return nil
}

// TestSetSeatModelBarePickFailureSaysWhatStands pins the honest
// closing notice (the 小马/小狐 contradictory-log regression): a bare
// pick the relay rejects keeps the live session on the old tier — the
// staffing row still lands (the single truth), the failure notice
// names the thinking-intensity requirement, and the closing notice
// says 落账＋沿用旧档, never a blind 已切换. The recovery re-pick
// with an intensity then really switches.
func TestSetSeatModelBarePickFailureSaysWhatStands(t *testing.T) {
	b := &pickyBridge{}
	st := recruitStageCfg(t, b, false, func(c *Config) {
		c.Library = openTestLibrary(t)
	})
	tap := newSystemTap(st.hub)
	postDispatch(t, st.f, "/dispatch/birth", `{"name":"小思","role":"后端"}`)
	// stub 世界成员常驻 running，而失败路径要在闲置位当场试切——先落闸。
	d := st.f.Get(chat.LobbyKey)
	m := d.lookup("小思")
	if m == nil {
		t.Fatal("小思应已入座")
	}
	d.setStatus(m, StatusIdle)
	postDispatch(t, st.f, "/dispatch/model", `{"name":"小思","model":"prov/GLM-X"}`)
	row, ok := st.f.staff.Get(chat.LobbyKey, "小思")
	if !ok || row.Model != "prov/GLM-X" || row.Reasoning != "" {
		t.Fatalf("编制行应照落（唯一真相）: %+v", row)
	}
	// System 行只广播不进 History，观察员座位异步入队——等收口行到
	// 队头，前面的失败详情行必已排空（FIFO）。
	waitFor(t, func() bool { return tap.count("prov/GLM-X（会话沿用旧档）") > 0 },
		"收口播报应说落账且会话沿用旧档")
	if tap.count("要求思考强度") == 0 {
		t.Fatalf("失败播报应点名思考强度要求，得：%v", tap.snapshot())
	}
	for _, s := range tap.snapshot() {
		if strings.Contains(s, "座位模型已切换为 prov/GLM-X") {
			t.Fatalf("切失败不得谎称已切换: %s", s)
		}
	}
	// 恢复路径：补一档强度重选，应真实下发并播已切换
	postDispatch(t, st.f, "/dispatch/model", `{"name":"小思","model":"prov/GLM-X","reasoning":"high"}`)
	sel := b.lastModel()
	if sel.ModelID != "GLM-X" || sel.Options == nil || sel.Options.ReasoningLevel != "high" {
		t.Fatalf("带强度重选应真实下发: %+v", sel)
	}
	waitFor(t, func() bool { return tap.count("座位模型已切换为 prov/GLM-X（思考 high）") > 0 },
		"恢复路径应播已切换")
}

// TestSetSeatModelRunningDefersToTurnEnd pins the mid-turn shape: a
// pick on a running member parks on wantModel, the closing notice says
// 回合结束时切换 (not a blind 已切换), and the flush at the turn's
// terminal really issues the setModel.
func TestSetSeatModelRunningDefersToTurnEnd(t *testing.T) {
	b := &stubBridge{}
	st := recruitStageCfg(t, b, false, func(c *Config) {
		c.Library = openTestLibrary(t)
	})
	tap := newSystemTap(st.hub)
	postDispatch(t, st.f, "/dispatch/birth", `{"name":"小跑","role":"后端"}`)
	d := st.f.Get(chat.LobbyKey)
	m := d.lookup("小跑")
	if m == nil {
		t.Fatal("小跑应已入座")
	}
	d.setStatus(m, StatusRunning) // stub 世界出生即常驻 running，这里显式钉住
	postDispatch(t, st.f, "/dispatch/model", `{"name":"小跑","model":"prov/GLM-Y"}`)
	d.mu.Lock()
	want := m.fab.wantModel
	d.mu.Unlock()
	if want == nil || want.ID != "prov/GLM-Y" {
		t.Fatalf("在跑成员的改档应挂在 wantModel 等回合末: %+v", want)
	}
	waitFor(t, func() bool { return tap.count("prov/GLM-Y（回合结束时切换）") > 0 },
		"收口播报应说回合结束时切换")
	for _, s := range tap.snapshot() {
		if strings.Contains(s, "座位模型已切换为 prov/GLM-Y") {
			t.Fatalf("未切换不得谎称已切换: %s", s)
		}
	}
	// 回合终了：flush 落地真实的 setModel
	d.setStatus(m, StatusIdle)
	d.flushModel(m)
	sel := b.lastModel()
	if sel.ProviderID != "prov" || sel.ModelID != "GLM-Y" {
		t.Fatalf("回合末应真实下发切换: %+v", sel)
	}
}
