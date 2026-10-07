// Package capability is the skill & MCP assembly domain: the studio's
// two capability assets — ① Skills (named instruction fabric, with kb
// manual references) ② MCP servers (http/sse model-context servers a
// member's session is born with) — stored as one global library in
// ~/.niuma/skills.json + ~/.niuma/mcps.json, plus Compose, the
// single-source pure function that folds a profile and an assembly
// list into one seat's effective fabric. Pure model layer: the
// dispatch/server/cli consumption lives on top.
package capability

import (
	"regexp"
	"strings"
)

var (
	keyPattern       = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
	reasoningPattern = regexp.MustCompile(`^[a-z0-9]{1,24}$`)
)

// ValidKey reports whether key is a legal asset slug — the same
// alphabet and rule as the project key: url- and filename-safe
// [a-z0-9_-], 1–32 runes. The key is the immutable primary key;
// Config.skills/mcps and staffing rows foreign-key on it.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// ModelTier is the seat's model pick ("providerId/modelId" or a bare
// id + its thinking intensity) — the shape the staffing row's model
// slot and the dispatcher's seat-model fold speak. Skills never carry
// a model; this type lives here because staffing, dispatch and the
// fabric preview all read it as one vocabulary.
type ModelTier struct {
	ID        string `json:"id"`                  // providerId/modelId or a bare model id
	Reasoning string `json:"reasoning,omitempty"` // e.g. "high"; empty = default
}

// ValidReasoning reports whether r is a legal thinking-intensity slug
// (empty is legal too — the platform default tier).
func ValidReasoning(r string) bool {
	return r == "" || reasoningPattern.MatchString(r)
}

// validManualKey checks one skill's manual reference against the kb
// doc key rules (same alphabet as agents' manual field, two levels
// max) — but rejects instead of silently dropping: a typo'd key saved
// quietly would just never resolve.
func validManualKey(m string) bool {
	if m == "" || len(m) > 64 {
		return false
	}
	segs := strings.Split(m, "/")
	if len(segs) > 2 {
		return false
	}
	for _, seg := range segs {
		if !validKeySegment(seg) {
			return false
		}
	}
	return true
}

func validKeySegment(seg string) bool {
	if seg == "" {
		return false
	}
	for _, r := range seg {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// noWhitespace reports whether s contains no rune a blank or control
// one could hide a separator in (ids, urls and header names are single
// tokens).
func noWhitespace(s string) bool {
	for _, r := range s {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func clampRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}
