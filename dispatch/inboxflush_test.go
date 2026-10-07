package dispatch

// inboxflush_test.go — 收件箱异步落盘的契约钉：热路径只 marshal＋入队
// （读改写＋fsync 出了临界区）；ordered 保存的令牌（现拆为锁内
// prepareInboxSaveOrdered＋锁外 enqueueInboxSave）只在快照真正落地时
// 关（「消费即落盘」的防线不因异步化变宽）；满队降级 dirty、
// FlushInbox 阻塞补位后盘上是全量快照；先排干再 WipeProjectLanes，
// 清掉的键不被在途写复活；乱序迟到的旧快照被 flusher 按序号丢弃，
// 已捎带的线不随旧档复活。

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func readInboxQueue(t *testing.T, d *Dispatcher, name string) []string {
	t.Helper()
	path, err := d.inboxPath(name)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("inbox 文件应存在: %v", err)
	}
	var file map[string]struct {
		Queue []string `json:"queue"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatalf("inbox 文件应可解析: %v", err)
	}
	return file[d.projectKey].Queue
}

func TestInboxOrderedTokenClosesOnLanding(t *testing.T) {
	_, d, _, _ := readDispatcherWithSpeaker(t)
	m := d.lookup("张三")
	d.enqueueFrom(m, "李四", "排队行一", []int64{1}, nil, time.Now().Unix(), nil)

	d.mu.Lock()
	job := d.prepareInboxSaveOrdered(m)
	d.mu.Unlock()
	bar := d.enqueueInboxSave(job)
	if bar == nil {
		t.Fatal("有序保存应返回落地令牌")
	}
	select {
	case <-bar:
	case <-time.After(3 * time.Second):
		t.Fatal("落地令牌迟迟不关——快照没到盘上")
	}
	if q := readInboxQueue(t, d, "张三"); len(q) != 1 || q[0] != "排队行一" {
		t.Fatalf("令牌关闭时盘上应是最新快照: %v", q)
	}
}

// TestInboxStaleStragglerDroppedBySeq 钉序号制的核心不变量：锁外入队
// 让通道位置不再定序——迟到的旧快照（seq 更小）不得覆盖先到的新快照，
// 否则已折进投递的线会在重启读档时整条复活。
func TestInboxStaleStragglerDroppedBySeq(t *testing.T) {
	_, d, _, _ := readDispatcherWithSpeaker(t)
	m := d.lookup("张三")
	d.enqueueFrom(m, "李四", "已被投递消费的行", []int64{1}, nil, time.Now().Unix(), nil)

	// 锁内取旧快照（含待投行），放行锁后再造新世界：线已消费、车道已
	// 清——新快照先入队，迟到的旧快照后入队。
	d.mu.Lock()
	staleJob := d.prepareInboxSaveOrdered(m)
	d.mu.Unlock()

	d.mu.Lock()
	m.lanes.queue = nil // 消费发生：新快照的队列是空的
	freshJob := d.prepareInboxSaveOrdered(m)
	d.mu.Unlock()

	bar := d.enqueueInboxSave(freshJob)
	select {
	case <-bar:
	case <-time.After(3 * time.Second):
		t.Fatal("新快照的落地令牌迟迟不关")
	}
	staleBar := d.enqueueInboxSave(staleJob) // 通道里排在后面：按序号是旧档
	select {
	case <-staleBar:
	case <-time.After(3 * time.Second):
		t.Fatal("旧快照的令牌也应收尾关闭（不挂等）")
	}
	if q := readInboxQueue(t, d, "张三"); len(q) != 0 {
		t.Fatalf("迟到的旧快照应被丢弃，盘上不应复活已消费的行: %v", q)
	}
}

func TestInboxQueueFullDegradesToDirtyThenFlushSweeps(t *testing.T) {
	_, d, _, _ := readDispatcherWithSpeaker(t)
	m := d.lookup("张三")

	// 换一条没人消费的窄队列模拟磁盘拖尾（原 flusher 还挂在旧通道上，
	// 测试收尾即弃）。
	d.mu.Lock()
	d.inboxSwapMu.Lock()
	stalled := make(chan inboxJob, 1)
	d.inboxSave = stalled
	d.inboxSwapMu.Unlock()
	d.mu.Unlock()

	const N = 3
	// 不同发送方：L2 同人连发合并会让三线并成一节，这里要的就是 N 个
	// 独立条目来验全量快照。
	for i, from := range []string{"李四", "王五", "赵六"} {
		d.enqueueFrom(m, from, "线"+string(rune('A'+i)), nil, nil, time.Now().Unix(), nil)
	}
	d.mu.Lock()
	dirty := len(d.inboxDirty) > 0
	d.mu.Unlock()
	if !dirty {
		t.Fatal("窄队列下应有成员降级为 dirty（丢快照不丢真相）")
	}

	// 恢复消费（真 flusher 挂上换入的通道，自带合并与屏障语义）后冲账：
	// dirty 全量补写，屏障返回即盘上可见。
	go d.inboxFlushLoop()
	d.FlushInbox()
	d.mu.Lock()
	dirty = len(d.inboxDirty) > 0
	d.mu.Unlock()
	if dirty {
		t.Fatal("冲账后 dirty 应清空")
	}
	if q := readInboxQueue(t, d, "张三"); len(q) != N {
		t.Fatalf("冲账补写是全量快照（应见全部 %d 条）: %v", N, q)
	}
}

func TestWipeAfterFlushDoesNotResurrect(t *testing.T) {
	_, d, _, _ := readDispatcherWithSpeaker(t)
	m := d.lookup("张三")
	d.enqueueFrom(m, "李四", "待清的排队行", []int64{1}, nil, time.Now().Unix(), nil)

	// boot_fleet 清扫腿的次序：先排干在途写，再删键——删完不许复活。
	d.FlushInbox()
	n, err := WipeProjectLanes(d.cfg.InboxDir, d.projectKey)
	if err != nil || n < 1 {
		t.Fatalf("WipeProjectLanes 应触及张三的文件: n=%d err=%v", n, err)
	}
	path, err := d.inboxPath("张三")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ { // 给任何野生的在途写留足现形时间
		time.Sleep(20 * time.Millisecond)
		if b, err := os.ReadFile(path); err == nil {
			var file map[string]json.RawMessage
			if json.Unmarshal(b, &file) == nil {
				if _, hit := file[d.projectKey]; hit {
					t.Fatalf("清掉的键竟被在途写复活（第 %d 次查看）", i)
				}
			}
		}
	}
}
