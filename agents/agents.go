// Package agents stores "onboarding configs" for external AIs: the
// name/role they join the room with plus the prompt that drives them.
// The UI creates configs; the HTTP server exposes them so an external
// AI process can fetch its prompt (GET /agents/<name>/prompt).
package agents

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/wire"
)

const (
	maxNameLen   = 24 // matches chat's member-name cap
	maxRoleLen   = 24 // matches chat's role cap
	maxPromptLen = 4000
)

// Config is one saved AI onboarding config.
type Config struct {
	Name   string `json:"name"`
	Role   string `json:"role,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	// Manual is the member's role-manual key in the room memory
	// (v0.5, e.g. "roles/hr"); the person page links to it and the
	// thin onboarding prompt points the member there.
	Manual string `json:"manual,omitempty"`
	// SessionID (v1.0) binds the member to the ZCode session the
	// dispatcher drives (sess_...). Empty = dispatcher-managed birth
	// has not happened yet (manual recruit without binding). The
	// dispatcher resumes and drives exactly this session.
	SessionID string `json:"session_id,omitempty"`
	// Archived marks an offboarded member (v0.8 M1): the config and its
	// prompt stay servable (same-name recruit reinstates), but the
	// roster views (/agents default, /kb/people) stop listing it. Old
	// binaries reading this file ignore the field — a known one-way
	// compatibility, their write-back loses the flag.
	Archived bool `json:"archived,omitempty"`
	// Skills/MCPs (the capability assembly) are the profile-level
	// default: skill keys and MCP server keys. A non-empty staffing
	// override wholly replaces them (the Q18 explicit-takeover
	// ruling); keys resolve through the capability store and missing
	// ones degrade to nothing. The write face is the assemble verb's.
	Skills []string `json:"skills,omitempty"`
	MCPs   []string `json:"mcps,omitempty"`
	// LegacyPacks is the retired capability-pack assembly (pre-v2.13):
	// read-only, folded into Skills once at load and never written
	// back once cleared.
	LegacyPacks []string `json:"packs,omitempty"`
}

// Store is the persisted config list. All methods are safe for
// concurrent use. A store opened with an empty path is in-memory only.
type Store struct {
	mu   sync.Mutex
	path string
	cfgs []Config
	rev  uint64 // bumped on every mutation, for change detection
}

// DefaultPath returns the conventional storage location:
// ~/.niuma_agents.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma_agents.json"), nil
}

// Open loads the store from path. A missing file starts empty.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.cfgs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i := range s.cfgs {
		s.cfgs[i].foldLegacyPacks()
	}
	return s, nil
}

// foldLegacyPacks folds the retired packs references into Skills once
// (order: skills first, then packs) and clears the legacy slot so the
// next save drops it. References whose pack never became a skill
// degrade at compose time — the standing missing-key discipline.
func (c *Config) foldLegacyPacks() {
	if len(c.LegacyPacks) == 0 {
		return
	}
	c.Skills = dedupeKeys(append(c.Skills, c.LegacyPacks...))
	c.LegacyPacks = nil
}

// sanitizeName trims and clamps a config name, dropping control
// characters (they would break the single-line UI), falling back to a
// default.
func sanitizeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		out = append(out, r)
	}
	out = []rune(strings.TrimSpace(string(out)))
	if len(out) > maxNameLen {
		out = out[:maxNameLen]
	}
	if len(out) == 0 {
		return "AI"
	}
	return string(out)
}

// sanitizeRole clamps a role, turning control characters into spaces.
func sanitizeRole(role string) string {
	out := make([]rune, 0, len(role))
	for _, r := range role {
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		out = append(out, r)
	}
	if len(out) > maxRoleLen {
		out = out[:maxRoleLen]
	}
	return string(out)
}

func clampPrompt(p string) string {
	r := []rune(p)
	if len(r) > maxPromptLen {
		r = r[:maxPromptLen]
	}
	return string(r)
}

// Upsert stores the config under an exact name: an existing entry is
// UPDATED in place (field-by-field, empty strings keep the stored
// value), a new name is appended — never a "-2" twin. The recruit
// flow re-saves existing members (e.g. to backfill Manual) and a
// duplicate would fork their identity (D3). A write under an archived
// name clears the flag — same-name recruit IS reinstatement (v0.8 M1),
// no separate unarchive path exists.
func (s *Store) Upsert(name, role, manual, prompt string) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = sanitizeName(name)
	cfg := Config{
		Name:   name,
		Role:   sanitizeRole(role),
		Manual: sanitizeManual(manual),
		Prompt: clampPrompt(prompt),
	}
	for i, c := range s.cfgs {
		if c.Name == name {
			if cfg.Role == "" {
				cfg.Role = c.Role
			}
			if cfg.Manual == "" {
				cfg.Manual = c.Manual
			}
			if cfg.Prompt == "" {
				cfg.Prompt = c.Prompt
			}
			cfg.SessionID = c.SessionID // binding survives prompt re-saves
			cfg.Skills = c.Skills       // assembly survives prompt re-saves (write face is the assemble verb's)
			cfg.MCPs = c.MCPs
			s.cfgs[i] = cfg
			s.rev++
			s.saveLocked()
			return cfg
		}
	}
	s.cfgs = append(s.cfgs, cfg)
	s.rev++
	s.saveLocked()
	return cfg
}

// sanitizeManual validates a manual key against the kb doc rules
// without importing kb (cyclic): same alphabet, two levels max.
func sanitizeManual(m string) string {
	m = strings.TrimSpace(m)
	if len(m) > 64 {
		return ""
	}
	segs := strings.Split(m, "/")
	if len(segs) > 2 {
		return ""
	}
	for _, seg := range segs {
		if seg == "" {
			return ""
		}
		for _, r := range seg {
			ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-'
			if !ok {
				return ""
			}
		}
	}
	return m
}

// Remove deletes the config with that exact name and persists;
// reports whether it existed.
func (s *Store) Remove(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.cfgs {
		if c.Name == name {
			s.cfgs = append(s.cfgs[:i], s.cfgs[i+1:]...)
			s.rev++
			s.saveLocked()
			return true
		}
	}
	return false
}

// List returns a copy of the configs in insertion order (archived
// included — the raw store view).
func (s *Store) List() []Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Config(nil), s.cfgs...)
}

// ListFiltered returns the configs whose archived state equals
// wantArchived — the roster views' split (v0.8 M1): default listings
// want the live ones, ?archived=1 wants the departed.
func (s *Store) ListFiltered(wantArchived bool) []Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Config, 0, len(s.cfgs))
	for _, c := range s.cfgs {
		if c.Archived == wantArchived {
			out = append(out, c)
		}
	}
	return out
}

// The Wire* trio is the store's wire-mirror read face: the same views
// as List/ListFiltered/Get with the configs projected onto
// wire.AgentConfig (the board's card — name/role/manual/prompt/
// archived; the session and capability fields stay domain-side).
// Mirror-only consumers (kb's ConfigShelf, structural) drink here
// instead of the domain slice.

// WireList is List's wire-mirror face.
func (s *Store) WireList() []wire.AgentConfig {
	return configsWire(s.List())
}

// WireListFiltered is ListFiltered's wire-mirror face.
func (s *Store) WireListFiltered(wantArchived bool) []wire.AgentConfig {
	return configsWire(s.ListFiltered(wantArchived))
}

// WireGet is Get's wire-mirror face.
func (s *Store) WireGet(name string) (wire.AgentConfig, bool) {
	c, ok := s.Get(name)
	return configWire(c), ok
}

// configWire projects one config onto its protocol mirror.
func configWire(c Config) wire.AgentConfig {
	return wire.AgentConfig{
		Name: c.Name, Role: c.Role, Manual: c.Manual,
		Prompt: c.Prompt, Archived: c.Archived,
	}
}

// configsWire projects a config slice.
func configsWire(cs []Config) []wire.AgentConfig {
	if len(cs) == 0 {
		return nil
	}
	out := make([]wire.AgentConfig, len(cs))
	for i := range cs {
		out[i] = configWire(cs[i])
	}
	return out
}

// Archive flags the config with that exact name as offboarded and
// persists (v0.8 M1). It is idempotent — archiving an already-archived
// config succeeds again. Reports the stored config and whether the
// name existed at all.
func (s *Store) Archive(name string) (Config, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.cfgs {
		if c.Name == name {
			if !c.Archived {
				c.Archived = true
				s.cfgs[i] = c
				s.rev++
				s.saveLocked()
			}
			return c, true
		}
	}
	return Config{}, false
}

// BindSession records the member's ZCode session binding (v1.0) —
// the dispatcher's durable member→session registry. Binding persists
// across prompt re-saves; BindSession with an empty id clears it
// (unbind). Reports whether the name existed.
func (s *Store) BindSession(name, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.cfgs {
		if c.Name == name {
			c.SessionID = sessionID
			s.cfgs[i] = c
			s.rev++
			s.saveLocked()
			return true
		}
	}
	return false
}

// SetSkills overwrites the profile-level skill assembly (a non-empty
// staffing override still shadows it at compose time — Q18). Reports
// whether the name existed.
func (s *Store) SetSkills(name string, keys []string) bool {
	return s.setAssembly(name, func(c *Config) { c.Skills = dedupeKeys(keys) })
}

// SetMCPs overwrites the profile-level MCP server assembly. Reports
// whether the name existed.
func (s *Store) SetMCPs(name string, keys []string) bool {
	return s.setAssembly(name, func(c *Config) { c.MCPs = dedupeKeys(keys) })
}

func (s *Store) setAssembly(name string, mut func(*Config)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.cfgs {
		if c.Name == name {
			mut(&c)
			s.cfgs[i] = c
			s.rev++
			s.saveLocked()
			return true
		}
	}
	return false
}

// dedupeKeys normalizes an asset-key list: trimmed, empties dropped,
// first occurrence kept (assembly order is load-bearing — Compose
// concatenates in order).
func dedupeKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Get returns one config by exact name.
func (s *Store) Get(name string) (Config, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cfgs {
		if c.Name == name {
			return c, true
		}
	}
	return Config{}, false
}

// Rev returns the mutation counter; callers use it to detect changes
// without comparing config bodies.
func (s *Store) Rev() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

// saveLocked writes the JSON snapshot atomically (temp file + rename —
// a crash mid-write can never leave a torn store behind; a reader
// either sees the old page or the new one, never half of either). A
// failure is logged and the in-memory mutation STAYS, deliberately:
// rolling it back would fork the live studio from its own roster
// mid-run (the dispatcher just wired a session to that name), while
// memory-ahead only costs the row after a restart — the lesser evil,
// paid knowingly. The next successful save re-persists the whole
// snapshot anyway.
func (s *Store) saveLocked() {
	if s.path == "" {
		return
	}
	if err := persist.SaveJSON(s.path, s.cfgs, 0o644); err != nil {
		log.Printf("save agents file (in-memory state stays ahead until the next save): %v", err)
	}
}
