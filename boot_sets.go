package main

// boot_sets.go — the phase HAND-OFF contracts: what each boot phase
// consumes and produces, as struct literals (storeSet from the store
// phase, dispatchSet from the fleet/boot-gate phase, faceSet from the
// server-face phase, shellDeps for the exit choreography). A phase may
// be reordered only if the data flow through these still type-checks
// AND each phase's ordering invariants (documented on the function,
// boot_phase_*.go) still read true — the compiler covers the first,
// the comments cover the second, nothing is implicit anymore.

import (
	"net/http"
	"sync/atomic"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/assistant"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/dispatch"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/media"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/plugins"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// storeSet is every handle the domain-store phase produces; later
// phases (dispatch, server face, keepers, shell) consume from here.
type storeSet struct {
	agentStore  *agents.Store
	staff       *staffing.Store
	lib         *capability.Store
	audit       *capability.AuditLog
	pluginStore *plugins.Store
	engine      *tasks.Engine
	docs        *kb.DocsStore
	plans       *plan.Engine
	merges      *merge.Engine
	reqs        *requirements.Engine
	meets       meeting.Store
	notices     notice.Store
	media       *media.Store
	discoFile   string
	ownerName   string // the local human's display name (the host)
	// ledgerDB is the single-database handle (nil = the degraded
	// in-memory boot). It rides from the ledger leg to the serial tail,
	// where the task engine opens over db.Tasks() — the engine waits
	// for the owner join (it stamps the owner's name), so the handle
	// crosses the leg boundary instead of the engine opening inside
	// the leg. The server face's LedgerTx (the accept path's one
	// transaction) binds to it too.
	ledgerDB *sqlstore.DB
	// ident is the identity gate's product (multi-user shape; zero
	// value under single-user). The serial tail binds the engine and
	// the migrations to its subject; startServerFace wires its service.
	ident identitySet
}

// dispatchSet is the fleet/boot-gate phase's product.
type dispatchSet struct {
	fleet        *dispatch.Fleet
	dispatchHTTP http.Handler
	zcodeStatus  func() server.ZCodeState
	ast          *assistant.Assistant
	closing      *atomic.Bool
	closeBridge  func()
	interrupted  []chat.InterruptedSeat
}

// faceSet is the HTTP/WS face phase's product.
type faceSet struct {
	srv       *server.Server
	resetCh   chan struct{}
	rebuildCh chan struct{}
	devOn     bool
	devDir    string
}

// shellDeps feeds the exit/webview choreography phase.
type shellDeps struct {
	fleet       *dispatch.Fleet
	ast         *assistant.Assistant
	keepers     *server.Keepers
	closing     *atomic.Bool
	closeBridge func()
	srv         *server.Server
	hub         *chat.Hub
	registry    *chat.Registry
	resetCh     chan struct{}
	rebuildCh   chan struct{}
	staff       *staffing.Store
	interrupted []chat.InterruptedSeat
	devOn       bool
	devDir      string
	ownerName   string
}
