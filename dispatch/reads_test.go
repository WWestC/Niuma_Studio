package dispatch

// 已读回执的调度器半边（飞书式已读，chat/reads.go 拥有语义）：已读
// 打在回合终点——deliver 的 bridge.Send ack 只是送达（输入进了会话，
// 回合还没读它），onEvent 的终点冲账才是「读」；忙时排队、跨重启车道
// 的货运由排队投影（chat/queues.go）向房主亮「排队中」。迟到
// （>StaleDelay）送达的注入带 排队说明 头（原发时刻＋等待时长），
// 迟到回合的回复自动引用（引用 #N）它所回答的原消息——一条 20 分钟
// 前的「稍等」送达时要带钟，回答时要点名。

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// readBridge 记录每次注入文本、可注入失败模式（busy 排队路径用）。
// 与 sharedbridge_test 的 fakeBridge 分开各用，避免两处特性互相牵动。
// 账本带锁：车道 kick 把 Send 搬上了自己的 goroutine（laneKick），
// waitFor 的读与 kick 的写跨协程（wakeBridge 的既有纪律）。
type readBridge struct {
	baseBridge
	mu    sync.Mutex
	sends []string
	err   error // 非 nil 时每次 Send 都以它失败，直到清空（setErr 写、Send 读，均持锁）
}

func (f *readBridge) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

func (f *readBridge) allSends() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sends...)
}

func (f *readBridge) resetSends() {
	f.mu.Lock()
	f.sends = nil
	f.mu.Unlock()
}

// sendHas：内容特异性等待——sendCount 是全房共享账（小鹿的背景广播
// 冲刷也算数），多成员用例里「任意 send」会让终点打在错误的回合上
// （waitFor×kick 家族的实录根因）。
func (f *readBridge) sendHas(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sends {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func (f *readBridge) setErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func (f *readBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	f.sends = append(f.sends, content)
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return nil, nil
}

// waitFor polls cond on the sayFeed pump's own clock (routing is async)
// and fails the test when it never lands.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// readersOf snapshots one seq's reader set.
func readersOf(h *chat.Hub, seq int64) []string {
	_, rows := h.ReadReceipts()
	for _, row := range rows {
		if row.Seq == seq {
			return row.Readers
		}
	}
	return nil
}

// queuedWho snapshots one seq's queued-member set (排队中投影).
func queuedWho(h *chat.Hub, seq int64) []string {
	for _, row := range h.QueueRows() {
		if row.Seq == seq {
			return row.Who
		}
	}
	return nil
}

// readDispatcher builds a lobby-shaped dispatcher with 张三 attached and
// 李四 as the human speaker (各用例都要发言，fixture 直接带回座位).
func readDispatcherWithSpeaker(t *testing.T) (*chat.Hub, *Dispatcher, *readBridge, *chat.Client) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	rb := &readBridge{}
	d := Start(hub, store, rb, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1, // 巡检时钟静默：别让巡检注入冲刷背景车道搅乱断言
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-read-1"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := hub.Join("李四", "", false)
	// fixture 不替用例发言——背景车道保持空，忙时入队的货运才只含
	// 本用例的点名行（断言可稳定预判）
	return hub, d, rb, speaker
}

// 点名注入 ack 只是送达：小签必须等回合终点（onEvent 冲账）才翻已读。
func TestMentionWakeReadsAtTurnTerminal(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	hub.Say(speaker, "@张三 把这周的报表跑一下")
	seq := hub.LastSeq()
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "点名注入应发生")
	if readers := readersOf(hub, seq); readers != nil {
		t.Fatalf("送达≠已读：注入 ack 后不该立刻记读：%v", readers)
	}
	d.onEvent(turnCompletedEvent("s-read-1", "报表在跑"))
	waitFor(t, func() bool {
		r := readersOf(hub, seq)
		return len(r) == 1 && r[0] == "张三"
	}, "回合终点应冲账张三已读")
}

// 背景车道随点名注入冲刷送达——同样终点才读。
func TestBackgroundFlushReadsAtTurnTerminal(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	hub.Say(speaker, "随手同步：客户那边推了一天") // 未点名 → 背景车道
	ambient := hub.LastSeq()
	hub.Say(speaker, "@张三 顺便看下这条")
	wake := hub.LastSeq()

	waitFor(t, func() bool { return rb.sendCount() > 0 }, "点名注入应发生")
	if readersOf(hub, ambient) != nil || readersOf(hub, wake) != nil {
		t.Fatal("冲刷送达≠已读：终点之前不该记读")
	}
	d.onEvent(turnCompletedEvent("s-read-1", "看到了"))
	waitFor(t, func() bool {
		return len(readersOf(hub, ambient)) == 1 && len(readersOf(hub, wake)) == 1
	}, "回合终点应把冲刷的背景行一起记读")

	m := d.lookup("张三")
	d.mu.Lock()
	inj := len(m.lanes.inject)
	d.mu.Unlock()
	if inj != 0 {
		t.Fatalf("冲刷后背景车道应为空，还剩 %d 条", inj)
	}
}

// 忙时入队：排队投影亮「排队中」、不记读；出队送达后投影清、终点读。
func TestBusyQueueQueuedThenReadsAtTerminal(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	// 汇聚窗须在入队前关掉：点名路由是「先停车再 kick」（replies.go），
	// laneKick 给新鲜队头武装 DefaultCoalesceWindow（2s）的 kickTimer
	// ——本用例在 setErr(nil) 后手动 takeNext 取货，与那枚定时器的发
	// 车赛跑；-race 减速把用例体撑过窗长时定时器抢先消费队条，手动
	// 取货拿到空队列（reads_test 的既有偶红，包级 -race 下约 1/5 复
	// 现）。关窗后首踢立即发车、忙碌弹回停车道且不再排下一踢——等
	// 待条件见到队条时泵已收工，手动取货无竞争对手。同脚手架静音
	// PatrolEvery 的干扰钟纪律，此处静音的是另一口钟。
	d.mu.Lock()
	d.cfg.CoalesceWindow = -1
	d.mu.Unlock()
	rb.setErr(&zcode.Error{Code: zcode.ErrPromptRunning, Message: "A prompt is already running"})
	hub.Say(speaker, "@张三 忙完看这个")
	seq := hub.LastSeq()
	m := d.lookup("张三")
	waitFor(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(m.lanes.queue) == 1
	}, "busy 注入应入队")
	if readers := readersOf(hub, seq); readers != nil {
		t.Fatalf("排队中的消息不该记已读: %v", readers)
	}
	waitFor(t, func() bool {
		return len(queuedWho(hub, seq)) == 1 && queuedWho(hub, seq)[0] == "张三"
	}, "入队应亮排队中投影")

	rb.setErr(nil)
	next, seqs, acks, at, imgs := d.takeNext(m)
	if next == "" || len(seqs) != 1 || seqs[0] != seq {
		t.Fatalf("出队的货运不对: %q %v", next, seqs)
	}
	if at == 0 {
		t.Fatal("出队应带车道时间戳（迟到判定的真源）")
	}
	d.deliverQuoted("张三", next, seqs, acks, at, imgs)
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "出队后应送达")
	waitFor(t, func() bool { return queuedWho(hub, seq) == nil }, "送达后排队投影应清")
	if readersOf(hub, seq) != nil {
		t.Fatal("送达≠已读：终点之前不该记读")
	}
	d.onEvent(turnCompletedEvent("s-read-1", "忙完了，这就看"))
	waitFor(t, func() bool {
		r := readersOf(hub, seq)
		return len(r) == 1 && r[0] == "张三"
	}, "排队消息送达后的回合终点应记读")
}

// 迟到送达的注入带 排队说明 头：原发时刻＋等待时长＋「按当前语境判断」。
func TestStaleDeliveryStampsHeader(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	hub.Say(speaker, "等一下别发了@小鹿 稍等")
	seq := hub.LastSeq()
	old := time.Now().Unix() - 1200 // 20 分钟前入队

	d.deliverQuoted("张三", "【办公室消息｜来自 李四】等一下别发了@小鹿 稍等", []int64{seq}, []int64{seq}, old, nil)
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "注入应发生")
	if !strings.Contains(rb.allSends()[0], "排队说明") || !strings.Contains(rb.allSends()[0], "20 分钟") {
		t.Fatalf("迟到注入应带排队说明头（含等待时长）：\n%s", rb.allSends()[0])
	}

	// 新鲜送达（零戳＝从未排队）不带头
	rb.resetSends()
	d.deliverQuoted("张三", "【办公室消息｜来自 李四】新指令", nil, nil, 0, nil)
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "注入应发生")
	if strings.Contains(rb.allSends()[0], "排队说明") {
		t.Fatalf("新鲜注入不该带排队说明头：\n%s", rb.allSends()[0])
	}
}

// 迟到回合的回复自动引用它回答的原消息（结构化 Quote 字段——机械语境
// 头 At=false，不给被答的人挂点名）；新鲜回复不引。正文保持干净：
// 引用不再以文本前缀长在回答里（根治版）。
func TestStaleReplyAutoQuotes(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	// 原行故意不带 @（不走点名路由——路由器会再投一次、把迟到戳洗成
	// 新鲜）：这里手工以旧戳投递，模拟「排队 20 分钟后才送达」的迟到货
	hub.Say(speaker, "等一下别发了，稍等")
	seq := hub.LastSeq()

	d.deliverQuoted("张三", "【办公室消息｜来自 李四】等一下别发了，稍等", []int64{seq}, []int64{seq}, time.Now().Unix()-1200, nil)
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "注入应发生")
	d.onEvent(turnCompletedEvent("s-read-1", "收到，我在此暂停。"))
	waitFor(t, func() bool {
		for _, m := range hub.History() {
			if m.From == "张三" && m.Quote != nil {
				return true
			}
		}
		return false
	}, "迟到回合的回复应自动带结构化引用")
	for _, m := range hub.History() {
		if m.From != "张三" || m.Quote == nil {
			continue
		}
		// 引用字段照实（Seq/名字/摘要），At=false——机械引用不是点名：
		// 被答的人（房主李四）不进 mentions，房主不因一条迟到回答挂
		// 「有人@我」；正文保持干净——不带 @李四、不带旧式文本前缀
		if q := m.Quote; q.From != "李四" || q.At || q.Seq != seq || !strings.Contains(q.Snip, "稍等") {
			t.Fatalf("迟到引用字段不符：%+v", m)
		}
		if strings.Contains(m.Text, "@李四") || strings.Contains(m.Text, "「引用") || len(m.Mentions) != 0 {
			t.Fatalf("迟到引用不得点名被答的人、正文应干净：%+v", m)
		}
	}

	// 新鲜点名（零戳）的回复不引用——秒回的对话语境自明，引用是噪音
	rb.resetSends()
	hub.Say(speaker, "@张三 再说一次")
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "点名注入应发生")
	d.onEvent(turnCompletedEvent("s-read-1", "好的，这就重说。"))
	waitFor(t, func() bool {
		for _, m := range hub.History() {
			if m.From == "张三" && strings.Contains(m.Text, "这就重说") {
				return m.Quote == nil && !strings.HasPrefix(m.Text, "「引用 #")
			}
		}
		return false
	}, "新鲜回复不该带引用")
}

// 回答 AI 同事必须送到手上（v2.10 @投递）：回应恰好一位在编同事的点名、回复没带
// 任何 @ 时，调度器自动补 @——及时回复补在正文前；迟到回复写进引用头的名字（引用
// 即点名）。被答的是房主则不补（房主看得到全房，@ 只是红标噪音）；成员自己写了 @
// 就绝不重复补。
func TestAnswerDeliversAtToPeer(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	if err := d.attach("小鹿", "设计师", "s-read-2"); err != nil {
		t.Fatal(err)
	}
	deer := d.lookup("小鹿")

	sayOf := func(from, mark string) (chat.Message, bool) {
		for _, m := range hub.History() {
			if m.From == from && strings.Contains(m.Text, mark) {
				return m, true
			}
		}
		return chat.Message{}, false
	}

	// 及时：小鹿 @张三 提问，张三回答不带 @ → 正文前自动补 @小鹿。
	// 等待一律内容特异性（sendHas）：sendCount 是全房共享账，小鹿的背
	// 景广播冲刷也算数——「任意 send」会让终点打在错误的回合上（本家
	// 族偶发竞态的实录根因）。
	hub.Say(deer.seat, "@张三 这个图标用哪个尺寸？")
	waitFor(t, func() bool { return rb.sendHas("这个图标用哪个尺寸") }, "点名注入应发生")
	d.onEvent(turnCompletedEvent("s-read-1", "用 256 的，细节多。"))
	waitFor(t, func() bool {
		_, ok := sayOf("张三", "256")
		return ok
	}, "回答应贴回房间")
	if m, _ := sayOf("张三", "256"); !strings.HasPrefix(m.Text, "@小鹿") || len(m.Mentions) != 1 || m.Mentions[0] != "小鹿" {
		t.Fatalf("回答 AI 同事应自动补 @投递：%+v", m)
	}

	// 迟到：模拟排队 20 分钟才送达（原行不走点名路由，手工旧戳投递）——引用
	// 字段 At=true（引用即点名通道，正文不带 @），名字/Seq/摘要照实
	hub.Say(deer.seat, "顺带问下深色模式怎么办")
	seq := hub.LastSeq()
	rb.resetSends()
	d.deliverQuoted("张三", "【办公室消息｜来自 小鹿】顺带问下深色模式怎么办", []int64{seq}, []int64{seq}, time.Now().Unix()-1200, nil)
	waitFor(t, func() bool { return rb.sendHas("顺带问下深色模式") }, "迟到注入应发生")
	d.onEvent(turnCompletedEvent("s-read-1", "深色模式跟主色一起出。"))
	waitFor(t, func() bool {
		_, ok := sayOf("张三", "深色模式跟主色")
		return ok
	}, "迟到回答应贴回房间")
	if m, _ := sayOf("张三", "深色模式跟主色"); m.Quote == nil || m.Quote.From != "小鹿" || !m.Quote.At || m.Quote.Seq != seq ||
		strings.Contains(m.Text, "@小鹿") || strings.Contains(m.Text, "「引用") ||
		len(m.Mentions) != 1 || m.Mentions[0] != "小鹿" {
		t.Fatalf("迟到回答 AI 同事：引用字段应为 At 点名小鹿（引用即点名投递）、正文干净：%+v", m)
	}

	// 成员自己带了 @：绝不重复补
	rb.resetSends()
	hub.Say(deer.seat, "@张三 还有一问")
	waitFor(t, func() bool { return rb.sendHas("还有一问") }, "点名注入应发生")
	d.onEvent(turnCompletedEvent("s-read-1", "@小鹿 顺带说下，主色我也一起改了"))
	waitFor(t, func() bool {
		_, ok := sayOf("张三", "主色我也一起改了")
		return ok
	}, "回复应贴回房间")
	if m, _ := sayOf("张三", "主色我也一起改了"); strings.Count(m.Text, "@小鹿") != 1 || len(m.Mentions) != 1 {
		t.Fatalf("成员自己点了名就不得重复补 @：%+v", m)
	}

	// 被答的是房主：不补（v2.9 裁定照旧——房主看得到全房）
	rb.resetSends()
	hub.Say(speaker, "@张三 报表呢")
	waitFor(t, func() bool { return rb.sendHas("报表呢") }, "点名注入应发生")
	d.onEvent(turnCompletedEvent("s-read-1", "报表在这。"))
	waitFor(t, func() bool {
		_, ok := sayOf("张三", "报表在这")
		return ok
	}, "对房主的回答应贴回房间")
	if m, _ := sayOf("张三", "报表在这"); strings.Contains(m.Text, "@李四") || len(m.Mentions) != 0 {
		t.Fatalf("回答房主不该自动补 @：%+v", m)
	}
}

// 车道时间戳的持久化往返＋旧文件兼容：queue_at 随车道落盘；升级前的
// 旧文件（无 queue_at）读入补零＝新鲜（旧行为照旧，不打迟到戳）。
func TestQueueAtLaneRoundtripAndLegacy(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	rb.setErr(&zcode.Error{Code: zcode.ErrPromptRunning, Message: "busy"})
	hub.Say(speaker, "@张三 排队等我")
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "busy 注入应入队")

	m := d.lookup("张三")
	d.FlushInbox() // 落盘已异步：读盘/改盘前排干（旧文件覆盖段也在其后）
	d.mu.Lock()
	path, err := d.inboxPath("张三")
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("车道文件应存在：%v", err)
	}
	var file map[string]json.RawMessage
	if json.Unmarshal(b, &file) != nil {
		t.Fatal("车道文件应可解析")
	}
	var lanes struct {
		Queue   []string `json:"queue"`
		QueueAt []int64  `json:"queue_at"`
	}
	if err := json.Unmarshal(file["default"], &lanes); err != nil {
		t.Fatal(err)
	}
	if len(lanes.Queue) != 1 || len(lanes.QueueAt) != 1 || lanes.QueueAt[0] == 0 {
		t.Fatalf("queue_at 应随车道落盘：%v %v", lanes.Queue, lanes.QueueAt)
	}

	// 旧文件（升级前）：只有 queue，没有 queue_at——读入补零＝新鲜
	legacy := []byte(`{"default":{"queue":["【办公室消息】旧时代的排队行"]}}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	d.loadLanes(m)
	d.mu.Lock()
	first := m.lanes.queue[0]
	d.mu.Unlock()
	if len(m.lanes.queue) != 1 || first.text != "【办公室消息】旧时代的排队行" || first.at != 0 {
		t.Fatalf("旧文件读入应补零时间戳（laneEntry 化后对齐是类型保证）：%+v", first)
	}
}

// 撞车直投不覆盖活回合的货（v2.14）：点名回合在跑时巡报直投（不点名、
// 空货）撞进来——旧代码无条件武装，把活回合还没结账的货单槽洗成 nil：
// 回复照进房、已读永远丢。房主实录「回话了还挂未读」的全案根因——未读
// 的恰是当时正忙的成员（只有忙的人会被巡报/日报/加编通知这类不带车道
// 守卫的直投撞上；book/default 两房台账核账：每批同秒点名里没记账的
// 都是排队最久的那位）。
func TestBargeInKeepsLiveTurnReads(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	hub.Say(speaker, "@张三 把这周的报表跑一下")
	seq := hub.LastSeq()
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "点名注入应发生")
	// 点名回合仍在跑：巡报直投撞进来（Send 照旧 ack——app-server 把
	// 输入排队等活回合，ack 迟归不必在本例模拟）
	d.deliver("张三", "【全智能·产能巡报】例行巡检，无需回应", nil, nil)
	waitFor(t, func() bool { return rb.sendHas("产能巡报") }, "巡报直投应发生")
	if readers := readersOf(hub, seq); readers != nil {
		t.Fatalf("送达≠已读：活回合终局前不该记读：%v", readers)
	}
	d.onEvent(turnCompletedEvent("s-read-1", "报表在跑"))
	waitFor(t, func() bool {
		r := readersOf(hub, seq)
		return len(r) == 1 && r[0] == "张三"
	}, "活回合终局应冲账已读——撞车的空货直投不得洗掉货单槽")
}

// 撞车直投带房线货：活回合终局冲账活货并晋升 pending 货——撞车的输入
// 在 app-server 排队，按 FIFO 变成下一回合，它的货等下一回合终局照常
// 记账；两拍各记各的，不提前、不互吞。
func TestBargeInCargoPromotesAtLiveTerminal(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	hub.Say(speaker, "@张三 先看这条")
	seq1 := hub.LastSeq()
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "第一次注入应发生")
	// 造一枚真实 say seq（无点名不惊动车道），随撞车直投带出去
	hub.Say(speaker, "背景同步：数据源切好了")
	seq2 := hub.LastSeq()
	d.deliver("张三", "【办公室消息｜来自 李四】再看这条", []int64{seq2}, []int64{seq2})
	waitFor(t, func() bool { return rb.sendHas("再看这条") }, "撞车直投应发生")
	d.onEvent(turnCompletedEvent("s-read-1", "先答第一条")) // 活回合终局
	waitFor(t, func() bool {
		r := readersOf(hub, seq1)
		return len(r) == 1 && r[0] == "张三"
	}, "活回合终局应冲账活货")
	if r := readersOf(hub, seq2); r != nil {
		t.Fatalf("pending 货要等自己的回合终局，不该提前记读：%v", r)
	}
	d.onEvent(turnCompletedEvent("s-read-1", "再答补投的那条")) // 晋升货的回合终局
	waitFor(t, func() bool {
		r := readersOf(hub, seq2)
		return len(r) == 1 && r[0] == "张三"
	}, "晋升货应在其回合终局照常冲账")
}

// 超时弃单不清货（v2.14）：ack 未归＝结果未知，输入已写进 child 的
// stdin——货单留给消费它的回合，终局照常冲账。旧代码把货洗成 nil，
// 是「回话了还挂未读」的第二案犯（巡报撞车＋超时清货双杀，小马实录：
// 「请继续」17 分钟后送达、回合正常回话、台账永远未读）。
func TestDeliverTimeoutKeepsCargoForItsTurn(t *testing.T) {
	d, m := newDeliverFixture(t, context.DeadlineExceeded)
	speaker, _ := d.hub.Join("小李", "", false)
	d.hub.Say(speaker, "随手同步：无需回应") // 无点名：不惊动车道，只造一枚真实 say seq
	seq := d.hub.LastSeq()
	d.deliver("小H", "【办公室消息｜来自 小李】看看这条", []int64{seq}, []int64{seq})
	d.mu.Lock()
	got := append([]int64(nil), m.cargo.readSeqs...)
	d.mu.Unlock()
	if len(got) != 1 || got[0] != seq {
		t.Fatalf("超时（结果未知）不清货：货单应留给消费它的回合，实为 %v", got)
	}
	d.onEvent(turnCompletedEvent("s_1", "看到了"))
	waitFor(t, func() bool {
		r := readersOf(d.hub, seq)
		return len(r) == 1 && r[0] == "小H"
	}, "超时输入的回合终局应照常冲账已读")
}

// 确认行协议的终局尾巴（v2.14）：「已读要点」的回合也是回合终点——
// 旧代码提前 return，座位钉死在 Running（车道 kick 的守卫永远等不到
// 空闲，排队线滞留）、货单没人结。确认行吞镜不吞账。
func TestResumeAckLineStillSettlesTerminal(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	hub.Say(speaker, "@张三 接续要点已备好")
	seq := hub.LastSeq()
	waitFor(t, func() bool { return rb.sendCount() > 0 }, "点名注入应发生")
	d.onEvent(turnCompletedEvent("s-read-1", "已读要点，从第一条继续"))
	waitFor(t, func() bool {
		r := readersOf(hub, seq)
		return len(r) == 1 && r[0] == "张三"
	}, "确认行也是回合终点：货单应照常冲账")
	m := d.lookup("张三")
	d.mu.Lock()
	st := m.status
	d.mu.Unlock()
	if st != StatusIdle {
		t.Fatalf("确认行后座位应翻回 Idle，仍是 %s", st)
	}
}
