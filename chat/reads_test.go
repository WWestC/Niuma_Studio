package chat

// The read-receipt ledger's own tests (飞书式已读, reads.go): broadcast
// + dedupe, the say-only honesty rule, the boot floor, and the
// persistence that keeps chips alive across restarts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// lastSaySeq returns the newest say frame's seq — Say no longer returns
// the delivered message, the history ring is the record.
func lastSaySeq(t *testing.T, h *Hub) int64 {
	t.Helper()
	hist := h.History()
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Type == MsgSay {
			return hist[i].Seq
		}
	}
	t.Fatal("历史里没有 say 帧")
	return 0
}

// recvFrame reads one frame off a client's stream or fails the test.
func recvFrame(t *testing.T, c *Client, d time.Duration) Message {
	t.Helper()
	select {
	case m := <-c.Receive():
		return m
	case <-time.After(d):
		t.Fatal("等待帧超时")
		return Message{}
	}
}

// recvUntil drains frames until one of the wanted type arrives (the
// join handshake's welcome/history/join frames come first).
func recvUntil(t *testing.T, c *Client, typ string, d time.Duration) Message {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			t.Fatalf("等待 %s 帧超时", typ)
		}
		m := recvFrame(t, c, remain)
		if m.Type == typ {
			return m
		}
	}
}

func TestMarkReadBroadcastsAndDedupes(t *testing.T) {
	h := NewHub()
	sp, _ := h.Join("房主", "", false)
	h.Say(sp, "各位好，今天对齐排期")
	seq := lastSaySeq(t, h)
	li, _ := h.Join("李四", "", false)

	h.MarkRead("李四", seq)
	frame := recvUntil(t, li, MsgRead, 2*time.Second)
	if frame.From != "李四" || len(frame.Seqs) != 1 || frame.Seqs[0] != seq {
		t.Fatalf("read 帧形状不对: %+v", frame)
	}
	since, rows := h.ReadReceipts()
	if len(rows) != 1 || rows[0].Seq != seq || len(rows[0].Readers) != 1 || rows[0].Readers[0] != "李四" {
		t.Fatalf("快照不对: since=%d rows=%+v", since, rows)
	}

	// 同人同 seq 的重复已读与未知 seq 都必须安静——回执不许说谎
	h.MarkRead("李四", seq)
	h.MarkRead("李四", seq+424242)
	select {
	case m := <-li.Receive():
		t.Fatalf("不应再广播 read 帧: %+v", m)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestReadLedgerIgnoresNonSay(t *testing.T) {
	h := NewHub()
	h.SystemRecorded("系统留痕行")
	h.MarkRead("李四", h.LastSeq())
	if _, rows := h.ReadReceipts(); len(rows) != 0 {
		t.Fatalf("系统行不该挣得回执: %+v", rows)
	}
}

func TestReadLedgerPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.jsonl")
	h1 := NewHub()
	h1.SetHistoryPath(path)
	sp, _ := h1.Join("房主", "", false)
	h1.Say(sp, "第一条")
	m1 := lastSaySeq(t, h1)
	h1.Say(sp, "第二条")
	m2 := lastSaySeq(t, h1)
	h1.MarkRead("李四", m1)
	h1.MarkRead("王五", m1)
	h1.MarkRead("王五", m2)
	h1.FlushLedgers() // 落盘已异步：开新房前排干（重启测试的告别排水）

	h2 := NewHub()
	h2.SetHistoryPath(path)
	since, rows := h2.ReadReceipts()
	if since != m1 {
		t.Fatalf("floor 未随账本持久化: since=%d want=%d", since, m1)
	}
	if len(rows) != 2 {
		t.Fatalf("重启丢了回执: %+v", rows)
	}
	if len(rows[0].Readers) != 2 || rows[0].Readers[0] != "李四" || rows[0].Readers[1] != "王五" {
		t.Fatalf("读者集不合: %+v", rows[0])
	}
	if len(rows[1].Readers) != 1 || rows[1].Readers[0] != "王五" {
		t.Fatalf("第二条读者不合: %+v", rows[1])
	}
}

func TestReadLedgerFloorsAtRestoredHistory(t *testing.T) {
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
	// 功能上线前的历史消息：floor 之下，读了也不记账（宁缺毋谎）
	h.MarkRead("李四", 1000)
	if _, rows := h.ReadReceipts(); len(rows) != 0 {
		t.Fatalf("floor 下的 seq 不该记账: %+v", rows)
	}
	sp, _ := h.Join("房主", "", false)
	h.Say(sp, "新消息")
	seq := lastSaySeq(t, h)
	if seq <= 1000 {
		t.Fatalf("新 seq 应越过恢复的 lastSeq: %d", seq)
	}
	h.MarkRead("李四", seq)
	_, rows := h.ReadReceipts()
	if len(rows) != 1 || rows[0].Seq != seq {
		t.Fatalf("floor 上的 seq 该记账: %+v", rows)
	}
}
