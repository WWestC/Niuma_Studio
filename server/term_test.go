package server

// term_test.go — 终端转录读面（r_17）：GET /p/{key}/term 出转录册快照
// （名册序成员行 + working 戳 + 折叠终态条目；?name= 收窄；退场成员
// 的册照出；未知项目 404——/p face 规则）；GET /p/{key}/term/file 出
// 整册 jsonl 下载（含状态迁移的中间行；无名册 404；无 root 404）。
// 注册表带 root 时终端槽由 registry 在 hub 构造时自动挂上。

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

func startTermServer(t *testing.T) (*Server, *chat.Hub, *chat.Client) {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	root := t.TempDir()
	registry := chat.NewRegistry(projStore, root, "", "") // root attached: term dir live
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
	seat, _ := hub.Join("小猿", "画师", false)
	ok := true
	hub.AppendTerm([]chat.TraceEntry{
		{From: "小猿", Kind: chat.TraceInput, Src: "房主", Text: "画一张图"},
		{From: "小猿", Kind: chat.TraceTool, Call: "c1", Tool: "Bash", State: "call"},
	})
	hub.AppendTerm([]chat.TraceEntry{
		{From: "小猿", Kind: chat.TraceTool, Call: "c1", State: "done", Output: "ok", OK: &ok},
		{From: "小猿", Kind: chat.TraceAsk, Text: "用什么风格？"},
		{From: "小猿", Kind: chat.TraceAnswer, Text: "房主应答：风格 → 像素", OK: &ok},
	})
	return s, hub, seat
}

func TestProjectTermEndpoint(t *testing.T) {
	s, hub, seat := startTermServer(t)

	var body struct {
		Project string `json:"project"`
		Members []struct {
			From    string            `json:"from"`
			Working bool              `json:"working"`
			Entries []chat.TraceEntry `json:"entries"`
		} `json:"members"`
	}
	resp, err := http.Get(addrHTTP(s) + "/p/proj-a/term")
	if err != nil {
		t.Fatalf("GET term: %v", err)
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
		if m.From != "小猿" {
			continue
		}
		found = true
		// 三态工具折成一张终态卡：input + tool + ask + answer = 4 条。
		if len(m.Entries) != 4 {
			t.Fatalf("折叠终态应 4 条，得 %d：%+v", len(m.Entries), m.Entries)
		}
		if m.Entries[0].Kind != chat.TraceInput || m.Entries[0].Src != "房主" {
			t.Fatalf("input 行不符：%+v", m.Entries[0])
		}
		if m.Entries[1].Kind != chat.TraceTool || m.Entries[1].Tool != "Bash" ||
			m.Entries[1].State != "done" || m.Entries[1].Output != "ok" {
			t.Fatalf("工具终态卡不符：%+v", m.Entries[1])
		}
		if body.Members[0].Working {
			t.Fatal("非干活成员 working 应为 false")
		}
	}
	if !found {
		t.Fatalf("名册成员行缺失：%+v", body.Members)
	}

	// ?name= 收窄到一人一行。
	resp, err = http.Get(addrHTTP(s) + "/p/proj-a/term?name=" + "小猿")
	if err != nil {
		t.Fatalf("GET term?name: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if len(body.Members) != 1 || body.Members[0].From != "小猿" {
		t.Fatalf("收窄不符：%+v", body.Members)
	}

	// 退场成员的册照出：名册没有、文件还在（终端是史册不是名册的镜子）。
	hub.Leave(seat)
	resp, err = http.Get(addrHTTP(s) + "/p/proj-a/term")
	if err != nil {
		t.Fatalf("GET term after leave: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	var ghost bool
	for _, m := range body.Members {
		if m.From == "小猿" && len(m.Entries) == 4 {
			ghost = true
		}
	}
	if !ghost {
		t.Fatalf("退场成员的册应照出：%+v", body.Members)
	}

	// 未知项目 404（/p face 规则）。
	resp, err = http.Get(addrHTTP(s) + "/p/nope/term")
	if err != nil {
		t.Fatalf("GET nope: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知项目应 404，得 %d", resp.StatusCode)
	}

	// 整册下载：jsonl 原文、含注入行与状态迁移中间行（append-only）。
	resp, err = http.Get(addrHTTP(s) + "/p/proj-a/term/file?name=" + "小猿")
	if err != nil {
		t.Fatalf("GET term/file: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "画一张图") ||
		!strings.Contains(string(b), `"state":"call"`) {
		t.Fatalf("整册下载不符：%d %q", resp.StatusCode, string(b[:min(len(b), 200)]))
	}
	// 没有册的名字：404。
	resp, err = http.Get(addrHTTP(s) + "/p/proj-a/term/file?name=" + "无此人")
	if err != nil {
		t.Fatalf("GET term/file ghost: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("无名册应 404，得 %d", resp.StatusCode)
	}
}

func TestProjectTermFacesWithoutRoot(t *testing.T) {
	lobby := chat.NewHub()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "") // in-memory: no root
	ws := t.TempDir()
	if _, err := projStore.Create(projects.Project{Key: "proj-b", Name: "乙",
		Workspace: filepath.Join(ws, "proj-b")}); err != nil {
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

	// 快照面照常（内存镜像直读，/retention 之外的常态）。
	hub, err := registry.Hub("proj-b")
	if err != nil {
		t.Fatalf("hub: %v", err)
	}
	hub.Join("小鹿", "设计", false)
	hub.AppendTerm([]chat.TraceEntry{{From: "小鹿", Kind: chat.TraceSys, Text: "测试行"}})
	resp, err := http.Get(addrHTTP(s) + "/p/proj-b/term")
	if err != nil {
		t.Fatalf("GET term: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "测试行") {
		t.Fatalf("内存态快照不符：%d %s", resp.StatusCode, string(b))
	}

	// 下载面 404（无 root 无文件——/retention 的纪律）。
	resp, err = http.Get(addrHTTP(s) + "/p/proj-b/term/file?name=" + "小鹿")
	if err != nil {
		t.Fatalf("GET term/file: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("无 root 的下载面应 404，得 %d", resp.StatusCode)
	}
}
