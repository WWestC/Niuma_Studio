package chat

// The room's emoji reactions (飞书式表情回应): per-message facts about
// which members reacted which emoji — a reaction is an OPINION about
// the original line, never a new chat message, so it can never mention
// anyone, wake anyone, or be replied to (the ack ledger's construction,
// borrowed whole). Where an ack answers someone (and therefore may not
// target one's own line), a reaction judges the line itself — reacting
// to your own message is allowed and normal.
//
// Any client feeds the ledger: the owner's workbench face and any
// seated member send {"type":"react","seq":N,"emoji":"👍","on":true}.
// The hub is the STATE store and broadcaster: MarkReact records one
// fact and broadcasts the reminder-only "react" frame (never stored in
// the chat history — the read/ack frame contract), ReactReceipts serves
// the HTTP snapshot (GET /p/{key}/reacts), and the ledger persists
// beside the room's history so chips survive restarts.
//
// Honesty rules the ledger keeps (a chip must never lie):
//   - only member conversation lines count — say AND report: both are
//     someone talking to the room; system lines are the room's own
//     voice and earn nothing;
//   - a seq below `since` (the tracking floor) is ignored: history that
//     predates the ledger's boot shows no chips rather than stale ones,
//     and a persisted floor keeps that cut stable across restarts;
//   - the emoji must be a real one-line token (validReactEmoji) and the
//     per-line palette is capped — a reaction is a gesture, not a wall.

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/WWestC/Niuma_Studio/util"
)

// reactsCap bounds the ledger — one entry per tracked say line, the
// same bound the read/ack ledgers keep (chat/reads.go's readsCap
// rationale: the chat history ring keeps 100 lines and the workbench
// window 300).
const reactsCap = 300

// reactEmojiCap bounds the distinct emojis one line may carry — beyond
// it new palette slots are ignored (existing reactions stay).
const reactEmojiCap = 16

// ReactSet is one emoji's reactor set (the HTTP projection's row).
type ReactSet struct {
	Emoji string   `json:"emoji"`
	Names []string `json:"names"`
}

// ReactRow is one message's reaction sets (the HTTP projection's row).
type ReactRow struct {
	Seq       int64      `json:"seq"`
	Reactions []ReactSet `json:"reactions"`
}

// reactLedger is the hub's emoji-reaction state. Every field is
// accessed under the hub's own mutex — the ledger keeps no lock of its
// own (the ackLedger discipline).
type reactLedger struct {
	bySeq map[int64]map[string]map[string]struct{} // seq → emoji → reactors
	// since is the first tracked seq: 0 = track everything (fresh
	// in-memory hubs, tests); the boot floors it at the restored tail's
	// newest seq + 1; a persisted ledger carries its own floor across
	// restarts, whichever is set first wins.
	since int64
	path  string // history/<key>.reacts.json; "" = in-memory only
	// dirty: the last marshaled snapshot could not enqueue (queue
	// full) — retried by the next mark or the flush barrier (the
	// readLedger discipline).
	dirty bool
}

// validReactEmoji guards the palette slot: non-empty, at most 8 runes
// (a skin-tone/ZWJ sequence or family emoji), no control characters —
// the broadcast frame and every tooltip stay one line.
func validReactEmoji(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)
	if len(r) > 8 {
		return false
	}
	for _, c := range r {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// MarkReact records one member's emoji reaction on the named say seq
// and, when the fact is fresh, broadcasts one "react" frame to the
// room. Returns true when the ledger changed. Idempotent per (seq,
// emoji, reactor); ignored silently: untracked seqs (below the floor,
// absent from the history ring), non-conversation lines (system lines
// earn no reactions), invalid emoji, adds past the per-line palette
// cap, and removes of facts that were never there. Reacting to one's
// own line is allowed — an opinion is not an answer. Safe for
// concurrent use.
func (h *Hub) MarkReact(reactor string, seq int64, emoji string, on bool) bool {
	if reactor == "" || seq <= 0 || !validReactEmoji(emoji) {
		return false
	}
	h.mu.Lock()
	if seq < h.reacts.since { // since 的写方（ResetDurable）也在本锁内
		h.mu.Unlock()
		return false
	}
	if _, ok := h.sayLineAuthorLocked(seq); !ok {
		h.mu.Unlock()
		return false // system lines and stale seqs earn no reactions
	}
	emojis := h.reacts.bySeq[seq]
	if emojis == nil {
		if !on {
			h.mu.Unlock()
			return false // removing from nothing: nothing to broadcast
		}
		emojis = make(map[string]map[string]struct{})
		if h.reacts.bySeq == nil {
			h.reacts.bySeq = make(map[int64]map[string]map[string]struct{})
		}
		h.reacts.bySeq[seq] = emojis
	}
	names := emojis[emoji]
	if on {
		if names == nil {
			if len(emojis) >= reactEmojiCap {
				h.mu.Unlock()
				return false // the line's palette is full
			}
			names = make(map[string]struct{})
			emojis[emoji] = names
		}
		if _, dup := names[reactor]; dup {
			h.mu.Unlock()
			return false
		}
		names[reactor] = struct{}{}
	} else {
		if names == nil {
			h.mu.Unlock()
			return false
		}
		if _, had := names[reactor]; !had {
			h.mu.Unlock()
			return false
		}
		delete(names, reactor)
		if len(names) == 0 {
			delete(emojis, emoji)
			if len(emojis) == 0 {
				delete(h.reacts.bySeq, seq)
			}
		}
	}
	h.pruneReactsLocked()
	h.saveReactsLocked()
	frame := Message{Type: MsgReact, From: reactor, Seq: seq, Emoji: emoji, On: on, TS: util.Now()}
	h.mu.Unlock()
	h.Broadcast(frame) // reminder-only: re-locks briefly, ordering is irrelevant
	return true
}

// ReactReceipts snapshots the ledger: the tracking floor plus every
// recorded row, oldest first, emojis and reactor names sorted. Rows
// below the floor cannot render chips and are dropped.
func (h *Hub) ReactReceipts() (int64, []ReactRow) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reactReceiptsLocked()
}

// reactReceiptsLocked is ReactReceipts' under-the-lock body
// (saveReactsLocked shares it). Callers hold h.mu.
func (h *Hub) reactReceiptsLocked() (int64, []ReactRow) {
	seqs := make([]int64, 0, len(h.reacts.bySeq))
	for seq := range h.reacts.bySeq {
		if seq >= h.reacts.since {
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	rows := make([]ReactRow, 0, len(seqs))
	for _, seq := range seqs {
		emojiSet := h.reacts.bySeq[seq]
		emojis := make([]string, 0, len(emojiSet))
		for e := range emojiSet {
			emojis = append(emojis, e)
		}
		sort.Strings(emojis)
		sets := make([]ReactSet, 0, len(emojis))
		for _, e := range emojis {
			names := make([]string, 0, len(emojiSet[e]))
			for n := range emojiSet[e] {
				names = append(names, n)
			}
			sort.Strings(names)
			sets = append(sets, ReactSet{Emoji: e, Names: names})
		}
		rows = append(rows, ReactRow{Seq: seq, Reactions: sets})
	}
	return h.reacts.since, rows
}

// pruneReactsLocked evicts the oldest entries past the cap (seqs are
// monotonic). Callers hold h.mu.
func (h *Hub) pruneReactsLocked() {
	if len(h.reacts.bySeq) <= reactsCap {
		return
	}
	seqs := make([]int64, 0, len(h.reacts.bySeq))
	for seq := range h.reacts.bySeq {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, seq := range seqs[:len(seqs)-reactsCap] {
		delete(h.reacts.bySeq, seq)
	}
}

// attachReactsLocked derives the ledger's persistence slot from the
// room's history slot (history/<key>.reacts.json) and loads what it
// holds — the attachAcksLocked discipline: call once at boot before
// loadHistoryLocked floors the seq counter; a persisted floor wins, an
// absent or corrupt file degrades to the computed one. Callers hold
// h.mu.
func (h *Hub) attachReactsLocked(historyPath string) {
	if historyPath == "" {
		return
	}
	h.reacts.path = strings.TrimSuffix(historyPath, ".jsonl") + ".reacts.json"
	b, err := os.ReadFile(h.reacts.path)
	if err != nil || len(b) == 0 {
		return // absent: the boot computes its own floor
	}
	var disk struct {
		Since  int64      `json:"since"`
		Reacts []ReactRow `json:"reacts"`
	}
	if json.Unmarshal(b, &disk) != nil {
		log.Printf("chat: reacts file %s unreadable; starting fresh", h.reacts.path)
		return
	}
	if disk.Since > 0 {
		h.reacts.since = disk.Since // a zero floor yields to the computed one
	}
	if h.reacts.bySeq == nil {
		h.reacts.bySeq = make(map[int64]map[string]map[string]struct{}, len(disk.Reacts))
	}
	for _, row := range disk.Reacts {
		emojis := make(map[string]map[string]struct{}, len(row.Reactions))
		for _, set := range row.Reactions {
			if !validReactEmoji(set.Emoji) {
				continue
			}
			names := make(map[string]struct{}, len(set.Names))
			for _, n := range set.Names {
				names[n] = struct{}{}
			}
			emojis[set.Emoji] = names
		}
		if len(emojis) > 0 {
			h.reacts.bySeq[row.Seq] = emojis
		}
	}
}

// floorReactsLocked marks the boot's tracking start (the restored
// history's newest seq + 1) — honored only when no persisted ledger
// carried a floor across the restart. Callers hold h.mu.
func (h *Hub) floorReactsLocked(seq int64) {
	if h.reacts.since == 0 {
		h.reacts.since = seq
	}
}

// saveReactsLocked persists the whole ledger: marshal under the lock,
// hand the bytes to the ordered ledger flusher (the reads/acks
// discipline — the IO left the critical section). A full queue drops
// the snapshot to dirty. Callers hold h.mu.
func (h *Hub) saveReactsLocked() {
	b := h.marshalReactsLocked()
	if b == nil {
		return
	}
	h.queueLedgerSaveLocked(&h.reacts.dirty, h.reacts.path, b, false)
}

// saveReactsForceLocked is the flush path's retry (blocking enqueue —
// the flusher never takes h.mu). Callers hold h.mu.
func (h *Hub) saveReactsForceLocked() {
	if h.reacts.path == "" {
		return
	}
	if b := h.marshalReactsLocked(); b != nil {
		h.queueLedgerSaveLocked(&h.reacts.dirty, h.reacts.path, b, true)
	}
}

// marshalReactsLocked renders the whole ledger as its persisted JSON
// (nil = nothing to write). A few hundred rows at most. Callers hold
// h.mu.
func (h *Hub) marshalReactsLocked() []byte {
	if h.reacts.path == "" {
		return nil
	}
	_, rows := h.reactReceiptsLocked()
	b, err := json.Marshal(struct {
		Since  int64      `json:"since"`
		Reacts []ReactRow `json:"reacts"`
	}{Since: h.reacts.since, Reacts: rows})
	if err != nil {
		return nil
	}
	return b
}
