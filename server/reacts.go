package server

// reacts.go — the emoji reactions' read face (飞书式表情回应): GET
// /p/{key}/reacts serves the room's reaction ledger snapshot — the
// workbench's hydration source for the reaction chips under messages
// the observer replay alone cannot know (the ledger never enters the
// chat history, so a cold window must fetch it). Same-shape discipline
// with the ledger's projection: {"project", "since",
// "reacts":[{seq, reactions:[{emoji, names}]}]}.

import (
	"net/http"

	"github.com/WWestC/Niuma_Studio/chat"
)

// handleProjectReacts serves the room's emoji-reaction snapshot (GET
// /p/{key}/reacts): the tracking floor plus every recorded row, oldest
// first, emojis and reactor names sorted. Unknown / draft / archived
// projects are 404 — the registry refuses them (the /p face's rule).
func (s *Server) handleProjectReacts(w http.ResponseWriter, r *http.Request) {
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
	// instantiation it may cause is memory-only (the acks face's rule)
	hub, err := s.opts.Registry.Hub(key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	since, rows := hub.ReactReceipts()
	if rows == nil {
		rows = []chat.ReactRow{}
	}
	writeJSON(w, map[string]any{"project": key, "since": since, "reacts": rows})
}
