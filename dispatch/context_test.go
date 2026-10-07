package dispatch

// context_test.go — P1 上下文用量仪表的行为面：SeatSessions 的成员
// 快照（名字序、会话对得上），fleet 端点 /dispatch/context 的接线
// （GET 200 JSON、POST 拒绝），空舰队不起子进程照常应答。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// TestSeatSessionsSnapshotsMembers — 在座成员的 (名, 会话, 状态) 三元
// 组快照：两位成员各带各的会话 id，名字序稳定（读面确定性）。
func TestSeatSessionsSnapshotsMembers(t *testing.T) {
	_, d, _ := startCfgRoom(t, nil)
	seats := d.SeatSessions()
	if len(seats) != 2 {
		t.Fatalf("应两位成员，实 %d", len(seats))
	}
	byName := map[string]SeatSession{}
	for _, s := range seats {
		byName[s.Name] = s
	}
	if byName["甲"].Session != "s-kick-a" || byName["乙"].Session != "s-kick-b" {
		t.Fatalf("会话对不上：%v", seats)
	}
	if byName["甲"].Status == "" {
		t.Fatalf("状态不该为空：%v", seats)
	}
}

// TestContextEndpointServes — /dispatch/context 的接线面：GET 出 200
// 与可解码的 JSON 数组（空舰队＝空数组，不起台账子进程）；POST 拒
// 405。
func TestContextEndpointServes(t *testing.T) {
	f := inertFleet(t)
	h := f.HTTPHandler()

	req := httptest.NewRequest(http.MethodGet, "/dispatch/context", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET 应 200，实 %d：%s", rr.Code, rr.Body.String())
	}
	var rows []ProjectContextGauges
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("应答应可解码：%v（%s）", err, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/dispatch/context", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 应 405，实 %d", rr.Code)
	}
}

// TestContextGaugesPairsProjectsWithOwnMembers — 回归：项目标签必须跟着
// 自己的成员走。分离排序的错位 bug（keys 排了序、dispatchers 没跟着
// 重排）依赖 map 迭代序，上线实测把大厅成员贴到了 book 标签下——两个
// 调度器起在同一 fleet 里，断言成员名与项目键严格对号。
func TestContextGaugesPairsProjectsWithOwnMembers(t *testing.T) {
	dir := t.TempDir()
	projs, err := projects.Open(filepath.Join(dir, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projs.EnsureLobby(dir); err != nil {
		t.Fatal(err)
	}
	// z-b 用自己的子目录：v2.13 起两个在册项目不得共用一个工作区（隔离
	// 绑定按目录一对一）——本测试考的是仪表配对，不是目录语义
	if _, err := projs.Create(projects.Project{Key: "z-b", Name: "乙项目", Workspace: filepath.Join(dir, "zb")}); err != nil {
		t.Fatal(err)
	}
	if _, err := projs.Activate("z-b"); err != nil { // Create 落账为 draft，激活才有房
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
	agentStore.Upsert("大厅甲", "工程师", "", "")
	agentStore.BindSession("大厅甲", "s-ctx-lob")
	if _, err := staff.Join(chat.LobbyKey, "大厅甲", "工程师", "s-ctx-lob"); err != nil {
		t.Fatal(err)
	}
	agentStore.Upsert("项目乙", "工程师", "", "")
	agentStore.BindSession("项目乙", "s-ctx-prj")
	if _, err := staff.Join("z-b", "项目乙", "工程师", "s-ctx-prj"); err != nil {
		t.Fatal(err)
	}
	f := StartFleet(nil, agentStore, projs, staff, nil, Config{
		Workspace: dir, PatrolEvery: -1, DailyAt: "off", InboxDir: t.TempDir(),
	})
	hubA, hubB := chat.NewHub(), chat.NewHub()
	if !f.Attach(newGateBridge(), map[string]*chat.Hub{chat.LobbyKey: hubA, "z-b": hubB}) {
		t.Fatal("Attach 应成功")
	}
	t.Cleanup(f.Stop)

	// Attach 的项目调度器起在异步路径上（起房走完 staffing 采用才算
	// 归位）——等两个键都在，才断言配对。
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.byKey) >= 2 && f.byKey["z-b"] != nil
	}, "两个项目的调度器都应起")

	rows := f.ContextGauges()
	if len(rows) != 2 {
		t.Fatalf("应两行项目，实 %d：%v", len(rows), rows)
	}
	got := map[string][]string{}
	for _, r := range rows {
		names := make([]string, 0, len(r.Members))
		for _, m := range r.Members {
			names = append(names, m.Name)
		}
		got[r.Project] = names
	}
	if len(got[chat.LobbyKey]) != 1 || got[chat.LobbyKey][0] != "大厅甲" {
		t.Fatalf("大厅行应是大厅甲：%v", got)
	}
	if len(got["z-b"]) != 1 || got["z-b"][0] != "项目乙" {
		t.Fatalf("项目行应是项目乙：%v", got)
	}
}
