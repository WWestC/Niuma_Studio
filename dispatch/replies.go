package dispatch

// replies.go — 会话事件回房面：onSay 点名路由、onEvent/onState 终点、
// 回复塑形（自动补 @/迟到引用）、sayThrough、计划落地/加编通知注入。
// 拆自 dispatcher.go（v2.15 结构整理）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// sayThrough mirrors a line through the member's seat. When the
// member's own one-shot CLI holds the seat at that instant (the
// supersede lease), SayBridged on the dead seat would silently drop
// the words — so briefly wait the command out, re-take the seat and
// mirror; a holder that outstays the wait hears the reply as a room
// system line instead of losing it. quote（结构化引用） rides the say
// as fields when the reply names its line (迟到回答的自动引用).
func (d *Dispatcher) sayThrough(m *member, text string, quote *chat.Quote) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		// m.seat is swapped under d.mu by the re-seat paths (reseatNow);
		// reading it bare races with them while the lease waits here.
		d.mu.Lock()
		seat := m.seat
		d.mu.Unlock()
		select {
		case <-seat.Done():
			// superseded moments ago: usually the one-shot already left
		default:
			d.hub.SayBridgedQuoted(seat, text, quote)
			return
		}
		if _, held := d.hub.SeatHolderToken(m.name); !held {
			d.reseatNow(m.name, seat, "")
			// 只有真的换上了新座位才值得立刻重照：成员若在镜像等待
			// 期间被离编（kick/offboard/耐心耗尽的 seatGone），m.seat
			// 永远停在死座位上、reseatNow 变成空操作——裸 continue 在
			// 此处零睡眠绕过 deadline，曾把读循环烧到 100% CPU 数小时。
			d.mu.Lock()
			fresh := m.seat
			d.mu.Unlock()
			if fresh != seat {
				continue
			}
			// 无进展（成员已走，或名字被竞速者瞬间拿放）：落入下面共
			// 享的耐心门，3 秒后照旧以系统行保住原文。
		}
		if time.Now().After(deadline) || d.stopped() {
			d.hub.System(i18n.Sf("[调度] %s 的回复贴回时座位暂被占用，原文：%s", m.name, text))
			return
		}
		select {
		case <-d.stop:
			d.hub.System(i18n.Sf("[调度] %s 的回复贴回时座位暂被占用，原文：%s", m.name, text))
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Whisper delivers the owner's private line (私信, manual v0.10 M4
// 占位落地) into one managed member's lane — the same queue/wake
// machinery as any injection, so a busy member reads it as the next
// turn's opening. The wrapper tells the member the line was private
// (their reply rides the normal bridge echo into the room — the
// asymmetry is stated, not hidden). Unknown name = loud refusal; the
// server turns that into the composer's refusal line.
func (d *Dispatcher) Whisper(name, text string) error {
	if d.lookup(name) == nil {
		return errors.New(i18n.Sf("%s 不在调度中——私信只投在编成员", name))
	}
	d.deliver(name, fmt.Sprintf(
		"【私信】房主单独对你说（本条不进房间消息流；你的回复照常回房，注意口径）：%s", text), nil, nil)
	return nil
}

// Steer interrupts the member's running turn with extra input.
func (d *Dispatcher) Steer(name, text string) error {
	m := d.lookup(name)
	if m == nil {
		return errors.New(i18n.Sf("%s 不在调度中", name))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := d.bridge.Steer(ctx, d.sessionOf(m), text)
	if zcode.IsCode(err, zcode.ErrSessionNotActive) {
		if rerr := d.reactivate(m); rerr != nil {
			return rerr
		}
		err = d.bridge.Steer(ctx, d.sessionOf(m), text)
	}
	// r_17：steer 不走 deliverQuoted（bridge.Steer 直给），单独落 input
	// 行——房主的中途修正进同一册终端转录；v2.9 起双门，工作过程同见。
	if err == nil {
		d.emitTrace([]chat.TraceEntry{{From: name, Kind: chat.TraceInput,
			Src: "房主（steer）", Text: clipMiddle(text, chat.TermOutMax)}})
	}
	return err
}

// quoteWire renders a structured quote back into the agent-facing line
// face (「引用 [#N] [@]X：snip」[（转发自「via」）]\nbody): models have
// long read their quote context in exactly this shape — the root fix
// only moved the header OUT of the stored text (复制/检索不再把引用带下
// 来)； the injected view keeps it, so an agent reading a quoted reply
// still sees which line it answers.
func quoteWire(q *chat.Quote, body string) string {
	if q == nil {
		return body
	}
	head := "「引用"
	if q.Seq > 0 {
		head += fmt.Sprintf(" #%d", q.Seq)
	}
	if q.At {
		head += " @" + q.From
	} else {
		head += " " + q.From
	}
	head += "：" + q.Snip + "」"
	if q.Via != "" {
		head += fmt.Sprintf("（转发自「%s」）", q.Via)
	}
	return head + "\n" + body
}

// onSay is the hub observer: pure-rule routing.
func (d *Dispatcher) onSay(msg chat.Message) {
	// A bridged turn reply (origin=bridge) is a deliverable, not a new
	// instruction — the v1.0 ruling keeps its silence in the summons
	// family (the hub's summons gate never expands @所有人/@岗位组 on a
	// bridged line) and keeps it out of the background context lane.
	// One clause rides through (manual §bridge 回显/§引用回复: 「被 @ 到
	// 其中的内容照常响应」「单名点名不受回显豁免」): the per-name @ a
	// member's reply literally carries wakes that peer — member-to-
	// member delegation, the 「收到」receipt discipline capping the
	// loop (a receipt is never a new line, so an FYI answer cannot
	// echo into another wake). The speaker is never its own target: a
	// member quoting its own line must not re-inject its own words.
	bridge := msg.Origin == chat.OriginBridge
	d.mu.Lock()
	var targets []*member
	for _, name := range msg.Mentions {
		if name == msg.From {
			continue
		}
		if m, ok := d.members[name]; ok {
			targets = append(targets, m)
		}
	}
	unaddressed := !bridge && len(targets) == 0 && msg.Type == chat.MsgSay
	all := make([]*member, 0, len(d.members))
	for _, m := range d.members {
		all = append(all, m)
	}
	d.mu.Unlock()

	line := fmt.Sprintf("【办公室消息｜来自 %s】%s", msg.From, quoteWire(msg.Quote, msg.Text))
	if n := len(msg.Images); n > 0 {
		// 输入图片：正文只点名「附带」（中性措辞——单轮图片有预算上限，
		// 超出的会在投递面注明未随附；逐线文案不预支「已看到」的承诺，
		// 退化路径〔旧桥/文件丢失/预算截断〕也不撒谎）。
		names := make([]string, 0, n)
		for _, im := range msg.Images {
			if im.Name != "" {
				names = append(names, im.Name)
			}
		}
		if len(names) > 0 {
			line += fmt.Sprintf("\n（本条附带 %d 张图片：%s）", n, strings.Join(names, "、"))
		} else {
			line += fmt.Sprintf("\n（本条附带 %d 张图片）", n)
		}
	}
	// 斜杠附着（/技能）：人类消息里的 /key token 命中技能库时，注入正文
	// 以【技能｜key】节前置——聊天流原文不动（token 留在原话里，可读可
	// 检索）；成员回显（bridge）不参与——技能附着是人类的话筒，不是成员
	// 互相点菜的通道。
	if !bridge {
		line = d.attachSlashSkills(msg.Text, line)
	}
	// 收到纪律的 footer 不再随线逐条拼（v2.11 L5）：合并/并车会让一轮
	// 携多条点名线，逐条 footer 既重复又教不出「一轮一收到」——挪到
	// deliverQuoted 的投递面，按本轮是否携带房线货、折了几节出一遍。
	for _, m := range targets {
		// the mention-wake injection IS the read: the lane entry reports
		// the seq on its Send ack (or the parked line's eventual send).
		// The line parks first and a lane kick delivers it off the pump
		// goroutine — one hung Send must freeze only this member's lane,
		// never the room's routing (see laneKick). from rides the entry
		// for L2's same-sender fold and L4's congestion credit.
		d.enqueueFrom(m, msg.From, line, []int64{msg.Seq}, []int64{msg.Seq}, msg.TS, msg.Images)
		d.laneKick(m)
	}
	if unaddressed {
		// nobody addressed: context for everyone, wake nobody. The seq
		// rides the lane — the read lands when the flush finally ships it.
		// 图片只留占位提及（背景车道不开回合，图不注入——想让某位
		// 成员看图就 @ TA，点名线才带附件）。
		bgText := msg.Text
		if n := len(msg.Images); n > 0 {
			bgText += fmt.Sprintf("（附图 %d 张）", n)
		}
		d.mu.Lock()
		for _, m := range all {
			m.lanes.inject = append(m.lanes.inject, injectEntry{text: fmt.Sprintf("%s：%s", msg.From, bgText), read: msg.Seq})
			if len(m.lanes.inject) > InjectCap {
				m.lanes.injDropped += len(m.lanes.inject) - InjectCap
				m.lanes.inject = m.lanes.inject[len(m.lanes.inject)-InjectCap:]
			}
			d.saveLanes(m)
		}
		d.mu.Unlock()
	}
}

// Announce pushes one room announcement (飞书式群公告) to every member:
// wake=true delivers an addressed line now — each member's turn reply
// lands back in the room, so the owner sees the staff react in the
// conversation; wake=false parks a summary in the background lane (the
// silent-publish discipline: context for the next addressed message,
// no wake, no reply owed).
func (d *Dispatcher) Announce(from, content string, wake bool) {
	d.mu.Lock()
	names := make([]string, 0, len(d.members))
	for name := range d.members {
		names = append(names, name)
	}
	d.mu.Unlock()
	for _, name := range names {
		if wake {
			// 公告走车道（onSay 同款纪律）：一个冷会话成员的挂起注入
			// 不能挡住其余成员的公告送达。
			line := fmt.Sprintf(
				"【公告｜来自 %s】\n%s\n\n——以上是本房间公告全文（niuma kb notice 可随时回读）。请阅读：与你的岗位相关就回应并行动，无关则一句话确认已知悉即可。",
				from, content)
			if m := d.lookup(name); m != nil {
				d.enqueueAt(m, line, nil, nil, time.Now().Unix(), nil)
				d.laneKick(m)
			}
		} else {
			d.background(name, fmt.Sprintf("【公告更新｜%s 发布】%s", from, content))
		}
	}
}

// background parks one line in the member's context-only lane (the
// unaddressed-say discipline, reused by silent notices): it prefixes
// the member's next addressed message, never waking a turn on its own.
func (d *Dispatcher) background(name, line string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.members[name]
	if !ok {
		return
	}
	m.lanes.inject = append(m.lanes.inject, injectEntry{text: line}) // read 0: not a room message, no receipt to earn
	if len(m.lanes.inject) > InjectCap {
		m.lanes.injDropped += len(m.lanes.inject) - InjectCap
		m.lanes.inject = m.lanes.inject[len(m.lanes.inject)-InjectCap:]
	}
	d.saveLanes(m)
}

// historyKey is the read-back pointer's room key (the lobby's ""
// spelling maps to its route name — /p/default/history).
func (d *Dispatcher) historyKey() string {
	if d.projectKey != "" {
		return d.projectKey
	}
	return chat.LobbyKey
}

// PlanLanded is the plan-accept wake — v2 P4-b's missing publish leg:
// the task/plan broadcast frames carry no @ and the wake family only
// rides delivered say lines, so after the host accepts a proposal the
// dispatcher delivers the news itself. Every dispatcher-managed
// assignee gets one addressed line listing the tasks now theirs (a
// nomination awaiting their confirm says how); the orchestrator gets
// the acceptance with the full landing map (pool tasks ride their
// line — there is nobody else to tell). Names that hold no managed
// member (the host, a human, a typo) simply find no member — deliver
// is a no-op for them, and the orchestrator's map still shows where
// everything went. Replies land back in the room through the members'
// own seats — the follow-up the host accepted the plan for.
func (d *Dispatcher) PlanLanded(planID, planTitle, submitter string, landed []*tasks.Task) {
	// group by assignee, landing order preserved; "" (pool) rides the
	// orchestrator's line
	var order []string
	mine := map[string][]*tasks.Task{}
	for _, t := range landed {
		if t == nil {
			continue
		}
		who := t.Assignee
		if who == "" {
			who = submitter
		}
		if _, ok := mine[who]; !ok {
			order = append(order, who)
		}
		mine[who] = append(mine[who], t)
	}
	// 版本管理（v2.7）的开工行：git 项目把「当前分支·版本·提交规范」
	// 缀在发布尾（gitLineForLanded 的静默纪律——非 git 项目一行都不
	// 多话）；v2.7.1 起按受派人各算各的（隔离成员读其专属树的分支）。
	for _, who := range order {
		m := d.lookup(who)
		if m == nil {
			continue
		}
		gitLine := d.gitLineForLanded(who, landed)
		if who == submitter {
			d.deliver(m.name, withGitLine(orchestratorLandedLine(planID, planTitle, submitter, landed), gitLine), nil, nil)
			continue
		}
		d.deliver(m.name, withGitLine(assigneeLandedLine(planID, planTitle, who, mine[who]), gitLine), nil, nil)
	}
}

// withGitLine appends the 版本管理 tail to a 任务发布 injection（空行
// 串直接原样返回）。
func withGitLine(line, gitLine string) string {
	if gitLine == "" {
		return line
	}
	return line + "\n" + gitLine
}

// assigneeLandedLine renders one assignee's 【任务发布】 injection: the
// tasks now theirs, each tagged with its next move — a Pending
// nomination still needs their confirm (the CLI verb in the exact
// shape the seat-token path expects), a direct assignment carries the
// ledger verbs (动手置 doing、完工关单): the assignee flows their own
// task states. Chat prose like the host's 「开工」 never reaches the
// ledger, so the card itself must teach both moves — the nomination
// branch teaches confirm, the direct branch teaches start/close.
func assigneeLandedLine(planID, planTitle, who string, ts []*tasks.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【任务发布｜提案 %s「%s」已被房主接受】\n以下任务指派给你：\n", planID, planTitle)
	for _, t := range ts {
		if t.Pending != nil {
			fmt.Fprintf(&b, "· %s「%s」——待你确认接收：task confirm %s --name %s（拒收用 task decline；有异议直接说明）\n",
				t.ID, t.Title, t.ID, who)
		} else {
			fmt.Fprintf(&b, "· %s「%s」——已生效，按排期开工：动手时 task update %s --name %s --status doing 置进行中，完工当时 task update %s --name %s --status done --progress 100%% 关单（task show %s 看详情/起止/依赖）\n",
				t.ID, t.Title, t.ID, who, t.ID, who, t.ID)
		}
	}
	// 开工第一读：简报指针（发布卡只带单号清单——上下文按需拉取，
	// 不塞进注入，与薄注入纪律同源）。
	if len(ts) > 0 {
		fmt.Fprintf(&b, "开工前领一页全量上下文（需求原文/父单/依赖/评审纪要/分支）：task brief %s（每单各自可取）。\n", ts[0].ID)
	}
	b.WriteString("你的回复会贴回办公室；若只需知晓，回「收到」即可。")
	return b.String()
}

// orchestratorLandedLine renders the orchestrator's 【提案入库】
// injection: the acceptance plus every task's destination — their own
// follow-up drill (催办 @负责人) has the whole map in one place, pool
// tasks included.
func orchestratorLandedLine(planID, planTitle, submitter string, landed []*tasks.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【提案入库｜%s「%s」已被房主接受】\n%d 条任务已入库：\n", planID, planTitle, len(landed))
	for _, t := range landed {
		if t == nil {
			continue
		}
		dest := t.Assignee
		switch {
		case dest == "":
			dest = "任务池（无人认领）"
		case dest == submitter:
			dest = "你"
		case t.Pending != nil:
			dest += "（待其确认接收）"
		}
		fmt.Fprintf(&b, "· %s「%s」→ %s\n", t.ID, t.Title, dest)
	}
	b.WriteString("请按排期跟进：催办直接 @负责人；任务池的单子提醒房主处理或重新提案改派。")
	return b.String()
}

// EstablishmentAdded is the row face's hiring-desk wake (the server's
// EstablishmentAdded hook, fleet-routed here on the LOBBY dispatcher —
// HR 人事 is a studio-level post seated in the lobby, the keeper's
// lobby-alarm twin; the line itself names the scope the row landed
// in): the host's panel add is host-only knowledge, and HR only ever
// reacts to what reaches its session. One addressed 【加编通知】 goes
// to the seated HR member; their manual owns the procedure (立手册 →
// dispatch birth), the notice just hands over the row. false = no
// seated HR (not birthed yet, offboarded) — the server turns that
// into the room's fallback note.
func (d *Dispatcher) EstablishmentAdded(scope string, row kb.EstablishmentRow, prev int, by string) bool {
	d.mu.Lock()
	var hr *member
	for _, m := range d.members {
		if m.role == agents.HRRole {
			hr = m
			break
		}
	}
	d.mu.Unlock()
	if hr == nil {
		return false
	}
	d.deliver(hr.name, hrEstablishmentLine(scope, row, prev, by), nil, nil)
	return true
}

// hrEstablishmentLine renders HR's 【加编通知】 injection: the row the
// host just wrote IS the confirmed post row the manual's step 1 waits
// for, and the notice hands over a READY-TO-RUN birth command keyed by
// the post（--post 岗位锚：身份按表反查、行落锚，HR 不再手抄身份串
// ——「一字不差」靠抄写是从前的事故源）。The closing line keeps the
// 收到 discipline — an FYI notice must not force a reply.
func hrEstablishmentLine(scope string, row kb.EstablishmentRow, prev int, by string) string {
	where := "Niuma_Studio 编制表"
	if scope != chat.LobbyKey {
		where = "项目 " + scope + " 的编制表"
	}
	auto := "否"
	if row.AutoFill {
		auto = "是"
	}
	birthCmd := "<exe> dispatch birth --name <名> --post " + row.Key
	if scope != chat.LobbyKey {
		birthCmd += " --project " + scope // 落房明示，不靠 cwd 缺省
	}
	var b strings.Builder
	if prev <= 0 {
		fmt.Fprintf(&b, "【加编通知｜%s 在%s加了岗位行】\n· 岗位：%s（key %s，身份「%s」）\n· 编制 %d，自动补员 %s，手册 %s\n",
			by, where, row.Name, row.Key, row.Role, row.Headcount, auto, manualOrNone(row.Manual))
		fmt.Fprintf(&b, "房主亲手在面板加的行就是已确认的岗位行——请按你的手册推进：先立/读岗位手册，再到岗。\n到岗命令（身份按表自动锚定，勿手抄 --role）：%s\n", birthCmd)
	} else {
		fmt.Fprintf(&b, "【加编通知｜%s 扩了%s的编】\n· 岗位：%s（key %s，身份「%s」）编制 %d → %d（需补 %d 人），自动补员 %s\n",
			by, where, row.Name, row.Key, row.Role, prev, row.Headcount, row.Headcount-prev, auto)
		fmt.Fprintf(&b, "请按你的手册跟进补员（到岗命令同上形状：%s；或与房主确认现有人手已够）。\n", birthCmd)
	}
	b.WriteString("\n若这则通知只需知晓、无需你行动：回「收到」即可。")
	return b.String()
}

// manualOrNone renders the row's 手册 cell for HR's notice: empty is
// 「未立」 — the manual's step 2 (先起草岗位手册) then visibly applies.
func manualOrNone(manual string) string {
	if strings.TrimSpace(manual) == "" {
		return "未立"
	}
	return manual
}

// noticePrefix is the birth injection's current-announcement slot: a
// joiner meets the room's notice ahead of the onboarding prompt (the
// Feishu new-member rule). A nil/empty NoticeOf yields "" — the birth
// stays byte-for-byte the pre-notice shape.
func (d *Dispatcher) noticePrefix() string {
	if d.cfg.NoticeOf == nil {
		return ""
	}
	content, by, ok := d.cfg.NoticeOf(d.projectKey)
	if !ok || strings.TrimSpace(content) == "" {
		return ""
	}
	who := ""
	if by != "" {
		who = "（" + by + " 发布）"
	}
	return fmt.Sprintf("【当前房间公告%s】\n%s\n——以上是本房当前公告全文（niuma kb notice 可随时回读）。\n\n", who, content)
}

// reactivate walks the -32004 → resume → subscribe ladder.
func (d *Dispatcher) reactivate(m *member) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := d.bridge.ResumeSession(ctx, d.sessionOf(m)); err != nil {
		return err
	}
	if _, err := d.bridge.Subscribe(ctx, d.sessionOf(m)); err != nil {
		return err
	}
	return nil
}

// onState tracks the earliest busy signal (patch.status "running"
// arrives before any session event — Z-Deck measurement) AND the
// failure terminal: a turn that dies provider-side (M1 spike F:
// prompt_failed) never emits a stopReason envelope, so this patch is
// the only terminal signal for it.
func (d *Dispatcher) onState(sessionID, status, reason string) {
	if status == "running" {
		d.mu.Lock()
		var turned []*member
		for _, m := range d.members {
			if m.sessionID == sessionID && m.status != StatusRunning {
				turned = append(turned, m)
			}
		}
		d.mu.Unlock()
		for _, m := range turned {
			// 外部点火：这次翻转不经 deliverQuoted 的武装（成员闲着，
			// 会话却跑了起来——房主在桌面端直接输入、steer、或重挂订阅
			// 到的旧回合继续）。同一纪律照旧：亮灯前先落行，抽屉里看的
			// 见「这轮不是调度器注入的、为什么在跑」（v2.10 同步收口）。
			d.turnOpen(m, i18n.S("回合开始（会话自发或外部输入——本回合非调度器注入，过程照录）"))
			d.setStatus(m, StatusRunning)
		}
		return
	}
	if !strings.Contains(reason, "fail") && !strings.Contains(reason, "error") {
		return
	}
	d.mu.Lock()
	var m *member
	for _, cand := range d.members {
		if cand.sessionID == sessionID {
			m = cand
			break
		}
	}
	d.mu.Unlock()
	if m != nil {
		// 折叠回合半途死掉（compact.go）：标记清掉，失败面照常走。
		d.mu.Lock()
		m.watch.compacting = false
		d.mu.Unlock()
	}
	if m != nil && d.setStatus(m, StatusIdle) { // flip=true ⇔ wasRunning
		// 双门（v2.9）：这类回合 provider 端从不发 stopReason 信封——
		// 不落这一行，抽屉里这回合就凭空蒸发（input 进去了、没有任何
		// 出口痕迹）。聊天流的 sayThrough 是对话面，这里是档案面。
		d.emitTrace([]chat.TraceEntry{{From: m.name, Kind: chat.TraceSys,
			Text: clipMiddle(i18n.Sf("本轮失败中止：%s", zcode.TurnFailHint(reason)), 4096)}})
		d.takeAckCargo(m)                 // the failed turn owes no 收到（先取，让晋升的 pending 货留给下一回合终局）
		reads, _, _ := d.takeReadCargo(m) // the failed turn still consumed its input — 已读 at its terminal
		d.hub.MarkRead(m.name, reads...)
		d.sayThrough(m, i18n.Sf("（本轮失败中止：%s）", zcode.TurnFailHint(reason)), nil)
		d.explainFail(m) // 底层真因补遗（failexplain.go）：异步翻 CLI 日志，不占读循环
		d.stampRowUnread(m.name, d.sessionOf(m))
		// 同 onEvent 终局：onState 也跑在读循环 goroutine 上（Hooks
		// 契约），延迟模型切换与排队线续投都不得在本栈同步等 ack。
		d.drainTerminal(m)
	}
}

// onEvent mirrors turn replies and drains the queue: each finished
// turn publishes the member's final text through their seat
// (origin=bridge), then the next queued line — if any — starts a new
// turn (one message per turn, the dsh queue semantics). A turn's reply
// mirrors EXACTLY ONCE, off the typed turn.completed terminal: the
// projection also emits a no-kind stopReason=="stop" frame at every
// model-request boundary inside the turn, and the model often restates
// its pending closing words verbatim at each one — mirroring those
// frames put the same reply in the room two-three times (房主实录
// 2026-10-03 book 房：小苗一回合三条回复，两条逐字相同，时刻精确落在
// 中间请求的边界上). The 工作过程 tap rides the same stream head:
// every event (not just terminals) first passes traceEvent, which
// classifies/attributes/coalesces the displayable slice
// (dispatch/trace.go).
func (d *Dispatcher) onEvent(ev zcode.Event) {
	if d.bridge != nil {
		d.traceEvent(ev)
	}
	// 新回合开工：上一回合的边界口信作废——残留的暂存文本不得漏进
	// 这一回合的终点。
	if ev.Type == "turn.started" {
		if m := d.lookupBySession(ev.SessionID); m != nil {
			m.watch.pendingReply = ""
		}
		return
	}
	// 真终点（类型化 turn.completed，每回合恰一次）：镜像 response 全
	// 文；response 缺形时用最后一条边界帧的口信兜底。resultType 非
	// success 的终点由失败信封或 state 面发话，这里只做簿记——一句终
	// 点不说两遍。
	if content, resultType, ok := ev.TurnCompleted(); ok {
		m := d.lookupBySession(ev.SessionID)
		if m == nil {
			return
		}
		if content == "" {
			content = m.watch.pendingReply
		}
		m.watch.pendingReply = ""
		d.turnTerminal(m, content, "", resultType == "" || resultType == "success")
		return
	}
	// 请求边界帧（stopReason=="stop"）：只暂存不镜像。折叠回合若只走
	// 这一型收尾，折叠标记在此就地消费（摘要照旧不进房）。
	if content, ok := ev.TurnStopped(); ok {
		if m := d.lookupBySession(ev.SessionID); m != nil {
			m.watch.pendingReply = content
			if d.foldTerminal(m) {
				m.watch.pendingReply = ""
				d.setStatus(m, StatusIdle)
				d.drainTerminal(m)
			}
		}
		return
	}
	// 失败终点（error 等）：照旧当场落房。
	content, stopReason, ok := ev.TurnDone()
	if !ok {
		return
	}
	m := d.lookupBySession(ev.SessionID)
	if m == nil {
		return
	}
	d.turnTerminal(m, content, stopReason, true)
}

// turnTerminal lands one turn terminal exactly once: the speak face
// (mirrored say / receipts / failure line; speak=false books only — a
// turn.completed with a non-success resultType has already been spoken
// for by the failure envelope or the state face), the read/ack cargo
// drains, the Idle flip and the lane continuation.
func (d *Dispatcher) turnTerminal(m *member, content, stopReason string, speak bool) {
	// 折叠回合的终点（compact.go）：摘要不进房、货单本来就空——按标
	// 记吞掉，翻回 Idle 并照常续投车道。
	if d.foldTerminal(m) {
		d.setStatus(m, StatusIdle)
		d.drainTerminal(m)
		return
	}
	cargo := d.takeAckCargo(m)                   // this turn's 收到 cargo, drained at its terminal
	reads, turnAck, turnAt := d.takeReadCargo(m) // …and its 已读 cargo, with the quote arm

	spoke := false // any branch the room can see dots the sidebar row
	if speak {
		if content != "" && isAckReply(content) && len(cargo) > 0 {
			// 飞书式收到：an FYI mention answered with a bare 「收到」 becomes
			// receipts under the addressed lines instead of a mirrored say —
			// nothing new enters the stream, so nothing can @ anyone back.
			// Turns without room-message cargo (birth, patrol, daily,
			// announcements) keep the visible reply: there is no line to
			// hang a receipt on, and swallowing it would lose the words.
			d.hub.MarkAck(m.name, cargo...)
			spoke = true
		} else if content != "" {
			// AI 重编译投票的协议线（提议/同意/反对重编译前缀）先于镜像拦
			// 截：票已记账成系统行，原话不进聊天流；非协议线照常走镜像。
			if d.voteLine(m, content) {
				spoke = true
			} else {
				// 自制技能协议线（```niuma-skill 围栏块）：入库的块从镜像剥
				// 除、系统行回执；开关关则原文照旧＋忽略行。剥完为空的回复
				// 不再镜像（回执就是这一轮的话）。
				content, skillActed := d.skillProposal(m, content)
				spoke = spoke || skillActed
				if content != "" {
					reply, quote := d.routeReply(m, content, turnAck, turnAt)
					// r_20 t_195 确认行协议：「已读要点，从…继续」开头的回复
					// 是接续的举手礼——私发调度器不进房不广播（根治全员围观
					// 回执），同会话第二次直接吞（幂等）；房间只落一条极简系
					// 统行。确认行也是回合终点：不提前 return——货单冲账、
					// 翻回 Idle、续投车道一拍不落，否则座位钉死在 Running
					// （车道 kick 的守卫永远等不到空闲），排队线滞留、已读
					// 丢账。
					if strings.HasPrefix(reply, "已读要点") {
						if !m.watch.resumeAcked {
							m.watch.resumeAcked = true
							d.hub.System(i18n.Sf("%s 已接续（要点已读）", m.name))
						}
					} else {
						d.sayThrough(m, reply, quote)      // a late answer names its line (Quote fields); an unanswered-to @ delivers it to the peer
						d.noteMeetingTurn(m.name, content) // a live review meeting keeps its minutes
					}
				}
				spoke = true
			}
		} else if stopReason != "" && stopReason != "stop" {
			d.sayThrough(m, i18n.Sf("（本轮异常中止：%s，排队消息继续）", zcode.TurnFailHint(stopReason)), nil)
			spoke = true
		}
	}
	d.hub.MarkRead(m.name, reads...) // 已读 at the terminal: the turn consuming these lines ended
	if spoke {
		d.stampRowUnread(m.name, d.sessionOf(m))
	}

	d.setStatus(m, StatusIdle)
	// 终局尾巴（延迟模型切换＋排队线续投）绝不落在本函数的调用者栈
	// 上：onEvent 由 app-server 读循环同步驱动（zcode.Client 的 Hooks
	// 契约），Send/SetModel 的 ack 都要靠这条被占住的读循环才能读到
	// ——在本栈同步等就是自锁烧完整个等待窗（房主实录：反向请求 13
	// 连宣告、积压事件一秒冲刷出「一秒 17 轮模型」、末了注入超时弃
	// 单）。laneKick 纪律（hub 泵冻死事故修出，见其注释）同样约束终
	// 局路径——见 drainTerminal。
	d.drainTerminal(m)
}

// routeReply shapes a mirrored turn reply for the room: the stale
// auto-quote below, plus the peer-delivery @ the room's wake rules
// demand. A reply that answers exactly one colleague — a dispatcher-
// driven member, NOT the owner — yet carries no @ of its own would
// land in that colleague's background lane: the asker only hears it
// next time someone else @s them, and the conversation dies
// mid-question (房主实录：员工提问/回答不带 @，对面收不到). The
// dispatcher delivers it instead: fresh replies get「@同事 」
// prefixed; a stale single-line reply carries the @ inside the quote
// header's from-token (引用即点名, manual §引用回复 — the wake
// parser resolves the from-@ while the snip stays masked). The
// owner's answers stay @-free — v2.9's ruling unchanged: the owner
// watches the room, an @ would only buy a badge and a ding.
func (d *Dispatcher) routeReply(m *member, content string, turnAck []int64, turnAt int64) (string, *chat.Quote) {
	peer, deliver := d.answeredPeer(m.name, content, turnAck)
	_, quote := d.quoteStaleReply(content, turnAck, turnAt, deliver)
	if quote != nil || !deliver {
		return content, quote // the stale single-line quote already carries the delivery (its At wakes the peer)
	}
	return "@" + peer + " " + content, nil
}

// answeredPeer resolves whom this turn's reply answers and whether
// the dispatcher must deliver the @ itself: every addressed line
// shares one author, that author is a live dispatcher-driven member
// (the owner and outsiders read the room themselves — an @ there is
// noise), and the reply carries no '@' of its own (any @ — a
// hand-written mention, a quote prefix's from-token — means the
// member already addressed whom they meant; never double-address).
func (d *Dispatcher) answeredPeer(speaker, content string, turnAck []int64) (string, bool) {
	if len(turnAck) == 0 || strings.ContainsRune(content, '@') {
		return "", false
	}
	peer := ""
	for _, seq := range turnAck {
		from, _, ok := d.hub.SayLine(seq)
		if !ok || from == speaker {
			return "", false
		}
		if peer != "" && from != peer {
			return "", false // several askers: whom to address is the member's call, not ours
		}
		peer = from
	}
	d.mu.Lock()
	_, driven := d.members[peer]
	d.mu.Unlock()
	return peer, driven
}

// quoteStaleReply pairs a mirrored reply with the structured quote of
// the one addressed line it answered (chat.Quote — fields on the
// message, never a text prefix: the stored body stays clean, the room
// renderer draws the gray quote block from the fields and the #N jump
// works) when the reply's turn answered exactly one addressed line that
// was already stale when delivered: a reply to a 20-minute-old 「稍等」
// otherwise lands as an answer to nothing the room can see. At (the
// reply-mention, 引用即点名) is set only in the routeReply delivery
// case (the answered peer is a dispatcher-driven colleague — the field
// wakes them): for the owner it stays bare, mechanical context, not an
// address — a live @ would resolve through mentionsLocked and re-ping
// the 房主 badges 有人@我 on every late answer. Fresh turns quote
// nothing — the latest @ is obviously the prompt; multi-address turns
// quote nothing either (which line would be THE line?).
func (d *Dispatcher) quoteStaleReply(content string, turnAck []int64, turnAt int64, atFrom bool) (string, *chat.Quote) {
	if len(turnAck) != 1 || turnAt <= 0 {
		return content, nil
	}
	if wait := time.Now().Unix() - turnAt; wait <= d.staleAfter() {
		return content, nil
	}
	from, text, ok := d.hub.SayLine(turnAck[0])
	if !ok {
		return content, nil
	}
	return content, &chat.Quote{Seq: turnAck[0], From: from, At: atFrom, Snip: snipLine(text)}
}

// snipLine flattens a say line to its one-line quote form: first line,
// newlines gone, rune-capped — the composer's snipOf semantics in the
// dispatcher's size (a quote prefix must never dwarf the reply).
func snipLine(s string) string {
	s = strings.TrimSpace(util.FirstLine(s))
	runes := []rune(s)
	if len(runes) > 32 {
		runes = append(runes[:32], []rune("…")...)
	}
	return string(runes)
}

func (d *Dispatcher) lookupBySession(sid string) *member {
	if sid == "" {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, m := range d.members {
		if m.sessionID == sid {
			return m
		}
	}
	return nil
}

// ownsSession reports whether one of this dispatcher's members holds
// the session — the shared bridge's reverse-request router.
func (d *Dispatcher) ownsSession(sid string) bool {
	return d.lookupBySession(sid) != nil
}

// setStatus is the single writer of a member's run state (v2.10 收口):
// 每一条开/结束回合的路径都必须走这里——别处不许直写 m.status——
// 成员态、看护失速钟与 hub 上的 working 镜像由同一扇门翻转，结构性
// 不可能各说各话。返回是否真的发生了翻转：翻上 Running 的调用方欠
// 抽屉一行回合开场（turnOpen，先落行再亮灯——亮着的 chip 背后永不
// 是空抽屉），翻下 Idle 的调用方据此决定是否播报失败终态。
func (d *Dispatcher) setStatus(m *member, status string) bool {
	d.mu.Lock()
	flip := m.status != status
	m.status = status
	// Turn bookkeeping for the watchdog: every Running stretch starts a
	// fresh stall clock and re-arms the one-shot note; idle zeroes both.
	if status == StatusRunning {
		m.watch.runningSince = time.Now()
		m.watch.stallNoted = false
	} else {
		m.watch.runningSince = time.Time{}
		m.watch.stallNoted = false
	}
	d.mu.Unlock()
	if flip {
		d.hub.SetWorkState(m.name, status == StatusRunning, util.Now())
	}
	return flip
}

// reassertWork re-stamps the working tell on a freshly (re)taken seat:
// hub 座位记录出生时 Working=false，且 SetWorkState 在名字无座期间是
// no-op——回合进行中成员自家 CLI 一次性顶座（seat-lease）再退场后，
// 若不按调度器真相重烫，chip 会在整段 Running 里保持熄灭，而调度
// 侧仍算 TA 在干活。重挂座位后必须重申一次（v2.10 同步收口）。
func (d *Dispatcher) reassertWork(m *member) {
	d.mu.Lock()
	running := m.status == StatusRunning
	d.mu.Unlock()
	if running {
		d.hub.SetWorkState(m.name, true, util.Now())
	}
}
