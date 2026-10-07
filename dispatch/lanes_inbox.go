package dispatch

// lanes_inbox.go — the inbox PERSISTENCE family, split from lanes.go
// (zero logic edits): path safety, saveLanes' snapshot/enqueue, the
// ordered flusher (batch drain + landed-seq memory), FlushInbox's
// drain barriers, loadLanes' restore and WipeProjectLanes' cleanup.
// The lanes themselves (delivery mechanics, kicks, cargo) stayed in
// lanes.go; the file split follows the in-memory-truth vs
// on-disk-read-model seam.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/util"
)

// inboxMu serializes every inbox-file read-modify-write across all
// dispatchers in this process (files are per-member and shared).
var inboxMu sync.Mutex

// inboxFileSafe renders a member name as one path-safe file leaf. The
// raw name reaches BirthPost from HTTP bodies and from the HR agent's
// own `dispatch birth --name` (an LLM chooses it), and sanitizeName
// strips control characters only — '/' and '..' survive. Unfolded they
// would let a birth name walk the inbox join out of ~/.niuma/inbox
// (saveLanes atomically REPLACES whatever lands at the joined path).
// Folding follows termFileLeaf's rules (slash friends and leading/doubled
// dots become '_'); when folding changes the name at all, a short digest
// of the raw name is appended (seatTokenFileSafe's rule) so two distinct
// hostile names never share one inbox file. Clean names — including the
// CJK ones — keep their historical path byte-for-byte.
func inboxFileSafe(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '\x00' || r == '\n' || r == '\r':
			b.WriteByte('_')
		case r == '.' && (b.Len() == 0 || strings.HasSuffix(b.String(), ".")):
			b.WriteByte('_') // 无点开头、无点连点：'..' 同款顾虑
		default:
			b.WriteRune(r)
		}
	}
	leaf := b.String()
	changed := leaf != name
	if utf8.RuneCountInString(leaf) > 96 {
		leaf = string([]rune(leaf)[:96])
		changed = true
	}
	if leaf == "" {
		leaf, changed = "_", true
	}
	if changed {
		sum := sha256.Sum256([]byte(name))
		leaf += "-" + hex.EncodeToString(sum[:])[:8]
	}
	return leaf + ".json"
}

func (d *Dispatcher) inboxPath(name string) (string, error) {
	dir := d.cfg.InboxDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".niuma", "inbox")
	}
	return filepath.Join(dir, inboxFileSafe(name)), nil
}

// inboxJob is one queued inbox write: a full snapshot of ONE member's
// lanes for THIS project (snapshots are whole-state — only the newest
// per member matters, the flusher coalesces), or a pure barrier
// (name == "" && done != nil, FlushInbox's drain token).
type inboxJob struct {
	name  string
	lanes inboxLanes
	seq   uint64        // the snapshot's inboxSeq (0 = barrier/dirty legs): flusher keeps the max per member
	done  chan struct{} // closed once the snapshot has landed (ordered saves wait on it)
}

// saveLanes persists the member's lanes into THIS project's lane of
// their inbox file, preserving every other project's lanes: marshal
// and enqueue under d.mu — the read-modify-write + fsync left the
// critical section (the chat ledger flusher's discipline). A full
// queue degrades to dirty (retried by the next save or swept by
// FlushInbox): the in-memory lane is the truth, the inbox file is the
// restart read model, a skipped snapshot costs staleness — never
// loss. Callers hold d.mu.
func (d *Dispatcher) saveLanes(m *member) {
	d.queueInboxSaveLocked(m, false)
}

// prepareInboxSaveOrdered snapshots the member's lanes under d.mu with
// the consume-before-send token, STAMPED with the member's inboxSeq —
// but does NOT enqueue. The caller releases d.mu first and then hands
// the job to enqueueInboxSave: the blocking channel send no longer
// runs inside the critical section (a stalled flusher used to wedge
// the whole dispatcher through it). Ordering survives the unlocked
// hop because the flusher keeps the MAX-seq snapshot per member, not
// the last one off the channel: a racing newer snapshot that enqueued
// earlier simply wins, and this job landing after it is dropped as a
// stale straggler — the folded lines cannot resurrect. Callers hold
// d.mu.
func (d *Dispatcher) prepareInboxSaveOrdered(m *member) inboxJob {
	lanes := d.snapshotLanesLocked(m)
	m.lanes.inboxSeq++
	job := inboxJob{name: m.name, lanes: lanes, seq: m.lanes.inboxSeq}
	if d.inboxSave == nil {
		// a hand-rolled zero-value Dispatcher (test stubs) has no
		// flusher: the legacy synchronous write, barrier pre-closed.
		d.writeInboxKey(m.name, lanes)
		job.done = make(chan struct{})
		close(job.done)
		return job
	}
	job.done = make(chan struct{})
	delete(d.inboxDirty, m.name)
	return job
}

// enqueueInboxSave is the out-of-lock half of the ordered save: a
// blocking send (never degrades — the flusher never takes d.mu, so
// the channel always drains) whose stall now costs only this
// delivery's latency, not the dispatcher's lock. Returns the job's
// barrier.
func (d *Dispatcher) enqueueInboxSave(job inboxJob) <-chan struct{} {
	if job.done == nil {
		return nil
	}
	select {
	case <-job.done:
		return job.done // pre-closed by the no-flusher fallback
	default:
	}
	d.inboxSwapMu.Lock()
	ch := d.inboxSave
	d.inboxSwapMu.Unlock()
	if ch == nil {
		return job.done
	}
	ch <- job
	return job.done
}

// queueInboxSaveLocked is the single enqueue face for the IN-lock
// paths. block = force a blocking enqueue (FlushInbox's dirty sweep —
// the only remaining blocking sender, and it drains a bounded dirty
// set); the non-blocking path degrades to dirty on a full queue.
// ordered saves no longer come through here (see
// prepareInboxSaveOrdered). Callers hold d.mu.
func (d *Dispatcher) queueInboxSaveLocked(m *member, block bool) <-chan struct{} {
	lanes := d.snapshotLanesLocked(m)
	if d.inboxSave == nil {
		// a hand-rolled zero-value Dispatcher (test stubs) has no
		// flusher: the legacy synchronous write.
		d.writeInboxKey(m.name, lanes)
		return nil
	}
	m.lanes.inboxSeq++
	job := inboxJob{name: m.name, lanes: lanes, seq: m.lanes.inboxSeq}
	if block {
		d.inboxSave <- job
		delete(d.inboxDirty, m.name)
		return nil
	}
	select {
	case d.inboxSave <- job:
		delete(d.inboxDirty, m.name)
	default:
		d.inboxDirty[m.name] = true // staleness, never loss: next save / FlushInbox retries
	}
	return nil
}

// snapshotLanesLocked renders the member's lanes as the inbox JSON
// shape — the legacy parallel arrays; the disk format is frozen for
// pre-refactor files. Callers hold d.mu.
func (d *Dispatcher) snapshotLanesLocked(m *member) inboxLanes {
	lanes := inboxLanes{InjDropped: m.lanes.injDropped}
	for _, e := range m.lanes.queue {
		lanes.Queue = append(lanes.Queue, e.text)
		lanes.ReadQueue = append(lanes.ReadQueue, e.read)
		lanes.AckQueue = append(lanes.AckQueue, e.ack)
		lanes.QueueAt = append(lanes.QueueAt, e.at)
		lanes.QueueFrom = append(lanes.QueueFrom, e.from)
		lanes.ImgQueue = append(lanes.ImgQueue, e.imgs)
	}
	for _, e := range m.lanes.inject {
		lanes.Inject = append(lanes.Inject, e.text)
		lanes.ReadInject = append(lanes.ReadInject, e.read)
	}
	return lanes
}

// writeInboxKey is the flusher's write leg (and the no-flusher
// fallback): the file keeps every other project's lanes — read,
// replace THIS project's key wholesale, atomic save. Never runs under
// d.mu; inboxMu serializes file access across all flushers in the
// process (files are per-member and shared between dispatchers).
func (d *Dispatcher) writeInboxKey(name string, lanes inboxLanes) {
	path, err := d.inboxPath(name)
	if err != nil {
		return
	}
	inboxMu.Lock()
	defer inboxMu.Unlock()
	file := map[string]inboxLanes{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &file) // a corrupt file yields to the lanes below
	}
	file[d.projectKey] = lanes
	b, err := json.Marshal(file)
	if err != nil {
		return
	}
	_ = persist.Save(path, b, 0o600)
}

// inboxFlushLoop is the inbox flusher goroutine: batch-drains what is
// already queued, keeps only the NEWEST snapshot per member (full-state
// writes make intermediates redundant), writes each touched file once,
// then closes every token — superseded snapshots' waiters included
// (the newer write covers them), barriers last (FIFO order kept).
func (d *Dispatcher) inboxFlushLoop() {
	for {
		// Snapshot the channel under the swap lock (tests re-point it to
		// stall this loop; d.mu is off-limits here — see the field's
		// lock-order note).
		d.inboxSwapMu.Lock()
		ch := d.inboxSave
		d.inboxSwapMu.Unlock()
		first, ok := <-ch
		if !ok {
			return
		}
		d.inboxFlushBatch(first, ch)
	}
}

// inboxFlushBatch is one drain pass (the loop body, extracted for its
// panic fence). The fence carries a debt: a dead inbox flusher would
// wedge every later 「锁外等落地」 waiter and every FlushInbox barrier
// — a hung fleet, not a stale file — so a panicking batch degrades to
// dropped snapshots with every token still closed (a waiter may see
// stale lanes, never a hang). pending is the not-yet-closed tail; the
// normal close pops as it goes, so the recover path can never
// double-close.
func (d *Dispatcher) inboxFlushBatch(first inboxJob, ch chan inboxJob) {
	newest := map[string]inboxJob{}
	pending := []chan struct{}{}
	collect := func(j inboxJob) {
		if j.name == "" {
			if j.done != nil {
				pending = append(pending, j.done)
			}
			return
		}
		if old, ok := newest[j.name]; ok {
			if j.seq < old.seq {
				// 乱序迟到的旧快照（锁外入队让通道位置不再定序）：
				// 当场丢弃，令牌随批次收尾关闭——本批落盘的新档已覆盖
				// 它，已捎带的线不会随旧档复活。
				if j.done != nil {
					pending = append(pending, j.done)
				}
				return
			}
			if old.done != nil {
				pending = append(pending, old.done) // superseded: the newer snapshot's landing covers it
			}
		}
		newest[j.name] = j
	}
	defer func() {
		if r := recover(); r != nil {
			util.LogPanic("dispatch: inbox flusher", r)
			for _, c := range pending {
				close(c)
			}
		}
	}()
	collect(first)
	for {
		var j inboxJob
		select {
		case j = <-ch:
			collect(j)
			continue
		default:
		}
		break
	}
	for _, j := range newest {
		if j.seq <= d.landedInboxSeq(j.name) {
			// 跨批迟到：覆盖它的快照在早前批次已落盘——丢弃、关令牌
			// （锁外入队让通道位置不再定序，批内 newest 之外还要有跨批
			// 记忆，迟到旧档才不能复活已消费的线）。
			if j.done != nil {
				pending = append(pending, j.done)
			}
			continue
		}
		d.writeInboxKey(j.name, j.lanes)
		d.markInboxLanded(j.name, j.seq)
		if j.done != nil {
			pending = append(pending, j.done)
		}
	}
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		close(c)
	}
}

// landedInboxSeq is the member's last snapshot seq that actually
// reached disk (0 = nothing yet) — the flusher's cross-batch memory
// for dropping out-of-lock stragglers.
func (d *Dispatcher) landedInboxSeq(name string) uint64 {
	d.inboxLandedMu.Lock()
	defer d.inboxLandedMu.Unlock()
	return d.inboxLanded[name]
}

// markInboxLanded records the landed seq (monotonic — never lowers).
// The map is lazily built: test stubs bypass Start, and a nil write
// here would panic the flusher's fence into dropping every batch.
func (d *Dispatcher) markInboxLanded(name string, seq uint64) {
	d.inboxLandedMu.Lock()
	defer d.inboxLandedMu.Unlock()
	if d.inboxLanded == nil {
		d.inboxLanded = map[string]uint64{}
	}
	if seq > d.inboxLanded[name] {
		d.inboxLanded[name] = seq
	}
}

// FlushInbox drains every pending inbox write (sweeping the
// enqueue-overflowed dirty members first). The farewell drain for Stop
// and the pre-wipe leg: on return the files reflect the lanes as of
// the last mutation. The dirty sweep snapshots under d.mu and does
// its BLOCKING enqueues outside it — a stalled flusher may slow this
// drain, never the dispatcher's lock.
func (d *Dispatcher) FlushInbox() {
	d.inboxSwapMu.Lock()
	ch := d.inboxSave
	d.inboxSwapMu.Unlock()
	if ch == nil {
		return
	}
	d.mu.Lock()
	var jobs []inboxJob
	for name := range d.inboxDirty {
		if m := d.members[name]; m != nil {
			lanes := d.snapshotLanesLocked(m)
			m.lanes.inboxSeq++
			jobs = append(jobs, inboxJob{name: m.name, lanes: lanes, seq: m.lanes.inboxSeq})
			delete(d.inboxDirty, m.name)
		}
	}
	d.mu.Unlock()
	for _, j := range jobs {
		ch <- j
	}
	bar := make(chan struct{})
	ch <- inboxJob{done: bar}
	<-bar
}

// loadLanes restores the member's lanes for THIS project only (a
// missing file/lane or a corrupt file = empty lanes; durability must
// never refuse adoption). Other projects' lanes stay on disk
// untouched.
func (d *Dispatcher) loadLanes(m *member) {
	path, err := d.inboxPath(m.name)
	if err != nil {
		return
	}
	inboxMu.Lock()
	defer inboxMu.Unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var file map[string]inboxLanes
	if json.Unmarshal(b, &file) != nil {
		return
	}
	lanes := file[d.projectKey]
	if len(lanes.Queue) > QueueCap {
		lanes.Queue = lanes.Queue[:QueueCap]
	}
	if len(lanes.ReadQueue) > QueueCap {
		lanes.ReadQueue = lanes.ReadQueue[:QueueCap]
	}
	// readq 对齐（ackq 的补法在这里补齐历史欠账）：旧格式/手改文件
	// 可短可长——短的补 nil（该线不带已读货，送达即送达），长的随线
	// 裁齐；这是装进 laneEntry 前的最后一道对齐。
	for len(lanes.ReadQueue) < len(lanes.Queue) {
		lanes.ReadQueue = append(lanes.ReadQueue, nil)
	}
	if len(lanes.ReadQueue) > len(lanes.Queue) {
		lanes.ReadQueue = lanes.ReadQueue[:len(lanes.Queue)]
	}
	if len(lanes.AckQueue) > QueueCap {
		lanes.AckQueue = lanes.AckQueue[:QueueCap]
	}
	// a pre-ack file (or a shorter read history) can leave ackq shorter
	// than queue — pad with nil so the lanes stay index-parallel; those
	// entries degrade to a visible reply instead of a receipt.
	for len(lanes.AckQueue) < len(lanes.Queue) {
		lanes.AckQueue = append(lanes.AckQueue, nil)
	}
	if len(lanes.AckQueue) > len(lanes.Queue) {
		lanes.AckQueue = lanes.AckQueue[:len(lanes.Queue)]
	}
	// queueAt keeps the same index-parallel discipline: a pre-stamp file
	// pads with zeros (= fresh — pre-upgrade entries drain without a
	// 排队说明, exactly their pre-upgrade behavior), an over-long tail
	// trims with its entries.
	for len(lanes.QueueAt) < len(lanes.Queue) {
		lanes.QueueAt = append(lanes.QueueAt, 0)
	}
	if len(lanes.QueueAt) > len(lanes.Queue) {
		lanes.QueueAt = lanes.QueueAt[:len(lanes.Queue)]
	}
	// queueFrom (v2.11 L2) same discipline: a pre-field file pads ""
	// (= a dispatcher line — never merged), an over-long tail trims.
	for len(lanes.QueueFrom) < len(lanes.Queue) {
		lanes.QueueFrom = append(lanes.QueueFrom, "")
	}
	if len(lanes.QueueFrom) > len(lanes.Queue) {
		lanes.QueueFrom = lanes.QueueFrom[:len(lanes.Queue)]
	}
	// imgq (输入图片) same discipline: a pre-image file pads nils (the
	// parked line drains text-only, its pre-image behavior), an
	// over-long tail trims with its entry.
	for len(lanes.ImgQueue) < len(lanes.Queue) {
		lanes.ImgQueue = append(lanes.ImgQueue, nil)
	}
	if len(lanes.ImgQueue) > len(lanes.Queue) {
		lanes.ImgQueue = lanes.ImgQueue[:len(lanes.Queue)]
	}
	if len(lanes.Inject) > InjectCap {
		lanes.Inject = lanes.Inject[len(lanes.Inject)-InjectCap:]
	}
	if len(lanes.ReadInject) > InjectCap {
		lanes.ReadInject = lanes.ReadInject[len(lanes.ReadInject)-InjectCap:]
	}
	// 对齐只在这道边界需要（legacy/手改文件可短可长）：补齐裁齐后
	// 装进 laneEntry——往后对齐是类型保证，不再是每处写点的纪律。
	m.lanes.queue = make([]laneEntry, len(lanes.Queue))
	for i := range lanes.Queue {
		m.lanes.queue[i] = laneEntry{text: lanes.Queue[i], at: lanes.QueueAt[i], from: lanes.QueueFrom[i],
			read: lanes.ReadQueue[i], ack: lanes.AckQueue[i], imgs: lanes.ImgQueue[i]}
	}
	m.lanes.inject = make([]injectEntry, len(lanes.Inject))
	for i := range lanes.Inject {
		m.lanes.inject[i] = injectEntry{text: lanes.Inject[i], read: lanes.ReadInject[i]}
	}
	m.lanes.injDropped = lanes.InjDropped
}

// WipeProjectLanes erases ONE project's lane cargo from every member's
// inbox file (~/.niuma/inbox/<name>.json 的项目分键) — the dismiss/
// reset/delete faces' half: undelivered cargo for wiped people is dead
// weight, and a recycled name must never inherit its predecessor's
// backlog（2026-10-04 book 事故实录：重置后复用的「小苗」接到了前任
// 小苗重置前的排队批，只好自行判废）。Other projects' lanes in the
// same file stay untouched; a file left with no lanes is removed. dir
// "" = the home default（inboxPath 同规）. Returns the files touched.
func WipeProjectLanes(dir, projectKey string) (int, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return 0, err
		}
		dir = filepath.Join(home, ".niuma", "inbox")
	}
	inboxMu.Lock()
	defer inboxMu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // no inbox at all: nothing to wipe
		}
		return 0, err
	}
	touched := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue // a file we cannot read is not ours to judge
		}
		var file map[string]inboxLanes
		if json.Unmarshal(b, &file) != nil {
			continue // corrupt: saveLanes' own corrupt-yields rule
		}
		if _, hit := file[projectKey]; !hit {
			continue
		}
		delete(file, projectKey)
		if len(file) == 0 {
			if err := os.Remove(path); err != nil {
				return touched, err
			}
			touched++
			continue
		}
		nb, err := json.Marshal(file)
		if err != nil {
			return touched, err
		}
		if err := persist.Save(path, nb, 0o600); err != nil {
			return touched, err
		}
		touched++
	}
	return touched, nil
}
