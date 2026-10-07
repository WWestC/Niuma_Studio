package chat

// drainbarrier_test.go — 出锁排干纪律的钉子：drainLedgers 的阻塞发
// 送坐在 NO 锁上（快照锁内取、发送锁外做）。停摆 flusher＋灌满队列
// 让 FlushLedgers 卡在发送上——此刻 h.mu 必须仍可获取（修复前
// drainLedgersLocked 握着 h.mu 阻塞入队，全房任何走锁路径都被饿死）。
// 同款钉住 ResetDurable：换通道排干窗口里 h.mu 同样可得，且窗口期
// 的行不丢（泊住）、旧文件不复活。

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// muAcquirable proves h.mu can be taken within budget (false = the
// blocking drain is sitting on the lock).
func muAcquirable(h *Hub, budget time.Duration) bool {
	done := make(chan struct{})
	go func() {
		h.mu.Lock()
		h.mu.Unlock()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(budget):
		return false
	}
}

func TestDrainLedgersBlockingSendsHoldNoLock(t *testing.T) {
	dir := t.TempDir()
	h := NewHub()
	h.SetHistoryPath(filepath.Join(dir, "room.jsonl"))
	sp, _ := h.Join("房主", "", false)
	msg, ok := h.Say(sp, "第一条")
	if !ok {
		t.Fatal("前置失败")
	}

	// 停摆 flusher：换入无消费者的通道（容量 1，很快满）。
	h.ledgerSwapMu.Lock()
	stalled := make(chan ledgerWrite, 1)
	h.ledgerSave = stalled
	h.ledgerSwapMu.Unlock()
	t.Cleanup(func() {
		// 收尾：换回活通道并把停摆队列排干，flusher 归位。
		h.ledgerSwapMu.Lock()
		h.ledgerSave = make(chan ledgerWrite, 64)
		h.ledgerSwapMu.Unlock()
		go h.flushLedgerLoop(h.ledgerSave)
		h.FlushLedgers()
	})

	h.MarkRead("李四", msg.Seq) // 快照入停摆队列（容量 1 占满）

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.FlushLedgers() // 阻塞在满队列的发送上——锁外
	}()
	// FlushLedgers 卡住的同时，h.mu 必须可得。
	time.Sleep(50 * time.Millisecond)
	if !muAcquirable(h, time.Second) {
		t.Fatal("FlushLedgers 阻塞期间 h.mu 被占用——阻塞发送坐回了锁上（出锁纪律被破坏）")
	}
	// 房间照常服务（走锁路径的 say 不被排干饿死）。
	if _, ok := h.Say(sp, "排干期间照常发言"); !ok {
		t.Fatal("排干阻塞期间发言被拒/被饿死")
	}

	// 放行：消费停摆队列，FlushLedgers 应能返回。
	go func() {
		for w := range stalled {
			if w.done != nil {
				close(w.done)
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("放行后 FlushLedgers 未返回")
	}
}

func TestResetDurableDrainsOutsideLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.json")
	h := NewHub()
	h.SetHistoryPath(path)
	sp, _ := h.Join("房主", "", false)
	if _, ok := h.Say(sp, "旧世界的行"); !ok {
		t.Fatal("前置失败")
	}
	h.FlushHistory()

	// 正确性钉：换通道排干后旧文件不复活、新世界照常落盘（排干期
	// 间 h.mu 可得性由前一个测试的停摆形状钉——reset 的屏障与
	// FlushLedgers 同一条出锁纪律）。
	if err := h.ResetDurable(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("重置后历史文件应已删除: %v", err)
	}
	// 重置后的房间照常可用。
	if _, ok := h.Say(sp, "新世界的行"); !ok {
		t.Fatal("重置后发言被拒")
	}
	h.FlushHistory()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("新世界应重新落盘: %v", err)
	}
	if len(b) == 0 || !strings.Contains(string(b), "新世界的行") {
		t.Fatalf("新世界文件内容不对: %q", string(b))
	}
}
