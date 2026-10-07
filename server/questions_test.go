package server

// questions_test.go — 向房主提问的读面（v2.5）：GET /p/{key}/questions
// 出该房开放中的提问卡快照（问题帧不进聊天历史，冷窗靠这一拉）；
// 未知/未开张项目 404（/p face 的统一规则）。

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

func TestProjectQuestionsEndpoint(t *testing.T) {
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

	hub, err := registry.Hub("proj-a")
	if err != nil {
		t.Fatalf("registry hub: %v", err)
	}

	// 空开放集：questions 为空数组而非 null（前端 ||[] 同族容错，但形状按约）
	resp, err := http.Get(addrHTTP(s) + "/p/proj-a/questions")
	if err != nil {
		t.Fatalf("GET questions: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Project   string          `json:"project"`
		Questions []chat.Question `json:"questions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if body.Project != "proj-a" || body.Questions == nil || len(body.Questions) != 0 {
		t.Fatalf("空开放集的形状不符：%+v", body)
	}

	// 出卡后快照如实携带结构化问题与窗口
	hub.AskQuestion(&chat.Question{ID: "q_t1", From: "小牛", TS: 1700000000, Due: 1700001800,
		Prompt: "标签视觉方向",
		Asks: []chat.QuestionAsk{{Question: "主打哪种风格？", Header: "风格",
			Options: []chat.QuestionOption{{Label: "像素复古", Desc: "8-bit 办公室"}}}}})
	resp, err = http.Get(addrHTTP(s) + "/p/proj-a/questions")
	if err != nil {
		t.Fatalf("GET questions: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if len(body.Questions) != 1 {
		t.Fatalf("应有 1 张开放卡，得到 %d", len(body.Questions))
	}
	q := body.Questions[0]
	if q.ID != "q_t1" || q.From != "小牛" || q.Due != 1700001800 ||
		len(q.Asks) != 1 || q.Asks[0].Options[0].Label != "像素复古" {
		t.Fatalf("快照丢字段：%+v", q)
	}

	// 未知项目 404
	resp, err = http.Get(addrHTTP(s) + "/p/no-such/questions")
	if err != nil {
		t.Fatalf("GET unknown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知项目应 404，得到 %d", resp.StatusCode)
	}
}
