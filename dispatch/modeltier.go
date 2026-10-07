package dispatch

// modeltier.go — 能力装配与座位模型档：composeFor/Assemble、模型档
// 热切与出生选档。拆自 dispatcher.go（v2.15 结构整理）。

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// composeFor folds a member's profile plus assembly into the
// effective fabric. (nil, nil, nil, nil) when no capability injection
// is wired — the v1 path stands; an error means composition overflowed
// the fabric cap (the caller decides: degrade at birth, refuse at
// assembly).
func (d *Dispatcher) composeFor(name string, cfg agents.Config) (*capability.EffectiveFabric, []string, []string, error) {
	if d.cfg.Library == nil {
		return nil, nil, nil, nil
	}
	skillKeys, mcpKeys := d.seatAssembly(name)
	skills := capability.EffectiveSkills(cfg.Skills, skillKeys)
	mcps := capability.EffectiveMCPs(cfg.MCPs, mcpKeys)
	fabric, err := capability.Compose(cfg,
		capability.ResolveSkills(d.cfg.Library, skills),
		capability.ResolveMCPs(d.cfg.Library, mcps),
		d.cfg.Library.ModelAuthoring())
	if err != nil {
		return nil, nil, nil, err
	}
	d.foldSeatModel(name, &fabric)
	return &fabric, skills, mcps, nil
}

// seatAssembly reads the seat-level overrides; a nil hook is no
// override on either list.
func (d *Dispatcher) seatAssembly(name string) (skills, mcps []string) {
	if d.cfg.StaffingAssembly == nil {
		return nil, nil
	}
	return d.cfg.StaffingAssembly(name)
}

// seatModel reads the seat-level model tier; a nil hook or a missing
// row is no pick. The reasoning half only rides with a pick.
func (d *Dispatcher) seatModel(name string) (model, reasoning string) {
	if d.cfg.StaffingModel == nil {
		return "", ""
	}
	return d.cfg.StaffingModel(name)
}

// foldSeatModel folds the seat-level model tier into a composed
// fabric: an explicit pick REPLACES the pack slot's tier (the seat is
// the operator's latest word — the same staffing-replaces-profile
// rule the packs slot follows, Q18), thinking intensity included; a
// reasoning-only pick (empty model — the ZCode picker's default-model
// shape) re-voices the intensity over whatever model the chain
// resolves, the pack slot's tier when one stands, else the process
// default; both slots empty leaves the composed tier untouched.
// Mutates in place; the fabric must not alias caller memory (Compose
// copies, Assemble's caller passes a fresh value).
func (d *Dispatcher) foldSeatModel(name string, fabric *capability.EffectiveFabric) {
	pick, pickReasoning := d.seatModel(name)
	switch {
	case pick != "":
		fabric.Model = &capability.ModelTier{ID: pick, Reasoning: pickReasoning}
	case pickReasoning != "":
		if fabric.Model != nil {
			tier := *fabric.Model // 拷贝再覆写强度：包槽的档对象可能被共享
			tier.Reasoning = pickReasoning
			fabric.Model = &tier
		} else if d.cfg.Model != "" {
			fabric.Model = &capability.ModelTier{ID: d.cfg.Model, Reasoning: pickReasoning}
		}
	}
}

// Assemble re-resolves a member's effective assembly (seat override ⊕
// profile default, Q18) and applies the effect matrix: skill bodies
// and manuals land on the next injection (the 【技能更新】 header);
// the model pick switches immediately when idle, at turn end when
// running; MCP servers take effect at the NEXT BIRTH — session/create
// is the only MCP door zcode exposes, a live session's fleet stays
// what it was born with. A composition overflow refuses the whole
// assembly — nothing changes. The ws/http/cli faces call this after
// mutating the assembly lists.
func (d *Dispatcher) Assemble(name string) error {
	if d.cfg.Library == nil {
		return errors.New(i18n.S("调度器未接能力装配"))
	}
	m := d.lookup(name)
	if m == nil {
		return errors.New(i18n.Sf("%s 不在调度中", name))
	}
	if d.store == nil {
		return errors.New(i18n.S("agents store 不可用"))
	}
	cfg, ok := d.store.Get(name)
	if !ok {
		return errors.New(i18n.Sf("%s 未建档", name))
	}
	seatSkillKeys, seatMCPKeys := d.seatAssembly(name)
	skills := capability.EffectiveSkills(cfg.Skills, seatSkillKeys)
	mcps := capability.EffectiveMCPs(cfg.MCPs, seatMCPKeys)
	fabric, err := capability.Compose(cfg,
		capability.ResolveSkills(d.cfg.Library, skills),
		capability.ResolveMCPs(d.cfg.Library, mcps),
		d.cfg.Library.ModelAuthoring())
	if err != nil {
		return errors.New(i18n.Sf("装配拒绝：%v", err))
	}
	d.foldSeatModel(name, &fabric)

	d.mu.Lock()
	header := capHeader(m.fab.skillKeys, m.fab.mcpKeys, skills, mcps, m.fab.fabric, &fabric)
	m.fab.fabric = &fabric
	m.fab.skillKeys = skills
	m.fab.mcpKeys = mcps
	if header != "" {
		m.fab.pendingCap = header
	}
	// Model pick: defer the switch when a turn is in flight — it
	// lands at the turn's terminal (onEvent/onState → flushModel).
	switchNow := fabric.Model != nil &&
		(fabric.Model.ID != m.fab.appliedModel || fabric.Model.Reasoning != m.fab.appliedReasoning)
	if switchNow && m.status == StatusRunning {
		m.fab.wantModel = fabric.Model
		switchNow = false
	}
	sid := m.sessionID
	d.mu.Unlock()

	if switchNow {
		d.applyTier(m, sid, fabric.Model)
	}
	return nil
}

// applyTier switches the seat's model tier and, on success, records
// what the session now runs on, reporting whether it landed. A failed
// switch keeps the old tier and says so once (applyModel) — the birth
// tolerance habit; the bool lets the write faces say what actually
// stands instead of a blind 已切换.
func (d *Dispatcher) applyTier(m *member, sessionID string, want *capability.ModelTier) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !d.applyModel(ctx, sessionID, m.name, want.ID, want.Reasoning) {
		return false
	}
	d.mu.Lock()
	m.fab.appliedModel, m.fab.appliedReasoning = want.ID, want.Reasoning
	d.mu.Unlock()
	return true
}

// flushModel applies a deferred model switch once the member leaves
// the running state (the turn-end half of the model slot).
func (d *Dispatcher) flushModel(m *member) {
	d.mu.Lock()
	want := m.fab.wantModel
	sid := m.sessionID
	m.fab.wantModel = nil
	d.mu.Unlock()
	if want == nil {
		return
	}
	d.applyTier(m, sid, want)
}

// SetSeatModel re-picks a member's seat model tier ("providerId/modelId"
// or a bare id + its thinking intensity, the CLI's reasoningLevel).
// Either slot may fly solo: an empty model keeps the default chain's
// model and a non-empty reasoning re-voices its intensity (the ZCode
// picker's shape — the default model is a real model there); emptying
// both clears the pick back to the pack ⊕ process default chain. The
// staffing row is written first — the single truth — then a seated
// member switches through the assembly effect matrix (immediately when
// idle, at turn end when mid-turn, the 【能力更新】 header riding the
// next injection); a member not currently dispatched keeps the written
// pick for their next birth. Works without the capability store too:
// the tier is applied straight to the session. The closing notice
// says what actually stands — switched now, deferred to the turn's
// end, or kept on the old tier — never a blind 已切换.
func (d *Dispatcher) SetSeatModel(name, model, reasoning string) error {
	if d.staff == nil {
		return errors.New(i18n.S("编制表未启用（座位模型仅在多办公室形态可用）"))
	}
	if strings.TrimSpace(name) == "" {
		return errors.New(i18n.S("name 必填"))
	}
	model = strings.TrimSpace(model)
	reasoning = strings.TrimSpace(reasoning)
	if _, err := d.staff.SetModel(d.projectKey, name, model, reasoning); err != nil {
		return err
	}
	label := model
	if label == "" {
		label = i18n.S("默认（调度配置）")
	}
	if reasoning != "" {
		label += i18n.Sf("（思考 %s）", reasoning)
	}

	m := d.lookup(name)
	if m == nil {
		d.hub.System(i18n.Sf("[调度] %s 的座位模型已改记为 %s（当前不在座，出生/召回时生效）", name, label))
		return nil
	}

	if d.cfg.Library != nil {
		if err := d.Assemble(name); err != nil {
			// The row is written — say what stands instead of refusing.
			d.hub.System(i18n.Sf("[调度] %s 的座位模型已落账为 %s（%v——接入后生效）", name, label, err))
			return nil
		}
	} else {
		// 无能力库时直切会话：显式 pick 用 pick 本身；强度单飞挂到默认
		// 链解析的模型（包槽若有则其模型，否则调度默认）——与
		// foldSeatModel 同一条链，不读 applied（那是可能刚被清掉的旧档）。
		target := model
		if target == "" && reasoning != "" {
			d.mu.Lock()
			if m.fab.fabric != nil && m.fab.fabric.Model != nil {
				target = m.fab.fabric.Model.ID
			}
			d.mu.Unlock()
			if target == "" {
				target = d.cfg.Model
			}
		}
		if target != "" {
			if d.applyTier(m, d.sessionOf(m), &capability.ModelTier{ID: target, Reasoning: reasoning}) {
				d.hub.System(i18n.Sf("[调度] %s 的座位模型已切换为 %s", name, label))
			} else {
				d.hub.System(i18n.Sf("[调度] %s 的座位模型已落账为 %s（会话沿用旧档）", name, label))
			}
			return nil
		}
	}

	// Clearing the pick (both slots empty): the matrix may now name no
	// tier at all (no pack slot either) — park the member back on the
	// process default so "回默认" means what it says, through the same
	// effect matrix as any other pick: immediately when idle, at turn
	// end when mid-turn. A reasoning-only pick never reaches here with
	// a resolvable model (the fold put a tier in the fabric and the
	// Assemble above switched or deferred it).
	if model == "" {
		d.mu.Lock()
		hasTier := m.fab.fabric != nil && m.fab.fabric.Model != nil
		running := m.status == StatusRunning
		sid := m.sessionID
		d.mu.Unlock()
		if !hasTier && d.providerID != "" && d.cfg.Model != "" {
			want := &capability.ModelTier{ID: d.cfg.Model, Reasoning: d.cfg.Reasoning}
			if running {
				d.mu.Lock()
				m.fab.wantModel = want
				d.mu.Unlock()
			} else {
				d.applyTier(m, sid, want)
			}
		}
	}

	// 收口播报按实况分岔，不得谎称切换：落账的行是唯一真相，但会话
	// 可能当场切成了、在跑要等回合末（wantModel 挂着）、切失败沿用了
	// 旧档（applyModel 已播过失败详情）、或当下无可切（出生时生效）。
	d.mu.Lock()
	var want capability.ModelTier
	hasWant := false
	if m.fab.fabric != nil && m.fab.fabric.Model != nil {
		want, hasWant = *m.fab.fabric.Model, true
	} else if model == "" && d.providerID != "" && d.cfg.Model != "" {
		want, hasWant = capability.ModelTier{ID: d.cfg.Model, Reasoning: d.cfg.Reasoning}, true
	}
	landed := hasWant && want.ID == m.fab.appliedModel && want.Reasoning == m.fab.appliedReasoning
	deferred := m.fab.wantModel != nil
	d.mu.Unlock()
	switch {
	case deferred:
		d.hub.System(i18n.Sf("[调度] %s 的座位模型已落账为 %s（回合结束时切换）", name, label))
	case landed:
		d.hub.System(i18n.Sf("[调度] %s 的座位模型已切换为 %s", name, label))
	case hasWant:
		d.hub.System(i18n.Sf("[调度] %s 的座位模型已落账为 %s（会话沿用旧档）", name, label))
	default:
		d.hub.System(i18n.Sf("[调度] %s 的座位模型已落账为 %s（出生时生效）", name, label))
	}
	return nil
}

// applyModel issues the SetModel for one tier. A "providerId/modelId"
// ref carries its own provider; a bare id resolves per-model through
// the ordered pick list (ResolveModelProvider — account faces first,
// the desktop picker's order; the process-level providerID only
// covers cfg.Model). Reports whether the switch landed; failures keep
// the old tier.
func (d *Dispatcher) applyModel(ctx context.Context, sessionID, name, modelID, reasoning string) bool {
	if modelID == "" {
		return false
	}
	var providerID string
	if p, m := zcode.ParseModelRef(modelID); p != "" {
		providerID, modelID = p, m
	} else {
		resolve := d.cfg.ProviderResolve
		if resolve == nil {
			resolve = zcode.ResolveModelProvider
		}
		providerID, _ = resolve(modelID)
	}
	if providerID == "" {
		d.hub.System(i18n.Sf("[调度] %s 的模型 %s 无可用个人 provider，沿用现档", name, modelID))
		return false
	}
	sel := zcode.ModelSelection{ProviderID: providerID, ModelID: modelID}
	if reasoning != "" {
		sel.Options = &zcode.ModelOptions{ReasoningLevel: reasoning}
	}
	if err := d.bridge.SetModel(ctx, sessionID, sel); err != nil {
		log.Printf("[调度] %s setModel(%s) 失败：%v（沿用旧档）", name, modelID, err)
		// 要求思考强度的拒单要说清怎么补：GLM-5.3 走个人中转会拒裸
		// pick，操作员重选一档强度即可——附上 CLI 档位表的可选项。
		if reasoning == "" && reasoningRequired(err) {
			if lv := zcode.ReasoningLevels(modelID); len(lv) > 0 {
				d.hub.System(i18n.Sf("[调度] %s 切换模型 %s 失败，沿用旧档（%v；该模型要求思考强度，可选 %s）",
					name, modelID, err, strings.Join(lv, "/")))
			} else {
				d.hub.System(i18n.Sf("[调度] %s 切换模型 %s 失败，沿用旧档（%v；该模型要求思考强度）",
					name, modelID, err))
			}
		} else {
			d.hub.System(i18n.Sf("[调度] %s 切换模型 %s 失败，沿用旧档（%v）", name, modelID, err))
		}
		return false
	}
	return true
}

// reasoningRequired reports whether a setModel rejection is the backend
// demanding an explicit thinking intensity (GLM-5.3 on the personal
// relay rejects a bare pick). The CLI's level table can't predict this
// — its catch-all rule gives every model a level list — so the
// rejection itself is the only oracle.
func reasoningRequired(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "reasoning level is required")
}

// noteApplied records the tier a freshly born session runs on (Birth
// path; Assemble goes through applyTier).
func (d *Dispatcher) noteApplied(name, modelID, reasoning string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if m, ok := d.members[name]; ok {
		m.fab.appliedModel, m.fab.appliedReasoning = modelID, reasoning
	}
}

// capHeader renders the digest the member sees on their next
// injection: skill adds/removes, the model tier change, and the MCP
// delta (which lands at the next birth — said so, honestly). Empty
// when nothing observable moved (re-assembling the same lists is
// silent).
func capHeader(oldSkills, oldMCPs, newSkills, newMCPs []string, oldF, newF *capability.EffectiveFabric) string {
	var parts []string
	if added := sliceDiff(newSkills, oldSkills); len(added) > 0 {
		parts = append(parts, "新增技能："+strings.Join(added, "、"))
	}
	if removed := sliceDiff(oldSkills, newSkills); len(removed) > 0 {
		parts = append(parts, "移除技能："+strings.Join(removed, "、"))
	}
	if added := sliceDiff(newMCPs, oldMCPs); len(added) > 0 {
		parts = append(parts, "新增 MCP："+strings.Join(added, "、")+"（下次出生生效）")
	}
	if removed := sliceDiff(oldMCPs, newMCPs); len(removed) > 0 {
		parts = append(parts, "移除 MCP："+strings.Join(removed, "、")+"（下次出生生效）")
	}
	if label := modelLabel(newF); label != "" && label != modelLabel(oldF) {
		parts = append(parts, "模型档位："+label)
	}
	if len(parts) == 0 {
		return ""
	}
	return "【技能更新｜生效】" + strings.Join(parts, "；")
}

// modelLabel renders a fabric's model tier ("id/reasoning"; reasoning
// omitted when empty; "" when the fabric names no model).
func modelLabel(f *capability.EffectiveFabric) string {
	if f == nil || f.Model == nil {
		return ""
	}
	if f.Model.Reasoning == "" {
		return f.Model.ID
	}
	return f.Model.ID + "/" + f.Model.Reasoning
}

// sliceDiff returns the values of a in order, keeping those absent
// from b (assembly digest helper — a package-local fact, not util
// material).
func sliceDiff(a, b []string) []string {
	var out []string
	skip := make(map[string]struct{}, len(b))
	for _, v := range b {
		skip[v] = struct{}{}
	}
	for _, v := range a {
		if _, dup := skip[v]; !dup {
			out = append(out, v)
		}
	}
	return out
}
