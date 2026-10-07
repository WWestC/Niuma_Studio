package chat

// questions.go — the room's interactive-question face (向房主提问,
// v2.5): the open asks a member's ZCode session has posted to the room
// through the AskUserQuestion reverse request. The dispatcher owns the
// pending tool call; the hub is only the PROJECTION and broadcaster —
// it keeps the open set so cold windows can hydrate (GET
// /p/{key}/questions) and folds the "question"/"question_done"
// reminder-only broadcasts (never stored in the chat history, the
// read-frame contract). The ask's durable transcript copy is the
// member's own say line, spoken by the dispatcher beside the card.
//
// No persistence on purpose: an open question cannot outlive the
// dispatcher that is waiting on it — a restart kills the member's
// pending turn (and with it the reverse request), so a ledger that
// survived would advertise a card whose clicks could never land.

import (
	"sort"

	"github.com/WWestC/Niuma_Studio/util"
)

// Question and its nested shapes now live in the wire package
// (Message carries them); chat re-exports via wire.go.

// questionLedger is the hub's open-question set. Every field is
// accessed under the hub's own mutex (the ackLedger discipline); the
// zero value is a usable empty ledger.
type questionLedger struct {
	open map[string]*Question // qid -> the live ask
}

// AskQuestion records an open ask and broadcasts the "question" frame
// (the card appears at the stream's tail). A duplicate id replaces the
// previous entry — ids are dispatcher-minted uniques, so this only
// guards against a pathological mint.
func (h *Hub) AskQuestion(q *Question) {
	if q == nil || q.ID == "" {
		return
	}
	h.mu.Lock()
	if h.questions.open == nil {
		h.questions.open = make(map[string]*Question)
	}
	cp := *q
	h.questions.open[q.ID] = &cp
	h.mu.Unlock()
	h.Broadcast(Message{Type: MsgQuestion, QID: q.ID, From: q.From,
		Question: &cp, TS: util.Now()})
}

// ResolveQuestion closes an ask — answered (by names the answerer,
// answer the choices that landed) or expired — and broadcasts the
// "question_done" frame so every card stops accepting clicks. Returns
// false when the qid was not open (already closed, never asked).
func (h *Hub) ResolveQuestion(qid, status, by string, answer map[string]string) bool {
	h.mu.Lock()
	if _, open := h.questions.open[qid]; !open {
		h.mu.Unlock()
		return false
	}
	delete(h.questions.open, qid)
	h.mu.Unlock()
	h.Broadcast(Message{Type: MsgQuestionDone, QID: qid, From: by,
		QStatus: status, Answer: answer, TS: util.Now()})
	return true
}

// OpenQuestions snapshots the open set, oldest ask first — the cold
// window's hydration source (GET /p/{key}/questions).
func (h *Hub) OpenQuestions() []Question {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Question, 0, len(h.questions.open))
	for _, q := range h.questions.open {
		out = append(out, *q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

// ExpireAllQuestions closes every open ask as expired. A fresh
// dispatcher attaching to this hub means the old waiter is gone
// (crash, swap, re-adopt), and a card without a waiter behind it
// must never stay clickable — the no-persistence rule's live twin.
func (h *Hub) ExpireAllQuestions() {
	h.mu.Lock()
	stale := make([]Question, 0, len(h.questions.open))
	for _, q := range h.questions.open {
		stale = append(stale, *q)
	}
	h.questions.open = map[string]*Question{}
	h.mu.Unlock()
	for _, q := range stale {
		h.Broadcast(Message{Type: MsgQuestionDone, QID: q.ID, QStatus: "expired", TS: util.Now()})
	}
}
