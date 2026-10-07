package server

// 需求立案的广播腿（t_170 声色同门）：立案成功（WS req_create 与 HTTP
// POST 两条路）必须向项目房落一帧 req{created}——带全量快照、from＝立
// 案人，与 task 族同语言。前端绿点门（chat/rooms.js workFresh）靠这帧
// 点亮；拒执（denied）仍是纯私回，房间不闻。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
)

// drainReqFrames collects the observer's frames for one settling beat.
func drainReqFrames(c *chat.Client) []chat.Message {
	var got []chat.Message
	deadline := time.After(250 * time.Millisecond)
	for {
		select {
		case m := <-c.Receive():
			got = append(got, m)
		case <-deadline:
			return got
		}
	}
}

// reqBroadcasts filters down to req{created} room broadcasts.
func reqBroadcasts(frames []chat.Message) []chat.Message {
	var out []chat.Message
	for _, m := range frames {
		if m.Type == chat.MsgReq && m.Event == "created" && m.Req != nil {
			out = append(out, m)
		}
	}
	return out
}

func TestReqCreateAsBroadcastsCreated(t *testing.T) {
	lobby := chat.NewHub()
	reqs := requirements.OpenMemory()
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{Requirements: reqs}, LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	obs := lobby.AttachObserver(chat.LobbyKey, false, 0)

	reply := s.plansFace().ReqCreateAs(lobby, "房主",
		&requirements.Req{ProjectKey: chat.LobbyKey, Title: "要个夜间模式", Body: "护眼"},
		chat.LobbyKey)
	if reply.Event != "created" || reply.Req == nil {
		t.Fatalf("立案应成功：%+v", reply)
	}
	// 私回之外，房间必须听见同一份快照（绿点门的喂帧）
	got := reqBroadcasts(drainReqFrames(obs))
	if len(got) != 1 {
		t.Fatalf("应广播一帧 req created，得 %d", len(got))
	}
	if got[0].Req.ID == "" || got[0].Req.Title != "要个夜间模式" {
		t.Fatalf("广播快照不完整：%+v", got[0].Req)
	}
	if got[0].From != "房主" {
		t.Fatalf("广播 from 应为立案人，得 %q", got[0].From)
	}
}

func TestReqHTTPPostBroadcastsCreated(t *testing.T) {
	lobby := chat.NewHub()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	reqs := requirements.OpenMemory()
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			Requirements: reqs,
		},
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	obs := lobby.AttachObserver(chat.LobbyKey, false, 0)

	payload, _ := json.Marshal(map[string]any{"title": "要个深色主题"})
	resp, err := http.Post(addrHTTP(s)+"/p/"+chat.LobbyKey+"/reqs",
		"application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /reqs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("立案应 200，得 %d", resp.StatusCode)
	}
	got := reqBroadcasts(drainReqFrames(obs))
	if len(got) != 1 {
		t.Fatalf("HTTP 立案也应广播一帧 req created，得 %d", len(got))
	}
	if got[0].Req.Title != "要个深色主题" || got[0].From != "房主" {
		t.Fatalf("广播快照/立案人不符：%+v %+v", got[0].Req, got[0].From)
	}
}

func TestReqHTTPPostReachesProjectRoom(t *testing.T) {
	lobby := chat.NewHub()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	if _, err := projStore.Create(projects.Project{Key: "proj-r", Name: "需求广播项目",
		Workspace: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-r"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "")
	projHub, err := registry.Hub("proj-r") // 房 hub 先转起来（Rooms() 才有得查）
	if err != nil {
		t.Fatalf("registry.Hub: %v", err)
	}
	reqs := requirements.OpenMemory()
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			Requirements: reqs,
		},
		Registry:  registry,
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	obs := projHub.AttachObserver("proj-r", false, 0)

	payload, _ := json.Marshal(map[string]any{"title": "项目房也要点亮"})
	resp, err := http.Post(addrHTTP(s)+"/p/proj-r/reqs",
		"application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /reqs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("立案应 200，得 %d", resp.StatusCode)
	}
	// 广播落进项目房自己的 hub（房行绿点的喂帧面），大厅不串台
	if got := reqBroadcasts(drainReqFrames(obs)); len(got) != 1 {
		t.Fatalf("项目房应收到一帧 req created，得 %d", len(got))
	}
}
