package server

// questions.go — the interactive questions' read face (向房主提问,
// v2.5): GET /p/{key}/questions serves the room's OPEN question set —
// the workbench's hydration source for the clickable option cards the
// observer replay alone cannot know (question frames never enter the
// chat history, so a cold window must fetch them). Same-shape
// discipline with the ledger's projection:
// {"project", "questions":[{id, from, ts, due, prompt, asks:[…]}]}.

import (
	"net/http"

	"github.com/WWestC/Niuma_Studio/chat"
)

// handleProjectQuestions serves the room's open-question snapshot
// (GET /p/{key}/questions), oldest ask first. Unknown / draft /
// archived projects are 404 — the registry refuses them (the /p
// face's rule).
func (s *Server) handleProjectQuestions(w http.ResponseWriter, r *http.Request) {
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
	qs := hub.OpenQuestions()
	if qs == nil {
		qs = []chat.Question{}
	}
	writeJSON(w, map[string]any{"project": key, "questions": qs})
}
