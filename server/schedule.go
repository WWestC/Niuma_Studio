package server

// v2 P4-c read faces: GET /p/{key}/schedule?version= serves the
// tasks.ScheduleView gantt projection with chat's member palette painted
// on (the composition root — tasks itself never imports chat, so the
// pure projection stays color-free; the bar color is its assignee's
// member color, the same hex a joined Member.Color carries, both
// frontends agree), and GET /p/{key}/staffing lists the project's
// staffing rows with session seat-presence and the effective packs
// (capability's existing two-level resolution). Both read-only and
// seatless; unknown and non-active projects are 404 — the schedule
// belongs to the open room face, sealed projects keep their /p/{key}/
// tasks ledger read.

import (
	"net/http"
	"time"

	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// handleProjectSchedule serves the gantt projection (GET /p/{key}/
// schedule, v2 P4-c). ?version= picks the lens (unknown name → 404);
// absent defaults to projects.CurrentVersion(now) — nil when nothing
// qualifies, which ScheduleView reads as the full-project view.
func (s *Server) handleProjectSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.Stores.Engine == nil || s.opts.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	p, ok := s.opts.Stores.ProjectStore.Get(key)
	if !ok || p.Status != projects.StatusActive {
		http.NotFound(w, r)
		return
	}
	now := time.Now()
	var v *projects.Version
	if name := r.URL.Query().Get("version"); name != "" {
		for i := range p.Versions {
			if p.Versions[i].Name == name {
				v = &p.Versions[i]
				break
			}
		}
		if v == nil {
			writeJSONErr(w, http.StatusNotFound, i18n.Sf("未知版本: %s（项目 %s）——核对项目设置里的版本表", name, key))
			return
		}
	} else {
		v = projects.CurrentVersion(p, now.Unix())
	}
	view := tasks.ScheduleView(p, v, s.opts.Stores.Engine.ListFiltered("", "", key, ""), now)

	// paint: lanes and bars carry the assignee's member color. A pool
	// bar ("" assignee) hashes like anyone — the client tells pools by
	// the empty assignee, not the color.
	type laneOut struct {
		tasks.Lane
		Color string `json:"color"`
	}
	type barOut struct {
		tasks.Bar
		Color string `json:"color"`
	}
	lanes := make([]laneOut, len(view.Lanes))
	for i, l := range view.Lanes {
		lanes[i] = laneOut{Lane: l, Color: chat.MemberColor(l.Assignee)}
	}
	bars := make([]barOut, len(view.Bars))
	for i, b := range view.Bars {
		bars[i] = barOut{Bar: b, Color: chat.MemberColor(b.Assignee)}
	}
	writeJSON(w, struct {
		ProjectKey    string    `json:"project_key"`
		Version       string    `json:"version,omitempty"`
		Range         []int64   `json:"range"`
		VersionWindow []int64   `json:"version_window,omitempty"`
		TodayTS       int64     `json:"today_ts"`
		Lanes         []laneOut `json:"lanes"`
		Bars          []barOut  `json:"bars"`
		Inbox         []string  `json:"inbox"`
		ActualDone    int       `json:"actual_done,omitempty"`
		ActualPlanned int64     `json:"actual_planned_secs,omitempty"`
		ActualSpent   int64     `json:"actual_spent_secs,omitempty"`
	}{
		ProjectKey: view.ProjectKey, Version: view.Version, Range: view.Range,
		VersionWindow: view.VersionWindow, TodayTS: view.TodayTS,
		Lanes: lanes, Bars: bars, Inbox: view.Inbox,
		ActualDone: view.ActualDone, ActualPlanned: view.ActualPlanned, ActualSpent: view.ActualSpent,
	})
}

// handleProjectStaffing serves the project's staffing table (GET
// /p/{key}/staffing, v2 P4-c): every row (all states, insertion order)
// with the session's seat presence — the live room's roster, the lobby
// reading the server's own hub, an un-instantiated room holding no
// seats — and the seat's EFFECTIVE packs through capability's existing
// resolution (profile defaults ⊕ staffing override, Q18; names resolve
// through the library, a missing key keeps its key with no name rather
// than vanishing).
func (s *Server) handleProjectStaffing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.Stores.ProjectStore == nil || s.opts.Stores.StaffStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	p, ok := s.opts.Stores.ProjectStore.Get(key)
	if !ok || p.Status != projects.StatusActive {
		http.NotFound(w, r)
		return
	}

	// the room that answers seat presence: the registry's instance when
	// one exists, the server's own hub for the lobby (and for the whole
	// single-room embedding shape), nobody for an active project nobody
	// dialed yet.
	var hub *chat.Hub
	switch {
	case s.opts.Registry == nil, key == chat.LobbyKey:
		hub = s.hub
	default:
		hub = s.opts.Registry.Rooms()[key] // nil = no room, no seats
	}

	type skillOut struct {
		Key  string `json:"key"`
		Name string `json:"name,omitempty"` // "" = not in the library (degrades at compose)
	}
	type rowOut struct {
		Person      string     `json:"person"`
		ProjectRole string     `json:"project_role,omitempty"`
		State       string     `json:"state"`
		SessionID   string     `json:"session_id,omitempty"`
		Seated      bool       `json:"seated"`
		Archived    bool       `json:"archived,omitempty"` // 档案已归档（已离职）——行状态已同步折成 offboard
		Skills      []skillOut `json:"skills"`
		Model       string     `json:"model,omitempty"`  // 座位模型选择（providerId/modelId 或裸 id；空=默认链）
		Branch      string     `json:"branch,omitempty"` // 座位分支槽（v2.7.1 分支隔离；空=共享主工作树）
	}
	rows := []rowOut{}
	for _, e := range s.opts.Stores.StaffStore.ListByProject(key) {
		var profile []string
		archived := false
		if s.opts.Stores.AgentStore != nil {
			if cfg, ok := s.opts.Stores.AgentStore.Get(e.Person); ok {
				profile = cfg.Skills
				// 牛马管理的归档就是全离。归档流程（archiveAs）现在就地
				// 离编全部占用行；这里折的是历史遗留的分叉行（归档先于
				// 该流程存在、或行属手招成员无 watchSeat 路径）——读取
				// 面如实说 offboard，不让已离职者以在岗示人。
				if cfg.Archived && e.Occupying() {
					e.State = staffing.StateOffboard
					archived = true
				}
			}
		}
		keys := capability.EffectiveSkills(profile, e.Skills)
		names := make(map[string]string, len(keys))
		for _, sk := range capability.ResolveSkills(s.opts.Stores.Library, keys) {
			names[sk.Key] = sk.Name
		}
		skills := make([]skillOut, 0, len(keys))
		for _, k := range keys {
			skills = append(skills, skillOut{Key: k, Name: names[k]})
		}
		seat := false
		if hub != nil {
			seat = hub.HasLiveMember(e.Person)
		}
		rows = append(rows, rowOut{Person: e.Person, ProjectRole: e.ProjectRole,
			State: e.State, SessionID: e.SessionID, Seated: seat, Archived: archived,
			Skills: skills, Model: e.Model, Branch: e.Branch})
	}
	writeJSON(w, map[string]any{"project": key, "rows": rows})
}
