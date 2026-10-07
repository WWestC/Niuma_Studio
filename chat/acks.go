package chat

// The room's ack receipts (飞书式收到): per-message facts about which
// members have acknowledged a say line — the loop-breaker for
// @-ping-pong. A member @-addressed by a message that needs no answer
// (FYI only) acks it instead of replying; the ack is a fact about the
// ORIGINAL line, never a new chat message, so it can never mention
// anyone, wake anyone, or be replied to — the infinite @-echo loop is
// dead by construction, not by etiquette.
//
// Two sources feed the ledger:
//   - the dispatcher (chat cannot see sessions): a member turn whose
//     reply is exactly 「收到」 to a mention-wake injection is swallowed
//     into a receipt for the seqs that turn carried — the model's own
//     judgment decides "no answer needed", the dispatcher's pure rule
//     does the conversion (dispatch/dispatcher.go);
//   - any client: the owner's workbench face (and any seated member)
//     sends {"type":"ack","seq":N} — the human counterpart of the same
//     gesture.
//
// The hub is the STATE store and broadcaster: MarkAck records +
// broadcasts a reminder-only "ack" frame (the read-frame contract —
// never stored in the chat history), AckReceipts serves the HTTP
// snapshot (GET /p/{key}/acks), and the ledger persists beside the
// room's history so chips survive restarts.
//
// Honesty rules the ledger keeps (a receipt must never lie):
//   - only member conversation lines count — say AND report: both are
//     someone talking to the room, both carry @-mentions that wake
//     members, so both are answerable with a 收到 (a mention inside a
//     report must not swallow its bare 「收到」 into nowhere); system
//     lines are the room's own voice and earn nothing;
//   - nobody acks their own line — an ack answers someone;
//   - a seq below `since` (the tracking floor) is ignored: history that
//     predates the ledger's boot shows no chips rather than a stale
//     未收到, and a persisted floor keeps that cut stable across
//     restarts.

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/WWestC/Niuma_Studio/util"
)

// acksCap bounds the ledger — one entry per tracked say line, the same
// bound the read ledger keeps (chat/reads.go's readsCap rationale: the
// chat history ring keeps 100 lines and the workbench window 300).
const acksCap = 300

// AckRow is one message's acker set (the HTTP projection's row).
type AckRow struct {
	Seq    int64    `json:"seq"`
	Ackers []string `json:"ackers"`
}

// ackLedger is the hub's ack-receipt state. Every field is accessed
// under the hub's own mutex — the ledger keeps no lock of its own (the
// readLedger discipline).
type ackLedger struct {
	bySeq map[int64]map[string]struct{}
	// since is the first tracked seq: 0 = track everything (fresh
	// in-memory hubs, tests); the boot floors it at the restored tail's
	// newest seq + 1; a persisted ledger carries its own floor across
	// restarts, whichever is set first wins.
	since int64
	path  string // history/<key>.acks.json; "" = in-memory only
	// dirty: the last marshaled snapshot could not enqueue (queue
	// full) — retried by the next mark or the flush barrier (the
	// readLedger discipline).
	dirty bool
}

// MarkAck records one member's 收到 of the named say seqs and, for
// every fact that is actually new, broadcasts one "ack" frame to the
// room. Returns the seqs that landed as fresh facts (empty = nothing
// recorded — the seatless owner face's receipt discipline). Idempotent
// per (seq, acker); untracked seqs (below the floor, absent from the
// history ring, not member conversation lines, the acker's own lines)
// are ignored silently. Safe for concurrent use.
func (h *Hub) MarkAck(acker string, seqs ...int64) []int64 {
	if acker == "" || len(seqs) == 0 {
		return nil
	}
	h.mu.Lock()
	var fresh []int64
	for _, seq := range seqs {
		if seq <= 0 || seq < h.acks.since {
			continue
		}
		from, ok := h.sayLineAuthorLocked(seq)
		if !ok || from == acker {
			continue // not a say line, or answering oneself
		}
		if h.acks.bySeq == nil {
			h.acks.bySeq = make(map[int64]map[string]struct{})
		}
		set := h.acks.bySeq[seq]
		if set == nil {
			set = make(map[string]struct{})
			h.acks.bySeq[seq] = set
		}
		if _, dup := set[acker]; dup {
			continue
		}
		set[acker] = struct{}{}
		fresh = append(fresh, seq)
	}
	if len(fresh) == 0 {
		h.mu.Unlock()
		return nil
	}
	h.pruneAcksLocked()
	h.saveAcksLocked()
	frame := Message{Type: MsgAck, From: acker, Seqs: fresh, TS: util.Now()}
	h.mu.Unlock()
	h.Broadcast(frame) // reminder-only: re-locks briefly, ordering is irrelevant
	return fresh
}

// AckReceipts snapshots the ledger: the tracking floor plus every
// recorded row, oldest first, acker names sorted. Rows below the floor
// cannot render chips and are dropped.
func (h *Hub) AckReceipts() (int64, []AckRow) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ackReceiptsLocked()
}

// ackReceiptsLocked is AckReceipts' under-the-lock body
// (saveAcksLocked shares it). Callers hold h.mu.
func (h *Hub) ackReceiptsLocked() (int64, []AckRow) {
	seqs := make([]int64, 0, len(h.acks.bySeq))
	for seq := range h.acks.bySeq {
		if seq >= h.acks.since {
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	rows := make([]AckRow, 0, len(seqs))
	for _, seq := range seqs {
		names := make([]string, 0, len(h.acks.bySeq[seq]))
		for n := range h.acks.bySeq[seq] {
			names = append(names, n)
		}
		sort.Strings(names)
		rows = append(rows, AckRow{Seq: seq, Ackers: names})
	}
	return h.acks.since, rows
}

// sayLineAuthorLocked reports the sender of the history ring's member
// conversation frame (say or report) with this seq — the ack's "answers
// someone" rule needs the author, not just the type. ok is false for
// system lines and seqs outside the ring. Callers hold h.mu.
func (h *Hub) sayLineAuthorLocked(seq int64) (string, bool) {
	for i := range h.history {
		if h.history[i].Seq == seq {
			if h.history[i].Type != MsgSay && h.history[i].Type != MsgReport {
				return "", false
			}
			return h.history[i].From, true
		}
	}
	return "", false
}

// pruneAcksLocked evicts the oldest entries past the cap (seqs are
// monotonic). Callers hold h.mu.
func (h *Hub) pruneAcksLocked() {
	if len(h.acks.bySeq) <= acksCap {
		return
	}
	seqs := make([]int64, 0, len(h.acks.bySeq))
	for seq := range h.acks.bySeq {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, seq := range seqs[:len(seqs)-acksCap] {
		delete(h.acks.bySeq, seq)
	}
}

// attachAcksLocked derives the ledger's persistence slot from the
// room's history slot (history/<key>.acks.json) and loads what it
// holds — the attachReadsLocked discipline: call once at boot before
// loadHistoryLocked floors the seq counter; a persisted floor wins, an
// absent or corrupt file degrades to the computed one. Callers hold
// h.mu.
func (h *Hub) attachAcksLocked(historyPath string) {
	if historyPath == "" {
		return
	}
	h.acks.path = strings.TrimSuffix(historyPath, ".jsonl") + ".acks.json"
	b, err := os.ReadFile(h.acks.path)
	if err != nil || len(b) == 0 {
		return // absent: the boot computes its own floor
	}
	var disk struct {
		Since int64    `json:"since"`
		Acks  []AckRow `json:"acks"`
	}
	if json.Unmarshal(b, &disk) != nil {
		log.Printf("chat: acks file %s unreadable; starting fresh", h.acks.path)
		return
	}
	if disk.Since > 0 {
		h.acks.since = disk.Since // a zero floor (pre-fix file) yields to the computed one
	}
	if h.acks.bySeq == nil {
		h.acks.bySeq = make(map[int64]map[string]struct{}, len(disk.Acks))
	}
	for _, row := range disk.Acks {
		set := make(map[string]struct{}, len(row.Ackers))
		for _, name := range row.Ackers {
			set[name] = struct{}{}
		}
		h.acks.bySeq[row.Seq] = set
	}
}

// floorAcksLocked marks the boot's tracking start (the restored
// history's newest seq + 1) — honored only when no persisted ledger
// carried a floor across the restart. Callers hold h.mu.
func (h *Hub) floorAcksLocked(seq int64) {
	if h.acks.since == 0 {
		h.acks.since = seq
	}
}

// saveAcksLocked persists the whole ledger: marshal under the lock,
// hand the bytes to the ordered ledger flusher (the reads/history
// discipline — the IO left the critical section). A full queue drops
// the snapshot to dirty. Callers hold h.mu.
func (h *Hub) saveAcksLocked() {
	b := h.marshalAcksLocked()
	if b == nil {
		return
	}
	h.queueLedgerSaveLocked(&h.acks.dirty, h.acks.path, b, false)
}

// saveAcksForceLocked is the flush path's retry (blocking enqueue —
// the flusher never takes h.mu). Callers hold h.mu.
func (h *Hub) saveAcksForceLocked() {
	if h.acks.path == "" {
		return
	}
	if b := h.marshalAcksLocked(); b != nil {
		h.queueLedgerSaveLocked(&h.acks.dirty, h.acks.path, b, true)
	}
}

// marshalAcksLocked renders the whole ledger as its persisted JSON
// (nil = nothing to write). A few hundred rows at most. Callers hold
// h.mu.
func (h *Hub) marshalAcksLocked() []byte {
	if h.acks.path == "" {
		return nil
	}
	_, rows := h.ackReceiptsLocked()
	b, err := json.Marshal(struct {
		Since int64    `json:"since"`
		Acks  []AckRow `json:"acks"`
	}{Since: h.acks.since, Acks: rows})
	if err != nil {
		return nil
	}
	return b
}
