package dispatch

// meeting.go — the requirement-review meeting clock (需求评审会议):
// whenever the orchestrator sits idle with at least one idle colleague
// to pull in (the room itself may be mid-flight — 复查#3: the trigger
// is chair+hands, not a fully idle office), the ORCHESTRATOR convenes
// the review —
// they pick one open requirement and pull its relevant colleagues
// into the office's ONE meeting room (the product manager among the
// invitees as the requirement's voice, NOT the chair; v2.4 re-org:
// meetings are the front yard of the orchestrator's 拆解 duty, the
// pre-v2.4 PM-chaired shape is retired). The patrol/daily pattern
// again: one tick goroutine per project dispatcher, silent triggers
// (session/send injections; the room hears the members' own mirrored
// turns, not the clock). The discussion is real in the only sense
// this house knows — each participant's session gets the agenda,
// their turn replies mirror into the office chat and land in the
// meeting's transcript; the chair's opening turn (requirement recap
// + draft breakdown) becomes the record's Opening, their summary turn
// becomes the 方案 — which the chair lands THEMSELVES through plan
// submit (the pre-v2.4 "@排期编排组" relay is gone: the person who
// needs the conclusion is already holding the meeting). The CHAIR
// holds a grip on the agenda clock (MeetingCtl: meeting end fires the
// current window early, meeting extend pushes its deadline out — AI
// turns are slow, the chair sees when the table is done before the
// window is). Occupancy is the store's single live slot per project:
// one room, one meeting at a time. The proposal slot is the second
// occupancy the clock respects: a plan awaiting the host's verdict
// holds new convenes (the valve gate — 消费在飞，不添新货，the same
// discipline the autopilot engine already keeps for 蓄水/拆解).

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// The meeting agenda's fixed beat. The discuss window spans the
// chair's opening and the participants' opinions; the summary window
// gives the chair room to land the 方案; then the room frees. Every
// turn is a full agent session and AI thinking is not that fast, so
// the windows are sized generously (25m together) — a window that
// outlasts the last turn only idles the room, one that's too short
// truncates the meeting before everyone has spoken. The freed room
// is re-triggerable on the first tick after it frees.
const (
	DefaultMeetingEvery = 10 * time.Minute
	MeetingDiscuss      = 15 * time.Minute
	MeetingClose        = 10 * time.Minute
	// MeetingMaxExtend caps one meeting_extend call (minutes). Calls
	// stack — the chair can hold the room as long as the table wants
	// — the cap only keeps a typo (600) from squatting the room for a
	// day.
	MeetingMaxExtend = 60
	// agendaCtlWait bounds MeetingCtl's wait for the agenda goroutine
	// to take a control: the goroutine is parked while a window is
	// open and answers at once, so a timeout means it is mid-switch
	// (between windows) or gone — a retryable refusal either way.
	agendaCtlWait = 3 * time.Second
	// FirstMeetingDelay is the boot head start before the first
	// occupancy check — the common "I just staffed an idle office and
	// filed a requirement" case should not wait a full interval.
	FirstMeetingDelay = 90 * time.Second
	// MeetingMaxParticipants caps the invitees beyond the chair (the
	// meeting room seats six; four around the table reads best).
	MeetingMaxParticipants = 3
	// AutoInjectMeetingYield (r_17 让拍) is how long the meeting clock
	// holds off convening after an autopilot injection (选题/拆解) went
	// out to this room's orchestrator: the injection may be about to
	// direct-submit a small requirement — the clock must not race it
	// into a meeting first. Big requirements stay in the pool and get
	// their meeting on a later beat.
	AutoInjectMeetingYield = 10 * time.Minute
	// MeetingReconveneAfter bounds the fallback reconvene: a
	// reviewed-but-still-open requirement (its plan parked in the
	// single slot awaiting the host, or refused and back in the pool)
	// only gets another meeting once its last one ended this long ago.
	// The minutes' own revision window is the cadence — 「24h 内 kb
	// write 修订提纯（过期走需求池重新评审）」— so the re-review rides
	// that same expiry. Without the cooldown the fallback re-chaired
	// the oldest open requirement every beat: a requirement that
	// cannot leave open hears 6+ identical reviews an hour, all noise,
	// real tokens (the r_29 死循环 accident).
	MeetingReconveneAfter = 24 * time.Hour
)

// MeetingEveryFromEnv resolves NIUMA_MEETING_EVERY: a duration ("45m",
// "90s") or a bare positive integer (minutes); "off"/"-" disables;
// malformed keeps the default — a bad env var never bricks the clock.
func MeetingEveryFromEnv() time.Duration {
	v := strings.TrimSpace(util.Env("MEETING_EVERY"))
	switch v {
	case "":
		return DefaultMeetingEvery
	case "off", "-":
		return -1
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return DefaultMeetingEvery
}

// isPMRole matches the product-manager seat — under the v2.4 order the
// PM is the requirement's owner and thus the first invitee, any role
// taxonomy that names 产品 (产品经理, 产品, 产品经理·平台组 …).
func isPMRole(role string) bool {
	return strings.Contains(role, "产品")
}

// startMeetingClock launches the tick goroutine (bridge-off
// dispatchers never get here — Start returns before adopt).
// MeetingEvery < 0 or no meeting store disables.
func (d *Dispatcher) startMeetingClock() {
	if d.cfg.Meetings == nil {
		return
	}
	every := d.cfg.MeetingEvery
	if every == 0 {
		every = DefaultMeetingEvery
	}
	if every < 0 {
		return
	}
	d.wg.Add(1)
	go d.meetingLoop(every)
}

// meetingLoop checks the room on the beat. cfg.MeetingTick (the
// fake-clock injection point) replaces the wall-clock timer wholesale
// when set.
func (d *Dispatcher) meetingLoop(every time.Duration) {
	defer d.wg.Done()
	var timer *time.Timer
	if d.cfg.MeetingTick == nil {
		timer = time.NewTimer(FirstMeetingDelay)
		defer timer.Stop()
	}
	for {
		var tick <-chan time.Time
		if timer != nil {
			tick = timer.C
		} else {
			tick = d.cfg.MeetingTick
		}
		select {
		case <-d.stop:
			return
		case <-tick:
			if timer != nil {
				timer.Reset(every)
			}
			util.Guard("dispatch: meeting clock", d.meetingTick)
		}
	}
}

// meetingTick is one occupancy check: a live meeting owns the room
// (its own goroutine drives the agenda), otherwise the trigger fires.
func (d *Dispatcher) meetingTick() {
	if d.cfg.Meetings == nil {
		return
	}
	if d.pausedNow() {
		return // 房间暂停：不查不召——进行中的会由议程窗自己冻结
	}
	if _, busy := d.cfg.Meetings.Active(d.projectKey); busy {
		return
	}
	d.tryConvene()
}

// meetingCand is one seated member as the convene logic sees them.
type meetingCand struct {
	name string
	role string
}

// tryConvene is the whole trigger: the project's orchestrator seated
// and hands-free, an open requirement nobody has reviewed yet, at
// least one idle colleague to pull in — and the proposal slot empty
// (the valve gate: a review's product is a proposal, and the single
// slot means a pending one must reach the host's verdict before the
// room produces another). Everything must line up or the tick passes
// silently — the clock keeps beating either way.
func (d *Dispatcher) tryConvene() {
	if d.cfg.Meetings == nil || d.cfg.Reqs == nil || d.cfg.Tasks == nil {
		return
	}
	// r_17 让拍：自动驾驶注入（选题/拆解）刚送到编排者手上时，编排者
	// 可能正要按注入直提小需求——会议钟让一拍，不抢跑开会；大需求
	// 留在池里，下一拍照常召集。
	d.mu.Lock()
	yieldUntil := d.apYieldUntil
	d.mu.Unlock()
	if time.Now().Before(yieldUntil) {
		return
	}
	// 阀门闸（待审提案背压）：本房提案槽里还压着一份等房主亲断的提案
	// 时，会议钟让位不开新评审。评审会的产出就是排期提案，而提案槽
	// 一房一件——阀门关着时再开一场，产出无处可去，只会把上一场的
	// 结论顶成「未审作废」（supersede 只该服务同需求的会内修订）。
	// 与引擎的既有纪律同源（roundProject 的「消费在飞，不添新货」：
	// 槽里有提案时蓄水/拆解注入同样让位），也让手动模式下的等待有
	// 单一焦点——全房等房主一个决定，而不是一边等一边产出新待审。
	// 房主批/驳（或自动驾驶过宽限代收）落槽后，下一拍照常召集。
	if d.cfg.Plans != nil {
		if cur := d.cfg.Plans.Pending(d.cfg.Subject, d.projectKey); cur != nil {
			return
		}
	}
	// deterministic order: the member map iterates randomly
	d.mu.Lock()
	cands := make([]meetingCand, 0, len(d.members))
	for _, m := range d.members {
		cands = append(cands, meetingCand{name: m.name, role: m.role})
	}
	d.mu.Unlock()
	sort.Slice(cands, func(i, j int) bool { return cands[i].name < cands[j].name })

	// the chair: the project's orchestrator — meetings are theirs to
	// convene (v2.4); a busy or missing orchestrator means no meeting
	var chair meetingCand
	found := false
	pool := make([]meetingCand, 0, len(cands))
	for _, c := range cands {
		if c.role == agents.OrchestratorRole {
			if !found && d.idle(c.name) {
				chair, found = c, true
			}
			continue // the orchestrator chairs; they never double as an invitee
		}
		pool = append(pool, c)
	}
	if !found {
		return
	}

	req, ok := d.pickRequirement()
	if !ok {
		return
	}

	// invitees, idle only — the requirement's owner (产品) first among
	// equals, then 岗位合适 (the requirement text naming the role's
	// stem), then the lightest todo pile, then name
	reqText := req.Title + "\n" + req.Body
	type pick struct {
		c    meetingCand
		fit  bool
		todo int
	}
	var picks []pick
	for _, c := range pool {
		if !d.idle(c.name) {
			continue
		}
		_, todo := d.load(c.name)
		fit := isPMRole(c.role) // the PM owns the requirement under review
		if !fit {
			if stem := chat.RoleStem(c.role); stem != "" && strings.Contains(reqText, stem) {
				fit = true
			}
		}
		picks = append(picks, pick{c: c, fit: fit, todo: todo})
	}
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].fit != picks[j].fit {
			return picks[i].fit
		}
		if picks[i].todo != picks[j].todo {
			return picks[i].todo < picks[j].todo
		}
		return picks[i].c.name < picks[j].c.name
	})
	if len(picks) > MeetingMaxParticipants {
		picks = picks[:MeetingMaxParticipants]
	}
	if len(picks) == 0 {
		return
	}
	names := make([]string, 0, len(picks))
	for _, p := range picks {
		names = append(names, p.c.name)
	}

	m, err := d.cfg.Meetings.Begin(d.projectKey, req.ID, req.Title, chair.name, names)
	if err != nil {
		return // raced a concurrent convene — the room is taken
	}

	started := i18n.Sf("需求评审会开始：%s《%s》｜主持 %s（编排者）｜参会 %s —— 会议室已占用，屏幕点亮",
		m.ReqID, m.ReqTitle, m.Chair, strings.Join(m.Participants, "、"))
	d.hub.Broadcast(chat.Message{Type: chat.MsgMeeting, Event: "started", Meeting: m.Wire(), Text: started, TS: util.Now()})
	d.hub.SystemRecorded(i18n.S("【会议】") + started)

	d.deliver(chair.name, meetingHostPrompt(m), nil, nil)
	for _, p := range picks {
		d.deliver(p.c.name, meetingGuestPrompt(m, p.c.role), nil, nil)
	}

	d.wg.Add(1)
	// 栅栏在 wg 之外：runAgenda 自己 defer wg.Done（panic 展开时照常收
	// 拢），一场议程 panic 降级为「这场会没开完」，不带走进程。
	go util.Guard("dispatch: meeting agenda", func() { d.runAgenda(m) })
}

// agendaReq is one chair control of the live agenda riding the window
// channel: end fires the current window now, minutes>0 extends it.
// The buffered reply channel carries the receipt (or, on the caller's
// timeout, nothing — the goroutine never blocks on a strayed waiter).
type agendaReq struct {
	end     bool
	minutes int
	reply   chan string
}

// runAgenda walks the meeting's phases on the wall clock: the discuss
// window, then the summary ask, then the close — with the chair's
// grip on every window (MeetingCtl): AI thinking is not that fast and
// the chair sees when the table is done before the window is
// (meeting end fires it early) or still hungry as it closes (meeting
// extend pushes the deadline out). A dispatcher stop abandons the
// agenda (Stop itself releases the room).
func (d *Dispatcher) runAgenda(m meeting.Meeting) {
	defer d.wg.Done()
	discuss, closing := d.cfg.MeetingDiscuss, d.cfg.MeetingClose
	if discuss <= 0 {
		discuss = MeetingDiscuss
	}
	if closing <= 0 {
		closing = MeetingClose
	}
	chairEnd, ok := d.agendaWindow(m, i18n.S("讨论窗"), discuss)
	if !ok {
		return
	}
	if _, live := d.cfg.Meetings.Active(d.projectKey); !live {
		return
	}
	if chairEnd {
		d.broadcastCtl(m, "advanced", i18n.Sf("评审会提速：主持 %s 宣布讨论结束，直接进入总结段", m.Chair))
	}
	d.cfg.Meetings.SetPhase(d.projectKey, meeting.PhaseSummary)
	d.deliver(m.Chair, meetingSummaryPrompt(m), nil, nil)

	if _, ok = d.agendaWindow(m, i18n.S("总结窗"), closing); !ok {
		return
	}
	d.closeMeeting() // the chair's end and the timer share the 散会 frame
}

// agendaWindow parks the agenda for one window (label is its
// room-facing name) and reports (chairEnd, ok): ok false = the
// dispatcher stopped; chairEnd true = the chair's meeting end closed
// the window, false = the timer simply ran out. While parked the
// window's control channel is published as the dispatcher's agenda
// grip — MeetingCtl's end/extend land here — and retracted on the way
// out, so a control that misses the bus (between windows, after the
// close) gets its loud refusal instead of writing to a dead timer.
func (d *Dispatcher) agendaWindow(m meeting.Meeting, label string, dur time.Duration) (chairEnd, ok bool) {
	timer := time.NewTimer(dur)
	defer timer.Stop()
	deadline := time.Now().Add(dur)
	ch := make(chan agendaReq, 4)
	d.mu.Lock()
	d.agenda = ch
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		if d.agenda == ch {
			d.agenda = nil
		}
		d.mu.Unlock()
	}()
	for {
		select {
		case <-timer.C:
			// 房间暂停：窗到点了但世界停着——议程停在原地等恢复
			//（不然讨论窗空转直达总结、散会无一人发言，暂停会把
			// 会「开完」）。stop 折回停机路径，恢复即当作窗自然到点。
			if !d.waitUnpaused() {
				return false, false
			}
			return false, true
		case <-d.stop:
			return false, false
		case req := <-ch:
			if req.end {
				req.reply <- i18n.Sf("已宣布%s结束", label)
				return true, true
			}
			deadline = deadline.Add(time.Duration(req.minutes) * time.Minute)
			if !timer.Stop() {
				select { // drain a fire racing this control
				case <-timer.C:
				default:
				}
			}
			timer.Reset(time.Until(deadline))
			d.broadcastCtl(m, "extended",
				i18n.Sf("评审会延时：主持 %s 把%s延长 %d 分钟（至 %s）", m.Chair, label, req.minutes, deadline.Format("15:04")))
			req.reply <- i18n.Sf("%s已延长 %d 分钟（至 %s）", label, req.minutes, deadline.Format("15:04"))
		}
	}
}

// MeetingCtl applies one chair control of the live meeting's agenda
// (the server's meeting_end/meeting_extend verbs, fleet-routed): end
// fires the current window now — the discuss window straight into the
// summary ask, the summary window straight into 散会 — else minutes
// extends the current window's deadline (clamped to MeetingMaxExtend a
// call; calls stack). The gate is the chair alone (the meeting is
// theirs to run) and the receipt comes from the agenda goroutine
// itself, so the answer says what actually happened.
func (d *Dispatcher) MeetingCtl(actor string, end bool, minutes int) (string, error) {
	if d.cfg.Meetings == nil {
		return "", errors.New(i18n.S("会议系统未启用"))
	}
	m, live := d.cfg.Meetings.Active(d.projectKey)
	if !live {
		return "", errors.New(i18n.S("会议室当前空闲，没有可控制的会议"))
	}
	if actor != m.Chair {
		return "", errors.New(i18n.Sf("会议钟只归本场主持 %s", m.Chair))
	}
	if !end {
		if minutes <= 0 {
			return "", errors.New(i18n.S("延长分钟数需为正整数"))
		}
		if minutes > MeetingMaxExtend {
			minutes = MeetingMaxExtend
		}
	}
	d.mu.Lock()
	ch := d.agenda
	d.mu.Unlock()
	if ch == nil {
		return "", errors.New(i18n.S("议程正在换挡（窗口切换中），稍候重试"))
	}
	req := agendaReq{end: end, minutes: minutes, reply: make(chan string, 1)}
	select {
	case ch <- req:
	case <-time.After(agendaCtlWait):
		return "", errors.New(i18n.S("议程未应答，稍候重试"))
	}
	select {
	case text := <-req.reply:
		return text, nil
	case <-time.After(agendaCtlWait):
		return "", errors.New(i18n.S("议程未应答，稍候重试"))
	case <-d.stop:
		return "", errors.New(i18n.S("调度器关闭"))
	}
}

// broadcastCtl publishes one chair-acted agenda change: the meeting
// frame family's reminder-only contract (never stored in the chat
// history; readers refetch GET /p/{key}/meeting), with the fresh live
// record when there is one (the phase moved) and the lobby-aggregate
// trail the started/ended lines keep.
func (d *Dispatcher) broadcastCtl(m meeting.Meeting, event, text string) {
	rec := m
	if live, ok := d.cfg.Meetings.Active(d.projectKey); ok {
		rec = live
	}
	d.hub.Broadcast(chat.Message{Type: chat.MsgMeeting, Event: event, Meeting: rec.Wire(), From: m.Chair, Text: text, TS: util.Now()})
	d.hub.SystemRecorded(i18n.S("【会议】") + text)
}

// closeMeeting frees the room and tells the office what was concluded.
func (d *Dispatcher) closeMeeting() {
	m, ok := d.cfg.Meetings.End(d.projectKey)
	if !ok {
		return
	}
	text := i18n.Sf("需求评审会结束：%s《%s》—— 会议室已释放，屏幕熄灭", m.ReqID, m.ReqTitle)
	if c := firstLine(m.Conclusion); c != "" {
		text += i18n.Sf("；方案摘要：%s", c)
	}
	d.hub.Broadcast(chat.Message{Type: chat.MsgMeeting, Event: "ended", Meeting: m.Wire(), Text: text, TS: util.Now()})
	d.hub.SystemRecorded(i18n.S("【会议】") + text)
	d.archiveMinutes(m) // r_25 t_208：散会即机械归档（混合归档的先落半）
}

// archiveMinutes writes the meeting's mechanically-compressed minutes
// into the kb (r_25 定稿 §〇方案 c). Best-effort with a surfaced
// residue: a failed archive is one system line, never a broken 散会.
// The key is day-scoped with a sequence (same req, same day, N-th
// meeting → -2), and WriteExpect(…,0) + retry keeps two meetings on
// one req from racing the same slot.
func (d *Dispatcher) archiveMinutes(m meeting.Meeting) {
	if d.cfg.Docs == nil {
		return
	}
	key, werr := d.writeMinutes(m)
	if werr != nil {
		log.Printf("[会议] %s: 纪要归档失败：%v", d.projectKey, werr)
		d.hub.SystemRecorded(i18n.Sf(
			"【会议】%s《%s》纪要归档失败（%v）——转录仍在会议台账，可让主持手动 kb write 补录", m.ReqID, m.ReqTitle, werr))
		return
	}
	// 主持修订轻提示（定稿 §四：不催不逼，知道即可——24h 修订窗）
	if m.Chair != "" {
		d.hub.SystemRecorded(i18n.Sf(
			"【会议】纪要已机械归档（%s），%s 可在 24h 内 kb write 修订提纯（过期走需求池重新评审）", key, m.Chair))
		// r_25 混合归档的后半——修订驱动：房间系统行主持的会话收不到
		// （成员只吃车道注入），把修订窗直递主持本人：一次注入、不重复
		// 催（「不催不逼」的边界），核对两件事＋怎么改；主持不在调度
		//（房主主持的会）则只有房间行，不硬投。
		if d.lookup(m.Chair) != nil {
			d.deliver(m.Chair, minutesRevisePrompt(key, m), nil, nil)
		}
	}
}

// minutesRevisePrompt is the 主持's one-shot revision invitation (r_25
// 定稿 §一：机械先落＋主持 24h 内 kb write 修订). The mechanical pass
// keeps skeleton only; the chair is the semantic authority — 逐人核对
// 清单 (小马①) and 结论失真 are the two things worth their eyes.
func minutesRevisePrompt(key string, m meeting.Meeting) string {
	return fmt.Sprintf(
		"【纪要修订】你主持的评审会（%s《%s》）已散会，纪要机械归档在黑板 %s（rev 1）。机械压缩只保骨架，请在 24h 修订窗内核对两件事：① 逐人核对清单（采纳项｜提出人｜去向）是否齐全；② 结论与方案节有无失真。需要修订就 kb write 该键（带 expect_rev=1 防并发覆盖）；过期视为认可机械版。核对无误则不必动作、不必在房内汇报。",
		m.ReqID, m.ReqTitle, key)
}

// writeMinutes picks the day-sequence slot and lands the doc; a rev
// race on the fresh key retries the next slot (bounded). The key
// follows the room (v2.9 隔离跟房走): the hall keeps the legacy
// ops/meetings/ shelf (its own), a project lands on p/<key>/meetings/
// — a room's review minutes belong to that room's shelf, not the
// studio's operations shelf.
func (d *Dispatcher) writeMinutes(m meeting.Meeting) (string, error) {
	prefix := meeting.MinutesKey(m.ReqID, 1)
	prefix = prefix[:strings.LastIndex(prefix, "-")+1] // ops/meetings/<req>-
	if d.projectKey != "" && d.projectKey != chat.LobbyKey {
		prefix = kb.ProjectDocKey(d.projectKey, strings.TrimPrefix(prefix, "ops/")) // p/<key>/meetings/<req>-
	}
	for seq := 1; seq <= 8; seq++ {
		key := fmt.Sprintf("%s%d", prefix, seq)
		if _, err := d.cfg.Docs.Get(key, 0); err == nil {
			continue // 该序号已被占用（同需求同日第 N 场）——试下一个
		}
		body := meeting.RenderMinutes(m)
		if _, err := d.cfg.Docs.WriteExpect(key, meeting.MinutesTitle(m), body, "会议归档", 0); err == nil {
			return key, nil
		}
		// 并发落进同键（另一场会恰好同拍归档）：换下一序号重试
	}
	return "", errors.New(i18n.Sf("序号槽 %s1-%d 均不可用", prefix, 8))
}

// noteMeetingTurn captures one mirrored turn of a live meeting: every
// participant's line lands in the transcript; the chair's first turn
// before the summary phase becomes the Opening (requirement recap +
// draft breakdown), their turns during the summary phase become the
// 方案.
func (d *Dispatcher) noteMeetingTurn(name, content string) {
	if d.cfg.Meetings == nil || content == "" {
		return
	}
	m, live := d.cfg.Meetings.Active(d.projectKey)
	if !live {
		return
	}
	inRoom := name == m.Chair
	for _, p := range m.Participants {
		if p == name {
			inRoom = true
		}
	}
	if !inRoom {
		return
	}
	if name == m.Chair {
		if m.Phase == meeting.PhaseSummary {
			d.cfg.Meetings.NoteConclusion(d.projectKey, content)
		} else if m.Opening == "" {
			d.cfg.Meetings.NoteOpening(d.projectKey, content)
		}
	}
	d.cfg.Meetings.Line(d.projectKey, name, content)
}

// pickRequirement chooses the convene's subject: the oldest open
// requirement this project has never taken through a meeting. When
// every entry has had its turn, the oldest one whose last review is
// stale past MeetingReconveneAfter gets the re-run (「过期走需求池
// 重新评审」) — a reviewed-but-unlanded requirement whose window is
// still fresh is the clock's silence, not its next agenda: the review
// is done, what's pending is the host's slot, and re-chaired every
// beat it was pure noise (the r_29 死循环).
func (d *Dispatcher) pickRequirement() (requirements.Req, bool) {
	// r_31：只捞净 open——parking（蓄着先不做）对会议钟不可见（「蓄着
	// 的时机未到，开会就是变相开工」评审裁定，与拆解注入同口径）
	open := make([]requirements.Req, 0, 4)
	for _, r := range d.cfg.Reqs.ListByProject(d.cfg.Subject, d.projectKey) {
		if r.Status == requirements.StatusOpen {
			open = append(open, r)
		}
	}
	if len(open) == 0 {
		return requirements.Req{}, false
	}
	lastAt := d.cfg.Meetings.ReviewedAt(d.projectKey)
	for _, r := range open {
		if _, seen := lastAt[r.ID]; !seen {
			return r, true
		}
	}
	staleBefore := util.Now() - int64(MeetingReconveneAfter/time.Second)
	for _, r := range open {
		if lastAt[r.ID] <= staleBefore {
			return r, true
		}
	}
	return requirements.Req{}, false
}

// idle reports whether the member has nothing in their hands (no
// doing task anywhere — hands are global, rooms are not).
func (d *Dispatcher) idle(name string) bool {
	doing, _ := d.load(name)
	return doing == 0
}

// load counts the member's doing and todo tasks (the convene's
// busyness thermometer).
func (d *Dispatcher) load(name string) (doing, todo int) {
	for _, t := range d.cfg.Tasks.TasksOf(name) {
		switch t.Status {
		case tasks.StatusDoing:
			doing++
		case tasks.StatusTodo:
			todo++
		}
	}
	return doing, todo
}

// meetingHostPrompt is the chair's convening brief (the orchestrator):
// open with the requirement recap and a DRAFT breakdown, then collect
// opinions by naming each participant (@ mentions are the delivery
// mechanism — the office routes them into sessions). The opening turn
// is the record's Opening; the closing 方案 arrives with the summary
// prompt.
func meetingHostPrompt(m meeting.Meeting) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【需求评审会·主持】now=%d 项目=%s 需求 %s《%s》\n", util.Now(), m.ProjectKey, m.ReqID, m.ReqTitle)
	b.WriteString("你是本会主持（编排者）。请先做开场陈述：复述需求背景与目标、用户场景、验收口径，\n")
	b.WriteString("并给出你的初步拆解草案（任务包列表：做什么/建议谁做/量级）——拆解可按入职规程参考四源。\n")
	b.WriteString("然后在办公室发言呈现开场，并在消息里逐一点名参会人（@" + strings.Join(m.Participants, " @") + "）征求他们的专业意见。\n")
	b.WriteString("议程钟在你手里：讨论收得拢就提前收——用你的 meeting end 命令立即结束当前议程段（讨论段直进总结、总结段当场散会）；\n")
	b.WriteString("窗口将尽话未了用 meeting extend 10（分钟）延长当前议程段（每次上限 60 分钟，可叠加）——别干等钟，也别让没聊完的话被窗口截断。\n")
	b.WriteString("（本条注入即开会指令：无需确认，直接开始；你的发言会自动回到办公室并记入会议纪要。）")
	return b.String()
}

// meetingGuestPrompt is one invitee's invite: their seat at the table
// is their role — the review wants their professional opinion. The
// product manager gets the requirement-owner's brief instead of the
// generic feasibility ask.
func meetingGuestPrompt(m meeting.Meeting, role string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【需求评审会·参会】now=%d 项目=%s 需求 %s《%s》：编排者 %s 已召集评审会，你在参会名单中。\n",
		util.Now(), m.ProjectKey, m.ReqID, m.ReqTitle, m.Chair)
	if isPMRole(role) {
		b.WriteString("你是需求方（产品）：请补充需求背景、用户场景与验收口径，回应其他参会人对需求的疑问，并从产品角度评审拆解草案。\n")
	} else {
		fmt.Fprintf(&b, "请从你的岗位（%s）出发，就需求可行性与实现方案提出具体意见：技术/设计方案、工作量量级、风险、更优解均可；等主持开场后在办公室直接发言。\n", role)
	}
	b.WriteString("（本条注入即参会指令：无需确认；你的发言会自动回到办公室并记入会议纪要。）")
	return b.String()
}

// meetingSummaryPrompt is the agenda's closing ask: the chair lands
// the final breakdown and files it THEMSELVES — plan submit carrying
// the requirement id, then @房主 for review (v2.4: the "@排期编排组"
// relay is retired; the orchestrator is already holding the meeting).
func meetingSummaryPrompt(m meeting.Meeting) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【需求评审会·总结】now=%d 需求 %s《%s》的讨论窗口结束。请汇总本次评审并播报：\n", util.Now(), m.ReqID, m.ReqTitle)
	b.WriteString("  1. 最终拆解方案（采纳了哪些意见、如何取舍；任务包：谁做什么、量级、起止）；\n")
	b.WriteString("  2. 遗留风险与需要房主决策的点。\n")
	fmt.Fprintf(&b, "播报后请把拆解落成排期提案：用你的 plan submit 命令提交（草稿带 \"req\":\"%s\"，任务包/起止/依赖按入职规程——工期按 AI 节奏估，分钟～小时级，别按人天排），并在办公室播报提案摘要、@房主 待审。", m.ReqID)
	b.WriteString("方案已播报、提案已提交即可 meeting end 当场散会；总结还没成形则 meeting extend 10 先延长总结窗。\n")
	return b.String()
}

// firstLine clips prose to its first line for one-line summaries.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 120 {
		return string(r[:120])
	}
	return s
}
