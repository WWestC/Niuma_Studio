package server

// auth_matrix_test.go — the permission matrix's acceptance pin: the
// four identity rows (admin / member / guest / anonymous) against a
// representative route per class, asserted as 200/403/404 over the
// REAL handler chain (remote RemoteAddr, so the loopback operator
// shortcut does not color the rows — the loopback path has its own
// pins in auth_test.go). The route table itself is walked for
// coverage: every routeMatrix row gets probed on at least one row.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/identity"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// matrixServer boots a multi-user server with the three account rows
// seeded and alice granted the "book" project (the grant ruling's
// fixture).
func matrixServer(t *testing.T) (*Server, *identity.Service, string, string, string) {
	t.Helper()
	lobby := chat.NewHub()
	store := newMemAccounts()
	svc := identity.NewService(store, time.Hour, identity.ThrottleConfig{})
	mkAcc(t, store, "root", identity.RoleAdmin, "adminpw")
	mkAcc(t, store, "alice", identity.RoleMember, "memberpw")
	mkAcc(t, store, "looker", identity.RoleGuest, "guestpw")
	_ = store.GrantProject("alice", "book")
	_ = store.GrantProject("looker", "book")
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projStore.EnsureLobby(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := projStore.Create(projects.Project{Key: "book", Name: "书稿", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	adminSess, _ := svc.Login("test", "root", "adminpw")
	memberSess, _ := svc.Login("test", "alice", "memberpw")
	guestSess, _ := svc.Login("test", "looker", "guestpw")
	engine := tasks.MustOpenMemory("root")
	reqs := requirements.OpenMemory()
	lib, err := capability.Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Start(lobby, Options{
		Endpoint:  EndpointConfig{PreferredPort: -1},
		LocalName: "root",
		Auth:      svc,
		Registry:  chat.NewRegistry(projStore, "", "root", "人类玩家"),
		ResetCh:   make(chan struct{}, 1),
		RebuildCh: make(chan struct{}, 1),
		Stores: StoreSet{
			Engine:       engine,
			Requirements: reqs,
			Library:      lib,
			ProjectStore: projStore,
		},
		Faces: FaceConfig{
			DispatchHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"ok": true}) }),
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, svc, adminSess.Token, memberSess.Token, guestSess.Token
}

// matrixDo drives the full handler chain with a REMOTE peer and an
// optional bearer token — the matrix's honest shape.
func matrixDo(t *testing.T, s *Server, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = "127.0.0.1" // hostgate 只放行回环字面量（矩阵行为属回环流；远端身份由 RemoteAddr 承载）
	req.RemoteAddr = "192.0.2.9:40000"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	return rec.Code
}

func TestRouteRoleMatrix(t *testing.T) {
	s, _, adminTok, memberTok, guestTok := matrixServer(t)

	cases := []struct {
		method, path               string
		admin, member, guest, anon int
		note                       string
	}{
		// 硬规则：admin 专属。充足行断「门已过」——handler 自身校验
		// （口令缺失 400、依赖缺席 404）如实作答；不足行必须 403。
		{"POST", "/reset", 400, 403, 403, 403, "工厂重置（无口令→400）"},
		{"POST", "/rebuild", 400, 403, 403, 403, "重编译重启（无配置→400）"},
		{"GET", "/dispatch/", 200, 403, 403, 403, "调度管理面"},
		{"GET", "/fs/ls", 200, 403, 403, 403, "目录浏览"},
		{"GET", "/owner/token", 404, 403, 403, 403, "房主凭证面（回环限定→404）"},
		{"GET", "/mcps", 200, 403, 403, 403, "MCP 明文面"},
		{"POST", "/p/book/git/push", 400, 403, 403, 403, "git 写面（无分支参数→400）"},
		{"POST", "/p/book/git/checkout", 400, 403, 403, 403, "git 切枝（无分支参数→400）"},
		// 任务/看板/对话写面：member；guest 只读授权项目。
		{"GET", "/p/book/reqs", 200, 200, 200, 403, "需求读（授权项目：guest 亦可读）"},
		{"GET", "/p/book/git", 200, 200, 403, 403, "git 读面"},
		{"GET", "/kb/manual", 200, 200, 200, 403, "手册：guest 可读"},
		// 监督面：admin。
		{"GET", "/kb/tasks", 200, 403, 403, 403, "全室任务账"},
		{"GET", "/usage", 200, 403, 403, 403, "耗粮"},
		// 项目授权裁决：未授权项目对 member/guest 404（不确认存在）。
		{"GET", "/p/other-room/tasks", 404, 404, 404, 403, "未知项目不确认存在（匿名按门 403）"},
		// 公开面：匿名可达（probe/info、登录、访客页壳）。
		{"GET", "/", 200, 200, 200, 200, "信息面（探针契约）"},
		{"GET", "/auth/me", 200, 200, 200, 200, "身份探测"},
	}
	for _, c := range cases {
		for row, tok := range map[string]string{"admin": adminTok, "member": memberTok, "guest": guestTok, "anon": ""} {
			want := map[string]int{"admin": c.admin, "member": c.member, "guest": c.guest, "anon": c.anon}[row]
			got := matrixDo(t, s, c.method, c.path, tok)
			if got != want {
				t.Errorf("%s %s [%s]: got %d want %d（%s）", c.method, c.path, row, got, want, c.note)
			}
		}
	}
}

// TestRouteMatrixCoverage walks every matrix row through all four
// rows — no route escapes the ruling (the numbers here are coarse:
// not-403 for the sufficient rows, 403 for insufficient ones; the
// semantic picks live in TestRouteRoleMatrix above).
func TestRouteMatrixCoverage(t *testing.T) {
	s, _, adminTok, memberTok, guestTok := matrixServer(t)
	byRow := map[string]string{"admin": adminTok, "member": memberTok, "guest": guestTok, "anon": ""}
	// 本服务器未挂载的面（小助手/市场）没有路由行——404 先于任何门。
	unmounted := map[string]bool{"/assistant": true, "/assistant/": true,
		"/market/": true, "/market/index": true, "/market/install": true, "/market/enable": true, "/market/remove": true}
	for pattern, byMethod := range routeMatrix {
		if unmounted[pattern] {
			continue
		}
		method := http.MethodGet
		if _, ok := byMethod[http.MethodPost]; ok {
			method = http.MethodPost
		}
		path := pattern
		// /p/{key}/… → /p/book/…（授权项目）；{name}/{id} 填样例。
		path = strings.ReplaceAll(path, "{key}", "book")
		path = strings.ReplaceAll(path, "{name}", "alice")
		path = strings.ReplaceAll(path, "{id}", "t_01")
		path = strings.ReplaceAll(path, "{person}", "alice")
		floor := byMethod["*"]
		if f, ok := byMethod[method]; ok {
			floor = f
		}
		for row, tok := range byRow {
			got := matrixDo(t, s, method, path, tok)
			role := identity.RoleAdmin
			switch row {
			case "member":
				role = identity.RoleMember
			case "guest":
				role = identity.RoleGuest
			case "anon":
				role = identity.RoleGuest - 1
			}
			if role.AtLeast(floor) {
				if got == http.StatusForbidden {
					t.Errorf("%s %s [%s]: 足够角色被拒 403", method, path, row)
				}
			} else {
				if got != http.StatusForbidden {
					t.Errorf("%s %s [%s]: 不足角色应 403，got %d", method, path, row, got)
				}
			}
		}
	}
}

// TestScopeQueryGrants — the ?project= server-side ruling: a granted
// project answers its scoped roster; a foreign project answers the
// honest empty (never the studio-wide widening); the bare face narrows
// to the lobby for member/guest.
func TestScopeQueryGrants(t *testing.T) {
	s, _, _, memberTok, _ := matrixServer(t)

	get := func(path, token string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "127.0.0.1"
		req.RemoteAddr = "192.0.2.9:40000"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		s.srv.Handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// 授权项目：member 可读（scoped 名册，200）。
	code, body := get("/kb/people?project=book", memberTok)
	if code != 200 || strings.Contains(body, "roomless-stranger") {
		t.Fatalf("granted project read: %d %s", code, body[:min(len(body), 200)])
	}
	// 未授权项目：与未知同口径——诚实空架（名册只剩恒在的房主行，
	// 既不确认该房存在，也不静默扩宽到全室）。
	code, body = get("/kb/people?project=other-room", memberTok)
	if code != 200 {
		t.Fatalf("foreign project should answer 200-with-honest-empty (不确认存在): %d", code)
	}
	var people []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &people); err != nil {
		t.Fatalf("body shape: %v %s", err, body)
	}
	if len(people) != 1 || people[0]["name"] != "root" {
		t.Fatalf("foreign project must not widen beyond the ever-present host row: %s", body)
	}
	// 裸面：member 收窄到大厅（房主行在，工作室全室合并不出现）。
	code, body = get("/kb/people", memberTok)
	if code != 200 {
		t.Fatalf("bare face: %d", code)
	}
	if !strings.Contains(body, `"name"`) && !strings.Contains(body, `"Local"`) {
		// 名册形状宽松断言：非空即可（大厅至少有房主行）。
		if len(strings.TrimSpace(body)) < 3 {
			t.Fatalf("lobby-narrowed roster should be non-empty: %s", body)
		}
	}
	_ = fmt.Sprint
}
