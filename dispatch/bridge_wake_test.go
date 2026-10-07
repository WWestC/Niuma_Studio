package dispatch

// 成员间 bridge 回显的单名点名（manual §bridge 回显「被 @ 到其中的内容
// 照常响应」/§引用回复「单名点名不受回显豁免」）：成员的回合回复以
// origin=bridge 贴回房间时，其中字面的 @对方 唤醒对方——成员间转交走
// 的就是这条路；召唤族（@所有人/@全员/@岗位组）由 hub 的 summons 门
// 挡在 upstream，回显不点名全房；无点名的回显保持 v1.0 的安静——不
// 唤醒任何人、也不进背景车道（回显是交付物，不是新语境）；自己 @ 自己
// （引用自己的旧话）不把自己再注入一遍。

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// wakeBridge 记录逐会话注入（session, content），只读面带锁——观察者
// 协程写、测试 waitFor 读。
type wakeBridge struct {
	baseBridge
	mu    sync.Mutex
	sends []struct{ session, content string }
}

func (f *wakeBridge) sent(session, substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sends {
		if s.session == session && strings.Contains(s.content, substr) {
			return true
		}
	}
	return false
}
func (f *wakeBridge) count(session string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sends {
		if s.session == session {
			n++
		}
	}
	return n
}

func (f *wakeBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	f.sends = append(f.sends, struct{ session, content string }{sessionID, content})
	f.mu.Unlock()
	return nil, nil
}

func startWakeRoom(t *testing.T) (*chat.Hub, *Dispatcher, *wakeBridge) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	wb := &wakeBridge{}
	d := Start(hub, store, wb, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	return hub, d, wb
}

// seenBridgeEcho registers a test observer AFTER the dispatcher's own
// (Start ran first) — the pump runs observers in registration order,
// so when this one fires for a bridged line the dispatcher's onSay has
// already fully processed it (deliver is synchronous inside onSay).
// Absence asserts after this point cannot race the routing.
func seenBridgeEcho(t *testing.T, hub *chat.Hub) <-chan string {
	t.Helper()
	seen := make(chan string, 8)
	hub.OnSay(func(m chat.Message) {
		if m.Origin == chat.OriginBridge {
			seen <- m.From
		}
	})
	return seen
}

func sawFrom(seen <-chan string, who string) bool {
	select {
	case f := <-seen:
		return f == who
	default:
		return false
	}
}

func TestBridgeEchoMentionWakesPeer(t *testing.T) {
	hub, d, wb := startWakeRoom(t)
	if err := d.attach("张三", "工程师", "s-bw-a"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("李四", "工程师", "s-bw-b"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := hub.Join("房主", "", false)
	if _, ok := hub.Say(speaker, "@李四 去把发布检查单跑了"); !ok {
		t.Fatal("say 未入史")
	}
	waitFor(t, func() bool { return wb.count("s-bw-b") == 1 }, "点名李四应注入")

	// 李四的回合回复点名张三：镜像回房（origin=bridge）后张三被唤醒
	d.onEvent(turnCompletedEvent("s-bw-b", "@张三 检查单跑完了，接口文档交接给你"))
	waitFor(t, func() bool {
		return wb.sent("s-bw-a", "【办公室消息｜来自 李四】") &&
			wb.sent("s-bw-a", "接口文档交接给你")
	}, "回显里的 @张三 应唤醒张三")
}

func TestBridgeEchoWithoutMentionStaysSilent(t *testing.T) {
	hub, d, wb := startWakeRoom(t)
	seen := seenBridgeEcho(t, hub)
	if err := d.attach("张三", "工程师", "s-bw-c"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("李四", "工程师", "s-bw-d"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := hub.Join("房主", "", false)
	if _, ok := hub.Say(speaker, "@李四 汇报一下进度"); !ok {
		t.Fatal("say 未入史")
	}
	waitFor(t, func() bool { return wb.count("s-bw-d") == 1 }, "点名李四应注入")

	// 无点名的回显：谁也不唤醒（张三零注入），也不进背景车道
	d.onEvent(turnCompletedEvent("s-bw-d", "检查单已归档，一切正常"))
	waitFor(t, func() bool { return sawFrom(seen, "李四") }, "回显应已过调度器")
	for _, s := range d.Snapshot() {
		if s.Inject != 0 {
			t.Fatalf("%s 的背景车道混入了回显：%d", s.Name, s.Inject)
		}
		if s.Name == "张三" && (s.Queued != 0 || wb.count("s-bw-c") != 0) {
			t.Fatalf("无点名回显唤醒了张三：%+v", s)
		}
	}
}

func TestBridgeEchoSelfMentionNoSelfWake(t *testing.T) {
	hub, d, wb := startWakeRoom(t)
	seen := seenBridgeEcho(t, hub)
	if err := d.attach("李四", "工程师", "s-bw-e"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := hub.Join("房主", "", false)
	if _, ok := hub.Say(speaker, "@李四 把上次的结果贴出来"); !ok {
		t.Fatal("say 未入史")
	}
	waitFor(t, func() bool { return wb.count("s-bw-e") == 1 }, "点名李四应注入")

	// 李四引用自己的旧话（正文里字面 @李四）：不得把自己再注入一遍
	d.onEvent(turnCompletedEvent("s-bw-e", "引用 @李四：上次的安排\n\n结果：全部通过"))
	waitFor(t, func() bool { return sawFrom(seen, "李四") }, "回显应已过调度器")
	if n := wb.count("s-bw-e"); n != 1 {
		t.Fatalf("自引用回显唤醒了自己：注入 %d 次", n)
	}
}

func TestBridgeEchoAllMentionDoesNotSummon(t *testing.T) {
	hub, d, wb := startWakeRoom(t)
	seen := seenBridgeEcho(t, hub)
	if err := d.attach("张三", "工程师", "s-bw-f"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("李四", "工程师", "s-bw-g"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := hub.Join("房主", "", false)
	if _, ok := hub.Say(speaker, "@李四 收尾"); !ok {
		t.Fatal("say 未入史")
	}
	waitFor(t, func() bool { return wb.count("s-bw-g") == 1 }, "点名李四应注入")

	// 回显里带 @所有人：hub 的 summons 门不做全员展开，谁也不被点名
	d.onEvent(turnCompletedEvent("s-bw-g", "@所有人 本期收工，辛苦了"))
	waitFor(t, func() bool { return sawFrom(seen, "李四") }, "回显应已过调度器")
	if n := wb.count("s-bw-f"); n != 0 {
		t.Fatalf("回显里的 @所有人 点名了张三：%d 次", n)
	}
	for _, s := range d.Snapshot() {
		if s.Inject != 0 {
			t.Fatalf("%s 的背景车道混入了回显：%d", s.Name, s.Inject)
		}
	}
}
