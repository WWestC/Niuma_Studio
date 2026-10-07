package verbs

// seat_scope.go — the routing/roster queries every domain face shares,
// as Seat methods (they read only Seat fields — no shell owed). These
// moved out of the shell's scope.go/plan.go so domain packages resolve
// rooms and rosters without taps; the shell keeps delegating wrappers.

import (
	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
)

// RoomKey resolves a hub back to its room key (""-free: unknown hubs
// read as the lobby).
func (st *Seat) RoomKey(hub *chat.Hub) string {
	if st.Registry != nil {
		if key, ok := st.Registry.KeyOf(hub); ok {
			return key
		}
	}
	return chat.LobbyKey
}

// HubProject resolves a hub back to its office key — the room a WS
// verb executes in (v2.10: bare task ids are per-project shelves, so
// every id-resolving verb needs the room it arrived in). The server's
// own hub (the lobby, never registry-instantiated) and a nil registry
// both read as the lobby.
func (st *Seat) HubProject(h *chat.Hub) string {
	if st.Registry == nil {
		return chat.LobbyKey
	}
	if k, ok := st.Registry.KeyOf(h); ok {
		return k
	}
	return chat.LobbyKey
}

// ProjectRosterHas reports whether name may TAKE ASSIGNMENTS in office
// key: the host always; a live seat in the project's room; an
// occupying staffing row (recruited, not yet seated). An empty key
// (the task pool) or a nil staffing store (feature off, legacy rooms)
// cannot be judged and allows — the nil-store discipline, not a
// silent widening of a checkable case.
func (st *Seat) ProjectRosterHas(key, name string) bool {
	if key == "" || name == "" {
		return true
	}
	if name == st.Local {
		return true
	}
	if st.Stores.StaffStore == nil {
		return true // staffing off: no per-project rosters exist to check against
	}
	if row, ok := st.Stores.StaffStore.OccupancyOf(name); ok && row.ProjectKey == key {
		return true
	}
	for _, row := range st.Stores.StaffStore.ListByProject(key) {
		if row.Person == name && row.Occupying() {
			return true
		}
	}
	if st.Registry != nil {
		for rk, h := range st.Registry.Rooms() {
			if rk != key || h == nil {
				continue
			}
			for _, m := range h.Members() {
				if m.Name == name {
					return true
				}
			}
		}
	}
	return false
}

// planReceipt hands the success frame to the requester when their
// connection sits OUTSIDE the room the frame broadcast to (a
// lobby-dialed host accepting a project plan sees no broadcast) — the
// cross-room receipt. Same-room requesters see the broadcast itself;
// the frame then arrives exactly once per connection. c nil (the
// seatless mirror channel) skips — its caller writes the return value
// back.
// PlanReceipt routes one review-gate receipt: broadcast to the
// plan's room, private copy to the requester (plan + git domains share
// it — the verbs layer's one broadcast helper).
func PlanReceipt(broadcastHub, requesterHub *chat.Hub, c *chat.Client, msg chat.Message) {
	if c != nil && broadcastHub != requesterHub {
		requesterHub.SendTo(c, msg)
	}
}

// IsOrchestrator reports whether actor carries the orchestrator role
// marker (or is the host — the host may file a proposal on anyone's
// behalf; the gate that matters is acceptance, which is host-only).
func (st *Seat) IsOrchestrator(actor string) bool {
	if actor == st.Local {
		return true
	}
	if st.Stores.AgentStore != nil {
		if cfg, ok := st.Stores.AgentStore.Get(actor); ok && cfg.Role == agents.OrchestratorRole {
			return true
		}
	}
	return false
}

// RoomHub resolves a project key to its hub (nil when the room is not
// instantiated; the lobby and a nil registry read as the seat's own
// hub). The roomops/reads/git faces share this resolution.
func (st *Seat) RoomHub(projectKey string) *chat.Hub {
	if projectKey == chat.LobbyKey || st.Registry == nil {
		return st.Hub
	}
	return st.Registry.Rooms()[projectKey]
}
