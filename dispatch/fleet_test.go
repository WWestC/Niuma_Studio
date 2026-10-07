package dispatch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// inertFleet is the --no-dispatch shape: HTTP face alive, zero
// dispatchers — the models listing still answers (it reads the
// machine's ZCode config, not the bridge).
func inertFleet(t *testing.T) *Fleet {
	t.Helper()
	dir := t.TempDir()
	projs, err := projects.Open(filepath.Join(dir, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	staff, err := staffing.Open(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	agentStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	return StartFleet(nil, agentStore, projs, staff, nil, Config{
		Workspace: dir, Model: "GLM-5.3", Reasoning: "high",
	})
}

// TestFleetModelsEndpoint pins the pick list's HTTP shape: the process
// default echoes back (the empty pick's fallback label) and every
// provider row carries id/name/models — machine-global, served without
// a bridge.
func TestFleetModelsEndpoint(t *testing.T) {
	f := inertFleet(t)
	req := httptest.NewRequest(http.MethodGet, "/dispatch/models", nil)
	rec := httptest.NewRecorder()
	f.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dispatch/models = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Default   string `json:"default"`
		Reasoning string `json:"reasoning"`
		Providers []struct {
			ID     string   `json:"id"`
			Name   string   `json:"name"`
			Models []string `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if out.Default != "GLM-5.3" || out.Reasoning != "high" {
		t.Fatalf("默认链未回显: %+v", out)
	}
	for _, p := range out.Providers {
		if p.ID == "" || len(p.Models) == 0 {
			t.Fatalf("空供应商混入清单: %+v", p)
		}
	}
}

// TestFleetModelWriteRefusesWithoutRoom pins the write face's refusal:
// the model re-pick routes through the project's dispatcher, and a
// room-less (inert) fleet answers the reason, not a silent 200.
func TestFleetModelWriteRefusesWithoutRoom(t *testing.T) {
	f := inertFleet(t)
	req := httptest.NewRequest(http.MethodPost, "/dispatch/model", strings.NewReader(`{"name":"张三","model":"p/m"}`))
	rec := httptest.NewRecorder()
	f.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无调度器的改档应 400: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "无调度器") {
		t.Fatalf("拒绝理由不对: %s", rec.Body.String())
	}
}
