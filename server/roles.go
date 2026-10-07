package server

// roles.go — the HTTP face's permission matrix (成文＋钉测试
// auth_matrix_test.go)：每条路由的最低角色，方法感知（同一路由
// GET 读／POST 写可不同级）。Start 的注册一律走 s.route()，它查
// routeMatrix——缺行即 panic（测试二进制启动现形，与动词注册表同
// 纪律）；刻意公开的面（登录、启动门、静态资产、访客分享页）走
// 原生 HandleFunc 不设门槛。单用户形态（无身份服务）矩阵整表不生效：
// 历史行为逐字节不变。
//
// 硬规则（任务书成文）：/reset、/rebuild、/dispatch/*、/fs/ls、
// /owner/token、/mcps 明文面、git 写面＝admin 专属；任务/看板/对话
// 写面＝member；访客只读面（/view、/dayreplay 的独立 token 语义）
// 保持原样。/p/{key}/* 额外过项目可见性：非管理员只见大厅与自己
// 被授权的项目（scope.go 的服务端强制，不再信任 ?project= 自报）。

import (
	"net/http"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/identity"
)

// routeMatrix maps every gated route pattern to its per-method role
// floor ("*" = any method). Additions are layering decisions: the
// matrix test walks this table, a new route without a row reds it.
var routeMatrix = map[string]map[string]identity.Role{
	// —— 工作室级管理与明文面（admin）——
	"/reset":               {"*": identity.RoleAdmin},
	"/rebuild":             {"*": identity.RoleAdmin},
	"/dispatch-wait":       {"*": identity.RoleAdmin},
	"/retention":           {"*": identity.RoleAdmin},
	"/budget":              {"*": identity.RoleAdmin},
	"/reveal":              {"*": identity.RoleAdmin},
	"/lang":                {"*": identity.RoleAdmin},
	"/owner/token":         {"*": identity.RoleAdmin}, // 房主凭证读面（回环限定之外再加身份门）
	"/fs/ls":               {"*": identity.RoleAdmin}, // 立项工作区目录浏览
	"/visitor":             {"*": identity.RoleAdmin}, // 访客通道开关（房主专属）
	"/offboard":            {"*": identity.RoleAdmin},
	"/usage":               {"*": identity.RoleAdmin},
	"/usage/turns":         {"*": identity.RoleAdmin},
	"/members":             {"*": identity.RoleAdmin}, // 全室名册（编排面）
	"/achieve/tests-green": {"*": identity.RoleMember},

	// —— 工作台级读面（studio-wide 监督面＝admin；手册/项目面按授权）——
	"/kb":                       {"*": identity.RoleAdmin},
	"/kb/manual":                {"*": identity.RoleGuest}, // 使用手册：人人可读
	"/kb/people":                {"*": identity.RoleGuest}, // ?project= 授权裁决（roomScope）
	"/kb/people/{name}":         {"*": identity.RoleGuest},
	"/kb/people/{name}/history": {"*": identity.RoleGuest},
	"/kb/capacity":              {"*": identity.RoleAdmin},
	"/kb/tasks":                 {"*": identity.RoleAdmin}, // 全室任务账（监督面）
	"/kb/tasks/{id}":            {"*": identity.RoleGuest}, // ?project= 消歧＋授权
	"/kb/tasks/{id}/brief":      {"*": identity.RoleMember},
	"/kb/establishment":         {"*": identity.RoleAdmin},
	"/kb/establishment/row":     {"*": identity.RoleAdmin},
	"/kb/docs":                  {"*": identity.RoleGuest}, // ?project= 授权裁决
	"/kb/docs/":                 {"*": identity.RoleGuest},

	// —— 人事档案面（member；提示词是入职材料）——
	"/agents":               {"*": identity.RoleMember},
	"/agents/{name}/prompt": {"*": identity.RoleMember},
	"/prompt":               {"*": identity.RoleMember},
	"/projects":             {"*": identity.RoleMember},
	"/projects/preflight":   {"*": identity.RoleMember},

	// —— 技能与 MCP 库：读 member，写 admin；/mcps 全 admin（明文凭证面）——
	"/skills":           {http.MethodGet: identity.RoleMember, "*": identity.RoleAdmin},
	"/skills/authoring": {"*": identity.RoleAdmin},
	"/skills/{key}":     {http.MethodGet: identity.RoleMember, "*": identity.RoleAdmin},
	"/mcps":             {"*": identity.RoleAdmin},
	"/mcps/{key}":       {"*": identity.RoleAdmin},

	// —— 媒体：上传 member；取回公开（id 即能力，访客页同享）——
	"/media": {http.MethodPost: identity.RoleMember, "*": identity.RoleAdmin},

	// —— 项目面：读按授权（gateProject），写与治理分级 ——
	"/p/{key}":                          {http.MethodGet: identity.RoleGuest, "*": identity.RoleAdmin},
	"/p/{key}/history":                  {"*": identity.RoleGuest},
	"/p/{key}/reads":                    {"*": identity.RoleGuest},
	"/p/{key}/acks":                     {"*": identity.RoleGuest},
	"/p/{key}/reacts":                   {"*": identity.RoleGuest},
	"/p/{key}/questions":                {"*": identity.RoleGuest},
	"/p/{key}/tasks":                    {"*": identity.RoleGuest},
	"/p/{key}/plan":                     {"*": identity.RoleMember},
	"/p/{key}/merge":                    {"*": identity.RoleMember},
	"/p/{key}/reqs":                     {http.MethodGet: identity.RoleGuest, "*": identity.RoleMember},
	"/p/{key}/reqs/{id}":                {"*": identity.RoleMember},
	"/p/{key}/reqs/{id}/park":           {"*": identity.RoleMember},
	"/p/{key}/reqs/{id}/unpark":         {"*": identity.RoleMember},
	"/p/{key}/meeting":                  {"*": identity.RoleMember},
	"/p/{key}/notice":                   {http.MethodGet: identity.RoleGuest, "*": identity.RoleAdmin},
	"/p/{key}/schedule":                 {"*": identity.RoleGuest},
	"/p/{key}/staffing":                 {"*": identity.RoleGuest},
	"/p/{key}/establishment":            {"*": identity.RoleGuest},
	"/p/{key}/establishment/row":        {"*": identity.RoleAdmin},
	"/p/{key}/staffing/{person}/fabric": {"*": identity.RoleMember},
	"/p/{key}/autopilot":                {"*": identity.RoleAdmin},
	"/p/{key}/pause":                    {"*": identity.RoleAdmin},
	"/p/{key}/rebuild-vote":             {"*": identity.RoleAdmin},
	"/p/{key}/lifecycle":                {"*": identity.RoleAdmin},
	"/p/{key}/dismiss":                  {"*": identity.RoleAdmin},
	"/p/{key}/reset":                    {"*": identity.RoleAdmin},
	"/p/{key}/delete":                   {"*": identity.RoleAdmin},
	"/p/{key}/trace":                    {"*": identity.RoleMember},
	"/p/{key}/trace/file":               {"*": identity.RoleMember},
	"/p/{key}/term":                     {"*": identity.RoleMember},
	"/p/{key}/term/file":                {"*": identity.RoleMember},
	"/p/{key}/fs/tree":                  {"*": identity.RoleMember},
	"/p/{key}/fs/file":                  {"*": identity.RoleMember},
	"/p/{key}/fs/search":                {"*": identity.RoleMember},
	"/p/{key}/replay":                   {"*": identity.RoleMember},
	"/p/{key}/replay/days":              {"*": identity.RoleMember},
	"/p/{key}/replay/share":             {"*": identity.RoleAdmin},

	// —— git：读面 member（按项目授权），写面/授权探测 admin ——
	"/p/{key}/git":              {"*": identity.RoleMember},
	"/p/{key}/git/branches":     {"*": identity.RoleMember},
	"/p/{key}/git/log":          {"*": identity.RoleMember},
	"/p/{key}/git/graph":        {"*": identity.RoleMember},
	"/p/{key}/git/req-activity": {"*": identity.RoleMember},
	"/p/{key}/git/fetch":        {"*": identity.RoleAdmin},
	"/p/{key}/git/push":         {"*": identity.RoleAdmin},
	"/p/{key}/git/pull":         {"*": identity.RoleAdmin},
	"/p/{key}/git/auth":         {"*": identity.RoleAdmin}, // 逐远端授权探测（凭据邻接面）
	"/p/{key}/git/init":         {"*": identity.RoleAdmin},
	"/p/{key}/git/checkout":     {"*": identity.RoleAdmin},
	"/p/{key}/git/branch":       {"*": identity.RoleAdmin},
	"/p/{key}/git/bind":         {"*": identity.RoleAdmin},
	"/p/{key}/git/unbind":       {"*": identity.RoleAdmin},
	"/p/{key}/git/policy":       {"*": identity.RoleAdmin},
	"/p/{key}/git/settings":     {"*": identity.RoleAdmin},
	"/p/{key}/git/hook":         {"*": identity.RoleAdmin},
	"/p/{key}/git/seats":        {"*": identity.RoleAdmin},
	"/p/{key}/git/seat":         {"*": identity.RoleAdmin},
	"/p/{key}/git/seat-cleanup": {"*": identity.RoleAdmin},

	// —— 内部上报腿（post-commit 播报；本机钩子走回环操作者）——
	"/internal/git/commit": {"*": identity.RoleMember},

	// —— 挂载面（Faces）：调度管理面与小助手＝admin ——
	// —— 账户管理面（admin；handler 内亦有同款门，矩阵行统一口径）——
	"/auth/users":                 {"*": identity.RoleAdmin},
	"/auth/users/{name}":          {"*": identity.RoleAdmin},
	"/auth/users/{name}/role":     {"*": identity.RoleAdmin},
	"/auth/users/{name}/password": {"*": identity.RoleAdmin},
	"/auth/users/{name}/projects": {"*": identity.RoleAdmin},
	"/dispatch/":                  {"*": identity.RoleAdmin},
	"/assistant":                  {"*": identity.RoleAdmin},
	"/assistant/":                 {"*": identity.RoleAdmin},
	"/market/":                    {"*": identity.RoleAdmin},
	"/market/index":               {http.MethodGet: identity.RoleMember, "*": identity.RoleAdmin},
	"/market/install":             {"*": identity.RoleAdmin},
	"/market/enable":              {"*": identity.RoleAdmin},
	"/market/remove":              {"*": identity.RoleAdmin},
}

// floorFor answers one request's floor (ok=false = ungated pattern —
// Start never asks for one, route() panics on unknown patterns).
func floorFor(pattern, method string) (identity.Role, bool) {
	byMethod, ok := routeMatrix[pattern]
	if !ok {
		return identity.RoleGuest, false
	}
	if r, ok := byMethod[method]; ok {
		return r, true
	}
	r, ok := byMethod["*"]
	return r, ok
}

// route registers one gated handler: the matrix row is the route's
// role contract (a missing row is a Start-time panic — the matrix is
// the reviewed artifact, same as the verb registry). /p/{key}/…
// patterns additionally carry the project-visibility gate.
func (s *Server) route(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	floor, ok := floorFor(pattern, "*") // presence check only; the method split resolves per request
	if !ok {
		panic("route matrix row missing: " + pattern)
	}
	_ = floor
	mux.Handle(pattern, s.gateRoute(pattern, h))
}

// gateRoute is the runtime door: under multi-user it resolves the
// request's identity and enforces (a) the method-aware role floor and
// (b) for /p/{key}/… patterns the session's project visibility.
// Anonymous refusals are 403 with the login-required copy (never a
// bare status line); single-user passes everything through untouched.
func (s *Server) gateRoute(pattern string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.multiuser() {
			auth := s.authOf(r)
			if floor, ok := floorFor(pattern, r.Method); ok && !auth.AtLeast(floor) {
				if auth == nil {
					writeJSONErr(w, http.StatusForbidden, identity.ErrLoginRequired().Error())
				} else {
					writeJSONErr(w, http.StatusForbidden,
						i18n.Sf("%s：此操作需要 %s 及以上权限（当前 %s）", r.URL.Path, floor.String(), roleName(auth)))
				}
				return
			}
			if len(r.PathValue("key")) > 0 && !s.projectVisible(auth, r.PathValue("key")) {
				// 与未知项目同一口径：不确认存在性。
				http.NotFound(w, r)
				return
			}
		}
		h(w, r)
	})
}

// projectVisible is the per-project scope ruling: single-user and
// admin/operator see everything; the lobby is everyone's hall; a
// member/guest sees exactly their granted projects.
func (s *Server) projectVisible(a *ConnAuth, key string) bool {
	if a == nil {
		return false
	}
	if a.AtLeast(identity.RoleAdmin) {
		return true
	}
	if key == "" || key == "default" {
		return true // the lobby is the studio's common hall
	}
	if s.opts.Auth == nil {
		return true
	}
	for _, p := range s.opts.Auth.Store().Projects(a.Username) {
		if p == key {
			return true
		}
	}
	return false
}

// hiddenProject is the scope sentinel for a project the session
// cannot see: it can never be a real key (projects.ValidKey's charset
// refuses control bytes), so every scoped face answers its honest
// empty — same discipline as an unknown project, never a leak and
// never a silent widening to the studio.
const hiddenProject = "\x00"

// scopeQuery is roomScopeQuery's session-aware successor: the read
// faces' ?project= self-report is now a REQUEST the server rules on —
// a project the session cannot see answers as unknown. "" keeps its
// studio-wide meaning for the admin/host face; member/guest sessions
// narrow to the lobby (their hall — the studio-wide assembly is the
// host's oversight face, not a member's).
func (s *Server) scopeQuery(r *http.Request) string {
	project := s.roomScopeQuery(r)
	if !s.multiuser() {
		return project
	}
	auth := s.authOf(r)
	if auth.AtLeast(identity.RoleAdmin) {
		return project
	}
	if project == "" {
		return "default" // the common hall, never the whole studio
	}
	if !s.projectVisible(auth, project) {
		return hiddenProject
	}
	return project
}
