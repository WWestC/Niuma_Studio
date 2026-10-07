package kb

// Doc-key scoping (v2.9 project isolation): the docs store is one
// physical shelf with three logical tiers — 公共文档库 (keys with no
// room prefix: manual, roles/, design/ …), 大厅私档 (ops/…, the
// studio's own operations — its establishment table, chronicle volume,
// room log, meeting minutes — plus the defensive p/default/… spelling)
// and 项目文档库 (p/<key>/…, one shelf per project office). The lobby
// is NOT the public library's owner, just another room: it reads and
// writes its own tier like every project does, and the public tier is
// host-writable shared read-only assets for everyone. Read faces
// filter through KeyInScope; the write faces gate by the writer's
// staffing occupancy against KeyRoom, so a project member's world is
// 公共＋自家, the hall's is 公共＋大厅私档 — never each other's.

import "strings"

// DocProjectPrefix is the per-project shelf prefix.
const DocProjectPrefix = "p/"

// LobbyShelfPrefix is the hall's own legacy shelf: the ops/ namespace
// (establishment, chronicle, room log, minutes) predates room scoping
// and stays put — owned by the lobby exactly like p/<key>/ is owned
// by a project.
const LobbyShelfPrefix = "ops/"

// KeyProject returns the project key a doc key is scoped to: "book"
// for p/book/x, "" for every other key. The defensive p/default/…
// spelling reads as the lobby ("default").
func KeyProject(key string) string {
	if !strings.HasPrefix(key, DocProjectPrefix) {
		return ""
	}
	rest := key[len(DocProjectPrefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// KeyRoom returns the ROOM that owns one doc key — the write-side and
// read-side truth of the three tiers: p/<key>/… belongs to that
// project, ops/… belongs to the lobby ("default", the studio's own
// operations room), everything else is the public library (""). The
// hall is its own room, not the public tier's caretaker.
func KeyRoom(key string) string {
	if strings.HasPrefix(key, DocProjectPrefix) {
		return KeyProject(key)
	}
	if strings.HasPrefix(key, LobbyShelfPrefix) {
		return "default"
	}
	return ""
}

// KeyInScope reports whether one doc key is visible to a seat in
// office proj: public keys are visible to every room, a room's own
// keys (its p/<proj>/ shelf, the lobby's ops/ + p/default/) to that
// room alone. An empty proj means the studio-wide face (the host) —
// everything is in scope.
func KeyInScope(key, proj string) bool {
	if proj == "" {
		return true
	}
	room := KeyRoom(key)
	return room == "" || room == proj
}

// ProjectDocKey builds one project shelf key: p/<key>/<leaf>.
func ProjectDocKey(project, leaf string) string {
	return DocProjectPrefix + project + "/" + leaf
}
