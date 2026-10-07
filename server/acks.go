package server

// acks.go — the ack receipts' read face (飞书式收到): GET /p/{key}/acks
// serves the room's ack ledger snapshot — the workbench's hydration
// source for the 「N人收到」 chips under messages the observer replay
// alone cannot know (the ledger never enters the chat history, so a
// cold window must fetch it). Same-shape discipline with the ledger's
// projection: {"project", "since", "acks":[{seq, ackers}]}.

import (
	"net/http"

	"github.com/WWestC/Niuma_Studio/chat"
)

// handleProjectAcks serves the room's ack-receipt snapshot (GET
// /p/{key}/acks): the tracking floor plus every recorded row, oldest
// first. Unknown / draft / archived projects are 404 — the registry
// refuses them (the /p face's rule).
func (s *Server) handleProjectAcks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.Registry == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	// resolving through the registry is the availability check; the
	// instantiation it may cause is memory-only (the history face's rule)
	hub, err := s.opts.Registry.Hub(key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	since, rows := hub.AckReceipts()
	if rows == nil {
		rows = []chat.AckRow{}
	}
	writeJSON(w, map[string]any{"project": key, "since": since, "acks": rows})
}
