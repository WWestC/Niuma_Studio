package chat

// reset.go — the room's wipe face: Hub.ResetDurable, the project-reset
// half (POST /p/{key}/reset). Offboarding one member is a ceremony
// (notices, ledger lines, archives); RESETTING a project is the
// opposite promise — 「和第一次进一样」, so everything a returning
// visitor could read about the old crowd goes: the jsonl history file
// and the in-memory ring, the read/ack/react ledgers (state AND disk),
// the open questions, the delivery queues, the trace and terminal
// journals (memory and terminal/<key>/), the departed snapshots and
// grace ghosts. What deliberately stays: connected clients (the owner
// keeps the room alive), the owner seat, and the monotonic seq —
// rewinding seq would collide with every cursor still held elsewhere
// (frontend since-cursors, the dispatcher's lanes); a fresh room
// simply starts numbering past the old crowd, exactly like a room
// that was quiet for a long time.

import (
	"errors"
	"os"
	"strings"
)

// ResetDurable wipes the room's durable conversation state (see the
// file header for the inventory). Best-effort per artifact: a file
// that refuses removal is reported (the caller surfaces it) while the
// in-memory wipe still stands — a half-reset room serves empty, never
// half-old. Callers should follow up with a System line so the fresh
// history opens with the reset's own receipt.
func (h *Hub) ResetDurable() error {
	// Drain both flushers BEFORE the files go: an in-flight history
	// batch would O_APPEND|O_CREATE the removed jsonl right back
	// (stale lines resurrected), an in-flight ledger snapshot would
	// rewrite a ledger the floor just declared dead. The drains'
	// blocking sends sit on NO lock (the flusher 家法)：both queues
	// are swapped under h.mu — producers hereafter enqueue on the
	// fresh queues only — then h.mu drops for the old queues'
	// barriers. The swap window's racing producers park (history in
	// pendHold, ledgers as dirty) and are DISCARDED with the world
	// being reset; the post-reset state clears the dirty flags (the
	// new world's empty ledgers owe no disk write).
	h.durableDrainMu.Lock()
	h.mu.Lock()
	oldPend := h.swapPendLocked()
	oldLedger := h.swapLedgerSaveLocked()
	h.holding = true
	h.ledgerHold = true
	h.mu.Unlock()
	retirePend(oldPend)
	retireLedgers(oldLedger)
	h.durableDrainMu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	h.pendHold = nil
	h.holding = false
	h.ledgerHold = false
	var errs []string

	if h.historyPath != "" {
		if err := os.Remove(h.historyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, "history: "+err.Error())
		}
	}
	h.history = nil

	// The three seq-ledgers: state gone, disk file gone, floor moved
	// past every old seq (a straggler MarkRead for a dead line is
	// ignored silently, the same as any pre-boot seq).
	floor := h.lastSeq + 1
	h.reads.bySeq = nil
	h.reads.since = floor
	h.reads.dirty = false
	if h.reads.path != "" {
		if err := os.Remove(h.reads.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, "reads: "+err.Error())
		}
	}
	h.acks.bySeq = nil
	h.acks.since = floor
	h.acks.dirty = false
	if h.acks.path != "" {
		if err := os.Remove(h.acks.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, "acks: "+err.Error())
		}
	}
	h.reacts.bySeq = nil
	h.reacts.since = floor
	h.reacts.dirty = false
	if h.reacts.path != "" {
		if err := os.Remove(h.reacts.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, "reacts: "+err.Error())
		}
	}

	// Ephemeral state that never persisted: open questions, delivery
	// queues. Dropped with the crowd they belonged to.
	h.questions.open = nil
	h.queues.bySeq = nil

	// Trace and terminal journals: memory always, terminal/<key>/ on
	// disk when the slot exists.
	h.trace = nil
	h.term = nil
	h.termLast = nil
	h.termCalls = nil
	if h.termDir != "" {
		if err := os.RemoveAll(h.termDir); err != nil {
			errs = append(errs, "term: "+err.Error())
		}
	}

	// The departed wall and grace ghosts: their people are deleted,
	// the board must not render phantom offline rows.
	h.departed = make(map[string]Member)
	h.departedOrder = nil
	h.ghosts = make(map[string]*ghostEntry)

	// In-flight @所有人 roll-calls die with their audience.
	h.allReqs = nil

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Forget drops the names' departed snapshots and grace ghosts — the
// bulk-dismiss half (ResetDurable's targeted sibling for the flow
// that keeps the history): a deleted person must not linger on the
// people board as a phantom offline row. Safe on unknown names.
func (h *Hub) Forget(names []string) {
	if len(names) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, name := range names {
		delete(h.departed, name)
		delete(h.ghosts, name)
		for i, n := range h.departedOrder {
			if n == name {
				h.departedOrder = append(h.departedOrder[:i], h.departedOrder[i+1:]...)
				break
			}
		}
	}
}
