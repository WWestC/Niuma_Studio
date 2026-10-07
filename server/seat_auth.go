package server

// seat_auth.go — the identity/trust domain's mount point and the
// hello-handshake wrappers: the domain (server/auth) owns the
// decisions, the shell owns the wiring (session.go dials these thin
// wrappers; the route matrix stays here too).

import (
	"net/http"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/WWestC/Niuma_Studio/identity"
	"github.com/WWestC/Niuma_Studio/server/auth"
)

// authFace is the identity domain's face over this server.
func (s *Server) authFace() *auth.Face {
	s.ensureSeat()
	return &auth.Face{
		Seat:           s.seat,
		Auth:           s.opts.Auth,
		OperatorToken:  s.opts.OperatorToken,
		StrictLoopback: s.opts.StrictLoopback,
		Visitor:        s.visitor,
		Visitors:       s.visitors,
		FirstSeen:      s.firstSeen,
		WebFS:          s.opts.Web.WebFS,
	}
}

// The hello-handshake surface (session.go's dials) — thin wrappers.
func (s *Server) multiuser() bool                       { return s.authFace().Multiuser() }
func (s *Server) authOf(r *http.Request) *auth.ConnAuth { return s.authFace().AuthOf(r) }
func (s *Server) dialAuth(r *http.Request, tok string) *auth.ConnAuth {
	return s.authFace().DialAuth(r, tok)
}
func (s *Server) authRequired(visitor string, ok bool) bool {
	return s.authFace().AuthRequired(visitor, ok)
}
func (s *Server) visitorFirstOnce(project string) { s.authFace().VisitorFirstOnce(project) }

// The verb-role floor's registry lookups (session.go's per-frame gate).
func verbRoleFloor(verb string) (identity.Role, bool) { return auth.VerbRoleFloor(verb) }
func denyVerbRole(hub *chat.Hub, client *chat.Client, verb string, have *auth.ConnAuth, min identity.Role) {
	auth.DenyVerbRole(hub, client, verb, have, min)
}
func roleName(a *auth.ConnAuth) string { return auth.RoleName(a) }

// The visitor channel's state holders (Start wires them; session.go's
// hello leg and the domain's face both read).
type (
	VisitorGate       = auth.VisitorGate
	visitorMeter      = auth.VisitorMeter
	firstVisitTracker = auth.FirstVisitTracker
	ConnAuth          = auth.ConnAuth
)

var (
	newVisitorMeter      = auth.NewVisitorMeter
	newFirstVisitTracker = auth.NewFirstVisitTracker
)

const sessionCookie = auth.SessionCookie
