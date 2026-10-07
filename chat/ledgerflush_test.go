package chat

// ledgerflush_test.go — 三账本异步落盘的契约钉：mark 路径只 marshal＋
// 入队（tmp+fsync+rename 的 IO 出了临界区，history flusher 的同款纪律）；
// 队列满降级为 dirty（丢的是快照不是真相，下一次 mark 或屏障重试）；
// FlushLedgers 屏障返回即盘上可见，且冲账重试是全量重写——dirty 恢复
// 后盘上是完整的当前账，不是增量尾巴。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLedgerFlusherBarrierLandsOnDisk(t *testing.T) {
	dir := t.TempDir()
	h := NewHub()
	h.SetHistoryPath(filepath.Join(dir, "room.jsonl"))
	sp, _ := h.Join("房主", "", false)
	msg, ok := h.Say(sp, "第一条")
	if !ok {
		t.Fatal("前置失败：Say 被拒")
	}
	h.MarkRead("李四", msg.Seq)
	h.MarkAck("李四", msg.Seq)
	h.MarkReact("李四", msg.Seq, "👍", true)

	h.FlushLedgers() // 屏障：返回即三份文件都已落盘
	var disk struct {
		Since int64     `json:"since"`
		Reads []ReadRow `json:"reads"`
	}
	for name, into := range map[string]any{
		"room.reads.json":  &disk,
		"room.acks.json":   new(struct{}),
		"room.reacts.json": new(struct{}),
	} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("屏障后 %s 应已落盘: %v", name, err)
		}
		if name == "room.reads.json" {
			if err := json.Unmarshal(b, into); err != nil {
				t.Fatalf("reads 文件不可解析: %v", err)
			}
		}
	}
	if len(disk.Reads) != 1 || len(disk.Reads[0].Readers) != 1 || disk.Reads[0].Readers[0] != "李四" {
		t.Fatalf("盘上 reads 与内存不合: %+v", disk.Reads)
	}
}

func TestLedgerQueueFullDegradesToDirtyThenFlushRetries(t *testing.T) {
	dir := t.TempDir()
	h := NewHub()
	h.SetHistoryPath(filepath.Join(dir, "room.jsonl"))
	sp, _ := h.Join("房主", "", false)

	// 换一条没人消费的窄队列模拟磁盘拖尾：mark 只能入队到满，满后降级
	// dirty（原 flusher 还挂在旧通道上，测试收尾即弃）。
	h.mu.Lock()
	h.ledgerSwapMu.Lock()
	stalled := make(chan ledgerWrite, 2)
	h.ledgerSave = stalled
	h.ledgerSwapMu.Unlock()
	h.mu.Unlock()

	const N = 5
	for i := 0; i < N; i++ {
		msg, _ := h.Say(sp, "消息"+string(rune('A'+i)))
		h.MarkRead("李四", msg.Seq)
	}
	h.mu.Lock()
	dirty := h.reads.dirty
	h.mu.Unlock()
	if !dirty {
		t.Fatal("队列满应置 dirty（丢快照不丢真相，待重试）")
	}

	// 恢复消费后冲账：直接把真正的 flushLedgerLoop 挂上换入的通道（它
	// 自带屏障语义——收到 done 关栅栏），dirty 的全量快照经阻塞入队补
	// 走，屏障返回即落盘。
	h.ledgerSwapMu.Lock()
	stalledCh := h.ledgerSave
	h.ledgerSwapMu.Unlock()
	go h.flushLedgerLoop(stalledCh)
	h.FlushLedgers()
	h.mu.Lock()
	dirty = h.reads.dirty
	h.mu.Unlock()
	if dirty {
		t.Fatal("冲账后 dirty 应清")
	}
	if _, rows := h.ReadReceipts(); len(rows) != N {
		t.Fatalf("内存真源一行不丢: %d", len(rows))
	}
	b, err := os.ReadFile(filepath.Join(dir, "room.reads.json"))
	if err != nil {
		t.Fatalf("冲账屏障后 reads 应落盘: %v", err)
	}
	var disk struct {
		Since int64     `json:"since"`
		Reads []ReadRow `json:"reads"`
	}
	if err := json.Unmarshal(b, &disk); err != nil {
		t.Fatalf("reads 文件不可解析: %v", err)
	}
	if len(disk.Reads) != N {
		// 满载偶红实录（2026-10-05，栅栏改造当天的排查）：`go test ./...`
		// 全仓并行下约 1/5 复现，单包/-count=5 从不红；断言拿到 2/5＝强
		// 制全量重写那次 persist.Save 失败（同窗口日志有成片
		// 「ledger save … chmod …tmp…: no such file or directory」——
		// 满载下 macOS 临时目录的 IO 抖动）。已 A/B 排除新代码：真原版
		// flusher 同负载同样偶红，非栅栏改造引入。根因在持久层/测试隔
		// 离与机器负载的交互，待专案；与 reads_test 的 -race 偶红同类。
		t.Fatalf("冲账重试是全量重写（盘上应见全部 %d 行，非增量尾巴）: %d", N, len(disk.Reads))
	}
}
