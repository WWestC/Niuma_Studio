package dispatch

// clocks.go — 节拍钟：巡检（patrol）、看护（watchdog）、日报（daily）。
// 拆自 dispatcher.go（v2.15 结构整理）。

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// PatrolEveryFromEnv resolves the interval from NIUMA_PATROL_EVERY (legacy DH_PATROL_EVERY still honored via util.Env): a
// duration ("45m", "90s", "1h") or a bare positive integer (minutes)
// overrides the default; anything malformed keeps the default — a bad
// env var must never brick the clock (or the room).
func PatrolEveryFromEnv() time.Duration {
	v := strings.TrimSpace(util.Env("PATROL_EVERY"))
	if v == "" {
		return DefaultPatrolEvery
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return DefaultPatrolEvery
}

// startPatrol launches the tick goroutine (bridge-off dispatchers never
// get here — Start returns before adopt). PatrolEvery < 0 disables.
func (d *Dispatcher) startPatrol() {
	every := d.cfg.PatrolEvery
	if every == 0 {
		every = DefaultPatrolEvery
	}
	if every < 0 {
		return
	}
	d.wg.Add(1)
	go d.runClock(every, d.cfg.PatrolTick, d.PatrolNow)
}

// runClock is the interval clocks' shared tick-goroutine skeleton
// (patrol and watchdog, previously two byte-for-byte loop copies): a
// wall timer unless cfg supplies the fake-clock channel wholesale
// (tests), one synchronous fire per tick, stop anywhere. The daily
// clock keeps its own loop — its beat anchors to a time of day, not an
// interval. Each fire rides a panic fence: a dead clock is a silently
// stopped patrol/watchdog (nobody restarts it), so one bad tick logs
// and the next beat fires as scheduled.
func (d *Dispatcher) runClock(every time.Duration, tick <-chan time.Time, fire func()) {
	defer d.wg.Done()
	var timer *time.Timer
	if tick == nil {
		timer = time.NewTimer(every)
		defer timer.Stop()
	}
	for {
		var t <-chan time.Time
		if timer != nil {
			t = timer.C
		} else {
			t = tick
		}
		select {
		case <-d.stop:
			return
		case <-t:
			if timer != nil {
				timer.Reset(every)
			}
			util.Guard("dispatch: tick clock", fire)
		}
	}
}

// PatrolNow fires one patrol tick: the §2.5 injection delivered to this
// project's orchestrator session. No orchestrator under management =
// no-op (the clock keeps ticking; a later birth starts catching ticks).
func (d *Dispatcher) PatrolNow() {
	if d.pausedNow() {
		return // 房间暂停：巡逻钟歇拍（恢复后下一拍照常）
	}
	name := d.orchestratorName()
	if name == "" {
		return
	}
	// r_16 t_179 第五项——停滞检查：在途单（todo/doing 非 pending）的
	// updated_ts 距今超阈即点名（「有活没动」的信号）。普通巡检模式同
	// 样生效（不依赖 autopilot）：阈值走 staffing 旋钮（patrol_stall，
	// 设置>默认 15min）。
	// r_14 微迭代（t_181 验收遗留五条款）：清单落定稿 §二三列格式
	// （t_NN｜负责人｜已停 M 分钟）——负责人不在房带（离线）标注、
	// 「待招·」占位单直接跳过；同单连续第二轮标（再）；autopilot 已
	// 续命的单 15min 记忆窗内标（已续命）——续命账目与清单共享记忆。
	stallMin := d.patrolStallMin()
	var stallRows []string
	if d.cfg.Tasks != nil {
		threshold := int64(stallMin) * 60
		now := util.Now()
		// 上一轮清单记忆先取走（（再）标记的比对基准——本轮重记）
		d.mu.Lock()
		prev := d.stallPrev
		d.stallPrev = map[string]bool{}
		resumeSnap := make(map[string]int64, len(d.resumeMark))
		for id, ts := range d.resumeMark {
			resumeSnap[id] = ts
		}
		d.mu.Unlock()
		live := map[string]bool{}
		for _, m := range d.hub.Members() { // 宽限幽灵也算在房——离线判据是「不在名册」
			live[m.Name] = true
		}
		cur := map[string]bool{}
		for _, t := range d.cfg.Tasks.ListFiltered("", "", d.projectKey, "") {
			if t.Status != tasks.StatusTodo && t.Status != tasks.StatusDoing {
				continue
			}
			if t.Pending != nil {
				continue // 待确认变更不算停滞——那是流程不是怠工
			}
			if strings.HasPrefix(t.Assignee, "待招") {
				continue // 待招占位直接跳过——催一个空座位没有意义（招聘线的事）
			}
			if t.UpdatedTS > 0 && now-t.UpdatedTS > threshold {
				owner := t.Assignee
				if owner == "" {
					owner = "待领" // 组池单——编排者的补货/改派盘面
				} else if !live[owner] {
					owner += "（离线）" // 双豁免补丁：计入但标注，@唤醒与否编排者自决
				}
				row := fmt.Sprintf("%s｜%s｜已停 %.0f 分钟", t.ID, owner, float64(now-t.UpdatedTS)/60)
				if prev[t.ID] {
					row += "（再）" // 连续第二轮：升级处置而非重复催（定稿纪律）
				}
				if now-resumeSnap[t.ID] < 15*60 {
					row += "（已续命）" // 自动驾驶已直达负责人——不再二次催
				}
				cur[t.ID] = true
				stallRows = append(stallRows, row)
			}
		}
		d.mu.Lock()
		d.stallPrev = cur
		for id, ts := range d.resumeMark { // 续命账目懒清（1h 外无比对价值）
			if now-ts > 3600 {
				delete(d.resumeMark, id)
			}
		}
		d.mu.Unlock()
	}
	// 清单出现时检查枚举里也点名停滞（定稿 §二排位：逾期之后、blocked
	// 之前）；无停滞整段省略——注入与四项基线逐字一致（零回退纪律）。
	checkLine := "逾期未完"
	if len(stallRows) > 0 {
		checkLine += "/停滞（在办无动静超阈）"
	}
	checkLine += "/blocked 超阈/待排期（无 start 的本项目任务）/提案滞留" + d.patrolGitLines()
	stallBlock := ""
	if len(stallRows) > 0 {
		stallBlock = "\n停滞清单（阈值 " + fmt.Sprint(stallMin) + " 分钟）：\n  " + strings.Join(stallRows, "\n  ")
	}
	d.deliver(name, fmt.Sprintf(
		"【巡检】now=%d 项目=%s。检查：%s。有异常按职权处置（改排期字段或催办 @负责人；未合并分支该收口时提交合并提案），无异常回一行%s",
		util.Now(), d.projectKey, checkLine, stallBlock), nil, nil)
}

// patrolStallMin（r_16 t_179）: 停滞阈的四级链——设置（staffing
// patrol_stall）> 内置默认 15min。dispatcher 没有 env 档（r14-stall 定稿
// 只划设置与默认两级）。
func (d *Dispatcher) patrolStallMin() int {
	if d.staff != nil {
		if v := d.staff.SettingsOf(d.projectKey).PatrolStallMin; v > 0 {
			return v
		}
	}
	return 15
}

// orchestratorName finds this room's orchestrator among the managed
// members by the role marker (the person's NAME is swappable, the role
// is the contract — orchestration §2.2).
func (d *Dispatcher) orchestratorName() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, m := range d.members {
		if m.role == agents.OrchestratorRole {
			return m.name
		}
	}
	return ""
}

// startWatchdog launches the probe tick goroutine (bridge-off
// dispatchers never get here — Start returns before adopt).
// WatchdogEvery < 0 disables.
func (d *Dispatcher) startWatchdog() {
	every := d.cfg.WatchdogEvery
	if every == 0 {
		every = DefaultWatchdogEvery
	}
	if every < 0 {
		return
	}
	d.wg.Add(1)
	go d.runClock(every, d.cfg.WatchdogTick, d.WatchdogNow)
}

// WatchdogNow fires one probe beat over every Running member. Idle
// members are deliberately untouched — reapable is their healthy
// state, and deliver's -32004 path resumes them on demand — EXCEPT
// those with a parked lane: a line only parks behind a running prompt,
// and a member whose dispatcher status says idle while lines sit
// parked means that prompt died without a terminal event (the session
// was reaped mid-turn) — nothing will ever pop the lane again. Each
// beat kicks those lanes: a live session takes the line now, a reaped
// one rides deliver's -32004 reactivate ladder, and a zombie prompt
// re-parks to wait out the app-server's own 10-minute reap (after
// which the ladder takes over). 房主实录: 两条点名线在小鹿的车道上
// 停了半小时无人再碰——本拍就是为那条车道补的一脚。
func (d *Dispatcher) WatchdogNow() {
	if d.pausedNow() {
		return // 房间暂停：探活/复活都先歇——resurrect 会开回合，暂停期不开
	}
	d.mu.Lock()
	type busy struct {
		m     *member
		since time.Time
		noted bool
	}
	var running []busy
	var stalled []*member
	for _, m := range d.members {
		if m.status == StatusRunning {
			running = append(running, busy{m: m, since: m.watch.runningSince, noted: m.watch.stallNoted})
		} else if len(m.lanes.queue) > 0 {
			stalled = append(stalled, m)
		}
	}
	d.mu.Unlock()
	for _, b := range running {
		if d.stopped() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := d.bridge.ProbeSession(ctx, d.sessionOf(b.m))
		cancel()
		switch {
		case err == nil:
			// The probe WAS the keepalive. Stall note comes with it —
			// a long-but-live turn deserves the one-time heads-up, not
			// an interruption.
			d.noteStall(b.m, b.since, b.noted)
		case zcode.IsCode(err, zcode.ErrSessionNotActive):
			// resurrect 尾上那记 deliver 可能挂到注入等待超时——放它
			// 到自己的 goroutine 上跑并挂 wg（Stop 收拢），探针环照常
			// 打下一拍（否则一个冷会话成员的拉回能把全房间的保活都
			// 冻住）。
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.resurrect(b.m)
			}()
		default:
			// Bridge trouble (dead child, wire stall) — the bridge
			// watchdog owns the respawn; nothing to verdict here.
		}
	}
	for _, m := range stalled {
		if d.stopped() {
			return
		}
		d.laneKick(m)
	}
	// P0 会话折叠（compact.go）：上下文越线的空闲成员补一刀折叠——
	// 阈值关着时这是零开销的一次阈值读。
	d.CompactNow()
}

// noteStall posts the one-time 「长回合」 note when a Running stretch
// outlives TurnStall (once per stretch — the flag re-arms at the next
// setStatus(Running)).
func (d *Dispatcher) noteStall(m *member, since time.Time, noted bool) {
	stall := d.cfg.TurnStall
	if stall == 0 {
		stall = DefaultTurnStall
	}
	if stall < 0 || since.IsZero() || noted || time.Since(since) < stall {
		return
	}
	d.mu.Lock()
	already := m.watch.stallNoted // re-check under the lock: beats can race
	m.watch.stallNoted = true
	d.mu.Unlock()
	if already {
		return
	}
	d.hub.System(i18n.Sf(
		"[看护] %s 的回合已持续 %d 分钟无终点事件（长回合）——可能在跑长活，会话已持续保活；若怀疑卡死，可 @%s 询问进展或用 dispatch steer 打断",
		m.name, int(time.Since(since).Minutes()), m.name))
	// 双门（v2.9）：这正是「亮着干活中、抽屉却空」的悬置回合——把
	// 「多久没动静」落进成员自己的时间线，抽屉里看得见悬置本身。
	d.emitTrace([]chat.TraceEntry{{From: m.name, Kind: chat.TraceSys,
		Text: clipMiddle(i18n.Sf(
			"回合已持续 %d 分钟无终点事件（长回合）——会话持续保活中，可能在跑长活",
			int(time.Since(since).Minutes())), 4096)}})
}

// resurrect brings a cold-session Running member back: reactivate
// (resume + subscribe — the persisted session keeps every word), tell
// the room what happened, and re-poke the member to continue the
// interrupted work. A failed reactivate parks the member idle with a
// pointer to the manual recall path — never silently.
func (d *Dispatcher) resurrect(m *member) {
	if err := d.reactivate(m); err != nil {
		log.Printf("[看护] %s 会话被回收，自动拉回失败：%v", m.name, err)
		d.setStatus(m, StatusIdle)
		d.hub.System(i18n.Sf(
			"[看护] %s 的回合被宿主会话回收打断，自动拉回失败（%v）——对话记忆完好，点名或召回即可让其继续",
			m.name, err))
		d.emitTrace([]chat.TraceEntry{{From: m.name, Kind: chat.TraceSys,
			Text: clipMiddle(i18n.Sf("回合被宿主的会话空闲回收打断，自动拉回失败（%v）——对话记忆完好，点名或召回即可继续", err), 4096)}})
		return
	}
	log.Printf("[看护] %s 会话被回收，已自动拉回并请其继续", m.name)
	d.hub.System(i18n.Sf(
		"[看护] %s 的回合被宿主会话空闲回收打断（对话记忆完好）——已自动拉回，请其从中断处继续",
		m.name))
	d.emitTrace([]chat.TraceEntry{{From: m.name, Kind: chat.TraceSys,
		Text: i18n.S("回合被宿主的会话空闲回收打断（对话记忆完好）——已自动拉回，从中断处继续")}})
	d.deliver(m.name, fmt.Sprintf(
		"【看护】你刚才的回合被宿主的会话空闲回收打断（非你自己的过错，对话记忆完好）。请从中断处继续干下去；若上一轮已接近完成，直接汇报结果即可。"),
		nil, nil)
}

// ParseDailyAt resolves a "HH:MM" local time ("" = the 21:00 default);
// "off"/"-" disables. Malformed values keep the default — a bad config
// must never brick the dispatcher (or the room).
func ParseDailyAt(at string) (hh, mm int, enabled bool) {
	at = strings.TrimSpace(at)
	switch at {
	case "":
		return 21, 0, true
	case "off", "-":
		return 0, 0, false
	}
	parts := strings.Split(at, ":")
	if len(parts) != 2 {
		return 21, 0, true
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 21, 0, true
	}
	return h, m, true
}

// DailyAtFromEnv resolves NIUMA_DAILY_AT ("" = the 21:00 default, "off"/
// "-" disables, "HH:MM" sets the beat; malformed keeps the default).
func DailyAtFromEnv() string {
	return strings.TrimSpace(util.Env("DAILY_AT"))
}

// startDaily launches the daily tick goroutine (bridge-off
// dispatchers never get here — Start returns before adopt).
func (d *Dispatcher) startDaily() {
	hh, mm, on := ParseDailyAt(d.cfg.DailyAt)
	if !on {
		return
	}
	d.wg.Add(1)
	go d.dailyLoop(hh, mm)
}

// dailyLoop waits for the next daily time between reports.
// cfg.DailyTick (the fake-clock injection point) replaces the
// wall-clock timer wholesale when set. The fire rides the same panic
// fence as runClock's: a dead daily clock is a silently missing
// report, and nobody restarts it.
func (d *Dispatcher) dailyLoop(hh, mm int) {
	defer d.wg.Done()
	if d.cfg.DailyTick == nil {
		for {
			timer := time.NewTimer(time.Until(nextDailyAt(hh, mm, time.Now())))
			select {
			case <-d.stop:
				timer.Stop()
				return
			case <-timer.C:
				util.Guard("dispatch: daily clock", func() { d.dailyFire(util.Now()) })
			}
		}
	}
	for {
		select {
		case <-d.stop:
			return
		case ts := <-d.cfg.DailyTick:
			util.Guard("dispatch: daily clock", func() { d.dailyFire(ts.Unix()) })
		}
	}
}

// nextDailyAt is today's hh:mm, or tomorrow's when it already passed.
func nextDailyAt(hh, mm int, now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// DailyNow fires one daily report at the wall clock; dailyFire is the
// injectable core (the fake clock's tick value becomes the report's
// now= stamp). No orchestrator under management = no-op (the clock
// keeps ticking; a later birth starts catching days).
func (d *Dispatcher) DailyNow() { d.dailyFire(util.Now()) }

func (d *Dispatcher) dailyFire(now int64) {
	if d.pausedNow() {
		return // 房间暂停：日报钟歇拍——恢复后次日照常（错过的当日不补）
	}
	name := d.orchestratorName()
	if name == "" {
		return
	}
	d.deliver(name, fmt.Sprintf(
		"【日报】now=%d 项目=%s。汇总今日任务动静/在办/风险，形成日报后向办公室播报。%s",
		now, d.projectKey, d.dailyStats(now)), nil, nil)
}

// dailyStats is the 日报's machine stat tail (r_30 t_133): 当日 done/在途/
// 水位计数一行——引擎快照同源现算（与巡报/驾驶舱共饮一份 store），编排者
// 引用即可不用手数。机器统计只附尾行不改写正文（自由叙事是日报的灵魂）；
// 写完顺手 append ops/daily-log（编排者手册纪律行）。
func (d *Dispatcher) dailyStats(now int64) string {
	var done, active, open int
	day := dayStartUnix(now)
	if d.cfg.Tasks != nil {
		for _, t := range d.cfg.Tasks.ListFiltered("", "", d.projectKey, "") {
			switch {
			case t.Status == tasks.StatusDone && t.UpdatedTS >= day:
				done++
			case t.Status == tasks.StatusTodo || t.Status == tasks.StatusDoing:
				active++
			}
		}
	}
	if d.cfg.Reqs != nil {
		for _, r := range d.cfg.Reqs.ListByProject(d.cfg.Subject, d.projectKey) {
			if r.Status == requirements.StatusOpen {
				open++
			}
		}
	}
	return fmt.Sprintf("统计尾行（引用即可）：当日 done %d · 在途 %d · 需求池 open %d。", done, active, open)
}

// dayStartUnix is the local midnight floor (自然日口径——与驾驶舱
// dayStart/停滞判定同钟).
func dayStartUnix(now int64) int64 {
	t := time.Unix(now, 0).Local()
	y, m, dd := t.Date()
	return time.Date(y, m, dd, 0, 0, 0, 0, time.Local).Unix()
}
