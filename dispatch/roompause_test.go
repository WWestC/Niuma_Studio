package dispatch

// 房间暂停的调度器面（Config.PauseProbe）：暂停期间车道不发车、直达
// 投递停进车道（队龄戳保留）；Resume() 重踢全房车道，停着的线按 FIFO
// 续投。在飞的一轮不在测试面里（杀模型调用没有意义，代码走读保证）。

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// startPauseRoom builds the wake-room shape (bridge_wake_test) with a
// flippable pause probe.
func startPauseRoom(t *testing.T) (*chat.Hub, *Dispatcher, *wakeBridge, *atomic.Bool) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	wb := &wakeBridge{}
	var paused atomic.Bool
	d := Start(hub, store, wb, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
		PauseProbe:  func() bool { return paused.Load() },
	})
	t.Cleanup(d.Stop)
	return hub, d, wb, &paused
}

// TestPauseParksSayUntilResume: 暂停期间房主点名成员——线停进车道，
// bridge 一发不发；恢复（探针翻 false＋Resume）后线按原文本送达。
func TestPauseParksSayUntilResume(t *testing.T) {
	hub, d, wb, paused := startPauseRoom(t)
	if err := d.attach("李四", "工程师", "s-pause-a"); err != nil {
		t.Fatal(err)
	}
	paused.Store(true)

	speaker, _ := hub.Join("房主", "", false)
	if _, ok := hub.Say(speaker, "@李四 把发布检查单跑了"); !ok {
		t.Fatal("say 未入史")
	}
	// 汇聚窗（缺省 ~2s）后的 kick 也该被门挡住——等过窗再断言，
	// 不让「还没发车」冒充「发不了车」
	waitFor(t, func() bool { return len(parkedQueue(t, d, "李四")) == 1 }, "暂停期点名应停进车道")
	if wb.count("s-pause-a") != 0 {
		t.Fatalf("暂停期竟有注入: %d", wb.count("s-pause-a"))
	}

	paused.Store(false)
	d.Resume()
	waitFor(t, func() bool { return wb.sent("s-pause-a", "发布检查单") }, "恢复后停着的线应续投")
}

// TestPauseParksDirectDeliver: 直达投递（巡逻/日报/会议那一族）暂停期
// 同样停车道——deliver 不再是绕开车道的后门；恢复后由 Resume 踢出。
func TestPauseParksDirectDeliver(t *testing.T) {
	_, d, wb, paused := startPauseRoom(t)
	if err := d.attach("王五", "产品经理", "s-pause-b"); err != nil {
		t.Fatal(err)
	}
	paused.Store(true)

	d.deliver("王五", "【巡检】检查：全部正常", nil, nil)
	if got := len(parkedQueue(t, d, "王五")); got != 1 {
		t.Fatalf("暂停期直达投递应停车道，队列长 %d", got)
	}
	if wb.count("s-pause-b") != 0 {
		t.Fatalf("暂停期直达投递竟发出: %d", wb.count("s-pause-b"))
	}
	// 内容原样停着（不含排队说明——新鲜线入队才盖戳，重投才需要）
	if !strings.Contains(parkedQueue(t, d, "王五")[0], "巡检") {
		t.Fatalf("停车道的内容丢了: %q", parkedQueue(t, d, "王五")[0])
	}

	paused.Store(false)
	d.Resume()
	waitFor(t, func() bool { return wb.sent("s-pause-b", "巡检") }, "恢复后直达线应续投")
}

// TestPauseWithoutProbeIsByteIdentical: 无探针＝从不暂停（nil 门恒开，
// v1 行为分毫不动）。
func TestPauseWithoutProbeIsByteIdentical(t *testing.T) {
	hub := chat.NewHub()
	store, _ := agents.Open("")
	wb := &wakeBridge{}
	d := Start(hub, store, wb, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if d.pausedNow() {
		t.Fatal("无探针的调度器不该有任何暂停态")
	}
	if err := d.attach("赵六", "工程师", "s-pause-c"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := hub.Join("房主", "", false)
	hub.Say(speaker, "@赵六 在吗")
	waitFor(t, func() bool { return wb.count("s-pause-c") == 1 }, "无探针时点名照常投递")
}

// parkedQueue reads one member's parked lane under the dispatcher lock.
func parkedQueue(t *testing.T, d *Dispatcher, name string) []string {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.members[name]
	if m == nil {
		return nil
	}
	out := make([]string, len(m.lanes.queue))
	for i, e := range m.lanes.queue {
		out[i] = e.text
	}
	return out
}

// TestResumeReadoptsMissingSeats（恢复即找回）：boot 时 attach 失败（会
// 话暂不可用）的编制成员不在调度器名下——暂停房没有看护的自愈腿（整房
// 跳过），恢复（ReadoptMissing，经 Fleet.ResumeRoom）就是它的补位时刻：
// 编制在册、会话绑定的缺席者重新入座；已在调度中的成员原样不动。
func TestResumeReadoptsMissingSeats(t *testing.T) {
	hub := chat.NewHub()
	agentStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staff, err := staffing.Open(filepath.Join(t.TempDir(), "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staff.Join(chat.LobbyKey, "甲", "工程师", "s-rm-a"); err != nil {
		t.Fatal(err)
	}
	wb := &wakeBridge{}
	var paused atomic.Bool
	d := Start(hub, agentStore, wb, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
		StaffStore:  staff,
		PauseProbe:  func() bool { return paused.Load() },
	})
	t.Cleanup(d.Stop)

	if d.lookup("甲") == nil {
		t.Fatal("boot adopt 应把编制成员甲归位")
	}
	// 乙的行 boot 后才落（等价于 boot 时 attach 失败——都缺席于调度器）
	if _, err := staff.Join(chat.LobbyKey, "乙", "设计师", "s-rm-b"); err != nil {
		t.Fatal(err)
	}
	paused.Store(true)
	d.ReadoptMissing()

	waitFor(t, func() bool { return d.lookup("乙") != nil }, "恢复补座应把编制在册的乙拉回来")
	online := map[string]bool{}
	for _, m := range hub.Members() {
		online[m.Name] = true
	}
	if !online["乙"] {
		t.Fatal("补座后房册里没有乙——座位没有真正回来")
	}
}
