package chat

// The room's read receipts (飞书式已读): per-message facts about which
// members have had a say line actually delivered into their ZCode
// session. The dispatch dispatcher is the only read-EVENT source — chat
// cannot see sessions, so "read" is whatever the dispatcher declares at
// the moment its bridge.Send acks (a mention-wake injection, or the
// background lane's flush riding the next one; a busy turn's queued
// line reads when it is finally sent, never before). The hub is the
// STATE store and broadcaster: MarkRead records + broadcasts a
// reminder-only "read" frame, ReadReceipts serves the HTTP snapshot,
// and the ledger persists beside the room's history so chips survive
// restarts.
//
// Honesty rules the ledger keeps (a receipt must never lie):
//   - only say lines count — reports never reach model sessions and
//     system lines are the room's own voice, so neither earns chips;
//   - a seq below `since` (the tracking floor) is ignored: history that
//     predates the ledger's boot shows no chips rather than a stale
//     未读, and a persisted floor keeps that cut stable across restarts.

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/WWestC/Niuma_Studio/util"
)

// readsCap bounds the ledger: one entry per tracked say line. The chat
// history ring keeps 100 lines and the workbench message window 300 —
// a ledger past that serves chips nobody can render anymore; the
// oldest (smallest seq — seqs are monotonic) entries evict first.
const readsCap = 300

// ReadRow is one message's reader set (the HTTP projection's row).
type ReadRow struct {
	Seq     int64    `json:"seq"`
	Readers []string `json:"readers"`
}

// readLedger is the hub's read-receipt state. Every field is accessed
// under the hub's own mutex — the ledger keeps no lock of its own.
type readLedger struct {
	bySeq map[int64]map[string]struct{}
	// since is the first tracked seq: 0 = track everything (fresh
	// in-memory hubs, tests); a boot with a history file floors it at
	// the restored tail's newest seq + 1; a persisted ledger carries
	// its own floor across restarts, whichever is set first wins.
	since int64
	path  string // history/<key>.reads.json; "" = in-memory only
	// dirty: the last marshaled snapshot could not enqueue (queue
	// full) — the next mark or FlushLedgers retries. Staleness of the
	// read model, never loss of the in-memory truth.
	dirty bool
}

// MarkRead records one member's read of the named say seqs and, for
// every fact that is actually new, broadcasts one "read" frame to the
// room. Idempotent per (seq, reader); untracked seqs (below the floor,
// absent from the history ring, not say lines) are ignored silently.
// Safe for concurrent use — the dispatcher's delivery paths race.
func (h *Hub) MarkRead(reader string, seqs ...int64) {
	if reader == "" || len(seqs) == 0 {
		return
	}
	h.mu.Lock()
	var fresh []int64
	for _, seq := range seqs {
		if seq <= 0 || seq < h.reads.since {
			continue
		}
		if !h.sayBySeqLocked(seq) {
			continue
		}
		if h.reads.bySeq == nil {
			h.reads.bySeq = make(map[int64]map[string]struct{})
		}
		set := h.reads.bySeq[seq]
		if set == nil {
			set = make(map[string]struct{})
			h.reads.bySeq[seq] = set
		}
		if _, dup := set[reader]; dup {
			continue
		}
		set[reader] = struct{}{}
		fresh = append(fresh, seq)
	}
	if len(fresh) == 0 {
		h.mu.Unlock()
		return
	}
	h.pruneReadsLocked()
	h.saveReadsLocked()
	frame := Message{Type: MsgRead, From: reader, Seqs: fresh, TS: util.Now()}
	h.mu.Unlock()
	h.Broadcast(frame) // reminder-only: re-locks briefly, ordering is irrelevant
}

// ReadReceipts snapshots the ledger: the tracking floor plus every
// recorded row, oldest first, reader names sorted. Rows below the
// floor cannot render chips and are dropped.
func (h *Hub) ReadReceipts() (int64, []ReadRow) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.readReceiptsLocked()
}

// sayBySeqLocked reports whether the history ring holds a SAY frame
// with this seq. Reports and system lines never earn receipts. Callers
// hold h.mu.
func (h *Hub) sayBySeqLocked(seq int64) bool {
	for i := range h.history {
		if h.history[i].Seq == seq {
			return h.history[i].Type == MsgSay
		}
	}
	return false
}

// pruneReadsLocked evicts the oldest entries past the cap. Callers
// hold h.mu.
func (h *Hub) pruneReadsLocked() {
	if len(h.reads.bySeq) <= readsCap {
		return
	}
	seqs := make([]int64, 0, len(h.reads.bySeq))
	for seq := range h.reads.bySeq {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, seq := range seqs[:len(seqs)-readsCap] {
		delete(h.reads.bySeq, seq)
	}
}

// attachHistory derives the ledger's persistence slot from the room's
// history slot (history/<key>.reads.json) and loads what it holds.
// Call once at boot, before loadHistoryLocked floors the seq counter
// (SetHistoryPath's contract): a persisted floor wins, an absent or
// corrupt file degrades to the computed one — persistence must never
// refuse boot. Callers hold h.mu.
func (h *Hub) attachReadsLocked(historyPath string) {
	if historyPath == "" {
		return
	}
	h.reads.path = strings.TrimSuffix(historyPath, ".jsonl") + ".reads.json"
	b, err := os.ReadFile(h.reads.path)
	if err != nil || len(b) == 0 {
		return // absent: the boot computes its own floor
	}
	var disk struct {
		Since int64     `json:"since"`
		Reads []ReadRow `json:"reads"`
	}
	if json.Unmarshal(b, &disk) != nil {
		log.Printf("chat: reads file %s unreadable; starting fresh", h.reads.path)
		return
	}
	if disk.Since > 0 {
		h.reads.since = disk.Since // a zero floor (pre-fix file) yields to the computed one
	}
	if h.reads.bySeq == nil {
		h.reads.bySeq = make(map[int64]map[string]struct{}, len(disk.Reads))
	}
	for _, row := range disk.Reads {
		set := make(map[string]struct{}, len(row.Readers))
		for _, name := range row.Readers {
			set[name] = struct{}{}
		}
		h.reads.bySeq[row.Seq] = set
	}
}

// floorReadsLocked marks the boot's tracking start (the restored
// history's newest seq + 1) — honored only when no persisted ledger
// carried a floor across the restart. Callers hold h.mu.
func (h *Hub) floorReadsLocked(seq int64) {
	if h.reads.since == 0 {
		h.reads.since = seq
	}
}

// saveReadsLocked persists the whole ledger: marshal under the lock
// (cheap, CPU-bound), hand the bytes to the ordered ledger flusher —
// the tmp+fsync+rename IO left the critical section (the history
// append's discipline, finally the ledgers' too). A full queue drops
// the snapshot to dirty (retried by the next mark or the flush
// barrier). Callers hold h.mu.
func (h *Hub) saveReadsLocked() {
	b := h.marshalReadsLocked()
	if b == nil {
		return
	}
	h.queueLedgerSaveLocked(&h.reads.dirty, h.reads.path, b, false)
}

// saveReadsForceLocked is the flush path's retry: a blocking enqueue
// (the flusher never takes h.mu, so it always drains). Callers hold
// h.mu.
func (h *Hub) saveReadsForceLocked() {
	if h.reads.path == "" {
		return
	}
	if b := h.marshalReadsLocked(); b != nil {
		h.queueLedgerSaveLocked(&h.reads.dirty, h.reads.path, b, true)
	}
}

// marshalReadsLocked renders the whole ledger as its persisted JSON
// (nil = nothing to write: no path, or a marshal failure the room
// logs away elsewhere). A few hundred rows at most. Callers hold h.mu.
func (h *Hub) marshalReadsLocked() []byte {
	if h.reads.path == "" {
		return nil
	}
	_, rows := h.readReceiptsLocked()
	b, err := json.Marshal(struct {
		Since int64     `json:"since"`
		Reads []ReadRow `json:"reads"`
	}{Since: h.reads.since, Reads: rows})
	if err != nil {
		return nil
	}
	return b
}

// readReceiptsLocked is ReadReceipts' under-the-lock body (saveReads
// shares it). Callers hold h.mu.
func (h *Hub) readReceiptsLocked() (int64, []ReadRow) {
	seqs := make([]int64, 0, len(h.reads.bySeq))
	for seq := range h.reads.bySeq {
		if seq >= h.reads.since {
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	rows := make([]ReadRow, 0, len(seqs))
	for _, seq := range seqs {
		names := make([]string, 0, len(h.reads.bySeq[seq]))
		for n := range h.reads.bySeq[seq] {
			names = append(names, n)
		}
		sort.Strings(names)
		rows = append(rows, ReadRow{Seq: seq, Readers: names})
	}
	return h.reads.since, rows
}
