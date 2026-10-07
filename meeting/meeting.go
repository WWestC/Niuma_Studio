// Package meeting is the requirement-review meeting ledger (需求评审
// 会议): one shared meeting room per project office — at most one LIVE
// meeting at a time (the room's occupancy truth) — plus the finished
// meetings' history, one shelf file per project under
// ~/.niuma/meetings/ (v2.10 project isolation: each project owns its
// history file AND its own m_NN counter). The ORCHESTRATOR convenes
// and chairs (v2.4: the meeting is the front yard of their 拆解 duty
// — they pull the requirement's relevant colleagues in, the product
// manager among them as a voice at the table, not the chair); a
// meeting deliberately owns no task/plan semantics: the chair lands
// the breakdown in the ledger through their own plan-proposal path.
// This package only records what happened in the room. (The pre-v2.4
// history files' "pm"/"prd" keys are dropped on load — display-only
// fields, no consumer.)
package meeting

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/util"
)

// Meeting lifecycle states. live occupies the room (the screen is lit,
// the seats are taken); done is history.
const (
	StatusLive = "live"
	StatusDone = "done"
)

// Phase is the in-meeting agenda the dispatcher's clock advances:
// opening waits for the chair's presentation (the requirement recap
// and draft breakdown), discuss collects opinions, summary waits for
// the chair's final 方案. It rides the record so a restart can at
// least tell what was in flight (the clock itself does not survive a
// restart — the room frees).
const (
	PhaseOpening = "opening"
	PhaseDiscuss = "discuss"
	PhaseSummary = "summary"
)

// Field caps (the tasks ledger's prose discipline).
const (
	MaxOpening    = 4000
	MaxConclusion = 2000
	MaxLine       = 500
	MaxTranscript = 40
	historyCap    = 60 // finished meetings kept per project shelf
)

// Line is one turn of the meeting's real discussion — the member's
// turn reply as mirrored into the office, captured verbatim.
type Line struct {
	TS   int64  `json:"ts"`
	By   string `json:"by"`
	Text string `json:"text"`
}

// Meeting is one requirement-review meeting. The ORCHESTRATOR convenes
// and chairs; Participants excludes the chair — the requirement's
// relevant colleagues (the product manager first when free). Opening/
// Conclusion capture the chair's own turns (first non-empty in the
// opening phase, last write wins in the summary phase); Transcript
// collects every participant's lines while live.
type Meeting struct {
	ID           string   `json:"id"` // m_NN, store-stamped
	ProjectKey   string   `json:"project_key"`
	ReqID        string   `json:"req,omitempty"`
	ReqTitle     string   `json:"req_title,omitempty"`
	Chair        string   `json:"chair"`
	Participants []string `json:"participants,omitempty"`
	Status       string   `json:"status"`
	Phase        string   `json:"phase,omitempty"`
	Opening      string   `json:"opening,omitempty"`
	Conclusion   string   `json:"conclusion,omitempty"`
	Transcript   []Line   `json:"transcript,omitempty"`
	StartTS      int64    `json:"start_ts,omitempty"`
	EndTS        int64    `json:"end_ts,omitempty"`
}

// Engine is every project's meeting shelf — the ONLY home of the
// domain's semantics (m_NN counter, occupancy gate, capped history,
// renumber). The live meeting is memory-only by design (a restart
// frees the room); End() moves it into the persisted history through
// the injected ShelfStore seam (the whole shelf — counter plus
// history — is ONE document). A nil seam is the in-memory engine
// (tests, home-less boot); a save failure logs and leaves memory
// ahead (the A-族 discipline). The engine binds ONE storage subject
// at open (the seam calls carry it).
type Engine struct {
	mu      sync.Mutex
	seam    ShelfStore
	subject string
	slots   map[string]*Shelf   // projectKey → that shelf's document body
	active  map[string]*Meeting // projectKey → the live meeting (the room)
}

// Shelf is one project's persisted envelope: the per-project id
// counter plus that shelf's finished history, oldest first, capped.
// The JSON tags are the bridge's import shape, byte-stable.
type Shelf struct {
	Next int       `json:"next"`
	Hist []Meeting `json:"hist"`
}

// OpenMemory builds the in-memory engine (single-user subject "").
func OpenMemory() *Engine {
	return &Engine{slots: map[string]*Shelf{}, active: map[string]*Meeting{}}
}

// OpenStore loads the engine over a persistence seam, bound to one
// storage subject ("" = the single-user historical shape, non-empty =
// the multi-user studio owner account). A corrupt shelf document is
// an error (the caller's corrupt-reset path decides what happens
// next); an empty backing is the fresh-install state. Per-shelf
// counters resume past each shelf's highest stored m_NN even when
// the envelope's next lags behind.
func OpenStore(seam ShelfStore, subject string) (*Engine, error) {
	e := &Engine{seam: seam, subject: subject, slots: map[string]*Shelf{}, active: map[string]*Meeting{}}
	if seam == nil {
		return e, nil
	}
	shelves, err := seam.OpenShelves(subject)
	if err != nil {
		return nil, err
	}
	for key, f := range shelves {
		if f.Next < 1 {
			f.Next = 1
		}
		for _, m := range f.Hist {
			var n int
			if _, err := fmt.Sscanf(m.ID, "m_%d", &n); err == nil && n >= f.Next {
				f.Next = n + 1
			}
		}
		e.slots[key] = f
	}
	return e, nil
}

// DefaultDir returns the conventional v2.10 location:
// ~/.niuma/meetings (one <key>.json per project inside).
func DefaultDir() (string, error) {
	root, err := projects.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "meetings"), nil
}

// LegacyPath returns the pre-isolation single file:
// ~/.niuma/meetings.json. SplitLegacyFile moves it into DefaultDir;
// the path stays known for that migration and the boot log.
func LegacyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma", "meetings.json"), nil
}

// SplitLegacyFile performs the one-time storage isolation move: the
// pre-v2.10 single history file (a bare Meeting array) splits into one
// file per project_key under dir, then the old file is renamed to
// <old>.migrated (kept for forensics, never deleted). Rows with an
// empty project_key (the oldest era predates project stamping) land
// on the lobby shelf "default". Idempotent: a missing old file is a
// no-op; a project whose target file already exists keeps that file.
// Returns the number of meetings moved.
func SplitLegacyFile(oldPath, dir string) (int, error) {
	b, err := os.ReadFile(oldPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var list []Meeting
	if err := json.Unmarshal(b, &list); err != nil {
		return 0, fmt.Errorf("parse %s: %w", oldPath, err)
	}
	bySlot := map[string]*Shelf{}
	for _, m := range list {
		key := m.ProjectKey
		if key == "" {
			key = projects.LobbyKey
		}
		sl, ok := bySlot[key]
		if !ok {
			sl = &Shelf{Next: 1}
			bySlot[key] = sl
		}
		sl.Hist = append(sl.Hist, m)
		var n int
		if _, err := fmt.Sscanf(m.ID, "m_%d", &n); err == nil && n >= sl.Next {
			sl.Next = n + 1
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	moved := 0
	for key, sl := range bySlot {
		target := filepath.Join(dir, key+".json")
		if _, err := os.Stat(target); err == nil {
			continue // existing shelf file wins — idempotent re-runs
		}
		out, err := json.MarshalIndent(sl, "", "  ")
		if err != nil {
			return moved, err
		}
		if err := persist.Save(target, out, 0o644); err != nil {
			return moved, err
		}
		moved += len(sl.Hist)
	}
	if err := os.Rename(oldPath, oldPath+".migrated"); err != nil {
		return moved, fmt.Errorf("archive legacy meetings file: %w", err)
	}
	return moved, nil
}

// Begin convenes the room's one live meeting (the occupancy gate: a
// second Begin while one is live refuses). The store stamps ID (that
// project's own counter), Status and StartTS and starts the agenda at
// the opening phase.
func (s *Engine) Begin(projectKey, reqID, reqTitle, chair string, participants []string) (Meeting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.active[projectKey]; busy {
		return Meeting{}, fmt.Errorf("项目 %s 的会议室已被占用", projectKey)
	}
	sl := s.slotForLocked(projectKey)
	m := &Meeting{
		ID:           fmt.Sprintf("m_%02d", sl.Next),
		ProjectKey:   projectKey,
		ReqID:        reqID,
		ReqTitle:     reqTitle,
		Chair:        chair,
		Participants: append([]string(nil), participants...),
		Status:       StatusLive,
		Phase:        PhaseOpening,
		StartTS:      util.Now(),
	}
	sl.Next++
	s.active[projectKey] = m
	return *m, nil
}

// Active returns the project's live meeting (a copy), if any — the
// room-occupancy read every trigger checks.
func (s *Engine) Active(projectKey string) (Meeting, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.active[projectKey]; ok {
		return copyMeeting(m), true
	}
	return Meeting{}, false
}

// SetPhase advances the live meeting's agenda.
func (s *Engine) SetPhase(projectKey, phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.active[projectKey]; ok {
		m.Phase = phase
	}
}

// NoteOpening records the chair's presentation (first non-empty wins —
// the kickoff turn is the opening, later lines are discussion).
func (s *Engine) NoteOpening(projectKey, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.active[projectKey]; ok && m.Opening == "" {
		m.Opening = clampUTF8(text, MaxOpening)
	}
}

// NoteConclusion records the chair's 方案 from the summary phase (last
// write wins — the chair may refine within the window).
func (s *Engine) NoteConclusion(projectKey, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.active[projectKey]; ok {
		m.Conclusion = clampUTF8(text, MaxConclusion)
	}
}

// Line appends one discussion turn to the live meeting's transcript.
func (s *Engine) Line(projectKey, by, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.active[projectKey]
	if !ok || by == "" || text == "" {
		return
	}
	m.Transcript = append(m.Transcript, Line{TS: util.Now(), By: by, Text: clampUTF8(text, MaxLine)})
	if over := len(m.Transcript) - MaxTranscript; over > 0 {
		m.Transcript = append([]Line(nil), m.Transcript[over:]...)
	}
}

// End releases the room: the live meeting moves into the persisted
// history (capped per project, oldest dropped). Reports the finished
// record.
func (s *Engine) End(projectKey string) (Meeting, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.active[projectKey]
	if !ok {
		return Meeting{}, false
	}
	delete(s.active, projectKey)
	m.Status = StatusDone
	m.EndTS = util.Now()
	out := copyMeeting(m)
	sl := s.slotForLocked(projectKey)
	sl.Hist = append(sl.Hist, out)
	if over := len(sl.Hist) - historyCap; over > 0 {
		sl.Hist = append([]Meeting(nil), sl.Hist[over:]...)
	}
	s.saveLocked(projectKey)
	return out, true
}

// DropProject wipes one project's meeting shelf outright — the project
// reset's half: the finished-review history, the live meeting's slot
// state and the shelf FILE all go (an empty-but-present file would
// reload on boot). The m_NN counter restarts from m_01, the first-open
// state. Returns how many finished meetings went.
func (s *Engine) DropProject(projectKey string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok {
		return 0
	}
	n := len(sl.Hist)
	delete(s.slots, projectKey)
	if s.seam != nil {
		if err := s.seam.DeleteShelf(s.subject, projectKey); err != nil {
			log.Printf("drop meetings shelf (%s): %v — 重启会复活该架，请检查单库", projectKey, err)
		}
	}
	return n
}

// History returns the project's finished meetings, newest first, at
// most n (0 = all).
func (s *Engine) History(projectKey string, n int) []Meeting {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok {
		return []Meeting{}
	}
	out := make([]Meeting, 0, 8)
	for i := len(sl.Hist) - 1; i >= 0; i-- {
		out = append(out, copyMeeting(&sl.Hist[i]))
		if n > 0 && len(out) >= n {
			break
		}
	}
	return out
}

// ReviewedAt returns each requirement's last review time (unix ts):
// the EndTS of the project's most recent finished meeting about it —
// the convene gate's "has had its turn, and how long ago" read (its
// caller cools a reconvene on this). Finished ones only — the live
// meeting's req is still pending its outcome, so a fresh convene must
// not pick it either way while the room is busy.
func (s *Engine) ReviewedAt(projectKey string) map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int64{}
	sl, ok := s.slots[projectKey]
	if !ok {
		return out
	}
	for _, m := range sl.Hist {
		if m.ReqID != "" && m.EndTS > out[m.ReqID] {
			out[m.ReqID] = m.EndTS
		}
	}
	return out
}

// RenumberPlan computes projectKey's compact renumber map (old id →
// new id) over the finished history with NO mutation: meetings
// renumber m_01.. oldest first, each already-compact entry keeping
// its slot (the plan-phase discipline the requirements shelf uses —
// guarded-out entries keep their number, later ones number past it).
func (s *Engine) RenumberPlan(projectKey string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok {
		return nil
	}
	next := 1
	plan := map[string]string{}
	for _, m := range sl.Hist {
		var n int
		if _, err := fmt.Sscanf(m.ID, "m_%d", &n); err != nil {
			continue
		}
		if n == next {
			next++
			continue
		}
		plan[m.ID] = fmt.Sprintf("m_%02d", next)
		next++
	}
	return plan
}

// RenumberApply rewrites ids on projectKey's history per mapping and
// resets that shelf's counter past the new maximum. The live meeting
// (if any — a boot-time migration never sees one) is untouched.
func (s *Engine) RenumberApply(projectKey string, mapping map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.slots[projectKey]
	if !ok {
		return fmt.Errorf("项目 %s 无会议架", projectKey)
	}
	max := 0
	for i, m := range sl.Hist {
		if nn, ok := mapping[m.ID]; ok {
			m.ID = nn
			sl.Hist[i] = m
		}
		var n int
		if _, err := fmt.Sscanf(m.ID, "m_%d", &n); err == nil && n > max {
			max = n
		}
	}
	sl.Next = max + 1
	s.saveLocked(projectKey)
	return nil
}

// RemapReq rewrites the shelf's requirement foreign keys (ReqID) per
// mapping — the v2.10 renumber migration's companion face (the
// requirement ids changed underneath the meetings that reviewed them).
func (s *Engine) RemapReq(projectKey string, mapping map[string]string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(mapping) == 0 {
		return 0
	}
	sl, ok := s.slots[projectKey]
	if !ok {
		return 0
	}
	n := 0
	for i, m := range sl.Hist {
		if m.ReqID != "" {
			if nn, ok := mapping[m.ReqID]; ok {
				m.ReqID = nn
				sl.Hist[i] = m
				n++
			}
		}
	}
	if m, ok := s.active[projectKey]; ok && m.ReqID != "" {
		if nn, ok := mapping[m.ReqID]; ok {
			m.ReqID = nn
			n++
		}
	}
	if n > 0 {
		s.saveLocked(projectKey)
	}
	return n
}

func (s *Engine) slotForLocked(projectKey string) *Shelf {
	sl, ok := s.slots[projectKey]
	if !ok {
		sl = &Shelf{Next: 1}
		s.slots[projectKey] = sl
	}
	return sl
}

func copyMeeting(m *Meeting) Meeting {
	out := *m
	out.Participants = append([]string(nil), m.Participants...)
	out.Transcript = append([]Line(nil), m.Transcript...)
	return out
}

// saveLocked rewrites one project's shelf through the seam. A nil
// seam is the in-memory engine; a persistence failure logs but does
// not abort the in-memory mutation (memory stays ahead until the next
// save). Callers hold s.mu.
func (s *Engine) saveLocked(projectKey string) {
	if s.seam == nil {
		return
	}
	if err := s.seam.SaveShelf(s.subject, projectKey, s.slots[projectKey]); err != nil {
		log.Printf("save meetings shelf (%s): %v — 内存态领先，下次保存追平", projectKey, err)
	}
}

// clampUTF8 trims and clips prose to max runes.
func clampUTF8(s string, max int) string {
	if utf8.RuneCountInString(s) > max {
		return string([]rune(s)[:max])
	}
	return s
}
