package dispatch

// vote_test.go — AI 重编译投票的调度器半边（dispatch/vote.go 的契约）：
// 开关门（关着不吞话）、开票（系统行＋【重编译投票】注入、发起人计同
// 意）、过半判定（agree×2 > 冻结分母；恰好一半不过）、一人一票幂等、
// 反对封死提前否决、超时作废、房主关闭作废、单人房提议即满票。触发腿
// 用记录型假钩子验证「恰好一次＋计票文案」。

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// voteNames 是测试班的在册名（按需取前 n 位）。
var voteNames = []string{"甲", "乙", "丙", "丁"}

// startVoteStage wires one project dispatcher over the recording bridge
// with a staffing store whose rebuild_vote switch is ON and n attached
// members. All clocks parked — nothing fires on its own. fire==nil keeps
// the unwired shape (the pass then lands with an honest line).
func startVoteStage(t *testing.T, n int, fire func(project, by, tally string) error) (*Dispatcher, *chat.Hub, *watchBridge, *staffing.Store) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staff, err := staffing.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staff.SetRebuildVote("proj-vote", true)
	wb := &watchBridge{}
	d := Start(hub, store, wb, Config{
		Workspace:    t.TempDir(),
		InboxDir:     t.TempDir(),
		PatrolEvery:  -1,
		DailyAt:      "off",
		MeetingEvery: -1,
		WatchdogTick: make(chan time.Time), // 停摆：探针永不自发
		ProjectKey:   "proj-vote",
		StaffStore:   staff,
		RebuildFire:  fire,
	})
	t.Cleanup(d.Stop)
	for i := 0; i < n && i < len(voteNames); i++ {
		if err := d.attach(voteNames[i], "开发", fmt.Sprintf("s-vote-%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	return d, hub, wb, staff
}

// fireLog records the fire leg's calls (the goroutine writes, the test
// reads — a mutex, not a channel, so 「恰好一次」可以直接数）.
type fireLog struct {
	mu    sync.Mutex
	calls []string
}

func (f *fireLog) hook() func(project, by, tally string) error {
	return func(project, by, tally string) error {
		f.mu.Lock()
		f.calls = append(f.calls, project+"|"+by+"|"+tally)
		f.mu.Unlock()
		return nil
	}
}

func (f *fireLog) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fireLog) first() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[0]
}

// sysRecorded flattens the hub's recorded system lines for Contains checks.
func sysRecorded(hub *chat.Hub) string {
	var b strings.Builder
	for _, m := range hub.History() {
		if m.Type == chat.MsgSystem {
			b.WriteString(m.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// saidBy collects a member's mirrored say lines (protocol lines must
// never appear here — they ledger as system lines instead).
func saidBy(hub *chat.Hub, name string) []string {
	var out []string
	for _, m := range hub.History() {
		if m.Type == chat.MsgSay && m.From == name {
			out = append(out, m.Text)
		}
	}
	return out
}

func voteSends(wb *watchBridge) []string {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return append([]string(nil), wb.sends...)
}

func hasVotePrompt(wb *watchBridge) int {
	n := 0
	for _, s := range voteSends(wb) {
		if strings.Contains(s, "【重编译投票｜") {
			n++
		}
	}
	return n
}

// 开关关着：协议前缀不当拦截——提议照常镜像成聊天，不开票、不注入、不触发。
func TestVoteSwitchOffPassesProposalThrough(t *testing.T) {
	fl := &fireLog{}
	d, hub, wb, staff := startVoteStage(t, 2, fl.hook())
	staff.SetRebuildVote("proj-vote", false)

	d.onEvent(turnCompletedEvent("s-vote-1", "提议重编译：主线积压太多，该编译重启了"))
	waitFor(t, func() bool { return len(saidBy(hub, "甲")) == 1 }, "开关关着，提议应照常镜像成聊天消息")
	if got := saidBy(hub, "甲")[0]; !strings.Contains(got, "提议重编译") {
		t.Fatalf("镜像内容走样: %q", got)
	}
	if strings.Contains(sysRecorded(hub), "发起重编译投票") {
		t.Fatal("开关关着不该开票")
	}
	if hasVotePrompt(wb) != 0 {
		t.Fatal("开关关着不该注入投票提示")
	}
	if fl.count() != 0 {
		t.Fatal("开关关着不该触发执行")
	}
}

// 开票：系统行记账（含分母与发起人计同意）、其余成员各收一份【重编译
// 投票】注入（发起人不收）、原提议不进聊天流、未过半不触发。
func TestVoteOpenRecordsAndPrompts(t *testing.T) {
	fl := &fireLog{}
	d, hub, wb, _ := startVoteStage(t, 3, fl.hook())

	d.onEvent(turnCompletedEvent("s-vote-1", "提议重编译：新代码已全部落地，该重启上线了"))
	waitFor(t, func() bool { return hasVotePrompt(wb) == 2 }, "乙丙应各收到一份投票注入")
	sys := sysRecorded(hub)
	if !strings.Contains(sys, "甲 发起重编译投票") || !strings.Contains(sys, "全房 3 人投票") {
		t.Fatalf("开票系统行缺要素:\n%s", sys)
	}
	if !strings.Contains(sys, "发起人已计同意") {
		t.Fatalf("开票行该说明发起人已计同意:\n%s", sys)
	}
	if lines := saidBy(hub, "甲"); len(lines) != 0 {
		t.Fatalf("提议不该镜像成聊天消息: %v", lines)
	}
	for _, s := range voteSends(wb) {
		if !strings.Contains(s, "同意重编译") || !strings.Contains(s, "反对重编译") {
			t.Fatalf("注入该教会两个投票前缀:\n%s", s)
		}
	}
	if fl.count() != 0 {
		t.Fatal("2/3 未到（此时只有发起人 1 票），不该触发")
	}
}

// 过半判定：4 人房 2 票恰好一半不过、第 3 票过半即触发（恰好一次，
// 计票文案随行），票关后多余的同意被吞不重触发。
func TestVoteMajorityFiresOnce(t *testing.T) {
	fl := &fireLog{}
	d, hub, wb, _ := startVoteStage(t, 4, fl.hook())

	d.onEvent(turnCompletedEvent("s-vote-1", "提议重编译"))
	waitFor(t, func() bool { return hasVotePrompt(wb) == 3 }, "乙丙丁应各收到开票注入")
	d.onEvent(turnCompletedEvent("s-vote-2", "同意重编译，改动确实该上线"))
	if fl.count() != 0 {
		t.Fatal("2/4 恰好一半，不过半不该触发")
	}
	if sys := sysRecorded(hub); strings.Contains(sys, "投票通过") {
		t.Fatal("一半不该判通过")
	}
	d.onEvent(turnCompletedEvent("s-vote-3", "同意重编译"))
	waitFor(t, func() bool { return fl.count() == 1 }, "3/4 过半应触发执行")
	if got := fl.first(); got != "proj-vote|甲|3/4 同意" {
		t.Fatalf("触发参数走样: %q", got)
	}
	if sys := sysRecorded(hub); !strings.Contains(sys, "重编译投票通过（3/4 同意）") {
		t.Fatalf("通过系统行缺计票:\n%s", sys)
	}
	d.onEvent(turnCompletedEvent("s-vote-4", "同意重编译，慢了一步"))
	waitFor(t, func() bool { return len(saidBy(hub, "丁")) == 1 }, "票已收卷，迟到的同意该照常说话")
	if fl.count() != 1 {
		t.Fatalf("票关后的同意不该重触发: %d", fl.count())
	}
}

// 一人一票：重复投票第二次吞掉——只记一票、只落一行、不重复计数。
func TestVoteDuplicateBallotSwallowed(t *testing.T) {
	fl := &fireLog{}
	d, hub, _, _ := startVoteStage(t, 4, fl.hook())

	d.onEvent(turnCompletedEvent("s-vote-1", "提议重编译"))
	d.onEvent(turnCompletedEvent("s-vote-2", "同意重编译"))
	d.onEvent(turnCompletedEvent("s-vote-2", "同意重编译（再说一遍）"))
	if n := strings.Count(sysRecorded(hub), "乙 投了同意票"); n != 1 {
		t.Fatalf("重复投票该只记一行，实记 %d 行:\n%s", n, sysRecorded(hub))
	}
	if fl.count() != 0 {
		t.Fatal("重复票不该让 2/4 变 3/4")
	}
	if lines := saidBy(hub, "乙"); len(lines) != 0 {
		t.Fatalf("重复票被镜像成聊天了: %v", lines)
	}
}

// 反对封死：4 人房 2 反对即数学上不可能过半，提前否决；否决后到达的
// 同意不再是协议线，照常说话。
func TestVoteAgainstBlocksEarly(t *testing.T) {
	fl := &fireLog{}
	d, hub, _, _ := startVoteStage(t, 4, fl.hook())

	d.onEvent(turnCompletedEvent("s-vote-1", "提议重编译"))
	d.onEvent(turnCompletedEvent("s-vote-2", "反对重编译，还有在途任务没收尾"))
	d.onEvent(turnCompletedEvent("s-vote-3", "反对重编译"))
	if sys := sysRecorded(hub); !strings.Contains(sys, "重编译投票未通过") {
		t.Fatalf("2/4 反对该提前否决:\n%s", sys)
	}
	if fl.count() != 0 {
		t.Fatal("否决不该触发")
	}
	d.onEvent(turnCompletedEvent("s-vote-4", "同意重编译"))
	waitFor(t, func() bool { return len(saidBy(hub, "丁")) == 1 }, "否决后的同意该照常说话")
}

// 超时作废：窗口内无人过半，票过期落一行作废、不触发；之后可以再提议。
func TestVoteTimeoutLapses(t *testing.T) {
	prev := VoteWindow
	VoteWindow = 40 * time.Millisecond
	t.Cleanup(func() { VoteWindow = prev })
	fl := &fireLog{}
	d, hub, _, _ := startVoteStage(t, 4, fl.hook())

	d.onEvent(turnCompletedEvent("s-vote-1", "提议重编译"))
	waitFor(t, func() bool { return strings.Contains(sysRecorded(hub), "本轮作废") }, "超时该落作废行")
	if fl.count() != 0 {
		t.Fatal("作废不该触发")
	}
	// 作废后能再开（空账已清）：新一轮照常开票
	d.onEvent(turnCompletedEvent("s-vote-2", "提议重编译：再来一次"))
	waitFor(t, func() bool { return strings.Count(sysRecorded(hub), "发起重编译投票") == 2 }, "作废后应能重新提议")
}

// 房主关闭开关（fleet 的取消腿）：在投的票作废，之后的同意不当协议线。
func TestVoteCancelVoidOpenBallot(t *testing.T) {
	fl := &fireLog{}
	d, hub, _, _ := startVoteStage(t, 4, fl.hook())

	d.onEvent(turnCompletedEvent("s-vote-1", "提议重编译"))
	if !d.CancelVote() {
		t.Fatal("在投的票该被作废")
	}
	if sys := sysRecorded(hub); !strings.Contains(sys, "本轮投票作废") {
		t.Fatalf("作废该落系统行:\n%s", sys)
	}
	if d.CancelVote() {
		t.Fatal("无票在投时作废是空操作")
	}
	d.onEvent(turnCompletedEvent("s-vote-2", "同意重编译"))
	waitFor(t, func() bool { return len(saidBy(hub, "乙")) == 1 }, "作废后的同意该照常说话")
	if fl.count() != 0 {
		t.Fatal("作废不该触发")
	}
}

// 单人房：提议即满票（1/1 过半），当场通过并触发。
func TestVoteSingleMemberInstantPass(t *testing.T) {
	fl := &fireLog{}
	d, hub, wb, _ := startVoteStage(t, 1, fl.hook())

	d.onEvent(turnCompletedEvent("s-vote-1", "提议重编译：就我一个，我说了算"))
	waitFor(t, func() bool { return fl.count() == 1 }, "1/1 提议即满票，应立即触发")
	if got := fl.first(); got != "proj-vote|甲|1/1 同意" {
		t.Fatalf("触发参数走样: %q", got)
	}
	if sys := sysRecorded(hub); !strings.Contains(sys, "重编译投票通过（1/1 同意）") {
		t.Fatalf("单人通过行缺计票:\n%s", sys)
	}
	if hasVotePrompt(wb) != 0 {
		t.Fatal("单人房没有别人可注入")
	}
}

// 执行腿未接线（内嵌/测试形态）：票照开照过，通过行之后跟一条说实话
// 的未执行行——不装作执行了。
func TestVotePassWithUnwiredFireLandsHonestLine(t *testing.T) {
	d, hub, _, _ := startVoteStage(t, 2, nil)

	d.onEvent(turnCompletedEvent("s-vote-1", "提议重编译"))
	d.onEvent(turnCompletedEvent("s-vote-2", "同意重编译"))
	sys := sysRecorded(hub)
	if !strings.Contains(sys, "重编译投票通过") {
		t.Fatalf("2/2 该判通过:\n%s", sys)
	}
	if !strings.Contains(sys, "执行通道未接线") {
		t.Fatalf("未接线该说实话:\n%s", sys)
	}
}
