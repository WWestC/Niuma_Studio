package verbs

// stores.go — the domain-store set (moved verbatim from the shell's
// ws.go): the verb layer's data world. The shell re-exports it as
// server.StoreSet (type alias) so Options assembly is unchanged.

import (
	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/media"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/plugins"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// StoreSet is Options' domain-store group: one handle per store; nil
// on any field is that feature's off switch.
type StoreSet struct {
	// AgentStore, when set, serves the saved AI onboarding configs so
	// an external AI can discover its prompt over HTTP.
	AgentStore *agents.Store
	// Engine is the task ledger + rank registry: it backs the /kb
	// task endpoints and the WS task write path. Nil disables both.
	Engine *tasks.Engine
	// Docs is the v0.5 room-memory store (manual documents): it backs
	// the WS kb_write/kb_append/kb_restore path. Nil disables writes
	// (reads belong to the /kb/docs endpoints, B3).
	Docs *kb.DocsStore
	// MediaStore, when set, backs 输入图片（聊天发图）: POST /media
	// stores an upload and hands back the reference the say frame's
	// images field carries; GET /media/{id} serves the bytes for <img>
	// rendering. Nil = both faces refuse and the WS say path ignores
	// an images field (text-only, the pre-image behavior).
	MediaStore *media.Store
	// Library, when set, serves the skill & MCP faces: GET /skills
	// and /mcps (library lists), GET/POST/DELETE /skills/{key} and
	// /mcps/{key} (detail / host-channel upsert / delete),
	// GET/POST /skills/authoring (the 自制技能 switch), GET
	// /p/{key}/staffing/{person}/fabric (the Compose preview — the
	// web UI's single source) and the WS skill_save/mcp_save/assemble
	// verbs. Nil = the faces answer 404 and the verbs deny.
	Library *capability.Store
	// StaffStore backs assemble's staffing target and the fabric
	// preview's seat-level override (v2 CAP-M3). Nil = staffing-target
	// assemblies deny, previews fold profile defaults only.
	StaffStore *staffing.Store
	// ProjectStore backs GET /projects — the room-switcher's data
	// source (v2 CAP-M3 batch). Nil = 404.
	ProjectStore *projects.Store
	// Audit receives the assembly audit rows
	// (~/.niuma/audit/assembly.jsonl, v2 CAP-M3). Nil or an
	// empty path = assemblies run unrecorded.
	Audit *capability.AuditLog
	// PlanStore backs the plan-proposal verbs (v2 P4-b): plan_submit/
	// plan_accept/plan_reject over WS and GET /p/{key}/plan. Nil = the
	// verbs deny and the endpoint 404s. The field is the domain engine
	// (the ONLY implementation since the sqlite promotion) — concrete
	// because the accept path drives its staging faces.
	PlanStore *plan.Engine
	// MergeStore backs the merge-proposal verbs (v2.8 gitflow L3):
	// merge_submit/merge_accept/merge_reject over WS and GET /p/{key}/
	// merge. Nil = the verbs deny and the endpoint 404s.
	MergeStore *merge.Engine
	// Requirements is the r_NN ledger the plan gate links to
	// (submit-time existence checks; accept flips req→split). Nil =
	// plans with a req still file, the linkage just cannot be verified
	// or written. Concrete for the accept path's staging faces.
	Requirements *requirements.Engine
	// Meetings backs GET /p/{key}/meeting (the meeting room's
	// occupancy + finished reviews). Nil = the endpoint 404s.
	Meetings meeting.Store
	// Notices is the per-room group-announcement store (飞书式群公告):
	// it backs GET /p/{key}/notice and the WS notice_save verb (the
	// owner's seatless face). Nil = the endpoint 404s and the verb
	// denies.
	Notices notice.Store
	// LedgerTx, when set, runs the accept path's three staged writes
	// (plan slot pop + task shelf + requirement flip) as ONE database
	// transaction — the verb-granularity closure the sqlite promotion's
	// hard condition demanded: either the whole landing commits or
	// nothing does (memory included — the engines adopt only after the
	// commit). Nil (the memory boot) leaves the staged writes as no-ops
	// on nil seams: memory is the truth there anyway.
	LedgerTx func(fn func(tx LedgerSeam) error) error
	// Plugins, when set, backs the plugin face (v3 preview): GET
	// /plugins.json (the workbench loader's index — each request also
	// re-applies wardrobe contributions, so CLI enable/disable lands on
	// the next window load) and GET /plugins/<id>/<path> static assets
	// from the store's root. Nil = the face is absent. The plugin
	// surface is deliberately read-only towards scheduling internals
	// (dispatch/autopilot/fleet stay out of reach).
	Plugins *plugins.Store
}

// LedgerSeam is the transaction-bound view LedgerTx hands its
// callback: the three seams the accept path writes through, all bound
// to one open transaction (sqlstore.TxStores satisfies it).
type LedgerSeam interface {
	Plan() plan.SlotStore
	Tasks() tasks.Store
	Requirements() requirements.ShelfStore
}
