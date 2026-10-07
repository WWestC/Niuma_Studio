package server

// trace_test.go — 工作过程读面（v2.8）：GET /p/{key}/trace 出环形缓冲
// 快照（名册序成员行 + working 戳 + 旧在前条目）；?name= 收窄到一人；
// 未知项目 404（/p face 规则）。图片窄端点：工作区内图片放行、出区/非
// 图片/不存在 400。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

func TestProjectTraceEndpoint(t *testing.T) {
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "") // in-memory rooms
	ws := t.TempDir()
	if _, err := projStore.Create(projects.Project{Key: "proj-a", Name: "项目甲",
		Workspace: filepath.Join(ws, "proj-a")}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-a"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{ProjectStore: projStore}, Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)

	hub, err := registry.Hub("proj-a")
	if err != nil {
		t.Fatalf("registry hub: %v", err)
	}
	hub.Join("小策", "编排", false)
	hub.AppendTrace([]chat.TraceEntry{
		{From: "小策", Kind: chat.TraceThink, Text: "拆需求"},
		{From: "小策", Kind: chat.TraceTool, Call: "c1", Tool: "Bash", State: "done"},
	})

	var body struct {
		Project string `json:"project"`
		Members []struct {
			From    string            `json:"from"`
			Working bool              `json:"working"`
			Entries []chat.TraceEntry `json:"entries"`
		} `json:"members"`
	}
	resp, err := http.Get(addrHTTP(s) + "/p/proj-a/trace")
	if err != nil {
		t.Fatalf("GET trace: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if body.Project != "proj-a" {
		t.Fatalf("project 不符：%s", body.Project)
	}
	var found bool
	for _, m := range body.Members {
		if m.From != "小策" {
			continue
		}
		found = true
		if len(m.Entries) != 2 || m.Entries[0].Kind != chat.TraceThink ||
			m.Entries[1].Kind != chat.TraceTool || m.Entries[1].Tool != "Bash" {
			t.Fatalf("条目不符：%+v", m.Entries)
		}
	}
	if !found {
		t.Fatalf("名册成员行缺失：%+v", body.Members)
	}

	// ?name= 收窄：只有一人一行。
	resp, err = http.Get(addrHTTP(s) + "/p/proj-a/trace?name=小策")
	if err != nil {
		t.Fatalf("GET trace?name: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if len(body.Members) != 1 || body.Members[0].From != "小策" {
		t.Fatalf("?name 收窄失败：%+v", body.Members)
	}

	// 未知项目 404。
	resp, err = http.Get(addrHTTP(s) + "/p/no-such/trace")
	if err != nil {
		t.Fatalf("GET unknown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知项目应 404，得到 %d", resp.StatusCode)
	}
}

func TestProjectTraceFileEndpoint(t *testing.T) {
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "")
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "art"), 0o755); err != nil {
		t.Fatal(err)
	}
	png := filepath.Join(ws, "art", "mock.png")
	if err := os.WriteFile(png, []byte("\x89PNG faketext"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(ws, "secret.txt")
	if err := os.WriteFile(secret, []byte("boom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := projStore.Create(projects.Project{Key: "proj-b", Name: "项目乙",
		Workspace: ws}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-b"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{ProjectStore: projStore}, Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)

	get := func(u string) *http.Response {
		t.Helper()
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("GET %s: %v", u, err)
		}
		resp.Body.Close()
		return resp
	}
	// 工作区内图片：200。
	if r := get(addrHTTP(s) + "/p/proj-b/trace/file?path=" + png); r.StatusCode != http.StatusOK {
		t.Fatalf("工作区内图片应 200，得到 %d", r.StatusCode)
	}
	// 工作区外：400（拿本仓库外一个必然存在的图片路径不必要——目录逃
	// 逸用 ../ 相对路径就能验）。
	if r := get(addrHTTP(s) + "/p/proj-b/trace/file?path=" + filepath.Join(ws, "..", "escape.png")); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("出工作区应 400，得到 %d", r.StatusCode)
	}
	// 非图片扩展名：400。
	if r := get(addrHTTP(s) + "/p/proj-b/trace/file?path=" + secret); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("非图片应 400，得到 %d", r.StatusCode)
	}
	// 不存在：400。
	if r := get(addrHTTP(s) + "/p/proj-b/trace/file?path=" + filepath.Join(ws, "nope.png")); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("不存在应 400，得到 %d", r.StatusCode)
	}
}
