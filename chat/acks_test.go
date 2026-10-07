package chat

// The ack-receipt ledger's own tests (飞书式收到, acks.go): broadcast +
// dedupe, the honesty rules (member lines only, nobody acks their own,
// floor), and the persistence that keeps chips alive across restarts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ackersOf snapshots one seq's acker set.
func ackersOf(t *testing.T, h *Hub, seq int64) []string {
	t.Helper()
	_, rows := h.AckReceipts()
	for _, row := range rows {
		if row.Seq == seq {
			return row.Ackers
		}
	}
	return nil
}

func TestMarkAckBroadcastsAndDedupes(t *testing.T) {
	h := NewHub()
	sp, _ := h.Join("房主", "", false)
	h.Say(sp, "各位好，今天对齐排期")
	seq := lastSaySeq(t, h)
	li, _ := h.Join("李四", "", false)

	fresh := h.MarkAck("李四", seq)
	if len(fresh) != 1 || fresh[0] != seq {
		t.Fatalf("首记应返回新事实: %v", fresh)
	}
	frame := recvUntil(t, li, MsgAck, 2*time.Second)
	if frame.From != "李四" || len(frame.Seqs) != 1 || frame.Seqs[0] != seq {
		t.Fatalf("ack 帧形状不对: %+v", frame)
	}
	if got := ackersOf(t, h, seq); len(got) != 1 || got[0] != "李四" {
		t.Fatalf("快照不对: %v", got)
	}

	// 同人同 seq 的重复收到与未知 seq 都必须安静——回执不许说谎
	if again := h.MarkAck("李四", seq); again != nil {
		t.Fatalf("重复收到不该再记: %v", again)
	}
	h.MarkAck("李四", seq+424242)
	select {
	case m := <-li.Receive():
		t.Fatalf("不应再广播 ack 帧: %+v", m)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestAckLedgerHonestyRules(t *testing.T) {
	h := NewHub()
	sp, _ := h.Join("房主", "", false)
	h.Say(sp, "只需知晓：下午三点复盘")
	seq := lastSaySeq(t, h)

	// 自己不答自己：房主收不到自己这条的回执
	h.MarkAck("房主", seq)
	if got := ackersOf(t, h, seq); got != nil {
		t.Fatalf("不该记自己的收到: %v", got)
	}

	// 系统行是房间自己的声音，挣不到回执
	h.SystemRecorded("系统留痕行")
	h.MarkAck("李四", h.LastSeq())
	if _, rows := h.AckReceipts(); len(rows) != 0 {
		t.Fatalf("系统行不该挣得回执: %+v", rows)
	}

	// report 也是成员的话——汇报里 @人同样可以被回执
	h.Report(sp, "周报：@李四 数据已齐")
	rep := h.LastSeq()
	h.MarkAck("李四", rep)
	if got := ackersOf(t, h, rep); len(got) != 1 || got[0] != "李四" {
		t.Fatalf("report 该可回执: %v", got)
	}
}

func TestAckLedgerPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.jsonl")
	h1 := NewHub()
	h1.SetHistoryPath(path)
	sp, _ := h1.Join("房主", "", false)
	h1.Say(sp, "第一条")
	m1 := lastSaySeq(t, h1)
	h1.Say(sp, "第二条")
	m2 := lastSaySeq(t, h1)
	h1.MarkAck("李四", m1)
	h1.MarkAck("王五", m1)
	h1.MarkAck("王五", m2)
	h1.FlushLedgers() // 落盘已异步：开新房前排干

	h2 := NewHub()
	h2.SetHistoryPath(path)
	since, rows := h2.AckReceipts()
	if since != m1 {
		t.Fatalf("floor 未随账本持久化: since=%d want=%d", since, m1)
	}
	if len(rows) != 2 {
		t.Fatalf("重启丢了回执: %+v", rows)
	}
	if len(rows[0].Ackers) != 2 || rows[0].Ackers[0] != "李四" || rows[0].Ackers[1] != "王五" {
		t.Fatalf("回执集不合: %+v", rows[0])
	}
}

func TestAckLedgerFloorsAtRestoredHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.jsonl")
	old, err := json.Marshal(Message{Type: MsgSay, From: "老王", Text: "旧消息", Seq: 1000, TS: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(old, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	h.SetHistoryPath(path)
	// 屏障挂 t.Cleanup：flusher 排干即静止，不再与 TempDir 清理赛跑
	t.Cleanup(func() { h.FlushLedgers() })
	// 功能上线前的历史消息：floor 之下，收到也不记账（宁缺毋谎）
	h.MarkAck("李四", 1000)
	if _, rows := h.AckReceipts(); len(rows) != 0 {
		t.Fatalf("floor 下的 seq 不该记账: %+v", rows)
	}
	sp, _ := h.Join("房主", "", false)
	h.Say(sp, "新消息")
	seq := lastSaySeq(t, h)
	h.MarkAck("李四", seq)
	if got := ackersOf(t, h, seq); len(got) != 1 {
		t.Fatalf("floor 上的 seq 该记账: %v", got)
	}
}
