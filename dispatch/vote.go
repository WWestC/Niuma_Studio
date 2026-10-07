package dispatch

// vote.go — AI 重编译投票：成员对「重新编译并重启」（rebuild）的举手
// 表决。开关按项目落在 staffing 设置（rebuild_vote，默认关、开启走
// POST /p/{key}/rebuild-vote 的 REBUILD 确认令牌）；开着时，成员回合
// 回复里以固定前缀说话就是投票协议——确认行（r_20「已读要点」）的同
// 款纪律：前缀钉死、在 onEvent 的镜像之前拦截、系统行记账。
//
// 协议词汇（前缀匹配，后面可附理由）：
//   - 「提议重编译…」 开一轮投票（发起人自动计同意）
//   - 「同意重编译…」 投同意票（仅在票开着时是协议线）
//   - 「反对重编译…」 投反对票（同上）
//
// 计票口径：开票那刻的在册成员数为分母（中途入/离场不动门槛——法定
// 人数开票即冻结），同意×2 > 分母即通过（4 人要 3 票、1 人提议即满
// 票）；反对×2 ≥ 分母则数学上不可能过半，提前收场。通过即调 Config.
// RebuildFire（main 接服务端的 MemberRebuild——与房主按钮同一条编译/
// 换位/交接核心，同一把 busy 锁，只是发起词换成计票）。所有投票相关
// 的房间反馈都是 SystemRecorded（历史级：重启后回放仍看得到这轮表决）。
//
// 三个收场：通过（触发执行）、否决（反对封死）、超时（VoteWindow 内
// 未过半作罢）；外加房主关开关作废（server 的关闭腿经 fleet 调
// CancelVote）。一人一票幂等（重投吞掉不记账）；票没开时的「同意/
// 反对重编译」不当协议线，按普通聊天照常镜像——成员平时讨论 rebuild
// 不该被吞话。

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
)

// VoteWindow is how long a ballot stays open without a verdict (a busy
// member's vote prompt parks behind their current turn — the window
// must outlive a long turn, not the other way round). A var (not a
// const) purely as the tests' throttle seam (PermWindow's discipline).
var VoteWindow = 10 * time.Minute

// The protocol prefixes (fixed vocabulary, the resume-ack discipline:
// deterministic interception, no model in the loop).
const (
	voteProposePrefix = "提议重编译"
	voteAgreePrefix   = "同意重编译"
	voteAgainstPrefix = "反对重编译"
)

// voteBallot is one open ballot (guarded by d.mu). total is frozen at
// open — the quorum the proposal must beat, not a live roster count.
type voteBallot struct {
	by      string // proposer (auto-counted agree)
	total   int    // roster size at open (frozen)
	agree   map[string]bool
	against map[string]bool
	timer   *time.Timer // the expiry clock (VoteWindow)
	fired   bool        // a verdict landed (pass/fail); the book is closed
}

// voteVerdict is a ballot's terminal ruling.
type voteVerdict int

const (
	voteNone voteVerdict = iota
	votePass             // 同意过半：触发执行
	voteFail             // 反对封死：同意不可能过半，提前否决
)

// voteEnabled reads the project's switch live (every proposal re-reads
// — the settings face's flip takes effect on the very next line).
func (d *Dispatcher) voteEnabled() bool {
	return d.staff != nil && d.staff.SettingsOf(d.projectKey).RebuildVote
}

// voteLine is onEvent's interception gate: true = the reply was vote
// protocol and is already ledgered (do not mirror it into the room as
// a say). Raw content is matched (not routeReply's shaped reply) — a
// protocol line must not be masked by a stale-quote header.
func (d *Dispatcher) voteLine(m *member, content string) bool {
	line := strings.TrimSpace(content)
	if strings.HasPrefix(line, voteProposePrefix) {
		if !d.voteEnabled() {
			return false // 开关关着：不吞话，当普通聊天照常镜像
		}
		d.voteOpen(m, strings.TrimSpace(strings.TrimPrefix(line, voteProposePrefix)))
		return true
	}
	if !strings.HasPrefix(line, voteAgreePrefix) && !strings.HasPrefix(line, voteAgainstPrefix) {
		return false
	}
	return d.voteCast(m, strings.HasPrefix(line, voteAgreePrefix))
}

// voteOpen starts one ballot: proposer auto-agrees, the room hears the
// recorded opening line, every OTHER member gets the frozen 【重编译
// 投票】 injection (Announce's wake discipline — one cold member's
// parked delivery never blocks the rest), and the single-member room
// passes on the spot (1/1 is a majority).
func (d *Dispatcher) voteOpen(m *member, reason string) {
	d.mu.Lock()
	if d.vote != nil && !d.vote.fired {
		d.mu.Unlock()
		d.hub.System(i18n.Sf("%s 的提议未受理：已有重编译投票进行中，先投完这轮", m.name))
		return
	}
	v := &voteBallot{
		by: m.name, total: len(d.members),
		agree: map[string]bool{m.name: true}, against: map[string]bool{},
	}
	if v.total < 1 {
		v.total = 1 // 发起人必在册，防御式夹回
	}
	verdict := voteResolveLocked(v)
	if verdict == voteNone {
		v.timer = time.AfterFunc(VoteWindow, d.voteExpire)
		d.vote = v
	} else {
		voteStopLocked(v) // 开票即满票（单人房）：当场收卷，不留一本开着的空账
	}
	var others []string
	for name := range d.members {
		if name != m.name {
			others = append(others, name)
		}
	}
	d.mu.Unlock()

	reasonLine := ""
	if reason != "" {
		reasonLine = i18n.Sf("，理由：%s", reason)
	}
	open := i18n.Sf("%s 发起重编译投票（全房 %d 人投票、发起人已计同意，超过半数同意即自动重新编译并重启工作室）%s",
		m.name, v.total, reasonLine)
	if v.total > 1 {
		open += i18n.S("——请回复「同意重编译」或「反对重编译」")
	}
	d.hub.SystemRecorded(open)
	for _, name := range others {
		if mm := d.lookup(name); mm != nil {
			d.enqueueAt(mm, votePromptLine(m.name, reason, v.total), nil, nil, time.Now().Unix(), nil)
			d.laneKick(mm)
		}
	}
	if verdict == votePass {
		d.voteAnnouncePass(v)
	}
}

// voteCast records one member's ballot (first vote wins; a re-vote is
// swallowed idempotently). No open ballot = not protocol (the reply
// mirrors as ordinary chat — rebuild being discussed is not rebuild
// being voted on).
func (d *Dispatcher) voteCast(m *member, yes bool) bool {
	d.mu.Lock()
	v := d.vote
	if v == nil || v.fired {
		d.mu.Unlock()
		return false
	}
	if v.agree[m.name] || v.against[m.name] {
		d.mu.Unlock()
		return true // 一人一票：重投吞掉，不记账不进房
	}
	if yes {
		v.agree[m.name] = true
	} else {
		v.against[m.name] = true
	}
	verdict := voteResolveLocked(v)
	if verdict != voteNone {
		d.vote = nil // 收场即闭卷（timer 由 voteStopLocked 停）
		voteStopLocked(v)
	}
	a, n, total := len(v.agree), len(v.against), v.total
	by := v.by
	d.mu.Unlock()

	if yes {
		d.hub.SystemRecorded(i18n.Sf("%s 投了同意票（同意 %d/%d，反对 %d）", m.name, a, total, n))
	} else {
		d.hub.SystemRecorded(i18n.Sf("%s 投了反对票（同意 %d/%d，反对 %d）", m.name, a, total, n))
	}
	switch verdict {
	case votePass:
		d.voteAnnouncePass(v)
	case voteFail:
		d.hub.SystemRecorded(i18n.Sf(
			"反对票已达 %d/%d——同意不可能过半，重编译投票未通过%s", n, total, voteProposerTail(by)))
	}
	return true
}

// voteAnnouncePass records the pass line and fires the rebuild (async —
// the compile runs seconds-to-minutes and must not park the bridge's
// event goroutine; the server face owns every studio-wide announcement).
func (d *Dispatcher) voteAnnouncePass(v *voteBallot) {
	tally := i18n.Sf("%d/%d 同意", len(v.agree), v.total)
	d.hub.SystemRecorded(i18n.Sf(
		"重编译投票通过（%s）——超过半数，立即执行重编译重启%s", tally, voteProposerTail(v.by)))
	if d.cfg.RebuildFire == nil {
		// open 已挡（开启面要求通道接线），防御式兜底：房间说实话
		d.hub.SystemRecorded(i18n.S("[重编译投票] 执行通道未接线（内嵌/测试形态）——本轮投票通过但未执行"))
		return
	}
	fire, project, by := d.cfg.RebuildFire, d.projectKey, v.by
	go util.Guard("dispatch: rebuild vote fire", func() {
		if err := fire(project, by, tally); err != nil {
			// 编译失败已由服务端全室通告（含原因）；这里只留日志。
			log.Printf("[重编译投票] %s: 投票通过（%s）但执行未完成：%v", project, tally, err)
		}
	})
}

// voteProposerTail renders the audit tail naming the proposal's author.
func voteProposerTail(by string) string {
	return i18n.Sf("（发起人：%s）", by)
}

// voteResolveLocked judges the ballot against its frozen quorum. Pure;
// callers hold d.mu and act on the verdict (closing the book is the
// caller's move, so voteOpen can keep the book open for its timer).
func voteResolveLocked(v *voteBallot) voteVerdict {
	if len(v.agree)*2 > v.total {
		return votePass
	}
	if len(v.against)*2 >= v.total {
		return voteFail // 剩余未投票全投同意也到不了过半
	}
	return voteNone
}

// voteStopLocked closes the ballot's clock. Callers hold d.mu.
func voteStopLocked(v *voteBallot) {
	v.fired = true
	if v.timer != nil {
		v.timer.Stop()
	}
}

// voteExpire is the window's terminal (AfterFunc): no verdict in time —
// the ballot lapses with a recorded line. Races a concurrent verdict by
// re-checking under d.mu (first-come wins, the loser no-ops).
func (d *Dispatcher) voteExpire() {
	select {
	case <-d.stop:
		return // 收摊中：不再发声
	default:
	}
	d.mu.Lock()
	v := d.vote
	if v == nil || v.fired {
		d.mu.Unlock()
		return
	}
	d.vote = nil
	voteStopLocked(v)
	a, total := len(v.agree), v.total
	d.mu.Unlock()
	d.hub.SystemRecorded(i18n.Sf(
		"重编译投票超时——同意 %d/%d 未过半，本轮作废（需要时可重新提议）", a, total))
}

// CancelVote voids an open ballot (the switch-off leg: the server's
// rebuild-vote face taps the fleet when the project's switch turns
// OFF). No ballot open = a no-op. Reports whether one was voided.
func (d *Dispatcher) CancelVote() bool {
	d.mu.Lock()
	v := d.vote
	if v == nil || v.fired {
		d.mu.Unlock()
		return false
	}
	d.vote = nil
	voteStopLocked(v)
	d.mu.Unlock()
	d.hub.SystemRecorded(i18n.S("房主关闭了 AI 重编译投票——本轮投票作废"))
	return true
}

// votePromptLine is the frozen 【重编译投票】 injection (the 自驾
// 驶·选题 family's shape): the motion with its reason, the quorum and
// consequence, and the exact reply prefixes that count.
func votePromptLine(by, reason string, total int) string {
	why := ""
	if reason != "" {
		why = "，理由：" + reason
	}
	return fmt.Sprintf(
		"【重编译投票｜%s 提议】\n%s 提议重新编译并重启工作室（rebuild）%s。本房共 %d 人投票（%s 已计同意），超过半数同意即立即自动执行——编译期间工作室照常运行，完成后自动重启，成员由调度器自动归位。\n请表态：同意回复「同意重编译」，反对回复「反对重编译」（必须以这五个字开头，后面可附理由）。",
		by, by, why, total, by)
}
