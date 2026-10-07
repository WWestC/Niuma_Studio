package server

// The establishment row CRUD face (v2.4): the 牛马管理 panel's write
// path over the frozen-contract table. Pins: add/update/delete of a
// custom row under the optimistic rev; the system posts (必须:
// 编排者/HR/小助手) refuse mutation with 403; a duplicate add and a
// stale expect_rev answer 409 with the room's current rev; a project
// scope creates its absent table on the first add; the GET faces
// carry must/system/rev for the UI's locked rows and retry anchor.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
)

// startEstStudio boots a server with a docs store holding one
// six-column legacy lobby table (the migration's target shape — the
// row API must be equally at home on it) plus one active project.
func startEstStudio(t *testing.T) *Server {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	legacy := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 |\n" +
		"|---|---|---|---|---|---|\n" +
		"| orchestrator | 编排者 | 排期编排 | 1 | 是 |  |\n" +
		"| developer | 开发 | 前端开发 | 1 | 否 | roles/developer |\n"
	if _, err := docs.Write("ops/establishment", "编制表", legacy, "房主"); err != nil {
		t.Fatalf("预置旧表：%v", err)
	}
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "")
	if _, err := projStore.Create(projects.Project{Key: "proj-a", Name: "项目甲",
		Workspace: t.TempDir() + "/proj-a"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-a"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
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

// estRowPOST fires one row op and answers status + decoded body.
func estRowPOST(t *testing.T, s *Server, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(addrHTTP(s)+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// getEstRows fetches the lobby report's rows keyed by 岗位 key.
func getEstRows(t *testing.T, s *Server) (map[string]kb.EstablishmentRow, int) {
	t.Helper()
	resp, err := http.Get(addrHTTP(s) + "/kb/establishment")
	if err != nil {
		t.Fatalf("GET /kb/establishment: %v", err)
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

func TestEstablishmentRowCRUD(t *testing.T) {
	s := startEstStudio(t)
	_, rev := getEstRows(t, s)

	// add：新自建岗（QA），必须=false 由服务端语义保证
	status, body := estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "add", "expect_rev": rev,
		"row": map[string]any{"key": "qa", "name": "测试", "role": "测试工程师",
			"headcount": 2, "auto_fill": true, "manual": "roles/qa"},
	})
	if status != http.StatusOK {
		t.Fatalf("加岗应 200，得 %d：%v", status, body)
	}
	rows, rev2 := getEstRows(t, s)
	qa := rows["qa"]
	if qa.Name != "测试" || qa.Headcount != 2 || !qa.AutoFill || qa.Must || qa.System {
		t.Fatalf("加岗行与请求漂移：%+v", qa)
	}
	if rev2 <= rev {
		t.Fatalf("写后 rev 应前进：%d → %d", rev, rev2)
	}
	// 小助手行随报告在场（迁移未跑、种子未落——报告面按注册表盖系统标记）；
	// 这里旧表无 assistant 行，报告只回表内行，断言三系统岗中的表内两行已锁
	if rows["orchestrator"].System != true || rows["orchestrator"].Must != true {
		t.Fatalf("旗舰行应带 system/must 标记：%+v", rows["orchestrator"])
	}

	// update：改编制与身份（身份列 = birth --role 的精确锚）
	status, body = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "update", "key": "developer", "expect_rev": rev2,
		"row": map[string]any{"key": "developer", "name": "开发", "role": "前端",
			"headcount": 2, "auto_fill": false, "manual": "roles/developer"},
	})
	if status != http.StatusOK {
		t.Fatalf("改岗应 200，得 %d：%v", status, body)
	}
	rows, rev3 := getEstRows(t, s)
	if dev := rows["developer"]; dev.Role != "前端" || dev.Headcount != 2 {
		t.Fatalf("改岗未生效：%+v", dev)
	}

	// 系统岗拒绝改动（403）：update 与 delete 双查
	status, body = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "update", "key": "orchestrator", "expect_rev": rev3,
		"row": map[string]any{"key": "orchestrator", "name": "编排", "role": "排期编排",
			"headcount": 2, "auto_fill": true},
	})
	if status != http.StatusForbidden {
		t.Fatalf("系统岗改行应 403，得 %d：%v", status, body)
	}
	status, body = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "delete", "key": "hr", "expect_rev": rev3})
	if status != http.StatusForbidden {
		t.Fatalf("系统岗删行应 403，得 %d：%v", status, body)
	}

	// 乐观锁：旧 rev 写 → 409 带 current_rev
	status, body = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "delete", "key": "developer", "expect_rev": rev})
	if status != http.StatusConflict {
		t.Fatalf("旧 rev 应 409，得 %d：%v", status, body)
	}
	if body["current_rev"].(float64) != float64(rev3) {
		t.Fatalf("409 应带最新 rev %d，得 %v", rev3, body["current_rev"])
	}

	// 重复 add → 409
	status, _ = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "add", "expect_rev": rev3,
		"row": map[string]any{"key": "qa", "name": "测试", "role": "测试工程师", "headcount": 1}})
	if status != http.StatusConflict {
		t.Fatalf("重复岗位 key 应 409，得 %d", status)
	}

	// 自建岗带必须=true → 400（必须列是系统岗专属）
	status, _ = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "add", "expect_rev": rev3,
		"row": map[string]any{"key": "ops", "name": "运维", "role": "运维", "headcount": 1, "must": true}})
	if status != http.StatusBadRequest {
		t.Fatalf("自建岗带必须应 400，得 %d", status)
	}

	// delete：用最新 rev 删 qa
	status, body = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "delete", "key": "qa", "expect_rev": rev3})
	if status != http.StatusOK {
		t.Fatalf("删岗应 200，得 %d：%v", status, body)
	}
	rows, _ = getEstRows(t, s)
	if _, ok := rows["qa"]; ok {
		t.Fatal("删岗后 qa 行不应再在")
	}
}

func TestEstablishmentRowProjectScopeCreatesTable(t *testing.T) {
	s := startEstStudio(t)

	// 项目缺表：GET 给空 rows + rev 0；add 直接建表
	resp, err := http.Get(addrHTTP(s) + "/p/proj-a/establishment")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var face struct {
		Rows []kb.EstablishmentRow `json:"rows"`
		Rev  int                   `json:"rev"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&face)
	resp.Body.Close()
	if len(face.Rows) != 0 || face.Rev != 0 {
		t.Fatalf("缺表项目应空 rows/rev0，得 %+v", face)
	}

	status, body := estRowPOST(t, s, "/p/proj-a/establishment/row", map[string]any{
		"op": "add", "expect_rev": 0,
		"row": map[string]any{"key": "be", "name": "后端", "role": "后端", "headcount": 1}})
	if status != http.StatusOK {
		t.Fatalf("项目首行 add 应 200，得 %d：%v", status, body)
	}
	// the answer IS the fresh report
	if body["rev"].(float64) < 1 {
		t.Fatalf("建表后 rev 应 ≥1：%v", body["rev"])
	}
	doc, err := s.opts.Stores.Docs.Get("p/proj-a/establishment", 0)
	if err != nil {
		t.Fatalf("项目表应已建：%v", err)
	}
	rows := mustParseRowsT(t, doc.Body)
	if len(rows) != 1 || rows[0].Key != "be" {
		t.Fatalf("项目表应恰一行 be：%s", doc.Body)
	}
	// 非激活项目 404（与 GET 同规则）
	if _, err := projStore404(t, s); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func projStore404(t *testing.T, s *Server) (int, error) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"op": "add", "row": map[string]any{
		"key": "x", "name": "x", "role": "x", "headcount": 1}})
	resp, err := http.Post(addrHTTP(s)+"/p/nope/establishment/row", "application/json", bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return resp.StatusCode, fmt.Errorf("未知项目应 404，得 %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func mustParseRowsT(t *testing.T, body string) []kb.EstablishmentRow {
	t.Helper()
	rows, perr := kb.EstablishmentRows(body)
	if perr != "" {
		t.Fatalf("解析失败：%s\n%s", perr, body)
	}
	return rows
}

// TestEstablishmentReportOrdersSystemPostsFirst pins the report faces'
// display order — 小助手最上、系统岗（编排者/HR）紧随、自建岗保持
// 文档行序压尾：一张乱序表（自建岗在前、系统岗沉底——正是 v2.4 迁移
// 把小助手补在末行的存量形状）在两个 GET 面上都按此序回来，而文档
// 本身的行序一字不动（重排只是展示，绝不回写房主的表）。
func TestEstablishmentReportOrdersSystemPostsFirst(t *testing.T) {
	s := startEstStudio(t)
	scrambled := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| developer | 开发 | 前端开发 | 1 | 否 | roles/developer | 否 |\n" +
		"| assistant | 小助手 | 使用顾问 | 1 | 否 |  | 是 |\n" +
		"| designer | 美术 | 美术设计师 | 1 | 否 |  | 否 |\n" +
		"| hr | HR | 人事 | 1 | 是 | roles/hr | 是 |\n" +
		"| orchestrator | 编排者 | 排期编排 | 1 | 是 |  | 是 |\n"
	if _, err := s.opts.Stores.Docs.Write("ops/establishment", "编制表", scrambled, "房主"); err != nil {
		t.Fatalf("铺乱序 Niuma_Studio 表：%v", err)
	}
	if _, err := s.opts.Stores.Docs.Write("p/proj-a/establishment", "编制表", scrambled, "房主"); err != nil {
		t.Fatalf("铺乱序项目表：%v", err)
	}
	want := []string{"assistant", "orchestrator", "hr", "developer", "designer"}
	for _, path := range []string{"/kb/establishment", "/p/proj-a/establishment"} {
		resp, err := http.Get(addrHTTP(s) + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		var face struct {
			Rows []kb.EstablishmentRow `json:"rows"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&face)
		resp.Body.Close()
		if len(face.Rows) != len(want) {
			t.Fatalf("%s 应 %d 行，得 %v", path, len(want), face.Rows)
		}
		for i, k := range want {
			if face.Rows[i].Key != k {
				got := make([]string, len(face.Rows))
				for j, r := range face.Rows {
					got[j] = r.Key
				}
				t.Fatalf("%s 展示序应为 %v，得 %v", path, want, got)
			}
		}
	}
	doc, err := s.opts.Stores.Docs.Get("ops/establishment", 0)
	if err != nil {
		t.Fatalf("Niuma_Studio 表不可读：%v", err)
	}
	if rows := mustParseRowsT(t, doc.Body); rows[0].Key != "developer" {
		t.Fatalf("文档首行应仍是 developer（行序是房主的手笔）：%s", doc.Body)
	}
}
