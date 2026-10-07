// Package verbs is the seated face's verb CONTRACT layer: the inbound
// envelope (the per-frame decode shape), the verb registry, the store
// set and fleet view a verb may touch, and the Seat that carries them.
// The shell (package server) builds a Seat and dispatches frames into
// the registry; domain face packages register their verbs here and
// drink from the Seat — nobody below the shell imports the shell, so
// the server family can split along domains without cycles.
//
// The Taps table is deliberate scaffolding, the boot_fleet pattern
// applied inward: every shell-owned helper a verb calls today is a
// func field the shell wires from its own methods. As each domain
// face migrates out of the shell, its taps move with it (the tap
// count only goes down). The end state: Seat carries stores, fleet,
// identity and routing; each face package holds its own logic.
package verbs

import (
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// Func is one member verb's handler: everything a verb can touch is
// the seat, the room hub, the client, the seated member and the frame
// envelope. A case-level `break` in the old giant switch was a
// `return` here (same "end of verb" meaning).
type Func func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope)

// Table maps frame types to handlers. The core say/report pair lives
// in verbs_say.go; every domain family self-registers from its
// verbs_*.go file's init (Register panics on a duplicate key — a
// mis-split shows up at test-binary start, not at the first frame).
var Table = map[string]Func{}

// Register folds one family's verbs into the registry.
func Register(vs map[string]Func) {
	for k, v := range vs {
		if _, dup := Table[k]; dup {
			panic("duplicate verb registration: " + k)
		}
		Table[k] = v
	}
}

// Seat is what a room verb may touch. Built once by the shell; the
// fields are the verb layer's whole world (see Taps for the shell
// helpers still owed).
type Seat struct {
	// Stores is the domain-store set (nil on any field is that
	// feature's off switch — the Options discipline, moved here).
	Stores StoreSet
	// Fleet is the dispatcher fleet view (FleetAPI below); nil means
	// dispatch is off — callers keep their documented degraded paths.
	Fleet FleetAPI
	// Local is the human operator's display name (the host).
	Local string
	// Registry routes hubs back to project keys; nil reads as the
	// lobby everywhere (hubProject's rule).
	Registry *chat.Registry
	// Hub is the server's own hub — the lobby.
	Hub *chat.Hub
	// Subject is the storage namespace every domain-store call carries
	// ("" = the single-user historical shape; multi-user boot binds the
	// studio owner account's name).
	Subject string
	// Taps are the shell-owned helpers the verb bodies call; the shell
	// wires every one (see Server.newVerbsSeat). Each domain
	// extraction moves its taps into the face package.
	Taps Taps
}

// Taps is the verb layer's owed-helper table: one func field per
// shell method the verb bodies reach, wired in one place (the shell's
// seat constructor). This is transitional scaffolding with a ratchet
// — migrations delete rows, additions are reviewed.
type Taps struct {
	// Task engine ops (ops.go + gitflow.go in the shell today).
	EngineOp          func(op func(*tasks.Engine) tasks.Outcome) tasks.Outcome
	TaskOp            func(h *chat.Hub, c *chat.Client, actor string, out tasks.Outcome)
	TaskUpdateGateOp  func(h *chat.Hub, c *chat.Client, actor, taskID string, p tasks.Patch)
	TaskConfirmGateOp func(h *chat.Hub, c *chat.Client, actor, taskID string)
	// KB ops (ops.go).
	KbOp        func(h *chat.Hub, c *chat.Client, actor, event string, op func(*kb.DocsStore) (kb.DocMeta, error))
	KbWriteGate func(actor, key string) error
	KbRestoreAs func(actor, key string, rev int) (kb.DocMeta, error)
	KbArchiveAs func(actor, key string, archive bool) (kb.DocMeta, error)
	KbDeleteOp  func(h *chat.Hub, c *chat.Client, actor, key string)
	// Operations verbs (autopilot.go in the shell until that domain's turn).
	// Lobby-only management (kick.go / rank.go / archive.go).
}

// KickMinRank is the management rank floor (Lv.8) the lobby
// management verbs and the library-write gates share — the family's
// one rank constant, moved here from the shell's kick.go.
const KickMinRank = 8

// DenyLobbyOnly draws the private refusal a project-routed connection
// gets for the owner-management verbs: kick/rank_set/agent_archive
// operate on the lobby roster, and cross-room management only arrives
// with the per-project dispatcher (P3-b).
func DenyLobbyOnly(hub *chat.Hub, client *chat.Client, verb string) {
	hub.SendTo(client, chat.Message{Type: chat.MsgSystem,
		Text: i18n.Sf("%s 目前仅限 Niuma_Studio 连接使用（项目房管理随 per-project 调度开放）", verb),
		TS:   time.Now().Unix()})
}
