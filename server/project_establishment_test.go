package server

// 项目编制的必须面（v2.5）：编制对项目不再是可选项——项目经生命周期
// 面（POST /p/{key}/lifecycle activate）开张的瞬间，编制表就位并带齐
// 三行系统岗：编排者/HR 分项目各一套（不同项目的 HR 互相独立——在岗
// 只数本项目的房间），小助手全工作室唯一（同一行进每张表，免在岗对
// 账）。行面照旧拒绝系统岗的改/删；空缺的系统岗在报告面照常算缺——
// 看护据此提醒，到岗的路是「招牛马」，不是把必须行删了重建。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
)

// startProjSeedStudio boots a server with docs + project store + registry
// and NO project yet — the test drives the full lifecycle face (立项 →
// 开张) so the activation consequences (projectOpen's seeding leg) run
// exactly as production would.
func startProjSeedStudio(t *testing.T) *Server {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "")
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			Docs:         docs,
		},
		Registry:  registry,
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// lifecyclePOST fires one lifecycle action and answers the status.
func lifecyclePOST(t *testing.T, s *Server, key, action string) int {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"action": action})
	resp, err := http.Post(addrHTTP(s)+"/p/"+key+"/lifecycle", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST lifecycle %s/%s: %v", key, action, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// projEstRows fetches one project's establishment report rows.
func projEstRows(t *testing.T, s *Server, key string) (map[string]kb.EstablishmentRow, int) {
	t.Helper()
	resp, err := http.Get(addrHTTP(s) + "/p/" + key + "/establishment")
	if err != nil {
		t.Fatalf("GET /p/%s/establishment: %v", key, err)
	}
	defer resp.Body.Close()
	var report kb.EstablishmentReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.ParseError != "" {
		t.Fatalf("表不应损坏：%s", report.ParseError)
	}
	byKey := map[string]kb.EstablishmentRow{}
	for _, r := range report.Rows {
		byKey[r.Key] = r
	}
	return byKey, report.Rev
}

// TestProjectActivationSeedsEstablishment pins the activation leg: the
// freshly opened project's table exists with the three system rows —
// all 必须/system, the advisor presence-exempt, the two flagship posts
// counting this project's room only (empty room = 缺 1, the hire path's
// visible pressure) — and the row face keeps refusing their mutation.
func TestProjectActivationSeedsEstablishment(t *testing.T) {
	s := startProjSeedStudio(t)

	// 立项（草稿）：编制面尚不可读（404——与 GET 的 active 门一致）
	b, _ := json.Marshal(map[string]any{"key": "proj-x", "name": "项目乙",
		"workspace": t.TempDir() + "/proj-x"})
	resp, err := http.Post(addrHTTP(s)+"/projects", "application/json", bytes.NewReader(b))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("立项应 200：%v %d", err, resp.StatusCode)
	}
	resp.Body.Close()
	if got := lifecyclePOST(t, s, "proj-x", "activate"); got != http.StatusOK {
		t.Fatalf("开张应 200，得 %d", got)
	}

	rows, rev := projEstRows(t, s, "proj-x")
	if rev < 1 {
		t.Fatalf("开张即建表，rev 应 ≥1：%d", rev)
	}
	for _, key := range []string{"orchestrator", "hr", "assistant"} {
		r, ok := rows[key]
		if !ok {
			t.Fatalf("开张后项目表应带系统岗 %s：%v", key, rows)
		}
		if !r.Must || !r.System {
			t.Fatalf("系统岗 %s 应 必须+system：%+v", key, r)
		}
	}
	// 小助手免在岗对账；编排者/HR 数本项目的房间——空房即缺 1（招牛马
	// 的可见压力），不数大厅的人（分项目各一套的语义就在这一格）
	if adv := rows["assistant"]; adv.Missing != 0 || adv.Present != nil {
		t.Fatalf("小助手应免在岗对账：%+v", adv)
	}
	for _, key := range []string{"orchestrator", "hr"} {
		if r := rows[key]; r.Missing != 1 || len(r.Present) != 0 {
			t.Fatalf("空项目房 %s 应缺 1（编制 1/在岗 0）：%+v", key, r)
		}
	}
	// HR 行带手册锚（roles/hr——出生 --role 与手册同源）
	if r := rows["hr"]; r.Role != "人事" || r.Manual != "roles/hr" {
		t.Fatalf("HR 行应与注册表同源：%+v", r)
	}

	// 行面照旧拒绝系统岗的改/删（必须行由系统维护）
	if status, _ := estRowPOST(t, s, "/p/proj-x/establishment/row", map[string]any{
		"op": "delete", "key": "hr", "expect_rev": rev}); status != http.StatusForbidden {
		t.Fatalf("项目系统岗删行应 403，得 %d", status)
	}
	if status, _ := estRowPOST(t, s, "/p/proj-x/establishment/row", map[string]any{
		"op": "update", "key": "orchestrator", "expect_rev": rev,
		"row": map[string]any{"key": "orchestrator", "name": "编排", "role": "排期编排", "headcount": 1},
	}); status != http.StatusForbidden {
		t.Fatalf("项目系统岗改行应 403，得 %d", status)
	}

	// 自建岗照常可加（系统岗不挡房主自己的编制）
	if status, body := estRowPOST(t, s, "/p/proj-x/establishment/row", map[string]any{
		"op": "add", "expect_rev": rev,
		"row": map[string]any{"key": "be", "name": "后端", "role": "后端", "headcount": 2},
	}); status != http.StatusOK {
		t.Fatalf("自建岗 add 应 200，得 %d：%v", status, body)
	}
	rows, _ = projEstRows(t, s, "proj-x")
	if rows["be"].Headcount != 2 || rows["be"].Must {
		t.Fatalf("自建行应原样落表：%+v", rows["be"])
	}
}
