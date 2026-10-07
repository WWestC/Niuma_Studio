package dispatch

// autopilot.go — 全智能模式's dispatcher half (v2.8). The engine's
// decisions live server-side (server/autopilot.go); this file owns the
// member-facing pieces:
//
//   - AutoPilotPoke: the low-water stocking poke's 【自动驾驶·选题】
//     injection into the orchestrator's session (the server engine's
//     cfg.Poke, fleet-routed — the EstablishmentAdded shape: server
//     decides, fleet routes, dispatcher delivers; r_17: busy or idle,
//     the pool is restocked whenever it dips below the target line);
//   - AutoPilotDivert: the stalled-backlog 【自动驾驶·拆解】 injection
//     (cfg.Divert, its own fleet route — r_17 split it off cfg.Poke so
//     the 拆解 intent never arrives wearing the 选题 header);
//   - AutoPilotResume: the resume gate's 【自动驾驶·续命】 injection
//     into a stalled doing-task assignee's session (cfg.Resume, the
//     same fleet route) — a busy member takes the line through the
//     queue lane like any @mention, an idle one is woken now;
//   - autoAdvanceOn: the 自动推进 half of the r_19 双开关 (the engine
//     reads the same staffing bits), for onReverse's two hands-off
//     branches (permission auto-allow, AskUserQuestion answered on
//     assumption) — those edits live in dispatcher.go next to the
//     paths they short-circuit. 自动补货 alone never touches them:
//     restocking is attended mode, the host's gates stand.
//
// The lobby is a first-class scope (the engine's round runs it first —
// the flagship orchestrator sits there): its UNSET settings entry
// simply reads off, byte-for-byte the pre-autopilot behavior; flipped
// on, studio-level autonomy runs the same loop (复查#6 — the old
// "project-only" note predates the lobby branch).

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// AutoPilotPoke delivers the stocking injection to the seated
// orchestrator. An error means nobody to drive (no orchestrator) — the
// server engine surfaces it to the room; the poke itself is silent by
// contract (the patrol/daily rule: the injection rides session/send,
// the orchestrator's ACTIONS produce the frames).
func (d *Dispatcher) AutoPilotPoke(openN int) error {
	name := d.orchestratorName()
	if name == "" {
		return errors.New(i18n.Sf("没有在岗编排者（岗位「%s」）——选题注入无人可送，请房主补齐编排者岗位或关闭自动补货", agents.OrchestratorRole))
	}
	d.deliver(name, autoPilotTopicPrompt(d.projectKey, name, openN, d.waterTarget(), d.stockGoal()), nil, nil)
	d.stampAutoYield()
	return nil
}

// stampAutoYield notes that an autopilot injection just went out to
// this room's orchestrator — the meeting clock yields one beat (r_17
// 让拍): the injection may be about to direct-submit a small
// requirement, and the clock must not race it into a meeting first.
func (d *Dispatcher) stampAutoYield() {
	d.mu.Lock()
	d.apYieldUntil = time.Now().Add(AutoInjectMeetingYield)
	d.mu.Unlock()
}

// autoPilotTopicPrompt is the frozen 【自动驾驶·选题】 injection
// (r_17 蓄水版, r_19 双开关版; 目标制补货版): the orchestrator STOCKS
// the pool — file requirements into the ledger until the water level
// reaches the target line, then stop and leave them there. The pre-r_17
// shape had the orchestrator push the single filing straight through to
// a proposal, which pinned the pool at 0..1 (the count the host kept
// seeing); consumption is now the 拆解 divert and the meeting clock's
// beat, never the restock's. Pinned by tests. target＝蓄水目标线的活口径
// （r_19：staffing 旋钮 water_target > 默认 3——引擎闸与措辞同源）。
// goal＝本次目标（目标制补货）：非空时选题围绕目标展开——「本期无新
// 需求」不再是合法的常态答复（那是无目标模式的旧出路，治的是「每 4
// 小时一句环境零变化」的死循环），目标达成或确认无法达成时走
// niuma autopilot complete 收摊（编排者的判定职权，关的是双开关）。
func autoPilotTopicPrompt(projectKey, orchestrator string, openN, target int, goal string) string {
	if goal != "" {
		return fmt.Sprintf(`【自动驾驶·选题】now=%d 项目=%s。房主已开启自动补货（本次目标：%s）：需求池水位 open %d 条、低于目标线 %d 条，由你补货——把需求先写进池里蓄着（先蓄需求，不急开工）。
做法：
1) 读 kb docs 与 task list --json，结合项目文档与已完成工作，判断通向本次目标最有价值的几条需求；
2) 用 niuma req create --name %s --project %s --title "…" --body "…" 逐条立进需求台账（body 里附可自验的验收标准：自动化测试或明确的检查步骤），把 open 补足到 ≥%d 条即停；
3) 在房内 say 播报你立了哪些、为什么通向目标。
本次目标达成判定（每轮自查）：
· 对照目标逐项核验：目标未达成前，不得以「环境零变化/本期无新需求」应付——要么立出通向目标的需求，要么说明目标受阻的具体环节与原因；
· 目标已全部达成，或确认无法达成/已作废（说明原因）：立即执行 niuma autopilot complete --name %s --project %s --note "一句话结论"——本房的自动补货与自动推进将自动关闭，随后在房内 say 播报收摊总结。
纪律：新立需求留在池里等拆解时机，不随手推进提案（拆解另有注入与会议钟）；选题质量优先于水位，不为凑数立低价值需求；拿不准的环节自行决策并在描述里注明假设。`,
			util.Now(), projectKey, goal, openN, target, orchestrator, projectKey, target, orchestrator, projectKey)
	}
	return fmt.Sprintf(`【自动驾驶·选题】now=%d 项目=%s。房主已开启自动补货：需求池水位 open %d 条、低于目标线 %d 条，由你补货——把需求先写进池里蓄着（先蓄需求，不急开工）。
做法：
1) 读 kb docs 与 task list --json，结合项目文档与已完成工作，判断接下来最有价值的几条需求；
2) 用 niuma req create --name %s --project %s --title "…" --body "…" 逐条立进需求台账（body 里附可自验的验收标准：自动化测试或明确的检查步骤），把 open 补足到 ≥%d 条即停；
3) 在房内 say 播报你立了哪些、为什么值得做。
纪律：新立需求留在池里等拆解时机，不随手推进提案（拆解另有注入与会议钟）；选题质量优先于水位，不为凑数立低价值需求；拿不准的环节自行决策并在描述里注明假设；确无可做的下一步就明说「本期无新需求」并说明理由——不硬造需求。`,
		util.Now(), projectKey, openN, target, orchestrator, projectKey, target)
}

// AutoPilotDivert is the【自动驾驶·拆解】wake (r_13 t_175; r_17 直提
// 旁路版): the pool has open requirements but nobody is decomposing
// them — the orchestrator is nudged to review and split what exists
// instead of filing yet another requirement.
func (d *Dispatcher) AutoPilotDivert(projectKey, orchestrator string, openN int) error {
	if d.lookup(orchestrator) == nil {
		return errors.New(i18n.Sf("没有在岗编排者（岗位「%s」）——拆解注入无人可送", agents.OrchestratorRole))
	}
	d.deliver(orchestrator, autoPilotDivertPrompt(projectKey, openN), nil, nil)
	d.stampAutoYield()
	return nil
}

// autoPilotDivertPrompt is the frozen【自动驾驶·拆解】injection (r_17
// 直提旁路版): consume what the pool already holds — small and
// unambiguous requirements go straight to plan submit; the big or
// contested ones STAY in the pool for the meeting clock (the
// orchestrator has no convene verb of their own —Meetings.Begin is
// the clock's alone— so the wording must not promise one). Pinned by
// tests.
func autoPilotDivertPrompt(projectKey string, openN int) string {
	return fmt.Sprintf(`【自动驾驶·拆解】now=%d 项目=%s。需求池有 %d 条开放需求但无人拆解——请评审立项推进，而不是再立新需求。读 kb docs 里这些开放需求（niuma req list --project %s），挑最有价值的一条拆解：小型清晰、无分叉的直接 plan submit 落提案（不必等评审会）；体量大或需要多方意见的不要直提——留在池里，会议钟稍后会召集评审再定稿。水位纪律：拆解既存需求优先于立新需求；顺手核对暂缓需求的复查期（req list 里 parking 行带原因与 review_after，到期未解禁的核对条件是否满足——暂缓不自动解禁，判断是你的职责）。
（注：parking 需求不在这 %d 条开放需求里——暂缓对拆解钟不可见，解禁走 req unpark。）`,
		util.Now(), projectKey, openN, projectKey, openN)
}

// AutoPilotPatrol delivers the【全智能·产能巡报】to the seated HR
// (r_13 t_175): a template injection carrying the engine's task-load
// snapshot — the HR broadcasts it in the room per the manual's rev 5
// template. An error means no seated HR: the engine cools the window
// silently.
func (d *Dispatcher) AutoPilotPatrol(projectKey, hr, context string) error {
	if hr == "" || d.lookup(hr) == nil {
		return fmt.Errorf("没有在岗人事（岗位「%s」）——巡报无人可送，本窗跳过", agents.HRRole)
	}
	prompt := fmt.Sprintf(`【自动推进·产能巡报】now=%d 项目=%s。引擎快照：%s。
请按 HR 手册 rev5 的巡报模板向房内播报产能现状（数据用 niuma task list --json 与 members 获取）：
| 成员 | 在办 | 状态 |
末行小结：在办 N 条 · 空闲 M 人 · 饱和度 X%%。
若快照带「⚠ 水位偏低」，加一句「⚠ 水位偏低：建议编排者优先补货选题」，并 @排期编排组。
纪律：照实报，不评价不催促——巡报是仪表盘不是军令状。`,
		util.Now(), projectKey, context)
	d.deliver(hr, prompt, nil, nil)
	return nil
}

// hrName finds this room's seated HR by the role marker (the
// orchestratorName pattern — r_13 t_175).
func (d *Dispatcher) hrName() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, m := range d.members {
		if m.role == agents.HRRole {
			return m.name
		}
	}
	return ""
}

// AutoPilotResume delivers the resume gate's stalled-task wake to one
// doing-task assignee (全智能模式's fourth gate; the AutoPilotPoke
// shape). An error means nobody to wake — the assignee is the host or
// a 待招 placeholder, not a managed member of this room — and the
// engine turns it into the room's ambient note, never a refusal. A
// busy member is NOT skipped: deliver's queue lane carries the line
// into their next turn, the same discipline as any @mention.
func (d *Dispatcher) AutoPilotResume(assignee string, stalled []*tasks.Task) error {
	if d.lookup(assignee) == nil {
		ids := make([]string, len(stalled))
		for i, t := range stalled {
			ids[i] = t.ID
		}
		return errors.New(i18n.Sf("任务 %s 负责人 %s 不在本房调度成员（房主本人或待招岗位）——停滞任务等人处置，暂无法自动续做",
			strings.Join(ids, "/"), assignee))
	}
	d.deliver(assignee, autoPilotResumePrompt(d.projectKey, stalled), nil, nil)
	// 续命账目（r_14 微迭代）：送达即记时刻——停滞清单的 15min 记忆窗
	// 读它标「（已续命）」，防清单二次催同一单（定稿 §三去重规则）。
	d.mu.Lock()
	for _, t := range stalled {
		d.resumeMark[t.ID] = util.Now()
	}
	d.mu.Unlock()
	return nil
}

// autoPilotResumePrompt is the frozen 【自动驾驶·续命】 injection:
// the assignee's doing tasks have sat past the stall window, so the
// mode supplies the host's 「接着干」 itself — finish, file the
// ledger straight, or move to what CAN move, on the member's own
// judgement. Pinned by tests.（r_19：随自动推进开关走——补货单开时
// 房主在场，停滞由普通巡检点名，不走这条。）
func autoPilotResumePrompt(projectKey string, stalled []*tasks.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【自动驾驶·续命】now=%d 项目=%s。自动推进巡检：你名下的进行中任务已长时间没有台账更新（负责人空闲、任务停滞）。房主已开启自动推进、不介入流程确认，请自行接续推进，不要等人点名：\n", util.Now(), projectKey)
	for _, t := range stalled {
		fmt.Fprintf(&b, "· %s「%s」\n", t.ID, t.Title)
	}
	b.WriteString("做法：读任务详情与最近上下文接着干；确已完成的用 task update 落账（状态 done＋结论写进进度或备注）；被前置卡住的在 log 说明原因后先推进下一个可动的事。纪律：拿不准的环节自行决策并在描述里注明假设。")
	return b.String()
}

// autoAdvanceOn reads the project's 自动推进 switch (r_19 split: the
// hands-off half — permission auto-allow and ask-on-assumption follow
// the ADVANCE switch alone; 自动补货 running solo never short-circuits
// the host's gates — the host is attending). The lobby is just another
// project key here (” maps to LobbyKey); a nil store or an entry never
// set reads off — the switch defaults off because it burns tokens
// unattended.
func (d *Dispatcher) autoAdvanceOn() bool {
	if d.cfg.StaffStore == nil {
		return false
	}
	key := d.cfg.ProjectKey
	if key == "" {
		key = chat.LobbyKey
	}
	return d.cfg.StaffStore.SettingsOf(key).AutoAdvance
}

// autoAskGraceDefault is the absent-owner ask's 代行宽限 fallback —
// staffing.DefaultAcceptDelayS is the single source (its server-side
// twin AutoPilotAcceptDelay reads the same constant; the old shape was
// two hand-synced 120s values because the import would cycle). A var
// (not a const) purely as the tests' seam (askWindow's discipline).
var autoAskGraceDefault = time.Duration(staffing.DefaultAcceptDelayS) * time.Second

// autoAskGrace is the 代行宽限 for an ask under 自动推进 with the
// owner absent: the same accept_delay knob as proposal auto-accept
// (staffing.AutoPilotKnobs.AcceptDelayS) — 代行等房主，全市一个宽限
// 口径，拧一个旋钮两条腿一起动。An absent owner's question never
// parks the member longer than this before the assumption release.
func (d *Dispatcher) autoAskGrace() time.Duration {
	if d.staff != nil {
		if k := d.staff.SettingsOf(d.projectKey).AutoPilotKnobs; k != nil && k.AcceptDelayS > 0 {
			return time.Duration(k.AcceptDelayS) * time.Second
		}
	}
	return autoAskGraceDefault
}

// waterTarget（r_19）: 蓄水目标线两级链——staffing 旋钮
// （autopilot_knobs.water_target，1–9）> 内置默认 agents.WaterTarget
// （3）。选题注入的「低于目标线/补足到」措辞与引擎蓄水闸、巡报快照
// 共用这一口径（每注入直读即热更）。
func (d *Dispatcher) waterTarget() int {
	if d.staff != nil {
		if k := d.staff.SettingsOf(d.projectKey).AutoPilotKnobs; k != nil && k.WaterTarget > 0 {
			return k.WaterTarget
		}
	}
	return agents.WaterTarget
}

// stockGoal（目标制补货）: staffing 的 stock_goal 直读（每注入即热更）。
// 空＝无目标模式（r_17 旧纪律：确无可做明说「本期无新需求」）——存量
// 已开的房与直连 SetAutoStock 的路径都走这条，不追补目标。
func (d *Dispatcher) stockGoal() string {
	if d.staff != nil {
		return d.staff.SettingsOf(d.projectKey).StockGoal
	}
	return ""
}
