package server

// Project isolation's enforcement layer (v2.9): the read faces scope
// by ?project= (the CLI's workspace-binding default), the write faces
// gate by IDENTITY — doc writes carry the writer's seat name over the
// WS, plan/task assignments carry the room — so the boundaries hold
// no matter how the client behaves. The honest edge: the plain HTTP
// GETs stay unauthenticated (localhost single-host app, the webview
// drinks from the same routes); ?project= is the scoping contract the
// in-studio CLIs honor by default, not a security boundary against a
// deliberately lying local caller.

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/util"
)

// roomScopeQuery reads ?project= off a read face: absent ("" after
// trim) keeps the studio-wide host view; anything else scopes the
// read, matching keys only by exact equality — a bogus or malformed
// key matches no room and answers the honest empty roster / the
// public-shelf-only docs, never a silent widening back to the whole
// studio.
func (s *Server) roomScopeQuery(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("project"))
}

// hubProject resolves a hub back to its office key — the room a WS
// verb executes in (v2.10: bare task ids are per-project shelves, so
// every id-resolving verb needs the room it arrived in). The server's
// own hub (the lobby, never registry-instantiated) and a nil registry
// both read as the lobby.
func (s *Server) hubProject(h *chat.Hub) string { return s.seat.HubProject(h) }

// kbShelf and kbLedger adapt the stores to kb's read faces, keeping a
// typed-nil store reading as a nil interface: kb's nil-tolerance
// (ranks default to 1, configs optional) must survive the interface
// boundary — a nil *tasks.Engine wrapped in a non-nil interface would
// panic inside the board instead of degrading.
func (s *Server) kbShelf() kb.ConfigShelf {
	if s.opts.Stores.AgentStore == nil {
		return nil
	}
	return s.opts.Stores.AgentStore
}

func (s *Server) kbLedger() kb.TaskLedger {
	if s.opts.Stores.Engine == nil {
		return nil
	}
	return s.opts.Stores.Engine
}

// scopedRooms is allRooms narrowed to one office (empty slice when the
// room is unknown — see roomScopeQuery).
func (s *Server) scopedRooms(project string) []kb.Room {
	if project == "" {
		return s.allRooms()
	}
	for _, r := range s.allRooms() {
		if r.Key == project {
			return []kb.Room{r}
		}
	}
	return []kb.Room{}
}

// scopedPeople is the roster one office may see (kb.People over the
// room set, then the config-derived strangers filtered out): the
// room's own seats and departures, the room's occupying staffing rows
// (offline-but-on-roster), and the host — always, every office needs
// to @房主. The lobby is scoped the same way (v2.9: the hall is a
// room of its own — niuma_studio's office, not the public roster);
// only the host's unscoped face keeps the studio-wide assembly.
func (s *Server) scopedPeople(project string) []kb.PersonSummary {
	rooms := s.scopedRooms(project)
	people := kb.People(rooms, s.kbShelf(), s.opts.LocalName, s.kbLedger())
	if project == "" {
		return people // the host's unscoped face keeps the studio-wide assembly
	}
	eligible := map[string]bool{}
	for _, r := range rooms {
		if r.Roster == nil {
			continue
		}
		for _, m := range r.Roster.Members() {
			eligible[m.Name] = true
		}
		for _, m := range r.Roster.Departed() {
			eligible[m.Name] = true
		}
	}
	if s.opts.Stores.StaffStore != nil {
		for _, row := range s.opts.Stores.StaffStore.ListByProject(project) {
			if row.Occupying() {
				eligible[row.Person] = true
			}
		}
	}
	if s.opts.LocalName != "" {
		eligible[s.opts.LocalName] = true
	}
	out := make([]kb.PersonSummary, 0, len(people))
	for _, p := range people {
		if eligible[p.Name] {
			out = append(out, p)
		}
	}
	// On-staff names with no row yet (recruited, never seated, no saved
	// config) still belong on the office's roster as offline rows.
	if s.opts.Stores.StaffStore != nil {
		for _, row := range s.opts.Stores.StaffStore.ListByProject(project) {
			if !row.Occupying() {
				continue
			}
			seen := false
			for _, p := range out {
				if p.Name == row.Person {
					seen = true
					break
				}
			}
			if !seen {
				out = append(out, kb.PersonSummary{Name: row.Person, Role: row.ProjectRole})
			}
		}
	}
	// The host row must exist even without a seat anywhere: the
	// orchestrator's four sources promise 房主 is always nameable.
	if s.opts.LocalName != "" {
		seen := false
		for _, p := range out {
			if p.Name == s.opts.LocalName {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, kb.PersonSummary{Name: s.opts.LocalName, Local: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scopedPeopleHas answers whether one name is visible on the scoped
// roster (the person/detail/history faces' gate).
func (s *Server) scopedPeopleHas(project, name string) bool {
	for _, p := range s.scopedPeople(project) {
		if p.Name == name {
			return true
		}
	}
	return false
}

// projectRosterHas reports whether name may TAKE ASSIGNMENTS in
// office key: the host always; a live seat in the project's room; an
// occupying staffing row (recruited, not yet seated). An empty key
// (the task pool) or a nil staffing store (feature off, legacy rooms)
// cannot be judged and allows — the nil-store discipline of
// checkPlanRefs, not a silent widening of a checkable case.
func (s *Server) projectRosterHas(key, name string) bool { return s.seat.ProjectRosterHas(key, name) }

// kbWriteGate is the doc-write boundary: the host writes anywhere; a
// seat writes the keys its OWN room owns — a project's p/<key>/ shelf,
// the lobby's ops/ + p/default/ (the hall is a room like any other,
// niuma_studio's own office, not the public library's caretaker); the
// public tier (no room prefix) is host-writable shared assets, so no
// room's editing ever reaches another room's view. The seat resolves
// through the staffing occupancy first, then through the room hubs (a
// manually joined seat without a staffing row still writes where it
// sits). A nil staffing store keeps the old open shelf (the feature
// is off; there are no per-room shelves to defend).
func (s *Server) kbWriteGate(actor, key string) error {
	if key == "" || actor == "" || actor == s.opts.LocalName {
		return nil
	}
	if s.opts.Stores.StaffStore == nil {
		return nil
	}
	proj := chat.LobbyKey // a stranger without a seat: the hall's own shelf
	if row, ok := s.opts.Stores.StaffStore.OccupancyOf(actor); ok {
		proj = row.ProjectKey
	} else if seatRoom := s.roomOfSeatedMember(actor); seatRoom != "" {
		proj = seatRoom
	}
	if room := kb.KeyRoom(key); room != "" && room == proj {
		return nil
	}
	return errors.New(i18n.Sf("文档 %s 不在你的写入门内（%s 的座位只能写本房拥有的键；公共文档库全室共享、房主执笔）——项目隔离拒绝跨房写入", key, proj))
}

// roomOfSeatedMember answers which room's hub currently holds the
// name as a member ("" when seated nowhere).
func (s *Server) roomOfSeatedMember(name string) string {
	if s.opts.Registry == nil {
		return ""
	}
	for key, h := range s.opts.Registry.Rooms() {
		if h == nil {
			continue
		}
		for _, m := range h.Members() {
			if m.Name == name {
				return key
			}
		}
	}
	return ""
}

// roomHub delegates to the seat (the family's shared routing).
func (s *Server) roomHub(projectKey string) *chat.Hub { return s.seat.RoomHub(projectKey) }

// roomKey resolves a hub back to its room key (seat delegation).
func (s *Server) roomKey(hub *chat.Hub) string { return s.seat.RoomKey(hub) }

// planHub resolves the hub a plan's frames belong to: the plan's
// project room through the registry, falling back to the server's own
// hub (the lobby / single-room embedding shape). Every resolution also
// rides the night-light say-feed tap (r_18 night-10): project hubs are
// wired here at first use, the lobby at Start.
func (s *Server) planHub(projectKey string) *chat.Hub {
	h := s.hub
	if s.opts.Registry != nil && projectKey != chat.LobbyKey {
		if ph, err := s.opts.Registry.Hub(projectKey); err == nil {
			h = ph
		}
	}
	s.wireNightTap(h)
	return h
}

// wireNightTap registers the hub's say-feed night-light tap (r_18
// night-10): a say/report line landing in the 0–6 点 window is office
// activity too. Idempotent per hub (nightHubs).
func (s *Server) wireNightTap(h *chat.Hub) {
	if h == nil || s.achieve == nil {
		return
	}
	if _, loaded := s.nightHubs.LoadOrStore(h, struct{}{}); loaded {
		return
	}
	h.OnSay(func(chat.Message) { s.nightLightTap(h, time.Unix(util.Now(), 0)) })
}
