package dispatch

// 看门狗（忙成员保活＋静默死亡看护）的行为面：
//   - 健康探针只做保活——不打扰房间、不拉会话、不给闲人探；
//   - 冷会话（-32004）在跑成员就地复活：resume+subscribe 梯子、房间
//     落一条 [看护] 说明、成员收到继续干活的中断接续注入、状态回到
//     Running（新一轮已启动）；
//   - 超过 TurnStall 的长回合只播一次「长回合」提示——报忧不擅动，
//     且同一回合不重复唠叨。
//
// 探针走 WatchdogTick 假钟（the patrol/daily contract），断言用
// waitFor 轮询（探针在 watchdog 自己的 goroutine 上跑）。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// watchBridge 在 ackBridge 之上把探针做成可编程的（nil = 健康触摸），
// 并给注入/复活记上加锁的账——看门狗在别的 goroutine 上写。
type watchBridge struct {
	ackBridge
	mu      sync.Mutex
	probes  int
	probeFn func(sid string) error
	resumes int
	sends   []string
}

func (f *watchBridge) ProbeSession(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes++
	if f.probeFn == nil {
		return nil
	}
	return f.probeFn(sessionID)
}

func (f *watchBridge) ResumeSession(ctx context.Context, sessionID string) (*zcode.ResumeResult, error) {
	f.mu.Lock()
	f.resumes++
	f.mu.Unlock()
	return nil, nil
}

func (f *watchBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	f.sends = append(f.sends, content)
	f.mu.Unlock()
	return &zcode.SendAck{}, nil
}

func (f *watchBridge) probeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probes
}

func (f *watchBridge) resumeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumes
}

func (f *watchBridge) sentText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.sends, "\n")
}

// systemTap joins an observer seat and collects every system line the
// room broadcasts (System rides the seat channel, not OnSay).
type systemTap struct {
	mu    sync.Mutex
	lines []string
}

func newSystemTap(hub *chat.Hub) *systemTap {
	tap := &systemTap{}
	seat, _ := hub.Join("看护观察员", "测试", false)
	go func() {
		for msg := range seat.Receive() {
			if msg.Type != chat.MsgSystem {
				continue
			}
			tap.mu.Lock()
			tap.lines = append(tap.lines, msg.Text)
			tap.mu.Unlock()
		}
	}()
	return tap
}

func (t *systemTap) count(substr string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, l := range t.lines {
		if strings.Contains(l, substr) {
			n++
		}
	}
	return n
}

// snapshot copies the collected lines under the lock — failure messages
// format it on every waitFor poll while the receiver goroutine appends.
func (t *systemTap) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.lines...)
}

// startWatchdogStage wires one dispatcher over the programmable probe
// bridge, attaches one member and marks them mid-turn.
func startWatchdogStage(t *testing.T, probeFn func(sid string) error) (*Dispatcher, *chat.Hub, *watchBridge, chan time.Time, *systemTap) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	wb := &watchBridge{probeFn: probeFn}
	tick := make(chan time.Time)
	d := Start(hub, store, wb, Config{
		Workspace:    t.TempDir(),
		InboxDir:     t.TempDir(),
		PatrolEvery:  -1,
		DailyAt:      "off",
		WatchdogTick: tick,
		TurnStall:    time.Hour, // 年轻回合不该触发提示（提示另有专测）
	})
	t.Cleanup(d.Stop)
	if err := d.attach("小鹿", "美术设计师", "s-wd-1"); err != nil {
		t.Fatal(err)
	}
	return d, hub, wb, tick, newSystemTap(hub)
}

// TestWatchdogHealthyProbeIsSilentKeepalive: 探针健康时只是保活——
// 房间无系统行、成员无注入、无复活、闲人根本不被探。
func TestWatchdogHealthyProbeIsSilentKeepalive(t *testing.T) {
	d, _, wb, tick, tap := startWatchdogStage(t, nil)
	busy := d.lookup("小鹿")
	d.setStatus(busy, StatusRunning)
	idleName := "小猿"
	if err := d.attach(idleName, "前端开发", "s-wd-2"); err != nil {
		t.Fatal(err)
	}

	tick <- time.Now()
	waitFor(t, func() bool { return wb.probeCount() >= 1 }, "探针没有打到忙成员")

	if wb.probeCount() != 1 {
		t.Fatalf("闲人也被探了：探针 %d 次（应只打忙成员 1 次）", wb.probeCount())
	}
	if wb.resumeCount() != 0 || wb.sentText() != "" {
		t.Fatalf("健康探针竟有副作用：resumes=%d sends=%q", wb.resumeCount(), wb.sentText())
	}
	if n := tap.count(""); n != 0 {
		t.Fatalf("健康保活打扰了房间（%d 条系统行）", n)
	}
}

// TestWatchdogResurrectsColdSession: 在跑成员的会话被回收（-32004）→
// 就地复活：resume 梯子、房间 [看护] 说明、中断接续注入、状态回 Running。
func TestWatchdogResurrectsColdSession(t *testing.T) {
	cold := func(string) error {
		return &zcode.Error{Code: zcode.ErrSessionNotActive, Message: "Session is not active"}
	}
	d, _, wb, tick, tap := startWatchdogStage(t, cold)
	busy := d.lookup("小鹿")
	d.setStatus(busy, StatusRunning)

	tick <- time.Now()
	// t_138 竞态修（r_33 ③）：复活链全异步（probe→resurrect goroutine→
	// reactivate→hub.System→deliver→setStatus），而 systemTap 经
	// seat.Receive() 的 goroutine 消化又有自己的一拍——三个断言点必须
	// 各自等终态，不能在第一个 waitFor 过后同步读：
	//   ①桥面（resume+注入） ②tap 面（房里的系统行——通道消化滞后于
	//   桥计数） ③status 面（deliver 的 setStatus 在 resume 之后）。
	// 每面 waitFor 3s 不放宽——等信号不等墙钟的墙钟还是原来的墙钟。
	waitFor(t, func() bool {
		return wb.resumeCount() >= 1 && strings.Contains(wb.sentText(), "【看护】")
	}, "冷会话未被复活（无 resume 或无接续注入）")
	waitFor(t, func() bool { return tap.count("[看护] 小鹿") == 1 },
		fmt.Sprintf("房间未见冷会话说明：lines=%v", tap.snapshot()))
	var status string
	waitFor(t, func() bool {
		d.mu.Lock()
		status = d.members["小鹿"].status
		d.mu.Unlock()
		return status == StatusRunning
	}, "复活后的成员应回到 Running（新一轮已由注入启动）")
}

// TestWatchdogStallNoteFiresOnce: 超过 TurnStall 的长回合播一次提示，
// 第二拍不重复；报忧不擅动（无注入、无复活）。
func TestWatchdogStallNoteFiresOnce(t *testing.T) {
	d, _, wb, tick, tap := startWatchdogStage(t, nil)
	busy := d.lookup("小鹿")
	d.setStatus(busy, StatusRunning)
	// 把回合起点拨回两分钟前——TurnStall 用测试档 1 分钟。
	d.mu.Lock()
	d.cfg.TurnStall = time.Minute
	busy.watch.runningSince = time.Now().Add(-2 * time.Minute)
	d.mu.Unlock()

	tick <- time.Now()
	waitFor(t, func() bool { return tap.count("长回合") >= 1 }, "长回合提示未落地")
	if wb.sentText() != "" || wb.resumeCount() != 0 {
		t.Fatalf("提示分支竟有动作：sends=%q resumes=%d", wb.sentText(), wb.resumeCount())
	}

	tick <- time.Now()
	waitFor(t, func() bool { return wb.probeCount() >= 2 }, "第二拍探针未到")
	time.Sleep(50 * time.Millisecond)
	if n := tap.count("长回合"); n != 1 {
		t.Fatalf("同一回合的长回合提示应只播一次，实际 %d 次", n)
	}
}
