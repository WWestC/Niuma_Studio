package chat

// v2 P3-a multi-room topology (PRD §4.3 / model 稿 §2.1): 项目=办公室=
// 工作区. The process hosts one Hub per ACTIVE project plus the lobby
// (default) — this registry is the map[key]*Hub between them. The
// lifecycle truth is the projects store: unknown, draft (never opened)
// and archived (frozen) keys never instantiate a Hub. Each room owns
// parameterized persistence slots under the v2 root — history/<key>.jsonl
// (append-only, one frame per line — P3-c) and room/<key>.json. Hub
// internals are untouched: every room decision stays inside its own Hub
// (single-writer discipline).

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/wire"
)

// LobbyKey re-exports the reserved lobby key so routing call sites (the
// server package) need no projects import of their own.
const LobbyKey = wire.LobbyKey // the reserved room key — protocol-owned (wire), projects-aliased

// Registry is the process's room set: one Hub per active project key.
// The zero-value is not usable — NewRegistry builds it.
type Registry struct {
	mu         sync.Mutex
	store      *projects.Store // lifecycle truth: which keys may have a room
	root       string          // v2 root dir; "" = every room in-memory (tests)
	hubs       map[string]*Hub
	ownerName  string // the local human's seat in every project room
	ownerRole  string // ("": embeds without an owner keep bare rooms)
	ownerToken string // studio-wide owner credential (owner-M1): every
	// PausedFn serves each hub's welcome-frame pause state by project
	// key (main wires it to the staffing settings' truth). Settable any
	// time — the per-hub closure reads the field lazily, so hubs
	// instantiated before the wiring catch up on their next welcome.
	PausedFn func(projectKey string) bool
	// hub's owner seat holds this same token, the one thing a mirror-say
	// must present — identity by proof, not by name assertion (the
	// p/book 2026-10-03 incident: an agent ran the owner-only mirror CLI
	// and spoke AS the host into the lobby, because the channel checked
	// only the name). main persists it to ~/.niuma_token_<owner> and
	// serves it to the workbench page loopback-only (/owner/token).
}

// NewRegistry builds the room set over a projects store. root is the
// v2 single-root directory (~/.niuma): rooms persist their
// history under root/history/<key>.jsonl and their seat snapshots under
// root/room/<key>.json; "" keeps every room in memory. The two
// subdirectories are created up front, best-effort — a failed mkdir
// only degrades persistence (saveHistoryLocked logs), never the room.
//
// ownerName/ownerRole ("" = no owner) seat the local human into every
// PROJECT room the instant it instantiates — the lobby parity rule
// (v2 P7: a project room differs from the lobby in DATA only). The
// owner's seat is registry-held and seatless-speaking, exactly the
// lobby's boot seat; main keeps joining the lobby's owner itself
// (before the server starts, for the /kb/people host flag), so the
// registry never touches the lobby key here.
func NewRegistry(store *projects.Store, root, ownerName, ownerRole string) *Registry {
	r := &Registry{store: store, root: root, hubs: make(map[string]*Hub),
		ownerName: ownerName, ownerRole: ownerRole}
	if ownerName != "" {
		r.ownerToken = newSeatToken()
	}
	if root != "" {
		for _, sub := range []string{"history", "room"} {
			if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
				log.Printf("registry: mkdir %s: %v — 该目录落盘不可用，办公室照常运行", sub, err)
			}
		}
	}
	return r
}

// Root re-exposes the v2 root directory ("" = every room in memory) —
// the retention policy's settings file and sweep live under it
// (server/retention.go); read-only, the root is fixed at construction.
func (r *Registry) Root() string { return r.root }

// HistoryPath is key's history slot under the v2 root ("" in-memory).
func (r *Registry) HistoryPath(key string) string {
	if r.root == "" {
		return ""
	}
	return filepath.Join(r.root, "history", key+".jsonl")
}

// SnapshotPath is key's seat-snapshot slot under the v2 root (""
// in-memory) — the graceful-exit hand-over file a taking-over instance
// restores (server.LoadRoomSnapshot).
func (r *Registry) SnapshotPath(key string) string {
	if r.root == "" {
		return ""
	}
	return filepath.Join(r.root, "room", key+".json")
}

// TermDir is key's 终端转录 slot under the v2 root ("" in-memory):
// terminal/<key>/<名>.jsonl, one append-only journal per member (r_17).
// A directory, not a file — the member name is the leaf.
func (r *Registry) TermDir(key string) string {
	if r.root == "" {
		return ""
	}
	return filepath.Join(r.root, "terminal", key)
}

// Hub returns key's room, instantiating it on first use (history slot
// attached). Only active projects get a room: illegal slugs, unknown
// keys, drafts and archived entries are refused — the caller rejects
// the dial with the error.
func (r *Registry) Hub(key string) (*Hub, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hubLocked(key)
}

func (r *Registry) hubLocked(key string) (*Hub, error) {
	if h, ok := r.hubs[key]; ok {
		return h, nil
	}
	if r.store == nil {
		return nil, errors.New(i18n.S("项目登记表不可用"))
	}
	if !projects.ValidKey(key) {
		return nil, errors.New(i18n.Sf("非法项目 key %q（[a-z0-9_-]{1,32}）", key))
	}
	p, ok := r.store.Get(key)
	if !ok {
		return nil, errors.New(i18n.Sf("项目不存在: %s", key))
	}
	switch p.Status {
	case projects.StatusDraft:
		return nil, errors.New(i18n.Sf("项目 %s 尚未开张（draft，不设办公室）", key))
	case projects.StatusArchived:
		return nil, errors.New(i18n.Sf("项目 %s 已归档（办公室冻结）", key))
	}
	h := NewHub()
	// welcome 的暂停水合（房间暂停）：闭包读字段而非值——main 可能在
	// registry 建好之后才接 staffing 真源，晚接也即刻生效。
	h.PausedFn = func() bool { return r.PausedFn != nil && r.PausedFn(key) }
	if path := r.HistoryPath(key); path != "" {
		h.SetHistoryPath(path)
	}
	if dir := r.TermDir(key); dir != "" {
		h.SetTermDir(dir)
	}
	// Every project room opens with the owner already seated (the
	// lobby-parity rule): roster, pixel floor and @-exclusion see the
	// host from the room's first frame, and an owner-named dial finds a
	// live owner seat — the server routes it to the seatless owner face
	// instead of a "-2" twin. Snapshots exclude this seat automatically
	// (Seats skips h.ownerClient), and a re-instantiation re-seats it.
	if key != LobbyKey && r.ownerName != "" {
		seat, _ := h.JoinWithToken(r.ownerName, r.ownerRole, false, r.ownerToken)
		h.SetOwnerSeat(seat)
	}
	r.hubs[key] = h
	return h, nil
}

// OwnerCredential returns the studio-wide owner token ("" when this
// registry has no owner): the one credential every hub's owner seat
// holds and the only thing that speaks a mirror say. main persists it
// to the seat-token file so the mirror CLI loads it, and the server
// serves it to the local workbench page loopback-only.
func (r *Registry) OwnerCredential() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ownerToken
}

// Lobby returns the lobby (default) Hub — the anchor of the Ebiten
// window, the mirror channel, the CLI default route and the server's
// own hub. Requires the store to hold default as active: main calls
// projects.EnsureLobby at boot before this.
func (r *Registry) Lobby() (*Hub, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hubLocked(LobbyKey)
}

// AdoptActive instantiates every active project's room at boot (store
// order), so their histories are loaded up front and their snapshots
// can be restored right after. Returns the adopted keys; an unusable
// entry aborts the walk with whatever came before.
func (r *Registry) AdoptActive() ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.store == nil {
		return nil, errors.New(i18n.S("项目登记表不可用"))
	}
	var keys []string
	for _, p := range r.store.List() {
		if p.Status != projects.StatusActive {
			continue
		}
		if _, err := r.hubLocked(p.Key); err != nil {
			return keys, err
		}
		keys = append(keys, p.Key)
	}
	return keys, nil
}

// Rooms returns the currently instantiated rooms (a copy) — the
// snapshot hand-over walks it on close; tests assert isolation with it.
// FlushHistory drains one room's pending history writes plus its
// seq-ledger saves (rooms not currently instantiated have no flusher
// and nothing pending).
func (r *Registry) FlushHistory(key string) {
	if hub, ok := r.Rooms()[key]; ok && hub != nil {
		hub.FlushHistory()
		hub.FlushLedgers()
		hub.FlushTerm()
	}
}

// FlushAllHistory drains every live room — the exit paths' farewell
// flush, so the last said lines and ledger facts never ride only in
// memory.
func (r *Registry) FlushAllHistory() {
	for _, hub := range r.Rooms() {
		hub.FlushHistory()
		hub.FlushLedgers()
		hub.FlushTerm()
	}
}

func (r *Registry) Rooms() map[string]*Hub {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]*Hub, len(r.hubs))
	for k, h := range r.hubs {
		out[k] = h
	}
	return out
}

// PostInterruptedCards routes the restart's interrupted seats to their
// own rooms (v2.12 卡跟房走): one SystemInterrupted card per project,
// the lobby (Project "" / LobbyKey) carrying only its own seats — a
// room's history and badge feed never carry another room's continue
// prompts, project isolation's same discipline for the restart card.
// text renders each card's Text from its seat names, so callers keep
// the wording. A seat whose room cannot resolve (theoretically
// impossible from the boot path, which walks live hubs) falls back to
// the lobby card with a log — never silently dropped.
func (r *Registry) PostInterruptedCards(seats []InterruptedSeat, text func(names []string) string) {
	if len(seats) == 0 || text == nil {
		return
	}
	byRoom := make(map[string][]InterruptedSeat)
	for _, s := range seats {
		key := s.Project
		if key == "" {
			key = LobbyKey
		}
		byRoom[key] = append(byRoom[key], s)
	}
	keys := make([]string, 0, len(byRoom))
	for k := range byRoom {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := byRoom[key]
		names := make([]string, len(group))
		for i, s := range group {
			names[i] = s.Name
		}
		target, err := r.Hub(key)
		if err != nil {
			log.Printf("registry: 项目房 %s 不可用（%v），该房接续卡落 Niuma_Studio 兜底", key, err)
			if target, err = r.Lobby(); err != nil {
				log.Printf("registry: 接续卡无处可落（%v）——%s 的中断提示被丢弃", err, strings.Join(names, "、"))
				continue
			}
		}
		target.SystemInterrupted(group, text(names))
	}
}

// KeyOf resolves a hub back to its project key (v2 P4-b: the plan
// verbs verify the submitting room against plan.project_key and route
// accept/reject broadcasts to the plan's room). The lobby hub (the
// server's own, never registry-instantiated) reports false — callers
// read that as chat.LobbyKey.
func (r *Registry) KeyOf(h *Hub) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, hh := range r.hubs {
		if hh == h {
			return k, true
		}
	}
	return "", false
}

// Unload drops key's room from the registry — the archive flow's
// unload half. The Hub itself is not torn down here: the caller
// persists it first (Server.Close writes its seat snapshot); after the
// unload the key resolves by its store status again (archived →
// refused; reactivated → a fresh room over the same history file). The
// lobby never unloads — it is process-constant.
func (r *Registry) Unload(key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == LobbyKey {
		return errors.New(i18n.S("Niuma_Studio 不可卸载（进程常驻房）"))
	}
	if hub, ok := r.hubs[key]; ok {
		// Farewell drain BEFORE the map drops the room: the flusher
		// goroutines outlive the registry entry, and PurgeFiles (the
		// usual next step) would see a straggler batch recreate a file
		// it just deleted. h.mu is not r.mu — one-direction nesting,
		// no hazard.
		hub.FlushHistory()
		hub.FlushLedgers()
		hub.FlushTerm()
	}
	if _, ok := r.hubs[key]; !ok {
		return errors.New(i18n.Sf("办公室未实例化: %s", key))
	}
	delete(r.hubs, key)
	return nil
}

// PurgeFiles deletes a NON-instantiated room's durable files — the
// project-delete face's sweep for drafts (never had a room) and
// archived projects (room unloaded at archive time, files stayed).
// The full per-key inventory under the v2 root: the history jsonl plus
// its three seq-ledger siblings (.reads/.acks/.reacts — the paths
// derive from the history slot, SetHistoryPath's rule), the seat
// snapshot, and the terminal journal directory. A still-instantiated
// hub refuses (its live writer would just grow the files back — the
// caller's room reset/reset-durable face is for that); unknown keys
// and the in-memory root are no-ops. Best-effort per artifact: the
// first refusal is reported, the rest still attempted.
func (r *Registry) PurgeFiles(key string) error {
	r.mu.Lock()
	if _, live := r.hubs[key]; live {
		r.mu.Unlock()
		return errors.New(i18n.Sf("项目 %s 的房间仍在册——先卸载再清文件（活房间的清除走重置面）", key))
	}
	root := r.root
	r.mu.Unlock()
	if root == "" {
		return nil
	}
	var errs []string
	history := filepath.Join(root, "history", key+".jsonl")
	for _, path := range []string{
		history,
		strings.TrimSuffix(history, ".jsonl") + ".reads.json",
		strings.TrimSuffix(history, ".jsonl") + ".acks.json",
		strings.TrimSuffix(history, ".jsonl") + ".reacts.json",
		filepath.Join(root, "room", key+".json"),
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, filepath.Base(path)+": "+err.Error())
		}
	}
	if err := os.RemoveAll(filepath.Join(root, "terminal", key)); err != nil {
		errs = append(errs, "terminal: "+err.Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
