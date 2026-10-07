package server

// pause_test.go — 房间暂停的服务端契约：GET/POST /p/{key}/pause 的读写
// 面、翻转广播（"pause" 帧 reminder-only ＋ event=pause 系统行留痕）、
// ResumeSink 只在真恢复（false 方向）触发、幂等重写不刷屏、未知项目
// 404、/projects 随行 paused 位、welcome 水合（迟到窗口一见即冻结）。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
)

type pauseStage struct {
	s        *Server
	lobby    *chat.Hub
	room     *chat.Hub
	staff    *staffing.Store
	resumed  []string
	lobbyTap *serverTap
	roomTap  *serverTap
}

func startPauseStage(t *testing.T) *pauseStage {
	t.Helper()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "")
	// 大厅与 main 同路：registry.Lobby()（hubLocked 装欢迎帧的 PausedFn）
	lobby, err := registry.Lobby()
	if err != nil {
		t.Fatalf("registry.Lobby: %v", err)
	}
	lobby.Join("房主", "boss", false)
	if _, err := projStore.Create(projects.Project{Key: "proj-pz", Name: "暂停项目",
		Workspace: t.TempDir() + "/w"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-pz"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	staff, err := staffing.Open("")
	if err != nil {
		t.Fatalf("staffing.Open: %v", err)
	}
	// welcome 水合线（main 同款）：registry 的 PausedFn 读 staffing
	registry.PausedFn = func(key string) bool { return staff.SettingsOf(key).Paused }
	st := &pauseStage{lobby: lobby, staff: staff}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			StaffStore:   staff,
		},
		LocalName: "房主",
		Registry:  registry,
		Fleet: FleetFuncs{ResumeRoomFn: func(project string) {
			st.resumed = append(st.resumed, project)
		}}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	st.s = s
	if st.room, err = registry.Hub("proj-pz"); err != nil {
		t.Fatalf("registry hub: %v", err)
	}
	st.lobbyTap = newServerTap(lobby)
	st.roomTap = newServerTap(st.room)
	return st
}

func pausePost(t *testing.T, s *Server, path, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(addrHTTP(s)+path, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func pauseGet(t *testing.T, s *Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(addrHTTP(s) + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestPauseReadWriteFace: GET 回显缺省 false；POST 翻写落 staffing、
// 广播 "pause" 帧、留 event=pause 系统行；恢复腿触发 ResumeSink。
func TestPauseReadWriteFace(t *testing.T) {
	st := startPauseStage(t)

	if code, out := pauseGet(t, st.s, "/p/proj-pz/pause"); code != 200 || out["paused"] != false {
		t.Fatalf("GET 初始态: code=%d out=%v", code, out)
	}

	code, out := pausePost(t, st.s, "/p/proj-pz/pause", `{"paused":true}`)
	if code != 200 || out["paused"] != true {
		t.Fatalf("POST 暂停: code=%d out=%v", code, out)
	}
	if !st.staff.SettingsOf("proj-pz").Paused {
		t.Fatal("暂停未落 staffing 设置")
	}
	pauseWait(t, func() bool {
		_, ok := st.roomTapFrame("pause", "paused")
		return ok
	}, "房间应收到 pause 帧（event=paused）")
	if _, ok := st.roomTap.find("system", "暂停了办公室"); !ok {
		t.Fatal("房间应收 event=pause 系统留痕行")
	}

	code, out = pausePost(t, st.s, "/p/proj-pz/pause", `{"paused":false}`)
	if code != 200 || out["paused"] != false {
		t.Fatalf("POST 恢复: code=%d out=%v", code, out)
	}
	pauseWait(t, func() bool {
		_, ok := st.roomTapFrame("pause", "resumed")
		return ok
	}, "房间应收到 pause 帧（event=resumed）")
	if len(st.resumed) != 1 || st.resumed[0] != "proj-pz" {
		t.Fatalf("恢复腿应触发一次 ResumeSink(proj-pz): %v", st.resumed)
	}
}

// roomTapFrame finds one broadcast frame by type+event (the tap's find
// keys on Text — pause frames carry no text).
func (st *pauseStage) roomTapFrame(typ, event string) (chat.Message, bool) {
	st.roomTap.mu.Lock()
	defer st.roomTap.mu.Unlock()
	for _, m := range st.roomTap.lines {
		if m.Type == typ && m.Event == event {
			return m, true
		}
	}
	return chat.Message{}, false
}

// roomTapCountText counts broadcast frames by type+text (system lines).
func (st *pauseStage) roomTapCountText(typ, substr string) int {
	st.roomTap.mu.Lock()
	defer st.roomTap.mu.Unlock()
	n := 0
	for _, m := range st.roomTap.lines {
		if m.Type == typ && strings.Contains(m.Text, substr) {
			n++
		}
	}
	return n
}

// roomTapEventCount counts broadcast frames of one type carrying any event
// (the pause flips).
func (st *pauseStage) roomTapEventCount(typ string) int {
	st.roomTap.mu.Lock()
	defer st.roomTap.mu.Unlock()
	n := 0
	for _, m := range st.roomTap.lines {
		if m.Type == typ && m.Event != "" {
			n++
		}
	}
	return n
}

func pauseWait(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

// TestPauseIdempotentRewriteQuiet: 同值重写只回执——不再广播、不再留
// 痕、不再触发恢复腿。
func TestPauseIdempotentRewriteQuiet(t *testing.T) {
	st := startPauseStage(t)
	pausePost(t, st.s, "/p/proj-pz/pause", `{"paused":true}`)
	pauseWait(t, func() bool {
		_, ok := st.roomTapFrame("pause", "paused")
		return ok
	}, "第一写应广播")
	sysCount := func() int { return st.roomTapCountText("system", "暂停了办公室") }
	pauseWait(t, func() bool { return sysCount() == 1 }, "第一写应留一条系统行")

	pausePost(t, st.s, "/p/proj-pz/pause", `{"paused":true}`)
	time.Sleep(120 * time.Millisecond) // 负断言的沉底窗：第二写若留痕/广播，此刻必已可见
	if n := sysCount(); n != 1 {
		t.Fatalf("幂等重写不应再留痕: %d", n)
	}
	if n := st.roomTapEventCount("pause"); n != 1 {
		t.Fatalf("幂等重写不应再广播: %d", n)
	}
	pausePost(t, st.s, "/p/proj-pz/pause", `{"paused":false}`)
	pausePost(t, st.s, "/p/proj-pz/pause", `{"paused":false}`)
	if len(st.resumed) != 1 {
		t.Fatalf("幂等重写不应再触恢复腿: %v", st.resumed)
	}
}

// TestPauseUnknownProject404AndProjectsMirror: 未知项目 404；/projects
// 列表随行 paused 位（切换栏按钮的状态源）。
func TestPauseUnknownProject404AndProjectsMirror(t *testing.T) {
	st := startPauseStage(t)
	if code, _ := pausePost(t, st.s, "/p/no-such/pause", `{"paused":true}`); code != 404 {
		t.Fatalf("未知项目应 404: %d", code)
	}
	if code, _ := pauseGet(t, st.s, "/p/no-such/pause"); code != 404 {
		t.Fatalf("未知项目 GET 应 404: %d", code)
	}

	pausePost(t, st.s, "/p/proj-pz/pause", `{"paused":true}`)
	resp, err := http.Get(addrHTTP(st.s) + "/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&list)
	found := false
	for _, p := range list {
		if p["key"] == "proj-pz" {
			found = true
			if p["paused"] != true {
				t.Fatalf("/projects 应随行 paused=true: %v", p)
			}
		}
	}
	if !found {
		t.Fatal("/projects 未列出 proj-pz")
	}
}

// TestPauseWelcomeHydrates: welcome 帧携带当前暂停态（迟到窗口一见即
// 冻结办公室）——registry.PausedFn 真源接线下的读面。
func TestPauseWelcomeHydrates(t *testing.T) {
	st := startPauseStage(t)
	st.staff.SetPaused("proj-pz", true)

	obs := st.room.AttachObserver("proj-pz", false, 0)
	select {
	case m := <-obs.Receive():
		if m.Type != chat.MsgWelcome || !m.Paused {
			t.Fatalf("welcome 应带 paused=true: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("welcome 未到")
	}
}

// TestPauseLobbyFace: 大厅（default 键）同面可用——暂停大厅走服务端
// 自己的 hub。
func TestPauseLobbyFace(t *testing.T) {
	st := startPauseStage(t)
	st.staff.SetPaused(chat.LobbyKey, true)
	obs := st.lobby.AttachObserver("default", false, 0)
	select {
	case m := <-obs.Receive():
		if m.Type != chat.MsgWelcome || !m.Paused {
			t.Fatalf("大厅 welcome 应带 paused=true: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("welcome 未到")
	}
	if code, out := pauseGet(t, st.s, "/p/default/pause"); code != 200 || out["paused"] != true {
		t.Fatalf("大厅 GET: code=%d out=%v", code, out)
	}
	if _, ok := st.roomTapFrame("pause", "paused"); ok {
		t.Fatal("大厅只写 staffing 未 POST——不该有 pause 广播")
	}
}
