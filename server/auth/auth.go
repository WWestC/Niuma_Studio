package auth

import (
	"crypto/subtle"
	"io/fs"
	"net/http"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/identity"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/server/roomops"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

// Face is the identity/trust domain's world: the seat, the identity
// service, the machine credential, the strict-loopback posture and
// the visitor channel's three state holders. Split from the shell's
// auth.go/ownertoken.go/visitor.go; the hello handshake keeps thin
// shell wrappers (session.go dials them).
type Face struct {
	*verbs.Seat
	Auth           *identity.Service
	OperatorToken  string
	StrictLoopback bool
	Visitor        *VisitorGate
	Visitors       *VisitorMeter
	FirstSeen      *FirstVisitTracker

	// WebFS serves the visitor share page's shell (the same embedded
	// index the main app rides).
	WebFS fs.FS
}

// SessionCookie is the browser workbench's session carrier (HttpOnly;
// the WS handshake reads it alongside hello.auth for non-browser
// clients).
const SessionCookie = "niuma_session"

// ConnAuth is one resolved identity: the account a request/dial acts
// as. Operator marks the machine-trust resolutions (loopback, service
// token) — same authority as an admin login, attributed honestly.
type ConnAuth struct {
	Username string
	Role     identity.Role
	Operator bool
}

// atLeast is the matrix check on the resolved role.
func (a *ConnAuth) AtLeast(min identity.Role) bool {
	if a == nil {
		return false
	}
	return a.Role.AtLeast(min)
}

// multiuser reports whether the identity service is armed (nil-safe
// on a bare *Server).
func (au *Face) Multiuser() bool {
	return au.Auth != nil && au.Auth.Enabled()
}

// authOf resolves one HTTP request's identity. Resolution order:
// single-user → nil (the permissive legacy shape, callers' gates read
// it as operator); then loopback peer → operator; then bearer/cookie
// session; then the studio service token (constant-time, any address
// — it is the machine's credential in bearer form). Anonymous
// otherwise (nil — multi-user gates refuse it).
func (au *Face) AuthOf(r *http.Request) *ConnAuth {
	if !au.Multiuser() {
		return nil
	}
	// A PRESENTED credential's verdict is final: a bad token refuses
	// even from the loopback (explicit beats implicit — silently
	// forgiving a stale token would hide exactly the misconfiguration
	// the refusal exists to surface).
	loopback := httputil.ReqFromLoopback(r)
	if tok := httputil.BearerToken(r); tok != "" {
		return au.TokenAuth(tok, loopback)
	}
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		if a := au.SessionAuth(c.Value); a != nil {
			return a
		}
	}
	if loopback && !au.StrictLoopback {
		return &ConnAuth{Username: au.Local, Role: identity.RoleAdmin, Operator: true}
	}
	return nil
}

// tokenAuth resolves one presented bearer-grade token (session or —
// LOOPBACK ONLY — the studio service credential; the machine
// credential is admin-grade, honoring it from beyond the machine
// would make a leaked file a remote root key). Nil when it matches
// nothing.
func (au *Face) TokenAuth(tok string, loopback bool) *ConnAuth {
	if a := au.SessionAuth(tok); a != nil {
		return a
	}
	if loopback && au.OperatorToken != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(au.OperatorToken)) == 1 {
		return &ConnAuth{Username: au.Local, Role: identity.RoleAdmin, Operator: true}
	}
	return nil
}

// sessionAuth maps a session token to its ConnAuth (nil on miss).
func (au *Face) SessionAuth(token string) *ConnAuth {
	sess, ok := au.Auth.Sessions().Verify(token)
	if !ok {
		return nil
	}
	return &ConnAuth{Username: sess.Username, Role: sess.Role}
}

// dialAuth is authOf's WS twin: hello.auth first (non-browser
// clients), then the handshake request's cookie (browser workbench).
func (au *Face) DialAuth(r *http.Request, helloToken string) *ConnAuth {
	if !au.Multiuser() {
		return nil
	}
	// hello.auth 的裁决是终局（坏令牌不因回环被赦免，同 authOf）。
	if helloToken != "" {
		return au.TokenAuth(helloToken, httputil.ReqFromLoopback(r))
	}
	return au.AuthOf(r)
}

// authRequired is the multi-user dial gate: without a resolved
// identity the connection is refused at the door (「连接即身份」的
// 废除点——名字不再是身份，会话才是）。The one exempt channel is the
// visitor share page's token-carrying observer dial (r_19's own
// token semantics, preserved whole).
func (au *Face) AuthRequired(helloVisitor string, visitorOK bool) bool {
	if !au.Multiuser() {
		return false
	}
	if helloVisitor != "" && visitorOK {
		return false // the visitor channel's own token IS the credential
	}
	return true
}

// --- the verb role matrix ------------------------------------------------
//
// 每动词最低角色（成文矩阵，钉在 auth_matrix_test.go）：对话与任务/
// 看板写面是 member；管理类（kick/rank_set/档案/提案裁决/公告/库与
// MCP）是 admin。AI 成员的在库流量走进程内 hub 直连（不经这张桌），
// 外部成员拨号在多用户下经 dialAuth 入座——member 门槛即其写面。
var VerbRole = map[string]identity.Role{
	// 对话与回执。
	chat.MsgSay:    identity.RoleMember,
	chat.MsgReport: identity.RoleMember,
	chat.MsgAck:    identity.RoleMember,
	chat.MsgReact:  identity.RoleMember,
	// 任务/看板写面。
	chat.MsgTaskCreate:  identity.RoleMember,
	chat.MsgTaskUpdate:  identity.RoleMember,
	chat.MsgTaskConfirm: identity.RoleMember,
	chat.MsgTaskDecline: identity.RoleMember,
	chat.MsgReqCreate:   identity.RoleMember,
	// 提案提交（评审产出）是 member；裁决（accept/reject）是 admin。
	chat.MsgPlanSubmit:    identity.RoleMember,
	chat.MsgPlanAccept:    identity.RoleAdmin,
	chat.MsgPlanReject:    identity.RoleAdmin,
	chat.MsgMergeSubmit:   identity.RoleMember,
	chat.MsgMergeAccept:   identity.RoleAdmin,
	chat.MsgMergeReject:   identity.RoleAdmin,
	chat.MsgTaskArbitrate: identity.RoleAdmin,
	// 会议室内动作（延时/收会）。
	chat.MsgMeetingExtend: identity.RoleMember,
	chat.MsgMeetingEnd:    identity.RoleMember,
	// kb：写面 member（kbWriteGate 仍按座位裁房架）；删除是破坏性
	// 动作，admin。
	chat.MsgKbWrite:     identity.RoleMember,
	chat.MsgKbAppend:    identity.RoleMember,
	chat.MsgKbRestore:   identity.RoleMember,
	chat.MsgKbArchive:   identity.RoleMember,
	chat.MsgKbUnarchive: identity.RoleMember,
	chat.MsgKbDelete:    identity.RoleAdmin,
	// 管理与库：admin（MCP 面是明文凭证的写面）。
	roomops.MsgKick:         identity.RoleAdmin,
	roomops.MsgRankSet:      identity.RoleAdmin,
	roomops.MsgAgentArchive: identity.RoleAdmin,
	chat.MsgAgentSave:       identity.RoleAdmin,
	chat.MsgSkillSave:       identity.RoleAdmin,
	chat.MsgMCPSave:         identity.RoleAdmin,
	chat.MsgAssemble:        identity.RoleAdmin,
	// 群公告是房主的 seatless 面；自动驾驶收摊是编排者的管理动作。
	chat.MsgNoticeSave:    identity.RoleAdmin,
	chat.MsgAutopilotDone: identity.RoleMember,
}

// verbRoleFloor answers the registered floor for one verb (ok=false:
// unlisted verbs carry no floor — legacy behaviors like the internal
// say channels stay ungated by design).
func VerbRoleFloor(verb string) (identity.Role, bool) {
	r, ok := VerbRole[verb]
	return r, ok
}

// DenyVerbRole speaks the role refusal: a private system line, never
// silent (the room hears nothing; the dialer learns why).
func DenyVerbRole(hub *chat.Hub, client *chat.Client, verb string, have *ConnAuth, min identity.Role) {
	who := i18n.S("未登录连接")
	if have != nil {
		who = have.Username
	}
	hub.SendTo(client, chat.Message{Type: chat.MsgSystem,
		Text: i18n.Sf("%s：动词 %s 需要更高权限（当前 %s，门槛 %s）——已拒绝", who, verb, RoleName(have), min.String()),
		TS:   time.Now().Unix()})
}

func RoleName(a *ConnAuth) string {
	if a == nil {
		return identity.RoleGuest.String()
	}
	return a.Role.String()
}

// --- HTTP auth endpoints -------------------------------------------------

// handleAuthLogin is POST /auth/login {username, password}: a session
// token back (JSON + HttpOnly cookie for the browser workbench).
// Refusals are identity.ErrBadCredentials()'s uniform copy — the
// roster is not enumerable through login.
func (au *Face) HandleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !au.Multiuser() {
		httputil.WriteJSONErr(w, http.StatusNotFound, i18n.S("本工作室未启用多用户账户体系"))
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSONBody(r, &body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("请求体需为 {username, password} JSON"))
		return
	}
	// source = TCP peer（反代后为代程地址——X-Forwarded-For 不可信，
	// 采信它等于让攻击者每次尝试换一把新钥匙）。
	sess, err := au.Auth.Login(r.RemoteAddr, body.Username, body.Password)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.Expires,
	})
	httputil.WriteJSON(w, map[string]any{
		"token": sess.Token, "expires": sess.Expires.Unix(),
		"username": sess.Username, "role": sess.Role.String(),
	})
}

// handleAuthLogout revokes the presenting token/cookie.
func (au *Face) HandleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if !au.Multiuser() {
		httputil.WriteJSONErr(w, http.StatusNotFound, i18n.S("本工作室未启用多用户账户体系"))
		return
	}
	if tok := httputil.BearerToken(r); tok != "" {
		au.Auth.Sessions().Revoke(tok)
	}
	if c, err := r.Cookie(SessionCookie); err == nil {
		au.Auth.Sessions().Revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1})
	httputil.WriteJSON(w, map[string]any{"ok": true})
}

// handleAuthMe answers the presenting identity (null when anonymous)
// — the workbench's boot probe.
func (au *Face) HandleAuthMe(w http.ResponseWriter, r *http.Request) {
	if !au.Multiuser() {
		httputil.WriteJSON(w, map[string]any{"multiuser": false})
		return
	}
	a := au.AuthOf(r)
	if a == nil {
		httputil.WriteJSON(w, map[string]any{"multiuser": true, "identity": nil})
		return
	}
	httputil.WriteJSON(w, map[string]any{
		"multiuser": true,
		"identity":  map[string]any{"username": a.Username, "role": a.Role.String(), "operator": a.Operator},
	})
}

// --- admin-only account management (the multi-user roster's faces) ------

// authAdminGate is the management faces' door: admin role or refuse
// (403 with the reason; 404 under single-user — the face doesn't
// exist there).
func (au *Face) AuthAdminGate(w http.ResponseWriter, r *http.Request) *ConnAuth {
	if !au.Multiuser() {
		httputil.WriteJSONErr(w, http.StatusNotFound, i18n.S("本工作室未启用多用户账户体系"))
		return nil
	}
	a := au.AuthOf(r)
	if !a.AtLeast(identity.RoleAdmin) {
		httputil.WriteJSONErr(w, http.StatusForbidden, i18n.Sf("账户管理需要管理员权限（当前 %s）", RoleName(a)))
		return nil
	}
	return a
}

// handleAuthUsers: GET the roster (credentials never leave) / POST
// open one account {username, password, role, display}.
func (au *Face) HandleAuthUsers(w http.ResponseWriter, r *http.Request) {
	if au.AuthAdminGate(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		out := []map[string]any{}
		for _, a := range au.Auth.Store().List() {
			out = append(out, map[string]any{
				"username": a.Username, "role": a.Role.String(),
				"display_name": a.DisplayName, "created_ts": a.CreatedTS,
				"projects": au.Auth.Store().Projects(a.Username),
			})
		}
		httputil.WriteJSON(w, out)
	case http.MethodPost:
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
			Display  string `json:"display_name"`
		}
		if err := readJSONBody(r, &body); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("请求体需为 {username, password, role, display_name} JSON"))
			return
		}
		username, ok := identity.SanitizeUsername(body.Username)
		if !ok {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("用户名需为 1-32 字符、无空格/控制字符"))
			return
		}
		role, ok := identity.ParseRole(body.Role)
		if !ok {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("角色需为 admin / member / guest"))
			return
		}
		salt, hash, err := identity.NewCredential(body.Password)
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := au.Auth.Store().Create(identity.Account{
			Username: username, Role: role, DisplayName: body.Display,
			Salt: salt, Hash: hash, CreatedTS: time.Now().Unix(),
		}); err != nil {
			httputil.WriteJSONErr(w, http.StatusConflict, err.Error())
			return
		}
		httputil.WriteJSON(w, map[string]any{"username": username, "role": role.String()})
	default:
		httputil.WriteJSONErr(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

// handleAuthUser: GET one account's shape / DELETE it (last-admin
// guard inside the service).
func (au *Face) HandleAuthUser(w http.ResponseWriter, r *http.Request) {
	if au.AuthAdminGate(w, r) == nil {
		return
	}
	name := r.PathValue("name")
	switch r.Method {
	case http.MethodGet:
		acc, ok := au.Auth.Store().Get(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		httputil.WriteJSON(w, map[string]any{
			"username": acc.Username, "role": acc.Role.String(),
			"display_name": acc.DisplayName, "created_ts": acc.CreatedTS,
			"projects": au.Auth.Store().Projects(name),
		})
	case http.MethodDelete:
		if err := au.Auth.DeleteAccount(name); err != nil {
			httputil.WriteJSONErr(w, http.StatusConflict, err.Error())
			return
		}
		httputil.WriteJSON(w, map[string]any{"ok": true})
	default:
		httputil.WriteJSONErr(w, http.StatusMethodNotAllowed, "GET/DELETE only")
	}
}

// handleAuthUserRole: POST {role} (last-admin guard inside).
func (au *Face) HandleAuthUserRole(w http.ResponseWriter, r *http.Request) {
	if au.AuthAdminGate(w, r) == nil {
		return
	}
	if r.Method != http.MethodPost {
		httputil.WriteJSONErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := readJSONBody(r, &body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("请求体需为 {role} JSON"))
		return
	}
	role, ok := identity.ParseRole(body.Role)
	if !ok {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("角色需为 admin / member / guest"))
		return
	}
	if err := au.Auth.SetRole(r.PathValue("name"), role); err != nil {
		httputil.WriteJSONErr(w, http.StatusConflict, err.Error())
		return
	}
	httputil.WriteJSON(w, map[string]any{"ok": true})
}

// handleAuthUserPassword: POST {password} — the admin's reset path.
func (au *Face) HandleAuthUserPassword(w http.ResponseWriter, r *http.Request) {
	if au.AuthAdminGate(w, r) == nil {
		return
	}
	if r.Method != http.MethodPost {
		httputil.WriteJSONErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSONBody(r, &body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("请求体需为 {password} JSON"))
		return
	}
	salt, hash, err := identity.NewCredential(body.Password)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := au.Auth.Store().SetPassword(r.PathValue("name"), salt, hash); err != nil {
		httputil.WriteJSONErr(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, map[string]any{"ok": true})
}

// handleAuthUserProjects: the project-grant set (scope.go's
// server-side enforcement reads it) — GET list / POST grant
// {project} / DELETE revoke ?project=.
func (au *Face) HandleAuthUserProjects(w http.ResponseWriter, r *http.Request) {
	if au.AuthAdminGate(w, r) == nil {
		return
	}
	name := r.PathValue("name")
	switch r.Method {
	case http.MethodGet:
		httputil.WriteJSON(w, au.Auth.Store().Projects(name))
	case http.MethodPost:
		var body struct {
			Project string `json:"project"`
		}
		if err := readJSONBody(r, &body); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("请求体需为 {project} JSON"))
			return
		}
		if err := au.Auth.Store().GrantProject(name, body.Project); err != nil {
			httputil.WriteJSONErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, map[string]any{"ok": true})
	case http.MethodDelete:
		if err := au.Auth.Store().RevokeProject(name, r.URL.Query().Get("project")); err != nil {
			httputil.WriteJSONErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, map[string]any{"ok": true})
	default:
		httputil.WriteJSONErr(w, http.StatusMethodNotAllowed, "GET/POST/DELETE only")
	}
}
