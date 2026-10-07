package chat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/util"
)

// The room's durable memory: the append-only jsonl chat history (v2
// P3-c) and the seat snapshots restarts hand over (Seats/RestoreSeats,
// v0.10 t_32/t_83).

// SetHistoryPath enables history persistence (history/<key>.jsonl, one
// JSON frame per line — v2 P3-c): the file is loaded immediately
// (missing file starts empty; a corrupt line is skipped — persistence
// must never refuse boot), and every later history-recorded append
// writes ONE line (model §4.4: appending is the event stream's natural
// shape; the array snapshot's whole-file rewrite amplification is
// gone). Call before any member joins.
func (h *Hub) SetHistoryPath(path string) {
	h.mu.Lock()
	h.attachReadsLocked(path)  // the reads ledger rides the history slot; a persisted floor wins
	h.attachAcksLocked(path)   // the acks ledger rides it too (same discipline)
	h.attachReactsLocked(path) // the reacts ledger rides it too (same discipline)
	h.historyPath = path
	h.loadHistoryLocked()
	h.startFlusherLocked()
	// a fresh room has no history file, so restoreLocked never ran —
	// floor all three ledgers here anyway (it only fills a zero value)
	h.floorReadsLocked(h.lastSeq + 1)
	h.floorAcksLocked(h.lastSeq + 1)
	h.floorReactsLocked(h.lastSeq + 1)
	h.mu.Unlock()
}

// loadHistoryLocked restores the persisted history: the newest
// historyCap frames reoccupy the in-memory ring and lastSeq is floored
// at the newest restored seq, so the monotonic order never rewinds
// across restarts — the pre-P3-c restore semantics, unchanged. A file
// a pre-P3-c writer left as a JSON-array snapshot (the P3-a/P3-b era)
// is restored the old way and converted to jsonl once, in place.
// Callers hold h.mu.
func (h *Hub) loadHistoryLocked() {
	b, err := os.ReadFile(h.historyPath)
	if err != nil || len(b) == 0 {
		return // absent: start empty, boot continues
	}
	if b[0] == '[' {
		var restored []Message
		if err := json.Unmarshal(b, &restored); err != nil {
			log.Printf("chat: history file %s unreadable (%v); starting empty", h.historyPath, err)
			return // corrupt: degrade to empty, never refuse boot
		}
		h.restoreLocked(restored)
		h.rewriteJSONLLocked(restored)
		return
	}
	restored := make([]Message, 0, historyCap)
	bad := 0
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			bad++ // a torn tail from a crash mid-append: skip it
			continue
		}
		restored = append(restored, m)
	}
	if bad > 0 {
		log.Printf("chat: history file %s: skipped %d unreadable line(s)", h.historyPath, bad)
	}
	h.restoreLocked(restored)
}

// restoreLocked installs the restored frames: trim past the ring cap,
// floor lastSeq at the newest seq (the file's last line — appends are
// seq-monotonic — sits inside the kept tail, so flooring over the tail
// equals flooring over the whole file), and floor the read ledger's
// tracking start one past it — restored history predates this boot's
// receipts. Callers hold h.mu.
func (h *Hub) restoreLocked(restored []Message) {
	if len(restored) > historyCap {
		restored = restored[len(restored)-historyCap:]
	}
	h.history = restored
	for _, m := range restored {
		if m.Seq > h.lastSeq {
			h.lastSeq = m.Seq
		}
	}
	h.floorReadsLocked(h.lastSeq + 1)
	h.floorAcksLocked(h.lastSeq + 1)   // restored history predates this boot's 收到 chips too
	h.floorReactsLocked(h.lastSeq + 1) // …and its 表情 chips as well
}

// rewriteJSONLLocked rewrites the whole file as jsonl (atomic
// temp + rename) — the one-time conversion of a legacy JSON-array
// snapshot; every later append extends the converted log. A failed
// conversion logs and continues: the in-memory restore stands and the
// loader skips whatever it cannot parse. Callers hold h.mu.
func (h *Hub) rewriteJSONLLocked(frames []Message) {
	writeHistoryFile(h.historyPath, frames)
}

// historyWrite is one queued history line, or a barrier: done != nil
// means "nothing to write — signal after everything queued before me
// has landed" (FlushHistory's drain token; the channel's FIFO order
// is the ordering guarantee).
type historyWrite struct {
	line []byte
	done chan struct{}
}

// ledgerWrite is the seq-ledgers' sibling of historyWrite: one queued
// full-file snapshot (.reads/.acks/.reacts.json — atomic rewrite via
// persist.Save), or a barrier (done != nil, FlushLedgers' drain
// token). A dropped snapshot costs staleness, never truth: the
// in-memory ledger is the truth and the next mark re-enqueues a
// fresher full rewrite (that is why the hot path may drop while the
// history path may not — an append is unrecoverable, a snapshot is
// idempotent).
type ledgerWrite struct {
	path string
	data []byte
	done chan struct{}
}

// startFlusherLocked launches the room's ordered history flusher. The
// say path only marshals and enqueues under h.mu — the open/write/close
// IO leaves the critical section (this file used to carry the hub's
// structural ceiling: every say serialized on one file append inside
// the single room mutex). Disk order stays seq order via channel FIFO.
// A full queue drops with a log line: the live room must never block
// on disk, and a lost line is the old failed-append discipline anyway.
// Callers hold h.mu.
func (h *Hub) startFlusherLocked() {
	if h.pend != nil || h.historyPath == "" {
		return
	}
	h.pend = make(chan historyWrite, 1024)
	go h.flushHistoryLoop(h.pend)
	// the ledger flusher rides the same start gate (one goroutine for
	// all three ledgers — their snapshots are independent files, order
	// between them doesn't matter, order per file is channel FIFO).
	h.ledgerSave = make(chan ledgerWrite, 64)
	go h.flushLedgerLoop(h.ledgerSave)
}

// flushHistoryLoop is the flusher goroutine: one open/write/close per
// BATCH (not per line) while messages flow, parked on the queue when
// idle. Barriers ride the queue in order and close their channel once
// every preceding line is written.
func (h *Hub) flushHistoryLoop(ch chan historyWrite) {
	for w := range ch {
		h.flushHistoryBatch(w, ch)
	}
	// 通道退役完毕（屏障落地＋close）：接管后继通道。迁移只发生在
	// close 上——按字段热迁移会在退役缓冲还有工作时就跳走，把屏障
	// 变成孤儿（换通道排干的实测死锁形状）。递归深度＝重写次数。
	h.pendSwapMu.Lock()
	next := h.pend
	h.pendSwapMu.Unlock()
	if next != ch {
		h.flushHistoryLoop(next)
	}
}

// flushHistoryBatch drains one open/write/close batch starting at w
// (the loop body, extracted for its panic fence). The fence is
// load-bearing: a dead flusher goroutine would wedge every later
// FlushHistory barrier — a hung studio, not a lost line — so a
// panicking batch degrades to a dropped batch with its barrier still
// closed: the waiter may observe a torn tail, never a hang. owed
// tracks the barrier this batch has pulled but not yet closed; f
// closes on the panic path too (one fd per panic is the cheaper
// accident).
func (h *Hub) flushHistoryBatch(w historyWrite, ch chan historyWrite) {
	var owed chan struct{}
	var f *os.File
	defer func() {
		if r := recover(); r != nil {
			util.LogPanic("chat: history flusher", r)
			if owed != nil {
				close(owed)
			}
		}
		if f != nil {
			f.Close()
		}
	}()
	if w.done != nil {
		close(w.done)
		return
	}
	f, err := os.OpenFile(h.historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("chat: history append: %v", err)
		return
	}
	for w.line != nil || w.done != nil {
		owed = w.done
		if w.line != nil {
			if _, err := f.Write(w.line); err != nil {
				log.Printf("chat: history append: %v", err)
			}
		} else {
			close(w.done)
		}
		owed = nil
		select {
		case w = <-ch:
		default:
			w = historyWrite{}
		}
	}
}

// FlushHistory drains the room's pending history writes: the barrier
// rides the queue's FIFO tail, so everything enqueued before this call
// has landed when it returns. Every FILE reader (the /history catch-up
// face) and every exit path drains first — the ring is the truth, the
// file is the read model, this closes the gap on demand. The blocking
// send sits on NO lock (callers must NOT hold h.mu — the durable
// rewrites' own drains go through swapPendLocked's out-of-lock path).
func (h *Hub) FlushHistory() {
	h.pendSwapMu.Lock()
	ch := h.pend
	h.pendSwapMu.Unlock()
	if ch == nil {
		return
	}
	bar := make(chan struct{})
	ch <- historyWrite{done: bar}
	<-bar
}

// swapPendLocked replaces the history queue with a fresh one and
// answers the old — the out-of-lock drain's first half (reset/prune):
// producers (under h.mu) hereafter enqueue on the NEW queue, so the
// old queue's remaining writes can be barriered to disk with h.mu
// dropped. Callers hold h.mu.
func (h *Hub) swapPendLocked() chan historyWrite {
	h.pendSwapMu.Lock()
	old := h.pend
	h.pend = make(chan historyWrite, cap(old))
	h.pendSwapMu.Unlock()
	return old
}

// swapLedgerSaveLocked replaces the ledger queue with a fresh one and
// answers the old (same contract as swapPendLocked; the ledgerSave
// FIELD lives under ledgerSwapMu because the flusher snapshots it per
// wake). Callers hold h.mu.
func (h *Hub) swapLedgerSaveLocked() chan ledgerWrite {
	h.ledgerSwapMu.Lock()
	old := h.ledgerSave
	h.ledgerSave = make(chan ledgerWrite, cap(old))
	h.ledgerSwapMu.Unlock()
	return old
}

// retirePend completes one queue's retirement: the barrier proves
// everything ever enqueued on it has landed (or degraded per the panic
// fence), then the close hands the consumer to its successor. Takes no
// locks — that is the point. Producers were frozen out at the swap
// (they read the field under h.mu), so nothing can arrive between the
// barrier and the close.
func retirePend(ch chan historyWrite) {
	if ch == nil {
		return
	}
	bar := make(chan struct{})
	ch <- historyWrite{done: bar}
	<-bar
	close(ch)
}

// barrierLedgers barriers the CURRENT queue (drainLedgers's tail —
// no retirement, no close: the queue keeps serving).
func barrierLedgers(ch chan ledgerWrite) {
	if ch == nil {
		return
	}
	bar := make(chan struct{})
	ch <- ledgerWrite{done: bar}
	<-bar
}

// retireLedgers is retirePend's ledger twin (barrier + close).
func retireLedgers(ch chan ledgerWrite) {
	if ch == nil {
		return
	}
	bar := make(chan struct{})
	ch <- ledgerWrite{done: bar}
	<-bar
	close(ch)
}

// releaseHoldLocked re-enqueues the drain window's parked lines (the
// durable rewrite is done; they must land). Queue-full drops keep the
// standing loss discipline (disk bottleneck, room lives). Callers hold
// h.mu.
func (h *Hub) releaseHoldLocked(discard bool) {
	held := h.pendHold
	h.pendHold = nil
	h.holding = false
	if discard {
		return
	}
	for _, w := range held {
		select {
		case h.pend <- w:
		default:
		}
	}
}

// flushLedgerLoop is the seq-ledgers' flusher goroutine: one
// persist.Save per queued snapshot (atomic temp+fsync+rename), parked
// on the queue when idle. Barriers ride in order. A failed write logs
// and the live room carries on — the ledger file is a read model.
func (h *Hub) flushLedgerLoop(ch chan ledgerWrite) {
	for w := range ch {
		h.flushLedgerOne(w)
	}
	// 同 flushHistoryLoop：只在 close 上迁移到后继通道。
	h.ledgerSwapMu.Lock()
	next := h.ledgerSave
	h.ledgerSwapMu.Unlock()
	if next != ch {
		h.flushLedgerLoop(next)
	}
}

// flushLedgerOne is one queued snapshot (or barrier), extracted for
// its panic fence — same doctrine as flushHistoryBatch: a dead ledger
// flusher would wedge drainLedgersLocked's blocking sends (they run
// under h.mu, the whole room would hang), so a panicking write
// degrades to one stale snapshot, never a dead pump. The barrier
// branch closes before any fallible work, so the recover path has no
// debt to settle.
func (h *Hub) flushLedgerOne(w ledgerWrite) {
	defer func() {
		if r := recover(); r != nil {
			util.LogPanic("chat: ledger flusher", r)
		}
	}()
	if w.done != nil {
		close(w.done)
		return
	}
	if err := persist.Save(w.path, w.data, 0o600); err != nil {
		log.Printf("chat: ledger save %s: %v", w.path, err)
	}
}

// queueLedgerSaveLocked hands one marshaled ledger snapshot to the
// flusher. Hot path (block=false): a full queue leaves *dirty set —
// the snapshot is retried by the next mark or swept by
// FlushLedgers/drainLedgersLocked (staleness, never loss). Flush path
// (block=true): the send blocks — safe under h.mu because the flusher
// never takes h.mu, so the queue always drains. Callers hold h.mu.
func (h *Hub) queueLedgerSaveLocked(dirty *bool, path string, data []byte, block bool) {
	if h.ledgerHold {
		// 换通道排干窗口：快照不追旧通道（会在删除/重写后复活旧
		// 文件），留 dirty 待新世界落定后重试（staleness-never-loss）。
		*dirty = true
		return
	}
	if h.ledgerSave == nil {
		// no flusher (in-memory room): the legacy synchronous write.
		if err := persist.Save(path, data, 0o600); err != nil {
			log.Printf("chat: ledger save %s: %v", path, err)
		}
		*dirty = false
		return
	}
	if block {
		h.ledgerSave <- ledgerWrite{path: path, data: data}
		*dirty = false
		return
	}
	select {
	case h.ledgerSave <- ledgerWrite{path: path, data: data}:
		*dirty = false
	default:
		*dirty = true
	}
}

// sweepLedgersLocked marshals every dirty ledger snapshot into queue
// writes (clearing the flags): the drain's SNAP-SHOT half, taken under
// h.mu. Callers hold h.mu.
func (h *Hub) sweepLedgersLocked() []ledgerWrite {
	var out []ledgerWrite
	if h.reads.dirty {
		if b := h.marshalReadsLocked(); b != nil && h.reads.path != "" {
			out = append(out, ledgerWrite{path: h.reads.path, data: b})
		}
		h.reads.dirty = false
	}
	if h.acks.dirty {
		if b := h.marshalAcksLocked(); b != nil && h.acks.path != "" {
			out = append(out, ledgerWrite{path: h.acks.path, data: b})
		}
		h.acks.dirty = false
	}
	if h.reacts.dirty {
		if b := h.marshalReactsLocked(); b != nil && h.reacts.path != "" {
			out = append(out, ledgerWrite{path: h.reacts.path, data: b})
		}
		h.reacts.dirty = false
	}
	return out
}

// drainLedgers forces every dirty ledger snapshot out and barriers
// the queue — 快照锁内取、阻塞发送锁外做 (the flusher 家法 the
// in-lock blocking sends used to break): the sweep marshals under
// h.mu, then the lock DROPS for the blocking sends and the barrier
// wait. Order stays the queue's FIFO: a concurrent mark racing the
// gap lands its snapshot first, the sweep's fuller snapshot
// supersedes it, the barrier closes the tail. On return every
// snapshot enqueued or swept before the barrier has landed on disk.
// The boot's attach readers, the reset/purge file faces and the exit
// paths are the ledger files' only readers; they all drain through
// here.
func (h *Hub) drainLedgers() {
	h.ledgerSwapMu.Lock()
	ch := h.ledgerSave
	h.ledgerSwapMu.Unlock()
	if ch == nil {
		return
	}
	h.mu.Lock()
	writes := h.sweepLedgersLocked()
	h.mu.Unlock()
	for _, w := range writes {
		ch <- w
	}
	barrierLedgers(ch)
}

// FlushLedgers is drainLedgers's public face. Callers must NOT hold
// h.mu (the drain drops it internally). The restart tests' farewell
// drain and the registry's unload path ride here.
func (h *Hub) FlushLedgers() {
	h.drainLedgers()
}

// appendHistoryLocked records msg as one pending jsonl line — marshal
// under the lock (cheap, CPU-bound), enqueue for the flusher, never
// touch the file from the critical section. A failed marshal or a full
// queue drops the line with the old discipline: a lost persisted line
// must never break the live room. Callers hold h.mu.
func (h *Hub) appendHistoryLocked(msg Message) {
	if h.historyPath == "" || h.pend == nil {
		return
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if h.holding {
		// 换通道排干窗口：行先泊住（无损），重写落地后再入队。
		if len(h.pendHold) < 4096 {
			h.pendHold = append(h.pendHold, historyWrite{line: append(line, '\n')})
		}
		return
	}
	select {
	case h.pend <- historyWrite{line: append(line, '\n')}:
	default:
		log.Printf("chat: history append queue full — 一行已丢弃（磁盘是瓶颈；房间照常）")
	}
}

// ScanHistory streams path's jsonl frames in file order to fn (return
// false stops the walk) — the read side of the append-only log and the
// full-history form beyond the in-memory ring of 100 (US-C4). A
// missing file is an empty history, not an error; unparsable lines are
// skipped. A plain file read: no locks, no seats, the room keeps
// serving while the log is walked.
func ScanHistory(path string, fn func(Message) bool) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if !fn(m) {
			return nil
		}
	}
	return sc.Err()
}

// SeatSnapshot is one seat in the room snapshot (v0.10 seat-M2): live
// members and grace ghosts alike, so a takeover restart hands the new
// instance the roster to restore as grace seats. Token is the seat
// credential slot (seat-M1 fills it; empty until then).
type SeatSnapshot struct {
	Name     string `json:"name"`
	Role     string `json:"role,omitempty"`
	Token    string `json:"token,omitempty"`
	Ghost    bool   `json:"ghost"`
	Deadline int64  `json:"deadline,omitempty"` // ms, ghosts only

	// Working / WorkingSince are the farewell snapshot's mid-turn
	// catch: this seat's member was running a ZCode turn when the
	// studio quit. The restart rolls them into the owner's continue
	// prompt — the turn itself never replays (unknown-outcome
	// discipline), the session context does. Live seats only; ghosts
	// never work anywhere. Since is seconds (Member.WorkingSince).
	Working      bool  `json:"working,omitempty"`
	WorkingSince int64 `json:"working_since,omitempty"`

	// ContextNotes (r_20 t_195) is the interrupted seat's 引用式
	// 接续要点——the farewell snapshot distills it at save time (先采
	// 要点再 teardown) and the restart's resume injection replays it.
	// 告别快照的冻结契约（引用不概括）在这里落盘。
	ContextNotes string `json:"context_notes,omitempty"`
}

// Seats snapshots every seat worth restoring: live members and
// grace-period ghosts, excluding the local owner's own seat (the new
// instance rebuilds it — main joins the GUI player itself).
func (h *Hub) Seats() []SeatSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]SeatSnapshot, 0, len(h.clients)+len(h.ghosts))
	for c := range h.clients {
		if c == h.ownerClient {
			continue
		}
		out = append(out, SeatSnapshot{Name: c.member.Name, Role: c.member.Role, Token: c.seatToken,
			Working: c.member.Working, WorkingSince: c.member.WorkingSince})
	}
	for _, g := range h.ghosts {
		if g.member.Name == h.owner {
			continue
		}
		out = append(out, SeatSnapshot{Name: g.member.Name, Role: g.member.Role,
			Token: g.token, Ghost: true, Deadline: g.deadline.UnixMilli()})
	}
	return out
}

// LastSeq reports the current monotonic message counter (snapshot
// handover, v0.10 seat-M2).
func (h *Hub) LastSeq() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastSeq
}

// FloorSeq raises the monotonic counter to at least seq (snapshot
// restore: the new instance must not rewind below the old one's last
// delivered message).
func (h *Hub) FloorSeq(seq int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if seq > h.lastSeq {
		h.lastSeq = seq
	}
}

// RestoreSeats re-seats a snapshot as grace-period ghosts (v0.10
// seat-M2): never a live seat — a live seat needs a live connection —
// each with a fresh full grace window so returning members reclaim
// their exact name with zero -2. The seat's credential rides along:
// without it every restart minted a fresh token per member, and any
// save/read timing gap between the re-keyed seat and the CLI's
// credential file opened a "-2" window (小狐-2, r_19). Carrying the
// token keeps credentials restart-STABLE — the dispatcher's adopt
// reclaims the ghost and continues the same token, the member's own
// one-shot CLI still supersedes — and it also restores the mismatch
// guard: a token-carrying stranger can no longer reclaim the ghost
// (empty ghost tokens short-circuit the check for ANY caller).
// Restoration is silent: no history, no broadcasts. Call before
// members join.
func (h *Hub) RestoreSeats(seats []SeatSnapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, st := range seats {
		if st.Name == "" || st.Name == h.owner {
			continue
		}
		// a same-name live member (owner-side early join) wins over the
		// snapshot — presence beats persistence
		found := false
		for c := range h.clients {
			if c.member.Name == st.Name {
				found = true
				break
			}
		}
		if found {
			continue
		}
		shirt, hair := paletteFor(st.Name)
		h.ghosts[st.Name] = &ghostEntry{
			member:   Member{Name: st.Name, Role: st.Role, Color: shirt, Hair: hair},
			deadline: time.Now().Add(h.grace),
			token:    st.Token,
		}
	}
	h.startSweeperLocked()
}

// SetOwner exempts name from @所有人 roll-calls: the owner is never
// required to reply. Call it once, after the owner joins.

// History returns a copy of the retained chat history.
func (h *Hub) History() []Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Message(nil), h.history...)
}
