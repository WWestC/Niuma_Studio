package cli

// scopeProject is the CLI's default project scope (v2.9 isolation):
// an explicit NIUMA_PROJECT wins (tests, power overrides); otherwise
// the workspace→project table (~/.niuma/ws-projects.json — the
// dispatcher writes it at room start / worktree birth) is probed with
// the process cwd and its ancestors, deepest first. "" (unbound —
// the host running from anywhere, or a stale table) keeps the
// studio-wide view: the host sees everything by design.
//
// The scope only narrows READS (people/docs/tasks/establishment pick
// up ?project=); every write boundary is enforced server-side by
// identity, so a member cannot accidentally widen itself by cd-ing
// out of its workspace — it can only lose its scoping default, and
// the worst case is the old studio-wide read.

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/util"
)

// scopeProject resolves the default project scope ("" = studio-wide).
func scopeProject() string {
	if v := strings.TrimSpace(util.Env("PROJECT")); v != "" {
		return v
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return scopeProjectAt(cwd)
}

// scopeProjectAt is scopeProject against an explicit directory (the
// seam the tests pin): walk the ancestor chain, deepest binding wins.
func scopeProjectAt(dir string) string {
	m, err := util.ReadWorkspaceProjects()
	if err != nil || len(m) == 0 {
		return ""
	}
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if key, ok := m[d]; ok {
			return key
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// withQuery appends the non-empty query pairs to a path (the scoped
// reads' shared URL builder — empty values drop out, so an unscoped
// call renders the bare path).
func withQuery(path string, pairs ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			v.Set(pairs[i], pairs[i+1])
		}
	}
	if len(v) == 0 {
		return path
	}
	return path + "?" + v.Encode()
}

// pickScope reconciles an explicit --project flag with the implicit
// scope: a non-empty flag wins; "-" or "all" forces the studio-wide
// face (the host's escape hatch from inside a bound workspace); the
// bare default drinks from the workspace binding.
func pickScope(flagged string) string {
	v := strings.TrimSpace(flagged)
	switch v {
	case "":
		return scopeProject()
	case "-", "all":
		return ""
	default:
		return v
	}
}

// dialRoom resolves the hello.project a seat-taking CLI dial rides:
// an explicit --project wins, else the workspace binding — the
// member's own office, the one room their saved seat credential
// redeems in (a lobby dial with a project room's token collides into
// a "-2" twin or gets refused at the seat door — the 小苗-2 loop:
// every `niuma say` from a project worktree landed in the lobby).
// "" (unbound: the host running from anywhere) and the lobby key keep
// the legacy lobby dial byte-identical.
func dialRoom(flagged string) string {
	room := pickScope(flagged)
	if room == chat.LobbyKey {
		return ""
	}
	return room
}
