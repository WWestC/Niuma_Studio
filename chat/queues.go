package chat

// queues.go — the room's delivery-queue projection (排队中，飞书式投递
// 状态的中间档): which members currently have which say lines parked on
// their next-turn lane — injected-but-unread is the read ledger's
// business, queued-and-unsent is this one's. The owner's chip turns a
// lie by omission into a fact: 「一艾特就已读」 was only ever the truth
// for the second of the two, and a line that sits behind a busy turn
// (or a rebuild) used to render as plain 未读 with no hint that the
// dispatcher already owns it.
//
// The dispatcher is the only source: SetQueued REPLACES the member's
// queued set wholesale (add, shrink, clear — one frame each time, no
// add/remove ambiguity for a dropped client to corrupt) and the hub
// broadcasts the resulting set as a reminder-only "queue" frame (the
// read-frame contract: never stored in the chat history). The state is
// deliberately NOT persisted — the durable truth is the dispatcher's
// inbox lanes; boot re-derives and re-broadcasts from them at adoption,
// so a crashed process leaves no stale chips behind.
//
// Honesty rules (the reads/acks discipline):
//   - only say lines in the history ring earn queue rows — the seqs
//     arrive from readq, which carries room messages only, and the
//     filter keeps a forged/rotten seq from inventing a chip;
//   - a member's set is a projection, never a promise: the line ships
//     when its turn comes (takeNext → deliver), and the set is the
//     dispatcher's queue AS OF the last push.

import (
	"sort"

	"github.com/WWestC/Niuma_Studio/util"
)

// QueuedRow is one message's queued-member set (the HTTP projection's
// row — the reads endpoint folds these rows into its snapshot).
type QueuedRow struct {
	Seq int64    `json:"seq"`
	Who []string `json:"who"`
}

// queueLedger is the hub's delivery-queue projection. Every field is
// accessed under the hub's own mutex — no lock of its own.
type queueLedger struct {
	bySeq map[int64]map[string]struct{}
}

// SetQueued replaces reader's queued set wholesale: the reader leaves
// every row it was in, then joins each named say line's row (seqs <= 0,
// below the tracking floor, or absent from the history ring are ignored
// silently). Broadcasts one reminder-only "queue" frame carrying the
// resulting set — empty when the queue drained. Safe for concurrent use.
func (h *Hub) SetQueued(reader string, seqs ...int64) {
	if reader == "" {
		return
	}
	h.mu.Lock()
	for seq, set := range h.queues.bySeq {
		delete(set, reader)
		if len(set) == 0 {
			delete(h.queues.bySeq, seq)
		}
	}
	seen := make(map[int64]struct{}, len(seqs))
	for _, seq := range seqs {
		if seq <= 0 || seq < h.reads.since || !h.sayBySeqLocked(seq) {
			continue
		}
		if _, dup := seen[seq]; dup {
			continue
		}
		seen[seq] = struct{}{}
		if h.queues.bySeq == nil {
			h.queues.bySeq = make(map[int64]map[string]struct{})
		}
		set := h.queues.bySeq[seq]
		if set == nil {
			set = make(map[string]struct{})
			h.queues.bySeq[seq] = set
		}
		set[reader] = struct{}{}
	}
	frame := Message{Type: MsgQueue, From: reader, TS: util.Now()}
	for seq := range seen {
		frame.Seqs = append(frame.Seqs, seq)
	}
	sort.Slice(frame.Seqs, func(i, j int) bool { return frame.Seqs[i] < frame.Seqs[j] })
	h.mu.Unlock()
	h.Broadcast(frame) // reminder-only: re-locks briefly, ordering is irrelevant
}

// QueueRows snapshots the projection: one row per queued say line,
// oldest first, member names sorted. Serves the reads endpoint's
// queues fold (GET /p/{key}/reads).
func (h *Hub) QueueRows() []QueuedRow {
	h.mu.Lock()
	defer h.mu.Unlock()
	seqs := make([]int64, 0, len(h.queues.bySeq))
	for seq := range h.queues.bySeq {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	rows := make([]QueuedRow, 0, len(seqs))
	for _, seq := range seqs {
		names := make([]string, 0, len(h.queues.bySeq[seq]))
		for n := range h.queues.bySeq[seq] {
			names = append(names, n)
		}
		sort.Strings(names)
		rows = append(rows, QueuedRow{Seq: seq, Who: names})
	}
	return rows
}

// SayLine hands back one history say line's sender and text ("" / false
// when the seq is gone or not a say) — the dispatcher's auto-quote
// (迟到回复的 引用 #N 前缀) reads the original here instead of
// re-deriving it from cargo snapshots.
func (h *Hub) SayLine(seq int64) (string, string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.history {
		if h.history[i].Seq == seq {
			if h.history[i].Type != MsgSay {
				return "", "", false
			}
			return h.history[i].From, h.history[i].Text, true
		}
	}
	return "", "", false
}
