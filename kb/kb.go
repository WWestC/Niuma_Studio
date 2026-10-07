// Package kb is the room's blackboard knowledge base: an embedded
// manual and read-only people queries assembled from the live roster
// and the saved AI configs. Both the pixel UI (the clickable
// blackboard) and the HTTP /kb/* endpoints consume the same assembly,
// so agents and humans always see the same board. (The notice page
// retired with the blackboard's own editor — announcements are the
// chat face's per-room group notices now: the notice package behind
// GET /p/{key}/notice.)
//
// kb is a domain package under the import discipline: it speaks wire
// mirror types and consumer-side interfaces only (Roster/ConfigShelf/
// TaskLedger below, ReqTitler in history.go — the pattern ReqTitler
// already established), never chat/agents/tasks directly. The real
// stores satisfy the interfaces structurally; the vocabulary kb
// quotes (status/rank/role words) lives as pinned private mirrors in
// vocab.go. The implementation registry is kb/implements_test.go —
// compile-time assertions, one jump from interface to producer.
package kb

import (
	_ "embed"
	"sort"

	"github.com/WWestC/Niuma_Studio/wire"
)

//go:embed manual.md
var manualMD string

// Manual returns the embedded manual (plain text, Chinese).
func Manual() string { return manualMD }

// Roster is the room's read face the board assembles from: one locked
// snapshot per call, never a snapshot per member (hub lock
// discipline). Satisfied structurally by *chat.Hub — its Member is
// wire.Member, an alias — so the room runtime needs no kb-aware code
// and kb needs no chat import.
type Roster interface {
	Members() []wire.Member
	GraceNames() []string
	Departed() []wire.Member
}

// ConfigShelf is the saved AI configs' read face — the agents store's
// narrow board view (name/role/manual/prompt/archived only), carried
// as the wire.AgentConfig mirror. Satisfied structurally by
// *agents.Store's Wire* methods.
type ConfigShelf interface {
	WireList() []wire.AgentConfig
	WireListFiltered(wantArchived bool) []wire.AgentConfig
	WireGet(name string) (wire.AgentConfig, bool)
}

// TaskLedger is the task engine's read face for the board: each
// person's rank, task counts and task ledger (as the wire.Task
// mirror — byte-identical JSON to the domain type, per the compat
// tests). Satisfied structurally by *tasks.Engine.
type TaskLedger interface {
	Rank(name string) int
	TaskStats(name string) (total, doing int)
	WireTasksOf(name string) []wire.Task
}

// PersonSummary is one row of the personnel list: a live member, a
// recently departed member, a saved (offline) AI config — or any mix
// of those when the names match exactly.
type PersonSummary struct {
	Name  string `json:"name"`
	Role  string `json:"role,omitempty"`
	Color string `json:"color,omitempty"` // shirt hex, online members only
	Hair  string `json:"hair,omitempty"`

	Online    bool `json:"online"`
	Grace     bool `json:"grace,omitempty"`      // presence-grace seat: no live connection behind it (v0.9 ME1)
	Archived  bool `json:"archived,omitempty"`   // saved config archived (v0.8 t_11): the board lists these in their own group; /kb/people itself stays active-only
	Local     bool `json:"local,omitempty"`      // the human behind this window
	HasPrompt bool `json:"has_prompt,omitempty"` // a saved AI config exists

	LastReport   string `json:"last_report,omitempty"`
	LastReportTS int64  `json:"last_report_ts,omitempty"`

	// Room is the project key of the seat this person currently holds
	// ("default" = the lobby); empty when they hold no seat anywhere
	// (an offline config, a departed memory).
	Room string `json:"room,omitempty"`

	// Working / WorkingSince mirror the member's turn-in-flight tell
	// (chat.Member.Working): the dispatcher's session currently runs a
	// turn. Live seats only — a ghost works nowhere. The chat panel
	// reads this on cold hydration; live flips ride member_work frames.
	Working      bool  `json:"working,omitempty"`
	WorkingSince int64 `json:"working_since,omitempty"`

	// Rank is the person's task-administration rank: 1–9, or 99 for
	// the host (rendered as 房主). Always present.
	Rank      int `json:"rank"`
	TaskCount int `json:"task_count,omitempty"`
	TaskDoing int `json:"task_doing,omitempty"`
}

// PersonDetail is one person's board page: the summary plus the saved
// onboarding prompt and the person's task ledger (in-progress first).
type PersonDetail struct {
	PersonSummary
	Prompt string      `json:"prompt,omitempty"`
	Manual string      `json:"manual,omitempty"` // the member's role manual (room-memory key), if saved
	Tasks  []wire.Task `json:"tasks,omitempty"`
}

// Room pairs a roster with its project key for the cross-room
// assembly: which office a seat lives in. The lobby comes first from
// the server; project rooms follow from the registry.
type Room struct {
	Key    string
	Roster Roster
}

// People merges every room's live roster, recently departed members
// and the saved AI configs, keyed by exact display name (join-time
// deduping means "Alice" and "Alice-2" are different people). Members
// seat into their OWN room's hub (the dispatcher joins a project
// member into that project's hub), so a lobby-only read used to strand
// every project-room member offline on the board while the sidebar
// roster — fed by the project room's own WS — showed them present; the
// two faces now share one cross-room truth. A name holding a live seat
// anywhere is online; a name whose every seat is a presence-grace
// ghost stays online-but-grace (the single-room semantics, applied
// studio-wide). Departed members keep their last identity and report
// so the board lists them as offline even without a saved config; the
// local human is flagged; the result is name-sorted so list indexes
// stay stable. ledger (may be nil) contributes each person's rank and
// task counts.
func People(rooms []Room, shelf ConfigShelf, localName string, ledger TaskLedger) []PersonSummary {
	people := make(map[string]*PersonSummary)
	// one locked snapshot per room per list — never a snapshot per
	// member (hub lock discipline). Seat rows collect first, then
	// fold: live seats beat ghosts, newest report wins the identity.
	type seatRow struct {
		room  string
		m     wire.Member
		ts    int64
		ghost bool
	}
	live := make(map[string]seatRow)
	ghostSeats := make(map[string]seatRow)
	for _, r := range rooms {
		if r.Roster == nil {
			continue
		}
		grace := make(map[string]bool, 4)
		for _, n := range r.Roster.GraceNames() {
			grace[n] = true
		}
		for _, m := range r.Roster.Members() {
			row := seatRow{room: r.Key, m: m, ts: m.LastReportTS, ghost: grace[m.Name]}
			if row.ghost {
				if _, ok := ghostSeats[m.Name]; !ok {
					ghostSeats[m.Name] = row
				}
				continue
			}
			if old, ok := live[m.Name]; !ok || row.ts > old.ts {
				live[m.Name] = row
			}
		}
	}
	for name, row := range live {
		people[name] = &PersonSummary{
			Name:         name,
			Role:         row.m.Role,
			Color:        row.m.Color,
			Hair:         row.m.Hair,
			Online:       true,
			Local:        name == localName,
			LastReport:   row.m.LastReport,
			LastReportTS: row.m.LastReportTS,
			Room:         row.room,
			Working:      row.m.Working,
			WorkingSince: row.m.WorkingSince,
		}
	}
	for name, row := range ghostSeats {
		if _, ok := people[name]; ok {
			continue // a live seat in any room beats every ghost
		}
		people[name] = &PersonSummary{
			Name:         name,
			Role:         row.m.Role,
			Color:        row.m.Color,
			Hair:         row.m.Hair,
			Online:       true,
			Grace:        true,
			Local:        name == localName,
			LastReport:   row.m.LastReport,
			LastReportTS: row.m.LastReportTS,
			Room:         row.room,
		}
	}
	for _, r := range rooms {
		if r.Roster == nil {
			continue
		}
		for _, m := range r.Roster.Departed() {
			if _, ok := people[m.Name]; ok {
				continue // a live member (or grace seat) wins
			}
			people[m.Name] = &PersonSummary{
				Name:         m.Name,
				Role:         m.Role,
				LastReport:   m.LastReport,
				LastReportTS: m.LastReportTS,
			}
		}
	}
	if shelf != nil {
		for _, c := range shelf.WireList() {
			if p, ok := people[c.Name]; ok {
				p.HasPrompt = c.Prompt != ""
				if p.Role == "" {
					p.Role = c.Role
				}
				continue
			}
			// offboarded configs are not offline fallbacks (v0.8 M1):
			// the roster stops breathing them back in; the archive
			// itself stays servable (prompt endpoint, same-name
			// recruit reinstates).
			if c.Archived {
				continue
			}
			people[c.Name] = &PersonSummary{
				Name:      c.Name,
				Role:      c.Role,
				HasPrompt: c.Prompt != "",
			}
		}
	}
	out := make([]PersonSummary, 0, len(people))
	for _, p := range people {
		// The archived sweep (the receipt's 「名册不再显示」 promise):
		// a just-departed seat (the presence grace / departed window)
		// still carries the name, so archived configs are dropped here
		// regardless of how the member entered the map — archive beats
		// presence, grace and the departed window alike.
		if shelf != nil {
			if c, ok := shelf.WireGet(p.Name); ok && c.Archived {
				continue
			}
		}
		if ledger != nil {
			p.Rank = ledger.Rank(p.Name)
			p.TaskCount, p.TaskDoing = ledger.TaskStats(p.Name)
		} else {
			p.Rank = 1
			if p.Local {
				p.Rank = rankHost
			}
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Person returns one person's detail, or ok=false when no member or
// config carries that exact name.
func Person(rooms []Room, shelf ConfigShelf, localName string, ledger TaskLedger, name string) (PersonDetail, bool) {
	for _, p := range People(rooms, shelf, localName, ledger) {
		if p.Name == name {
			d := PersonDetail{PersonSummary: p}
			if shelf != nil {
				if c, ok := shelf.WireGet(name); ok {
					d.Manual = c.Manual
					d.Prompt = c.Prompt
				}
			}
			if ledger != nil {
				d.Tasks = ledger.WireTasksOf(name)
			}
			return d, true
		}
	}
	return PersonDetail{}, false
}
