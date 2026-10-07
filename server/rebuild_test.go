package server

// rebuild_test.go — the recompile-restart face's contract: POST-only,
// 404 for embeds that never wired the choreography, and — the load-
// bearing one — a build that cannot find the source tree answers 400
// with the reason and drops NO restart token (go test 的 cwd 是包目录，
// 源码根不在候选里，正好钉住「失败的编译绝不重启」这道门)。真实的
// 编译成功路径（令牌投递→交接）住在 main 的编排里，端到端由设置卡
// 手验；409 双保险的 busy 锁在无源码环境下永远走不到，不在此伪造。
// 全室喊话契约也钉在这条失败道上：发起/失败两条通告必须落进每个
// 在役房间（大厅＋已开张项目房）的历史——成功分支的通告同一helper
// 出门，词形不另测。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

func startRebuildable(t *testing.T) (*Server, chan struct{}) {
	t.Helper()
	hub := chat.NewHub()
	hub.Join("房主", "boss", false)
	ch := make(chan struct{}, 1)
	s, err := Start(hub, Options{Endpoint: EndpointConfig{PreferredPort: -1}, RebuildCh: ch})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, ch
}

func postRebuildBody(t *testing.T, s *Server) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(addrHTTP(s)+"/rebuild", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatalf("POST /rebuild: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestRebuildWithoutSourceRefusesAndKeepsServing(t *testing.T) {
	s, ch := startRebuildable(t)
	code, out := postRebuildBody(t, s)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（body: %v）", code, out)
	}
	if out["ok"] == true {
		t.Error("失败的编译不得回 ok")
	}
	if msg, _ := out["error"].(string); msg == "" {
		t.Error("400 必须带原因（无源码/无工具链），不能只给状态行")
	}
	select {
	case <-ch:
		t.Fatal("失败的编译绝不能投重启令牌")
	default:
	}
	// 门没焊死：旧进程照常服务（/ 仍是我们的名字）
	if r, err := http.Get(addrHTTP(s) + "/"); err != nil {
		t.Fatalf("GET /: %v", err)
	} else {
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("GET / status = %d, want 200", r.StatusCode)
		}
	}
}

func TestRebuildMethodAndRouting(t *testing.T) {
	s, _ := startRebuildable(t)
	if r, err := http.Get(addrHTTP(s) + "/rebuild"); err != nil {
		t.Fatalf("GET /rebuild: %v", err)
	} else {
		r.Body.Close()
		if r.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET status = %d, want 405", r.StatusCode)
		}
	}
	if r, err := http.Post(addrHTTP(s)+"/rebuild/x", "application/json", bytes.NewBufferString("{}")); err != nil {
		t.Fatalf("POST /rebuild/x: %v", err)
	} else {
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("子路径 status = %d, want 404", r.StatusCode)
		}
	}
}

func TestRebuildAbsentWithoutChannel(t *testing.T) {
	hub := chat.NewHub()
	s, err := Start(hub, Options{Endpoint: EndpointConfig{PreferredPort: -1}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	if code, _ := postRebuildBody(t, s); code != http.StatusNotFound {
		t.Errorf("未接线 status = %d, want 404", code)
	}
}

// TestRebuildAnnouncesToEveryRoom — 重编译是全室级别的事：发起与
// 失败两条系统通告必须落进大厅与每个已开张项目房的历史（无源码
// 环境编译必败，正好走失败道；成功道出自同一 helper，不另钉）。
// 通告帧还必须带 chat.EventRebuild 标记：全室同刻落地的环境新闻不
// 欠任何一间房一次已读——客户端凭标记豁免未读徽标，否则一次重编译
// 全部项目冒两轮红点。
func TestRebuildAnnouncesToEveryRoom(t *testing.T) {
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
	room, err := registry.Hub("proj-a")
	if err != nil {
		t.Fatalf("registry.Hub: %v", err)
	}
	ch := make(chan struct{}, 1)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, RebuildCh: ch, Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)

	if code, _ := postRebuildBody(t, s); code != http.StatusBadRequest {
		t.Fatalf("无源码环境 status ≠ 400，后续断言无意义（got %d）", code)
	}
	for name, h := range map[string]*chat.Hub{"Niuma_Studio": lobby, "项目房": room} {
		var joined strings.Builder
		rebuildEvents := 0
		for _, m := range h.History() {
			joined.WriteString(m.Text + "\n")
			if m.Event == chat.EventRebuild {
				rebuildEvents++
			}
		}
		txt := joined.String()
		if !strings.Contains(txt, "已发起源码重编译") {
			t.Errorf("%s：没收到「发起重编译」通告（history: %q）", name, txt)
		}
		if !strings.Contains(txt, "重编译失败") {
			t.Errorf("%s：没收到失败收口通告（history: %q）", name, txt)
		}
		if strings.Contains(txt, "已重新编译") {
			t.Errorf("%s：编译失败，不该出现成功通告（history: %q）", name, txt)
		}
		if rebuildEvents != 2 {
			t.Errorf("%s：带 EventRebuild 标记的通告 = %d 条，want 2（发起＋失败；缺标记客户端就无法豁免未读红点）", name, rebuildEvents)
		}
	}
	select {
	case <-ch:
		t.Fatal("失败的编译绝不能投重启令牌")
	default:
	}
}
