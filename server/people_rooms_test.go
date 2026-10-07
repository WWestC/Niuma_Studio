package server

// The /kb/people cross-room contract (v2 multi-room sync): members
// seat into their OWN project room's hub, so the people endpoint must
// walk every room — the lobby-only read this replaces stranded every
// project-room member offline on the people board while the sidebar
// roster (fed by that room's own WS) showed them present. The endpoint
// and the sidebar now share one cross-room truth.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
)

// startStudio boots one server on an ephemeral port with a lobby hub
// plus a registry holding one active project room.
func startStudio(t *testing.T) (*Server, *chat.Hub) {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "") // in-memory rooms
	if _, err := projStore.Create(projects.Project{Key: "proj-a", Name: "项目甲",
		Workspace: t.TempDir() + "/proj-a"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-a"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, lobby
}

// TestKBPeopleCountsProjectRoomSeats: a project-room member answers
// online with the room named; a lobby-only member keeps the lobby room.
func TestKBPeopleCountsProjectRoomSeats(t *testing.T) {
	s, lobby := startStudio(t)
	projHub, err := s.opts.Registry.Hub("proj-a")
	if err != nil {
		t.Fatalf("registry hub: %v", err)
	}
	lobby.Join("小明", "后端·Niuma_Studio", false)
	projHub.Join("小红", "前端·项目甲", false)

	resp, err := http.Get(addrHTTP(s) + "/kb/people")
	if err != nil {
		t.Fatalf("GET /kb/people: %v", err)
	}
	defer resp.Body.Close()
	var people []kb.PersonSummary
	if err := json.NewDecoder(resp.Body).Decode(&people); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := make(map[string]kb.PersonSummary, len(people))
	for _, p := range people {
		byName[p.Name] = p
	}
	if p := byName["小红"]; !p.Online || p.Room != "proj-a" {
		t.Fatalf("项目房成员小红应在线且 room=proj-a，got %+v", p)
	}
	if p := byName["小明"]; !p.Online || p.Room != chat.LobbyKey {
		t.Fatalf("Niuma_Studio 成员小明应在线且 room=default，got %+v", p)
	}

	// the single-person detail face rides the same assembly
	detail, err := http.Get(addrHTTP(s) + "/kb/people/小红")
	if err != nil {
		t.Fatalf("GET /kb/people/小红: %v", err)
	}
	defer detail.Body.Close()
	var d kb.PersonDetail
	if err := json.NewDecoder(detail.Body).Decode(&d); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if !d.Online || d.Room != "proj-a" {
		t.Fatalf("牛马详情应同源聚合，got %+v", d.PersonSummary)
	}
}
