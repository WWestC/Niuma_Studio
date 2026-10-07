package chat

// Seat-credential files (seat-M1, v0.10): the token a seat holds,
// persisted next to the discovery file (~/.niuma_token_<name>, 0600)
// and presented on every CLI dial — presenting it is what lets a
// member's own one-shot command (say, plan submit, task confirm …)
// take their live seat back for a moment (supersede, no "-2" twin)
// instead of colliding with the dispatcher-held seat. The dispatcher
// saves the credential on every join; the CLI loads it on every dial.
// Losing the file loses only the supersede ability: the grace ghost
// still reclaims by name.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/persist"
)

// seatTokenFileSafe renders a member name as one path-safe segment.
// Non-ASCII names (小牛, 小马) would otherwise sanitize to the same
// underscore run and silently SHARE one credential file — two members
// minting and presenting each other's tokens. When sanitization
// changes the name at all, a short digest of the raw name is appended
// so distinct names always land on distinct files; pure-ASCII names
// keep their historical path byte-for-byte.
func seatTokenFileSafe(name string) string {
	safe := []rune(name)
	changed := false
	for i, r := range safe {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			safe[i] = '_'
			changed = true
		}
	}
	out := string(safe)
	if changed {
		sum := sha256.Sum256([]byte(name))
		out += "-" + hex.EncodeToString(sum[:])[:8]
	}
	return out
}

// seatTokenHome resolves the directory the credential files live in —
// a package var so tests (the dispatcher persists on every attach)
// can point the writes at a scratch dir instead of the real home.
var seatTokenHome = os.UserHomeDir

// OverrideSeatTokenHome points the credential files at dir and
// returns the restore func (tests only).
func OverrideSeatTokenHome(dir string) (restore func()) {
	prev := seatTokenHome
	seatTokenHome = func() (string, error) { return dir, nil }
	return func() { seatTokenHome = prev }
}

// SeatTokenPath returns ~/.niuma_token_<name> — the LEGACY name-only
// path, kept for the owner credential and as the read fallback during
// the per-project re-keying ("" when the home directory can't be
// resolved — callers treat that as "no credential").
func SeatTokenPath(name string) string {
	home, err := seatTokenHome()
	if err != nil {
		return ""
	}
	return filepath.Join(home, fmt.Sprintf(".niuma_token_%s", seatTokenFileSafe(name)))
}

// SeatTokenPathProject returns the per-project credential path
// ~/.niuma_token_<project>__<name>（分项目根治的座位凭证键）：a person
// flowing between rooms keeps one credential PER ROOM, so a one-shot
// CLI dial can never supersede the seat of a room the member left
// (the 小苗-2 shape: the fresh room's token overwrote the one global
// file, and the old room's ghost stopped matching). project=="" or an
// unresolvable home degrades to ""（the caller falls back to the
// legacy path — the owner's own credential lives there by design）.
func SeatTokenPathProject(project, name string) string {
	if strings.TrimSpace(project) == "" {
		return ""
	}
	home, err := seatTokenHome()
	if err != nil {
		return ""
	}
	return filepath.Join(home, fmt.Sprintf(".niuma_token_%s__%s",
		seatTokenFileSafe(project), seatTokenFileSafe(name)))
}

// LoadSeatToken reads the saved credential ("" when absent/unreadable
// — a first dial, or a reset session). A non-empty project reads the
// per-project key first and falls back to the legacy name-only file —
// a seat saved before the re-keying stays presentable through the
// transition.
func LoadSeatToken(project, name string) string {
	var paths []string
	if p := SeatTokenPathProject(project, name); p != "" {
		paths = append(paths, p)
	}
	paths = append(paths, SeatTokenPath(name))
	for _, p := range paths {
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if s := strings.TrimRight(string(b), "\r\n "); s != "" {
			return s
		}
	}
	return ""
}

// SaveSeatToken persists the credential atomically (temp+rename) with
// 0600 — owner-only, like the discovery file. An empty token saves
// nothing (a seatless face never rekeys the credential). A non-empty
// project writes the per-project key ONLY — writing the legacy file
// too would reintroduce the one-global-key clobbering the split
// exists to end (the legacy file the reader still falls back to is
// whatever an older build last wrote there).
func SaveSeatToken(project, name, token string) {
	p := SeatTokenPathProject(project, name)
	if p == "" {
		p = SeatTokenPath(name)
	}
	if p == "" || token == "" {
		return
	}
	_ = persist.Save(p, []byte(token), 0o600)
}

// SeatTokenProjects answers which projects hold a credential file for
// name（分项目凭证键的反查）: the wrong-room dial guard drinks from it —
// an unbound cwd that finds the name credentialed in ANOTHER room
// refuses instead of dialing bare（a bare dial reclaims grace ghosts
// by name, which is exactly the 串房 the split exists to end; the
// 小苗-2 shape, restated as the new key's own rule）.
func SeatTokenProjects(name string) []string {
	home, err := seatTokenHome()
	if err != nil {
		return nil
	}
	ents, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	suffix := "__" + seatTokenFileSafe(name)
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		fname := e.Name()
		if !strings.HasPrefix(fname, ".niuma_token_") || !strings.HasSuffix(fname, suffix) {
			continue
		}
		proj := strings.TrimSuffix(strings.TrimPrefix(fname, ".niuma_token_"), suffix)
		if proj != "" {
			out = append(out, proj)
		}
	}
	return out
}

// SeatToken exposes this seat's current credential so in-process seat
// holders (the dispatcher) can persist it for the member's own CLI
// one-shots to present. Stable for the client's lifetime (set at
// join, before the seat ever becomes visible).
func (c *Client) SeatToken() string { return c.seatToken }

// SeatHolderToken reports the credential of the LIVE seat holding
// name exactly (grace ghosts don't count; their ghost token is only
// reachable via a reclaim). ok is false when no live seat holds the
// name. The dispatcher's re-seat path uses it to decide between a
// same-token supersede (the member's own long-lived holder — a guard)
// and standing down (a stranger's seat).
func (h *Hub) SeatHolderToken(name string) (token string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.member.Name == name {
			return c.seatToken, true
		}
	}
	return "", false
}

// SeatDialTwins reports whether a hello (name, token) dial would be
// dedup-renamed into a "-2" twin by joinLocked — the WS seat door's
// predicate, kept beside joinLocked's own rules so the two can never
// drift. The blockers are exactly joinLocked's: a LIVE same-name seat
// the dialer cannot supersede (a different credential, or a bare dial
// — every live seat holds a minted token), or an un-reclaimed grace
// ghost whose credential mismatches the dialer's non-empty one (a
// member's saved credential is per-room; presenting the current room's
// token against another room's ghost — the 小苗-2 incident, where a
// paused room froze the ghost forever — twins on every dial). grace
// names the ghost case so the door's refusal can tell the two ways
// out apart: the live case wants the seat token, the ghost case wants
// either a bare dial (reclaims by name) or the member's own room.
func (h *Hub) SeatDialTwins(name, token string) (twins, grace bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.member.Name == name {
			return token == "" || c.seatToken != token, false
		}
	}
	if g, ok := h.ghosts[name]; ok {
		if token != "" && g.token != "" && g.token != token {
			return true, true
		}
	}
	return false, false
}
