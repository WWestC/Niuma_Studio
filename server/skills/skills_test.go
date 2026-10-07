package skills

// skills_test.go — the skill & MCP faces: the two libraries' HTTP
// read/upsert/delete over the loopback trust model, the authoring
// switch, header-value masking on list faces, and the composer's
// slash-command intercept (chatCommand) on the host channel's say.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

type skStage struct {
	s    *Face
	lib  *capability.Store
	hub  *chat.Hub
	said int
}

func newSkStage(t *testing.T) *skStage {
	t.Helper()
	lib, err := capability.Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	st := &skStage{lib: lib}
	st.hub = chat.NewHub()
	st.s = &Face{Stores: verbs.StoreSet{Library: lib},
		Registry: chat.NewRegistry(nil, t.TempDir(), "房主", "staff"),
		Local:    "房主"}
	st.s.Hub = st.hub
	return st
}

func (st *skStage) call(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	// 舞台即本机回环客户端（生产里所有来源都是回环）。
	req.RemoteAddr = "127.0.0.1:57000"
	rec := httptest.NewRecorder()
	switch {
	case path == "/skills":
		st.s.HandleSkills(rec, req)
	case strings.HasPrefix(path, "/skills/authoring"):
		st.s.HandleAuthoring(rec, req)
	case strings.HasPrefix(path, "/skills/"):
		req.SetPathValue("key", strings.TrimPrefix(path, "/skills/"))
		st.s.HandleSkill(rec, req)
	case path == "/mcps":
		st.s.HandleMCPs(rec, req)
	case strings.HasPrefix(path, "/mcps/"):
		req.SetPathValue("key", strings.TrimPrefix(path, "/mcps/"))
		st.s.HandleMCP(rec, req)
	default:
		t.Fatalf("no route for %s", path)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestSkillHTTPCrud(t *testing.T) {
	st := newSkStage(t)
	// 空库列表
	code, out := st.call(t, http.MethodGet, "/skills", "")
	if code != 200 {
		t.Fatalf("GET /skills = %d", code)
	}
	if len(out) != 0 { // 解析成 map 失败时空数组得 0 长度判断不了——用原始 body
		if b, ok := out["x"]; ok && b != nil {
			t.Fatalf("空库应回 []，得 %v", out)
		}
	}
	// POST 落库（路径即身份）
	code, _ = st.call(t, http.MethodPost, "/skills/review",
		`{"key":"mismatch","name":"评审","body":"先看错误面"}`)
	if code != 200 {
		t.Fatalf("POST /skills/review = %d", code)
	}
	sk, ok := st.lib.GetSkill("review")
	if !ok || sk.Name != "评审" || sk.Body != "先看错误面" {
		t.Fatalf("应按路径落库，得 %+v", sk)
	}
	// 非法 key 404（路由卫门）
	code, _ = st.call(t, http.MethodPost, "/skills/BadKey", `{"name":"x"}`)
	if code != 404 {
		t.Errorf("非法 key 应 404，得 %d", code)
	}
	// 合法 key 非法载荷 400（名称缺失）
	code, _ = st.call(t, http.MethodPost, "/skills/ok-key", `{"name":""}`)
	if code != 400 {
		t.Errorf("空名称应 400，得 %d", code)
	}
	// GET 详情含正文
	code, out = st.call(t, http.MethodGet, "/skills/review", "")
	if code != 200 || out["body"] != "先看错误面" {
		t.Errorf("详情应含正文，得 %d %v", code, out)
	}
	// DELETE 带引用面（无引用）
	code, out = st.call(t, http.MethodDelete, "/skills/review", "")
	if code != 200 || out["ok"] != true {
		t.Errorf("DELETE 应成功，得 %d %v", code, out)
	}
	if _, ok := st.lib.GetSkill("review"); ok {
		t.Error("删除后库中不应再有")
	}
}

func TestMCPHTTPAndMasking(t *testing.T) {
	st := newSkStage(t)
	code, _ := st.call(t, http.MethodPost, "/mcps/penpot",
		`{"name":"设计","type":"http","url":"http://127.0.0.1:4401/mcp","headers":[{"name":"Authorization","value":"Bearer secret-token-9876"}]}`)
	if code != 200 {
		t.Fatalf("POST /mcps/penpot = %d", code)
	}
	// 列表面不回明文头值：手解数组
	req := httptest.NewRequest(http.MethodGet, "/mcps", nil)
	rec := httptest.NewRecorder()
	st.s.HandleMCPs(rec, req)
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["key"] != "penpot" {
		t.Fatalf("列表应有 1 条，得 %v", rows)
	}
	if rec.Body.String() == "" || strings.Contains(rec.Body.String(), "secret-token-9876") {
		t.Error("列表不得回明文头值")
	}
	// 详情（回环读面）回明文
	code, out := st.call(t, http.MethodGet, "/mcps/penpot", "")
	if code != 200 {
		t.Fatalf("GET /mcps/penpot = %d", code)
	}
	if !containsJSON(mustJSONMap(t, out), "secret-token-9876") {
		t.Errorf("详情应回明文头值，得 %v", out)
	}
	// 安全核查修复的钉子：非回环来源的详情读裸 404（不确认存在性，
	// 不回明文）——httptest.NewRequest 默认的 192.0.2.1 正是外来源。
	reqOut := httptest.NewRequest(http.MethodGet, "/mcps/penpot", nil)
	reqOut.SetPathValue("key", "penpot")
	recOut := httptest.NewRecorder()
	st.s.HandleMCP(recOut, reqOut)
	if recOut.Code != http.StatusNotFound {
		t.Fatalf("非回环详情读该 404，得 %d", recOut.Code)
	}
	// stdio 拒绝
	code, _ = st.call(t, http.MethodPost, "/mcps/bad", `{"name":"x","type":"stdio","url":"http://x/y"}`)
	if code != 400 {
		t.Errorf("stdio 应 400，得 %d", code)
	}
}

func mustJSONMap(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func containsJSON(s, substr string) bool { return strings.Contains(s, substr) }

func TestAuthoringSwitchFace(t *testing.T) {
	st := newSkStage(t)
	code, out := st.call(t, http.MethodGet, "/skills/authoring", "")
	if code != 200 || out["on"] != false {
		t.Fatalf("缺省应关，得 %d %v", code, out)
	}
	st.call(t, http.MethodPost, "/skills/authoring", `{"on":true}`)
	if !st.lib.ModelAuthoring() {
		t.Fatal("POST 应翻转库内开关")
	}
	_, out = st.call(t, http.MethodGet, "/skills/authoring", "")
	if out["on"] != true {
		t.Errorf("GET 应回开态，得 %v", out)
	}
}

// 对话命令拦截：/skill new 落库＋系统行回执，/mcp add 同律，普通
// /<key> 附着词元不拦（那是调度器的注入面），非命令照旧放行。
func TestChatCommandIntercept(t *testing.T) {
	st := newSkStage(t)
	if st.s.ChatCommand(st.hub, "房主", "普通发言 /review @小马") {
		t.Error("非 /skill、/mcp 开头的 say 不应拦截")
	}
	if !st.s.ChatCommand(st.hub, "房主", "/skill new review 评审\n先看错误面。") {
		t.Fatal("/skill new 应拦截")
	}
	sk, ok := st.lib.GetSkill("review")
	if !ok || sk.Name != "评审" || sk.Body != "先看错误面。" || sk.CreatedBy != "房主" {
		t.Fatalf("命令应落库，得 %+v", sk)
	}
	if !hasSys(st.hub, "已保存技能 review") {
		t.Errorf("应有系统行回执，历史 %v", sysTexts(st.hub))
	}
	// /mcp add
	if !st.s.ChatCommand(st.hub, "房主", "/mcp add penpot 设计工具 http http://127.0.0.1:4401/mcp") {
		t.Fatal("/mcp add 应拦截")
	}
	m, ok := st.lib.GetMCP("penpot")
	if !ok || m.Type != "http" || m.URL != "http://127.0.0.1:4401/mcp" {
		t.Fatalf("MCP 应落库，得 %+v", m)
	}
	// /skill list 回清单
	if !st.s.ChatCommand(st.hub, "房主", "/skill list") || !hasSys(st.hub, "review（评审）") {
		t.Error("/skill list 应回清单系统行")
	}
	// 形状不对：拦下但拒绝，不落库
	before := len(st.lib.ListSkills())
	st.s.ChatCommand(st.hub, "房主", "/skill new")
	if after := len(st.lib.ListSkills()); after != before {
		t.Errorf("残缺命令不应落库：%d → %d", before, after)
	}
	if !hasSys(st.hub, "命令形状不对") {
		t.Error("残缺命令应有用法回执")
	}
}

func sysTexts(hub *chat.Hub) []string {
	var out []string
	for _, m := range hub.History() {
		if m.Type == chat.MsgSystem {
			out = append(out, m.Text)
		}
	}
	return out
}

func hasSys(hub *chat.Hub, substr string) bool {
	for _, s := range sysTexts(hub) {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
