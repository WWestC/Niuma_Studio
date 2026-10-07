package server

// /retention (v2.8.2 聊天记录保留期): the face's defaults (7, 0=永久),
// its validation, and the sweep the write triggers — live rooms prune
// through their Hub (file AND ring), hub-less history files prune
// direct, 永久 sweeps nothing. Studios here seed 永久 BEFORE Start so
// the boot sweep stays quiet and the POST under test is the only
// pruner in the room.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

// startRetentionStudio boots a studio persisting under a temp root.
// seedDays (nil = no policy file) lands before Start — a non-nil seed
// keeps the boot sweep deterministic.
func startRetentionStudio(t *testing.T, seedDays *int) (*Server, *chat.Registry, string) {
	t.Helper()
	root := t.TempDir()
	if seedDays != nil {
		if err := chat.SaveRetention(filepath.Join(root, "retention.json"), *seedDays); err != nil {
			t.Fatalf("seed policy: %v", err)
		}
	}
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	if _, err := projStore.Create(projects.Project{Key: "proj-a", Name: "项目甲",
		Workspace: t.TempDir() + "/proj-a"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-a"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	registry := chat.NewRegistry(projStore, root, "", "")
	lobby, err := registry.Lobby() // the production shape: the lobby lives in the registry
	if err != nil {
		t.Fatalf("Lobby: %v", err)
	}
	lobby.Join("房主", "boss", false)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, registry, root
}

// writeRoomHistory seeds key's jsonl with one stale frame (seq 1) and
// one fresh frame (seq 2), returning the file path.
func writeRoomHistory(t *testing.T, root, key string) string {
	t.Helper()
	path := filepath.Join(root, "history", key+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := time.Now().Unix()
	body := fmt.Sprintf(`{"type":"say","from":"甲","text":"stale","ts":%d,"seq":1}`+"\n"+
		`{"type":"say","from":"乙","text":"fresh","ts":%d,"seq":2}`+"\n", now-30*86400, now)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func roomLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func postRetention(t *testing.T, s *Server, days int) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(addrHTTP(s)+"/retention", "application/json",
		bytes.NewReader([]byte(fmt.Sprintf(`{"days":%d}`, days))))
	if err != nil {
		t.Fatalf("POST /retention: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// TestRetentionFaceDefaultsAndValidation: a studio without a policy
// answers the default 7; writes round-trip; out-of-range days refuse.
func TestRetentionFaceDefaultsAndValidation(t *testing.T) {
	zero := 0
	s, _, _ := startRetentionStudio(t, &zero) // 永久 seed: the boot sweep stays quiet

	resp, err := http.Get(addrHTTP(s) + "/retention")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got["days"] != float64(0) { // the seed (an unseeded studio answers 7 — the chat tests pin that default)
		t.Fatalf("seeded GET days = %v, want 0", got["days"])
	}

	if code, body := postRetention(t, s, 7); code != 200 || body["days"] != float64(7) {
		t.Fatalf("POST 7 = %d %v", code, body)
	}
	if code, body := postRetention(t, s, 0); code != 200 || body["days"] != float64(0) {
		t.Fatalf("POST 0（永久）= %d %v", code, body)
	}
	for _, bad := range []int{-1, 3651, 99999} {
		if code, _ := postRetention(t, s, bad); code != http.StatusBadRequest {
			t.Fatalf("POST %d = %d, want 400", bad, code)
		}
	}
}

// TestRetentionDefaultIsSeven: an unseeded studio (no retention.json,
// no history files to touch) reports the requested default.
func TestRetentionDefaultIsSeven(t *testing.T) {
	s, _, _ := startRetentionStudio(t, nil)
	resp, err := http.Get(addrHTTP(s) + "/retention")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got["days"] != float64(7) {
		t.Fatalf("default days = %v, want 7", got["days"])
	}
}

// TestRetentionPostSweepsImmediately: writing a finite policy prunes
// on the spot — the lobby (live, via its Hub), proj-a (live, ring
// included) and proj-b (hub-less, file direct) all lose their stale
// frame; 永久 afterwards prunes nothing more.
func TestRetentionPostSweepsImmediately(t *testing.T) {
	zero := 0
	s, registry, root := startRetentionStudio(t, &zero)
	lobbyPath := writeRoomHistory(t, root, "default")
	projAPath := writeRoomHistory(t, root, "proj-a")
	projBPath := writeRoomHistory(t, root, "proj-b") // never instantiated: hub-less

	projA, err := registry.Hub("proj-a") // live: the sweep must cut its ring too
	if err != nil {
		t.Fatalf("Hub proj-a: %v", err)
	}
	if len(projA.History()) != 2 {
		t.Fatalf("proj-a ring = %d frames, want 2 (loaded from the file)", len(projA.History()))
	}

	code, body := postRetention(t, s, 7)
	if code != 200 {
		t.Fatalf("POST 7 = %d %v", code, body)
	}
	if body["pruned"] != float64(3) {
		t.Fatalf("pruned = %v, want 3 (one stale frame per room)", body["pruned"])
	}
	for name, path := range map[string]string{"lobby": lobbyPath, "proj-a": projAPath, "proj-b": projBPath} {
		lines := roomLines(t, path)
		if len(lines) != 1 || !strings.Contains(lines[0], "fresh") {
			t.Fatalf("%s jsonl after sweep = %v, want the fresh frame only", name, lines)
		}
	}
	for _, m := range projA.History() {
		if m.Text == "stale" {
			t.Fatalf("proj-a ring still holds the pruned frame")
		}
	}

	// 永久 afterwards: nothing more to prune, nothing touched
	if code, body := postRetention(t, s, 0); code != 200 || body["pruned"] != float64(0) {
		t.Fatalf("POST 0 = %d %v, want pruned 0", code, body)
	}
}

// TestRetentionSweepRespectsPermanentPolicy: sweepRetention under 永久
// leaves even ancient frames standing.
func TestRetentionSweepRespectsPermanentPolicy(t *testing.T) {
	zero := 0
	s, _, root := startRetentionStudio(t, &zero)
	path := writeRoomHistory(t, root, "proj-a")
	if dropped := s.roomopsFace().SweepRetention(time.Now()); dropped != 0 {
		t.Fatalf("sweep under 永久 dropped %d, want 0", dropped)
	}
	if lines := roomLines(t, path); len(lines) != 2 {
		t.Fatalf("file touched under 永久: %v", lines)
	}
}

// TestRetentionFaceAbsentWithoutRoot: no registry root (in-memory
// embeds) — the whole face 404s, same discipline as the /p/ face.
func TestRetentionFaceAbsentWithoutRoot(t *testing.T) {
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	resp, err := http.Get(addrHTTP(s) + "/retention")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET without registry = %d, want 404", resp.StatusCode)
	}
}
