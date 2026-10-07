package dispatch

// seatack_test.go — 结构化回执（niuma ack 动词的调度器腿）：
//   - 本轮欠的点名行一次性全部挂「收到」回执（MarkAck 广播）；
//   - 回执后货单清空——终点再无欠账（文本「收到」不会二次挂）；
//   - 无货单/无此成员：false（CLI 的「无可回执」不是错误）。

import (
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
)

func acksOf(hub *chat.Hub) map[int64][]string {
	_, rows := hub.AckReceipts()
	out := map[int64][]string{}
	for _, r := range rows {
		out[r.Seq] = r.Ackers
	}
	return out
}

func TestSeatAckReceiptsWholeTurn(t *testing.T) {
	hub, d, rb, speaker := readDispatcherWithSpeaker(t)
	hub.Say(speaker, "@张三 例行通报")
	seq := hub.LastSeq()
	// 宽死线：汇聚窗 2s，race 模式下 3s 的 waitFor 偶尔不够
	if !waitQuiet(8*time.Second, func() bool { return rb.sendHas("例行通报") }) {
		t.Fatal("点名注入应发生")
	}

	if !d.SeatAck("张三") {
		t.Fatal("本轮欠点名行，SeatAck 应真")
	}
	got := acksOf(hub)
	if ackers := got[seq]; len(ackers) != 1 || ackers[0] != "张三" {
		t.Fatalf("seq %d 应挂上张三的回执（got %v）", seq, ackers)
	}
	// 货单已清：终点不欠、再 ack 为假
	d.mu.Lock()
	m := d.members["张三"]
	owes := len(m.cargo.ackSeqs) > 0 || len(m.cargo.turnAck) > 0
	d.mu.Unlock()
	if owes {
		t.Fatal("回执后货单应清空")
	}
	if d.SeatAck("张三") {
		t.Fatal("无货单时 SeatAck 应假")
	}
}

func TestSeatAckUnknownMember(t *testing.T) {
	_, d, _, _ := readDispatcherWithSpeaker(t)
	if d.SeatAck("查无此人") {
		t.Fatal("无此成员应假")
	}
}

// waitQuiet — waitSlow 的无 t 版（转储路径用）。
func waitQuiet(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
