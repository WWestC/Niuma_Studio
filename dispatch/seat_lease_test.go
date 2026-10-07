package dispatch

// seat_lease_test.go — the seat-lease contract (chat/seattoken.go):
// the dispatcher persists each member's seat credential on attach, a
// same-token one-shot (the member's own CLI: plan submit, task
// confirm, say) takes the seat for a moment WITHOUT offboarding the
// member or spawning a "-2" twin, and the dispatcher re-takes it the
// moment the command leaves. A stranger that outstays the patience
// window loses the member the seat — the historical kick/offboard
// semantics, unchanged.

import (
	"os"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// TestMain walls the whole dispatch test binary off the operator's
// real machine state, two layers deep:
//
//   - HOME points at a scratch dir for the entire run — every
//     os.UserHomeDir fallback (the inbox lane files' ~/.niuma/inbox,
//     the branch worktrees' ~/.niuma/wt, zcode's HomePathV2 …) lands
//     there no matter which test forgot to set its dir.
//   - the four task-index seams go no-op — a birth in a test that
//     forgot its per-dispatcher registerIndex seam used to pin phantom
//     members (【proj-p1】小鹿-人事 …) into the real ZCode sidebar, and
//     the app's 花名册 then disagreed with the desktop's 任务详情
//     about who exists where (1459 rows of 实录). Tests wanting the
//     row body still install their own d.registerIndex capture, which
//     takes precedence. The seat-token home override rides the same
//     scratch dir.
//
// Production is untouched — the seams hold the real verbs outside the
// test binary.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "niuma-seat-tokens-*")
	if err != nil {
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	restoreHome := chat.OverrideSeatTokenHome(dir)
	if h, err := os.UserHomeDir(); err == nil {
		_ = os.Setenv("HOME", dir) // UserHomeDir reads $HOME — the scratch wall
		defer func() { _ = os.Setenv("HOME", h) }()
	}
	taskIndexRegister = func(zcode.TaskIndexEntry) error { return nil }
	taskIndexArchive = func(string) error { return nil }
	taskIndexUnread = func(string) error { return nil }
	taskIndexReveal = func(string) {}
	// v2.11 L1 汇聚窗的测试 seam：老套件等点名注入的 waitFor 只有 3
	// 秒死线，2 秒默认窗贴着线跑必偶发超时——整个测试二进制内关窗
	// （即刻发车＝升级前行为），汇聚窗自己的行为面在 coalesce_test
	// 里用显式小窗考。
	DefaultCoalesceWindow = 0
	code := m.Run()
	restoreHome()
	os.Exit(code)
}

func leaseStage(t *testing.T, patience time.Duration) *twinStageOut {
	t.Helper()
	st := twinStage(t, true) // staffed: the offboard path is the assertion surface
	st.d.cfg.ReseatPatience = patience
	return st
}

func leaseWait(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// TestAttachPersistsSeatCredential: attach stamps the credential file
// the CLI reads — the single truth, re-stamped on every attach.
func TestAttachPersistsSeatCredential(t *testing.T) {
	st := leaseStage(t, time.Second)
	if _, err := st.d.Birth("小牛", "排期编排", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	tok := chat.LoadSeatToken(st.d.projectKey, "小牛")
	if tok == "" {
		t.Fatal("attach 未落盘座位凭证（CLI 端读不到，plan submit 必被 -2 顶座）")
	}
	if holder, ok := st.d.hub.SeatHolderToken("小牛"); !ok || holder != tok {
		t.Fatalf("落盘凭证与活座不一致：file=%q live=%q ok=%v", tok, holder, ok)
	}
}

// TestOneShotLeaseReseatsWithoutOffboarding: the member's own CLI
// presents the credential (same-token supersede), runs, leaves — the
// member must survive (staffing row active, sidebar row not folded)
// and the dispatcher must re-take the seat with the SAME credential.
func TestOneShotLeaseReseatsWithoutOffboarding(t *testing.T) {
	st := leaseStage(t, 2*time.Second)
	if _, err := st.d.Birth("小牛", "排期编排", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	tok := chat.LoadSeatToken(st.d.projectKey, "小牛")
	if tok == "" {
		t.Fatal("前置：凭证已落盘")
	}

	// the member's one-shot CLI: dial with the credential (exactly
	// what cli dialWithSeat does), hold the seat briefly, leave.
	lease, _, ok := st.d.hub.JoinFree("小牛", "排期编排", false, tok)
	if !ok || lease == nil {
		t.Fatal("带凭证拨号未能顶回自己的座位")
	}
	if !leaseWait(t, 2*time.Second, func() bool { return st.d.hub.HasLiveMember("小牛") }) {
		t.Fatal("租约期间座位应仍在线（由 CLI 持有）")
	}
	st.d.hub.Leave(lease) // the command's connection closes

	// the dispatcher re-takes the seat, same credential, member alive
	if !leaseWait(t, 3*time.Second, func() bool {
		holder, ok := st.d.hub.SeatHolderToken("小牛")
		return ok && holder == tok
	}) {
		t.Fatal("一次性命令退场后调度器未按原凭证重坐")
	}
	st.d.mu.Lock()
	_, managed := st.d.members["小牛"]
	st.d.mu.Unlock()
	if !managed {
		t.Fatal("自家凭证的短暂顶替把成员误判离岗（watchSeat 旧病）")
	}
	found := false
	for _, r := range st.d.staff.ListByProject("default") {
		if r.Person == "小牛" && r.State == "active" {
			found = true
		}
	}
	if !found {
		t.Fatal("租约后编制行应保持 active")
	}
	_, folds := st.snapshot()
	if len(folds) != 0 {
		t.Fatalf("租约不该折叠侧栏行（成员没走）：%v", folds)
	}
	for _, m := range st.d.hub.Members() {
		if m.Name == "小牛-2" {
			t.Fatal("自家租约产生了 -2 分身座位")
		}
	}
}

// TestWedgedLeaseOutstaysPatience: a same-credential holder that
// outstays the patience window (a wedged one-shot, or our own
// long-lived guard) is superseded BACK — the member survives with
// the seat and the credential intact; the fallback detach only fires
// for a holder we can neither outwait nor prove ours.
func TestWedgedLeaseOutstaysPatience(t *testing.T) {
	st := leaseStage(t, 300*time.Millisecond)
	if _, err := st.d.Birth("小牛", "排期编排", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	tok := chat.LoadSeatToken(st.d.projectKey, "小牛")
	lease, _, ok := st.d.hub.JoinFree("小牛", "排期编排", false, tok)
	if !ok || lease == nil {
		t.Fatal("带凭证拨号未能顶回自己的座位")
	}
	defer st.d.hub.Leave(lease)

	// patience expires with the lease still held: the dispatcher
	// supersedes it back (the guard branch) instead of offboarding.
	if !leaseWait(t, 3*time.Second, func() bool {
		holder, ok := st.d.hub.SeatHolderToken("小牛")
		return ok && holder == tok
	}) {
		t.Fatal("耐心耗尽后未按凭证强行顶回座位")
	}
	select {
	case <-lease.Done():
	default:
		// the lease may or may not notice the close yet; the seat
		// holder swap above is the assertion
	}
	st.d.mu.Lock()
	_, managed := st.d.members["小牛"]
	st.d.mu.Unlock()
	if !managed {
		t.Fatal("长租被顶回后成员不应离调度")
	}
	_, folds := st.snapshot()
	if len(folds) != 0 {
		t.Fatalf("顶回自家租约不该折叠侧栏行：%v", folds)
	}
}

// TestSayThroughSurvivesLeaseWindow: a turn reply mirrored while the
// member's one-shot holds the seat waits the command out and lands —
// the words are never silently dropped on the dead seat.
func TestSayThroughSurvivesLeaseWindow(t *testing.T) {
	st := leaseStage(t, 2*time.Second)
	if _, err := st.d.Birth("小牛", "排期编排", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	tok := chat.LoadSeatToken(st.d.projectKey, "小牛")
	lease, _, ok := st.d.hub.JoinFree("小牛", "排期编排", false, tok)
	if !ok || lease == nil {
		t.Fatal("带凭证拨号未能顶回自己的座位")
	}

	obs := st.d.hub.AttachObserver("", false, 0)
	seen := make(chan string, 4)
	go func() {
		for msg := range obs.Receive() {
			if msg.Type == chat.MsgSay {
				seen <- msg.Text
			}
		}
	}()

	st.d.mu.Lock()
	m, ok := st.d.members["小牛"]
	st.d.mu.Unlock()
	if !ok {
		t.Fatal("成员不在调度中")
	}
	go st.d.sayThrough(m, "拆解完成：r_01 → 三个任务包", nil)

	// the one-shot finishes mid-wait: the mirror must land right after
	time.Sleep(300 * time.Millisecond)
	st.d.hub.Leave(lease)

	select {
	case txt := <-seen:
		if txt != "拆解完成：r_01 → 三个任务包" {
			t.Fatalf("镜像内容走样：%q", txt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("租约窗口内的回合回复被静默丢弃")
	}
}

// TestSayThroughBoundedWhenMemberOffboardedMidMirror: the reply mirror
// waiting out a seat lease must stay bounded when the member is
// offboarded underneath it — the dead seat can never be re-taken
// (reseatNow is a no-op once the roster row is gone), and the bare
// reseatNow/continue loop used to spin with zero sleep AND no deadline
// on that path, pegging a core at 100% CPU for hours (t_139 实锤：读
// 循环 onEvent→turnTerminal→sayThrough 自旋）。The words must still
// land as the fallback system line at patience expiry.
func TestSayThroughBoundedWhenMemberOffboardedMidMirror(t *testing.T) {
	st := leaseStage(t, 2*time.Second)
	if _, err := st.d.Birth("小牛", "排期编排", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	tok := chat.LoadSeatToken(st.d.projectKey, "小牛")
	st.d.mu.Lock()
	m, ok := st.d.members["小牛"]
	st.d.mu.Unlock()
	if !ok {
		t.Fatal("成员不在调度中")
	}

	// the member is offboarded while the mirror waits: the roster row
	// disappears (what seatGone does) BEFORE the seat dies, so watchSeat
	// exits quietly and no path can ever swap m.seat again.
	st.d.mu.Lock()
	delete(st.d.members, "小牛")
	st.d.mu.Unlock()

	// kill the seat the way a one-shot supersede does (same-credential
	// takeover closes done), then drop the lease: name free, seat dead,
	// member gone — the exact wedged state the spin lived on.
	lease, _, ok := st.d.hub.JoinFree("小牛", "排期编排", false, tok)
	if !ok || lease == nil {
		t.Fatal("带凭证拨号未能顶回自己的座位")
	}
	st.d.hub.Leave(lease)

	tap := newSystemTap(st.d.hub)
	done := make(chan struct{})
	go func() { st.d.sayThrough(m, "末班总结：r_33 结单", nil); close(done) }()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("成员离编后 sayThrough 未有界返回（零睡眠自旋复发）")
	}
	// bounded is not enough: the fallback must preserve the words
	if !leaseWait(t, 2*time.Second, func() bool {
		return tap.count("末班总结：r_33 结单") > 0
	}) {
		t.Fatalf("兜底系统行未保住原文，已收集：%v", tap.snapshot())
	}
}

var _ = zcode.TaskIndexEntry{} // keep the import shape stable for stage seams

// TestWorkingTellFlipsWithTurn: the dispatcher's status flips stamp the
// roster member and broadcast member_work frames (reminder-family: no
// history line) — the chat panel's 「正在处理…」truth source.
func TestWorkingTellFlipsWithTurn(t *testing.T) {
	st := leaseStage(t, time.Second)
	obs := st.d.hub.AttachObserver("", false, 0)
	frames := make(chan chat.Message, 8)
	go func() {
		for msg := range obs.Receive() {
			if msg.Type == chat.MsgMemberWork {
				frames <- msg
			}
		}
	}()

	// Birth delivers the onboarding prompt — the first turn is already
	// in flight, so the tell must flip on with it.
	if _, err := st.d.Birth("小牛", "排期编排", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	st.d.mu.Lock()
	var sid string
	if m, ok := st.d.members["小牛"]; ok {
		sid = m.sessionID
	}
	st.d.mu.Unlock()
	if sid == "" {
		t.Fatal("成员不在调度中")
	}

	select {
	case f := <-frames:
		if f.From != "小牛" || !f.Working || f.Since <= 0 {
			t.Fatalf("working 帧内容走样：%+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("出生首轮未广播 member_work")
	}
	if m, _ := st.d.hub.SeatHolderToken("小牛"); m == "" {
		t.Fatal("座位没了")
	}
	for _, mm := range st.d.hub.Members() {
		if mm.Name == "小牛" && !mm.Working {
			t.Fatal("名册成员未标 working")
		}
	}

	// turn done → working off, no history line for either flip
	st.d.onEvent(turnCompletedEvent(sid, "拆解完成"))
	select {
	case f := <-frames:
		if f.From != "小牛" || f.Working {
			t.Fatalf("idle 帧内容走样：%+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("回合结束未广播 member_work(false)")
	}
	for _, mm := range st.d.hub.Members() {
		if mm.Name == "小牛" && mm.Working {
			t.Fatal("回合结束后仍标 working")
		}
	}
}
