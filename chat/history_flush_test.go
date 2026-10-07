package chat

// history_flush_test.go — the ordered flusher's contract: the say path
// enqueues under the lock and the file catches up asynchronously; FIFO
// order preserves seq order on disk; FlushHistory (the drain) makes
// every said line visible to file readers; the prune rewrite never
// amputates the pending tail.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func flushHub(t *testing.T) (*Hub, string) {
	t.Helper()
	h := NewHub()
	path := filepath.Join(t.TempDir(), "h.jsonl")
	h.SetHistoryPath(path)
	return h, path
}

func readAllFrames(t *testing.T, path string) []Message {
	t.Helper()
	var out []Message
	if err := ScanHistory(path, func(m Message) bool {
		out = append(out, m)
		return true
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

// enqueue 直通内部写径（不依赖 Client 装配），带回执 seq。
func enqueueSay(t *testing.T, h *Hub, text string) int64 {
	t.Helper()
	h.mu.Lock()
	h.lastSeq++
	msg := Message{Type: MsgSay, From: "小牛", Text: text, TS: time.Now().Unix(), Seq: h.lastSeq}
	h.history = append(h.history, msg)
	h.appendHistoryLocked(msg)
	seq := h.lastSeq
	h.mu.Unlock()
	return seq
}

func TestHistoryFlusherDrainsInOrder(t *testing.T) {
	h, path := flushHub(t)
	const n = 50
	for i := 0; i < n; i++ {
		enqueueSay(t, h, time.Now().Format(time.RFC3339Nano)) // 每行内容唯一
	}
	h.FlushHistory()
	frames := readAllFrames(t, path)
	if len(frames) != n {
		t.Fatalf("排水后文件应有 %d 行，得到 %d", n, len(frames))
	}
	for i := 1; i < len(frames); i++ {
		if frames[i].Seq <= frames[i-1].Seq {
			t.Fatalf("盘上顺序破坏：第 %d 行 seq %d 未严格递增于 %d", i, frames[i].Seq, frames[i-1].Seq)
		}
	}
}

func TestHistoryFlusherConcurrentSaysStayOrdered(t *testing.T) {
	h, path := flushHub(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				enqueueSay(t, h, "msg")
			}
		}()
	}
	wg.Wait()
	h.FlushHistory()
	frames := readAllFrames(t, path)
	if len(frames) != 200 {
		t.Fatalf("应有 200 行，得到 %d（丢行＝队列满或写失败）", len(frames))
	}
	for i := 1; i < len(frames); i++ {
		if frames[i].Seq <= frames[i-1].Seq {
			t.Fatalf("并发写后盘上顺序破坏于第 %d 行", i)
		}
	}
}

func TestPruneRewriteKeepsPendingTail(t *testing.T) {
	h, path := flushHub(t)
	for i := 0; i < 5; i++ {
		enqueueSay(t, h, "old")
	}
	h.FlushHistory()
	for i := 0; i < 5; i++ {
		enqueueSay(t, h, "new") // 尚未排水——prune 必须先排再剪
	}
	// keepSince 排掉全部（老行与新行都过期）
	dropped := h.PruneHistory(time.Now().Add(time.Hour).Unix())
	if dropped != 10 {
		t.Fatalf("应剪 10 行，得到 %d", dropped)
	}
	frames := readAllFrames(t, path)
	if len(frames) != 0 {
		t.Fatalf("全量剪后文件应为空，剩 %d 行（待写尾部被剪丢或重写撕裂）", len(frames))
	}
	// 剪后新 say 照常落盘（flusher 换到新文件续写）
	seq := enqueueSay(t, h, "after-prune")
	h.FlushHistory()
	frames = readAllFrames(t, path)
	if len(frames) != 1 || frames[0].Seq != seq {
		t.Fatalf("剪后追加应恰好一行 seq=%d，得到 %v", seq, frames)
	}
}

func TestFlushHistoryIdempotentAndInMemorySafe(t *testing.T) {
	// 内存房（无 SetHistoryPath）排水是无害 no-op
	h := NewHub()
	h.FlushHistory()
	// 有文件房重复排水不重复写
	h2, path := flushHub(t)
	enqueueSay(t, h2, "once")
	h2.FlushHistory()
	h2.FlushHistory()
	if frames := readAllFrames(t, path); len(frames) != 1 {
		t.Fatalf("重复排水不得重复写行，得到 %d", len(frames))
	}
}

// jsonl 一行一帧的钉：flusher 写出的字节就是旧同步路径的字节。
func TestFlusherLineShapeUnchanged(t *testing.T) {
	h, path := flushHub(t)
	msg := Message{Type: MsgSay, From: "小马", Text: "形状", TS: 1700000000, Seq: 42}
	h.mu.Lock()
	h.appendHistoryLocked(msg)
	h.mu.Unlock()
	h.FlushHistory()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(msg)
	if string(b) != string(want)+"\n" {
		t.Fatalf("行形状漂移：got %q want %q", b, append(want, '\n'))
	}
}
