// Package tasks is the room's first-class task ledger and personnel
// rank registry. Tasks carry structure (title, assignee, status,
// progress, log) and a lifecycle; ranks (Lv.1–9, host = 99) decide who
// may administrate whose task — strictly higher acts directly,
// same-or-lower enters the pending-proposal negotiation flow.
//
// The Engine owns the domain whole (arbitration, proposals, structure
// checks, the rank registry) over an in-memory truth, and persists
// through the injectable Store seam (store.go): one whole-shelf
// document per project plus the global rank registry. LocalStore (the
// JSON files ~/.niuma/tasks/<key>.json + ~/.niuma_ranks.json) is the
// default; empty path = in-memory. The v3 shard split gives every
// project shelf its own mutex (the rank registry a second, smaller
// one); a write path's permission check nests shard.mu → rankMu
// one-directionally, so every mutation stays atomic against its own
// shelf without serializing the studio.
package tasks

import (
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/util"

	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Task lifecycle states.
const (
	StatusTodo      = "todo"
	StatusDoing     = "doing"
	StatusDone      = "done"
	StatusCancelled = "cancelled"
)

// Task priority levels (飞书式四级，v2 飞书化补全). Empty = 未设置——
// 不设级也是一等公民，排序时沉在已设级的后面。Priority stays a plain
// display attribute: it never gates writes and never rides the
// structural checks (structure.go 只看结构字段).
const (
	PriorityUrgent = "urgent"
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)

// ValidPriority reports whether s is one of the four priority keys.
func ValidPriority(s string) bool {
	switch s {
	case PriorityUrgent, PriorityHigh, PriorityMedium, PriorityLow:
		return true
	}
	return false
}

// PriorityLabel maps a priority key to its Chinese label (CLI/display);
// empty and unknown come back empty — the caller decides the fallback.
func PriorityLabel(p string) string {
	switch p {
	case PriorityUrgent:
		return i18n.S("紧急")
	case PriorityHigh:
		return i18n.S("高")
	case PriorityMedium:
		return i18n.S("中")
	case PriorityLow:
		return i18n.S("低")
	}
	return ""
}

// Proposal kinds: an "assign" proposal rides on a just-created task
// (same-rank assignment awaiting acceptance; declining cancels the
// task); a "change" proposal patches an existing task.
const (
	ProposalAssign = "assign"
	ProposalChange = "change"
)

// Limits (mirrored in kb/manual.md's 上限速查).
const (
	MaxTasks    = 500
	MaxLog      = 100
	MaxTitle    = 120
	MaxDesc     = 2000
	MaxProgress = 2000
	MaxNote     = 2000
	maxNameLen  = 24 // matches chat's member-name cap
)

// RankHost is the local human's rank: higher than every Lv.1–9 and
// never persisted (the host is whoever the room's LocalName says).
const RankHost = 99

// SeedTaskNum is RETIRED (v2.11 号段完全重排): an empty lobby shelf now
// starts from t_01 like every other shelf — the v1 number space it
// used to duck (v2 起步种子 t_100) was washed away when the one-shot
// renumber migration started compacting EVERY shelf, lobby included.
// The constant stays (unreferenced) only as a tombstone for readers
// grepping old docs that still mention the seed.
const SeedTaskNum = 100

// Patch is the update envelope for task_update and proposals. The v1
// four keys plus the v2 text keys (the six scheduling keys and — same
// set/clear machinery — priority); empty fields mean "leave
// unchanged". The text keys follow the house all-string + CLI-flag-
// direct idiom (orchestration §5.1): project/version accept "-" to
// clear (project "-" detaches to the lobby), start/end take the human
// input encoding "YYYY-MM-DD HH:MM" ("-" clears, empty keeps), deps is
// "t_01,t_02" ("-" empties), milestone is "true"/"false", pct is
// "0"-"100" and priority is one of the four keys ("-" clears back to
// 未设置). patch.go parses them; storage stays Unix seconds and 0–100
// int (O6).
type Patch struct {
	Status   string `json:"status,omitempty"`
	Progress string `json:"progress,omitempty"`
	Note     string `json:"note,omitempty"`
	Assignee string `json:"assignee,omitempty"`

	Project   string `json:"project,omitempty"`
	Version   string `json:"version,omitempty"`
	Start     string `json:"start,omitempty"`
	End       string `json:"end,omitempty"`
	Deps      string `json:"deps,omitempty"`
	Milestone string `json:"milestone,omitempty"`
	Pct       string `json:"pct,omitempty"`
	Priority  string `json:"priority,omitempty"`
}

// Empty reports whether the patch would change nothing.
func (p Patch) Empty() bool {
	return p == (Patch{})
}

// LogEntry is one line of a task's change journal. Note-bearing
// updates append their note verbatim; structural changes append a
// synthesized summary.
type LogEntry struct {
	TS   int64  `json:"ts"`
	By   string `json:"by"`
	Note string `json:"note"`
}

// Proposal is a pending change awaiting confirmation from everyone in
// Need (any Decline discards it). At most one per task.
type Proposal struct {
	By    string   `json:"by"`
	Patch Patch    `json:"patch"`
	Need  []string `json:"need"`
	Got   []string `json:"got,omitempty"`
	Kind  string   `json:"kind,omitempty"` // assign | change
	TS    int64    `json:"ts"`
}

// Task is one ledger entry.
type Task struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Desc      string     `json:"desc,omitempty"`
	Assignee  string     `json:"assignee,omitempty"` // empty = task pool
	Status    string     `json:"status"`
	Progress  string     `json:"progress,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	CreatedTS int64      `json:"created_ts,omitempty"`
	UpdatedTS int64      `json:"updated_ts,omitempty"`
	Log       []LogEntry `json:"log,omitempty"`
	Pending   *Proposal  `json:"pending,omitempty"`

	// v2 ten-field extension (PRD §4.2, P1): structure three
	// (project_key/parent/req) plus scheduling seven (version/plan_id/
	// start_ts/end_ts/deps/milestone/progress_pct), with priority
	// riding along as a plain display key (never structural). All
	// omitempty, so v1 ledgers round-trip byte-stable.
	ProjectKey  string   `json:"project_key,omitempty"`  // empty = lobby (read-side default)
	Parent      string   `json:"parent,omitempty"`       // single parent: the task tree
	Req         string   `json:"req,omitempty"`          // mothering requirement (r_NN)
	Version     string   `json:"version,omitempty"`      // project-version name
	PlanID      string   `json:"plan_id,omitempty"`      // originating plan batch
	StartTS     int64    `json:"start_ts,omitempty"`     // Unix seconds
	EndTS       int64    `json:"end_ts,omitempty"`       // Unix seconds
	Deps        []string `json:"deps,omitempty"`         // prerequisite task ids (soft lock)
	Milestone   bool     `json:"milestone,omitempty"`    // durationless node
	ProgressPct int      `json:"progress_pct,omitempty"` // 0–100 int; Progress stays the human note
	Priority    string   `json:"priority,omitempty"`     // urgent|high|medium|low; empty = 未设置

	// Actual work clock (structured, stamped by applyPatch at the hop
	// itself): DoingTS = FIRST hop into doing (hands-on moment, zero
	// stays zero), DoneTS = hop into done (terminal, so exactly one).
	// The gantt's actualSpan reads these first and falls back to
	// scraping the 「状态 A→B」 log notes for legacy rows — the note
	// text stays the human audit trail, no longer the machine's
	// protocol (i18n wording could drift; these can't).
	DoingTS int64 `json:"doing_ts,omitempty"` // Unix seconds
	DoneTS  int64 `json:"done_ts,omitempty"`  // Unix seconds
}

// Outcome is what an operation produced: either a denial (private, to
// the requester only) or an applied/submitted change (broadcast).
type Outcome struct {
	Denied  bool
	Reason  string // denial reason (Denied)
	Event   string // created|updated|proposal|confirmed|declined
	Task    Task   // snapshot after the op
	Text    string // notification text for the broadcast
	Waiting string // comma-joined names still needing confirmation
}

func denied(reason string) Outcome { return Outcome{Denied: true, Reason: reason} }

// DefaultTaskDir returns ~/.niuma/tasks (one <key>.json per project
// inside — v2.10 storage isolation).
func DefaultTaskDir() (string, error) {
	root, err := projects.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "tasks"), nil
}

// LegacyTaskPath returns the pre-isolation single file:
// ~/.niuma_tasks.json. SplitLegacyFile moves it into DefaultTaskDir;
// the path stays known for that migration and the boot log.
func LegacyTaskPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma_tasks.json"), nil
}

// DefaultRankPath returns ~/.niuma_ranks.json.
func DefaultRankPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma_ranks.json"), nil
}

// LobbyKey mirrors projects.LobbyKey / chat.LobbyKey: the reserved key
// of the default project. The read side treats an empty project_key as
// the lobby (PRD §4.2 read-side default), so filtering by the lobby
// also sees unattached tasks.
const LobbyKey = "default"

// SlotKey normalizes a task's project to its numbering shelf: the v1
// empty shape and "default" are the lobby's ONE shelf — id counters,
// structure checks and shelf files all key through this so the two
// spellings can never split the lobby's tasks across files.
func SlotKey(projectKey string) string {
	if projectKey == "" {
		return LobbyKey
	}
	return projectKey
}

// Engine is the task ledger plus the rank registry, with the §6
// permission model baked in. All methods are safe for concurrent use.
// Persistence is the injected Store (one shelf document per project,
// ids are per-project t_NN counters — v2.10, two projects each own a
// t_01), so every id-resolving verb takes the project context it
// resolves in.
//
// v3 sharding (the "多写者" ceiling's removal): the ledger is split one
// shard per project shelf, each with its OWN mutex — two rooms' task
// writes no longer queue on each other, and every write pays O(its
// shelf) instead of O(the whole studio) in both scan and persistence
// (only the touched shelf's document is rewritten, outside the shard
// lock: snapshot under it, atomic save after it, generation-checked so
// a slower older writer can never land under a newer one — the fsync
// that used to run inside one engine-wide critical section is the IO
// this split evicted). The MaxTasks cap stays studio-wide (an atomic
// reservation), the rank registry keeps its own small lock, and a
// cross-shelf reattachment (a patch carrying a project key) funnels
// through relocate: moveMu first, then the two shard locks in
// sorted-key order — the only place two shard locks are held together,
// so no lock-order cycle can form.
type Engine struct {
	regMu  sync.Mutex // guards the shards map itself (shards reset in place, never removed)
	shards map[string]*shard

	moveMu sync.Mutex // serializes cross-shelf reattachments (relocate)
	total  atomic.Int64

	rankMu sync.Mutex
	ranks  map[string]int

	rankSaveMu sync.Mutex
	rankGen    uint64
	rankSaved  uint64

	store Store
	local string
	// subject is the storage namespace this engine's every shelf/rank
	// read and rewrite carries ("" single-user; the studio owner
	// account name under multi-user — one studio, one namespace).
	subject string

	// projectKeys supplies the legal project keys for structural
	// validation (v2 P4-a): main wires it to the projects store's
	// active ∪ draft set. Nil keeps validation on with an empty set —
	// an engine without a registry refuses NEW attachments while
	// unattached (v1-shape) tasks stay writable.
	projectKeys func() []string

	// accept：接受路径的暂存态（plan 引擎的锁全程持有——同一时
	// 刻恰一场接受，无需自己的锁）。
	accept *acceptCtx
}

// shard is one project's slice of the ledger plus its id counter and
// its own persistence-ordering state. mu covers tasks/next/gen; saveMu
// serializes shelf writers and carries the last-persisted generation
// (a save whose generation is not newer than saved is a no-op — the
// snapshot a slower writer carries must never land under a newer one).
type shard struct {
	key   string
	mu    sync.Mutex
	tasks []*Task
	next  int

	saveMu     sync.Mutex
	gen, saved uint64
}

// SetProjectKeys installs the known-project resolver used by the
// write-path structural validation. Callers must not call it while
// writes are in flight (main sets it once at boot).
func (e *Engine) SetProjectKeys(fn func() []string) { e.projectKeys = fn }

// Subject reports the storage namespace this engine is bound to (""
// = the single-user historical shape; the multi-user studio binds the
// admin account name at boot).
func (e *Engine) Subject() string { return e.subject }

func (e *Engine) projectKeysList() []string {
	if e.projectKeys == nil {
		return nil
	}
	return e.projectKeys()
}

// OpenMemory loads (or starts) the in-memory engine（subject ""）.
func OpenMemory(localName string) (*Engine, error) {
	return OpenStoreNS(NewMemStore(), "", localName)
}

// OpenStore loads the engine over an injected persistence seam in the
// single-user subject (""). Multi-user boot uses OpenStoreNS with the
// studio owner account name.
func OpenStore(store Store, localName string) (*Engine, error) {
	return OpenStoreNS(store, "", localName)
}

// OpenStoreNS loads the engine bound to ONE storage subject — the
// namespace every shelf/rank read and rewrite below carries. The
// studio's shared ledger is one namespace: multi-user mode binds the
// admin account's name, single-user binds "".
func OpenStoreNS(store Store, subject, localName string) (*Engine, error) {
	e := &Engine{store: store, subject: subject, local: localName, ranks: map[string]int{}}
	shelves, ranks, err := store.OpenShelves(subject)
	if err != nil {
		return nil, err
	}
	homes := map[string]*shard{}
	home := func(slot string) *shard {
		if ch, ok := homes[slot]; ok {
			return ch
		}
		ch := &shard{key: slot}
		homes[slot] = ch
		return ch
	}
	var total int64
	for _, key := range sortedShelfKeys(shelves) {
		f := shelves[key]
		if f.Next < 1 {
			f.Next = 1
		}
		if f.Next > home(key).next {
			home(key).next = f.Next
		}
		for _, t := range f.Tasks {
			if t.ProjectKey == "" && key == LobbyKey {
				t.ProjectKey = LobbyKey
			}
			h := home(SlotKey(t.ProjectKey))
			h.tasks = append(h.tasks, t)
			total++
			var n int
			if _, err := fmt.Sscanf(t.ID, "t_%d", &n); err == nil && n >= h.next {
				h.next = n + 1
			}
		}
	}
	for name, lv := range ranks {
		e.ranks[name] = lv
	}
	if total == 0 {
		// A truly fresh install: every shelf starts from t_01 (v2.11 —
		// the v2 seed of 100 is retired; the v2.11 renumber migration
		// compacts existing shelves, lobby included, onto 01 too).
		if h := home(LobbyKey); h.next < 1 {
			h.next = 1
		}
	}
	e.shards = homes
	e.total.Store(total)
	return e, nil
}

// shardFor returns (lazily creating) one slot's shard. The map only
// ever grows — DeleteProject resets the shard in place, so pointers
// readers already hold stay valid.
func (e *Engine) shardFor(slot string) *shard {
	e.regMu.Lock()
	defer e.regMu.Unlock()
	if e.shards == nil {
		e.shards = map[string]*shard{}
	}
	if ch, ok := e.shards[slot]; ok {
		return ch
	}
	ch := &shard{key: slot}
	e.shards[slot] = ch
	return ch
}

// shardSnapshot lists the shard pointers sorted by key — the
// deterministic shelf-major order the pre-shard global slice had, for
// cross-shard reads and sweeps.
func (e *Engine) shardSnapshot() []*shard {
	e.regMu.Lock()
	defer e.regMu.Unlock()
	out := make([]*shard, 0, len(e.shards))
	for _, ch := range e.shards {
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// reserveCap claims one MaxTasks slot studio-wide (CAS, so two rooms
// creating concurrently cannot both slip past the cap). Callers that
// deny AFTER reserving must releaseCap before returning.
func (e *Engine) reserveCap() bool {
	for {
		n := e.total.Load()
		if n >= MaxTasks {
			return false
		}
		if e.total.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (e *Engine) releaseCap() { e.total.Add(-1) }

// SplitLegacyFile performs the one-time storage isolation move: the
// pre-v2.10 single ledger (a bare Task array) splits into one shelf
// file per project under dir, then the old file is renamed to
// <old>.migrated (kept for forensics, never deleted). Rows with an
// empty project_key (the v1/pool shape) are stamped "default" on the
// way in — the lobby's two spellings converge to one. Idempotent: a
// missing old file is a no-op; a project whose target file already
// exists keeps that file. Returns the number of tasks moved.
func SplitLegacyFile(oldPath, dir string) (int, error) {
	b, err := os.ReadFile(oldPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var list []*Task
	if err := json.Unmarshal(b, &list); err != nil {
		return 0, fmt.Errorf("parse %s: %w", oldPath, err)
	}
	bySlot := map[string]*Shelf{}
	for _, t := range list {
		key := SlotKey(t.ProjectKey)
		f, ok := bySlot[key]
		if !ok {
			f = &Shelf{Next: 1}
			bySlot[key] = f
		}
		if t.ProjectKey == "" {
			t.ProjectKey = LobbyKey
		}
		f.Tasks = append(f.Tasks, t)
		var n int
		if _, err := fmt.Sscanf(t.ID, "t_%d", &n); err == nil && n >= f.Next {
			f.Next = n + 1
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	moved := 0
	for key, f := range bySlot {
		target := filepath.Join(dir, key+".json")
		if _, err := os.Stat(target); err == nil {
			continue // existing shelf file wins — idempotent re-runs
		}
		out, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			return moved, err
		}
		if err := persist.Save(target, out, 0o644); err != nil {
			return moved, err
		}
		moved += len(f.Tasks)
	}
	if err := os.Rename(oldPath, oldPath+".migrated"); err != nil {
		return moved, fmt.Errorf("archive legacy tasks file: %w", err)
	}
	return moved, nil
}

// LocalName returns the host's display name.
func (e *Engine) LocalName() string { return e.local }

// Rank returns name's rank: RankHost for the local human, the stored
// value otherwise, defaulting to Lv.1.
func (e *Engine) Rank(name string) int {
	e.rankMu.Lock()
	defer e.rankMu.Unlock()
	return e.rankLocked(name)
}

// rankLocked is the rank read every write path nests under its shard
// lock (shard.mu → rankMu, one-directional — rank paths never take a
// shard lock, so no cycle). Callers hold rankMu.
func (e *Engine) rankLocked(name string) int {
	if name == e.local {
		return RankHost
	}
	if r, ok := e.ranks[name]; ok && r >= 1 && r <= 9 {
		return r
	}
	return 1
}

// SetRank records name's rank (host-only path; callers are the local
// UI). lv is clamped to 1–9; the host's own rank cannot be set.
// Returns the broadcast text ("" when nothing changed).
func (e *Engine) SetRank(name string, lv int) string {
	if name == "" || name == e.local {
		return ""
	}
	lv = util.ClampInt(lv, 1, 9)
	e.rankMu.Lock()
	if e.ranks[name] == lv {
		e.rankMu.Unlock()
		return ""
	}
	e.ranks[name] = lv
	e.rankGen++
	e.rankMu.Unlock()
	e.saveRanksAfter()
	return i18n.Sf("已将 %s 的等级调整为 Lv.%d", name, lv)
}

// RemoveRank deletes name's rank row — the bulk-dismiss deep-clean's
// half: a deleted person's Lv.N must not shadow a same-name newcomer
// (who starts fresh at Lv.1). Reports whether a row existed.
func (e *Engine) RemoveRank(name string) bool {
	if name == "" || name == e.local {
		return false
	}
	e.rankMu.Lock()
	if _, ok := e.ranks[name]; !ok {
		e.rankMu.Unlock()
		return false
	}
	delete(e.ranks, name)
	e.rankGen++
	e.rankMu.Unlock()
	e.saveRanksAfter()
	return true
}

// Ranks returns a copy of the persisted rank map (host excluded).
func (e *Engine) Ranks() map[string]int {
	e.rankMu.Lock()
	defer e.rankMu.Unlock()
	out := make(map[string]int, len(e.ranks))
	for k, v := range e.ranks {
		out[k] = v
	}
	return out
}

// --- reads ----------------------------------------------------------------

// List returns tasks filtered by assignee and/or status (either may be
// empty), newest update first.
func (e *Engine) List(assignee, status string) []Task {
	return e.ListFiltered(assignee, status, "", "")
}

// ListFiltered is List plus the v2 P4-a project/version narrows (either
// may be empty). project matches the task's project_key exactly, with
// the lobby's read-side default: filtering by LobbyKey also sees
// unattached (empty-key) tasks. version matches the version name.
// Per-shard snapshots aggregate in shard-key order; a shard's own
// snapshot is internally consistent (cross-shard writes may interleave
// between two shards' sections — the board's UpdatedTS sort absorbs
// it).
func (e *Engine) ListFiltered(assignee, status, project, version string) []Task {
	out := make([]Task, 0, e.total.Load())
	for _, ch := range e.shardSnapshot() {
		ch.mu.Lock()
		for _, t := range ch.tasks {
			if SlotKey(t.ProjectKey) != ch.key {
				continue // in transit to another shelf (relocate pending)
			}
			if assignee != "" && t.Assignee != assignee {
				continue
			}
			if status != "" && t.Status != status {
				continue
			}
			if project != "" {
				if t.ProjectKey != project && !(project == LobbyKey && t.ProjectKey == "") {
					continue
				}
			}
			if version != "" && t.Version != version {
				continue
			}
			out = append(out, copyTask(t))
		}
		ch.mu.Unlock()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedTS > out[j].UpdatedTS })
	return out
}

// All returns every task, newest update first (board rendering groups it).
func (e *Engine) All() []Task { return e.List("", "") }

// Get resolves one bare id across every project's shelf — the HOST
// face's read (the assistant panel, the CLI without a scope). Ids are
// only unique within a project (v2.10), so a number two shelves both
// minted resolves to nothing here; project-scoped faces use GetIn.
func (e *Engine) Get(id string) (Task, bool) {
	var snap Task
	hits := 0
	for _, ch := range e.shardSnapshot() {
		ch.mu.Lock()
		for _, t := range ch.tasks {
			if t.ID != id || SlotKey(t.ProjectKey) != ch.key {
				continue
			}
			hits++
			if hits == 1 {
				snap = copyTask(t) // copied under the lock that owns it
			} else {
				ch.mu.Unlock()
				return Task{}, false // ambiguous: two shelves minted this number
			}
		}
		ch.mu.Unlock()
	}
	if hits == 0 {
		return Task{}, false
	}
	return snap, true
}

// GetIn resolves one id inside projectKey's shelf — the scoped read
// every room, page and gate should use. The lobby's two spellings
// ("" and "default") are one shelf.
func (e *Engine) GetIn(projectKey, id string) (Task, bool) {
	ch := e.shardFor(SlotKey(projectKey))
	ch.mu.Lock()
	defer ch.mu.Unlock()
	t := ch.findLocked(id)
	if t == nil {
		return Task{}, false
	}
	return copyTask(t), true
}

// TasksOf returns the tasks assigned to name, in-progress first, then
// todo, then finished/cancelled (newest update first within a group) —
// the person detail page's ordering.
func (e *Engine) TasksOf(name string) []Task {
	out := make([]Task, 0, 4)
	for _, ch := range e.shardSnapshot() {
		ch.mu.Lock()
		for _, t := range ch.tasks {
			if t.Assignee != name || SlotKey(t.ProjectKey) != ch.key {
				continue
			}
			out = append(out, copyTask(t))
		}
		ch.mu.Unlock()
	}
	rank := func(s string) int {
		switch s {
		case StatusDoing:
			return 0
		case StatusTodo:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].Status), rank(out[j].Status)
		if ri != rj {
			return ri < rj
		}
		return out[i].UpdatedTS > out[j].UpdatedTS
	})
	return out
}

// TaskStats returns name's total and in-progress task counts.
func (e *Engine) TaskStats(name string) (total, doing int) {
	for _, ch := range e.shardSnapshot() {
		ch.mu.Lock()
		for _, t := range ch.tasks {
			if t.Assignee != name || SlotKey(t.ProjectKey) != ch.key {
				continue
			}
			total++
			if t.Status == StatusDoing {
				doing++
			}
		}
		ch.mu.Unlock()
	}
	return total, doing
}

// Count returns the total number of tasks.
func (e *Engine) Count() int {
	n := 0
	for _, ch := range e.shardSnapshot() {
		ch.mu.Lock()
		n += len(ch.tasks)
		ch.mu.Unlock()
	}
	return n
}

// --- writes ---------------------------------------------------------------

// Create adds a task. Assigning to a same-or-higher-ranked other (not
// the host, not oneself) creates the task plus an "assign" proposal —
// the assignee must accept, or declining cancels the task.
// VerifyHintLine is the acceptance-task footer (r_26 t_215): every
// task whose TITLE names 验收 carries a one-line index into the
// manual's 验收纪律库 — the AI orchestrator writes the desc, the
// engine appends the reminder so no acceptance task ships without it.
// One line, never the full text (the manual is the source).
const VerifyHintLine = "\n\n⚠ 验收前读 kb manual 验收纪律节（八条）"

// verifyHint appends the acceptance reminder when the title names it
// and the desc doesn't already carry the line (idempotent — a
// hand-written hint is never doubled).
func verifyHint(title, desc string) string {
	if !strings.Contains(title, "验收") || strings.Contains(desc, "验收纪律") {
		return desc
	}
	if desc != "" && !strings.HasSuffix(desc, "\n") {
		desc += "\n"
	}
	return clampRunes(desc+VerifyHintLine, MaxDesc)
}

// Create is the CLI/host verb: one task, plain fields (the ten-field
// extension stays empty — plan landing fills it). projectKey mints the
// id on that project's own shelf AND attaches it there in one step (a
// task born on book's board carries book's t_NN — v2.10); empty stays
// the v1 pool shape (unattached, the lobby's shelf). Same-rank or
// higher assignee turns the create into a nomination awaiting their
// accept.
func (e *Engine) Create(projectKey, by, title, desc, assignee string) Outcome {
	by, title, desc, assignee = sanitizeName(by), clampRunes(strings.TrimSpace(title), MaxTitle),
		verifyHint(title, clampRunes(strings.TrimSpace(desc), MaxDesc)), sanitizeName(assignee)
	if title == "" {
		return denied(i18n.S("标题不能为空"))
	}
	if projectKey != "" && !projects.ValidKey(projectKey) {
		return denied(i18n.Sf("非法项目 key %q", projectKey))
	}
	if !e.reserveCap() {
		return denied(i18n.Sf("任务数已达上限 %d 条", MaxTasks))
	}

	ts := util.Now()
	ch := e.shardFor(SlotKey(projectKey))
	ch.mu.Lock()
	out := func() Outcome {
		defer ch.mu.Unlock()
		t := &Task{
			ID:         fmt.Sprintf("t_%02d", ch.nextFor()),
			Title:      title,
			Desc:       desc,
			Assignee:   assignee,
			ProjectKey: projectKey,
			Status:     StatusTodo,
			CreatedBy:  by,
			CreatedTS:  ts,
			UpdatedTS:  ts,
		}
		// v2 P4-a: the whole-set structural check before the append. The
		// created task carries no ten-field data through THIS path, so this
		// mostly keeps the ledger's ID integrity honest; CreatePlanned is
		// the pre-populated route plan_accept drives (P4-b).
		set := ch.structureSetLocked(nil, Task{})
		set = append(set, tenFieldProjection(t))
		if err := CheckStructure(set, e.projectKeysList()); err != nil {
			return denied(err.Error())
		}
		ch.tasks = append(ch.tasks, t)
		ch.gen++

		var out Outcome
		switch {
		case assignee == "", assignee == by, e.rankLocked(by) > e.rankLocked(assignee):
			t.log(ts, by, taskCreatedNote(assignee))
			out = Outcome{Event: "created"}
			out.Text = i18n.Sf("%s 创建了任务「%s」", by, title)
			if assignee != "" {
				out.Text += i18n.Sf(" → 指派给 %s", assignee)
			} else {
				out.Text += i18n.S("（任务池，待认领）")
			}
		default:
			// same-rank or higher assignee: create as awaiting acceptance
			t.Pending = &Proposal{
				By: by, Patch: Patch{Assignee: assignee},
				Need: []string{assignee}, Kind: ProposalAssign, TS: ts,
			}
			t.log(ts, by, i18n.Sf("创建并提名 %s 接收（等待确认）", assignee))
			out = Outcome{Event: "created", Waiting: assignee}
			out.Text = i18n.Sf("%s 提名 %s 接收任务「%s」，等待确认", by, assignee, title)
		}
		out.Task = copyTask(t)
		return out
	}()
	e.saveShardAfter(ch)
	return out
}

// CreatePlanned adds a task whose ten-field extension is already
// populated — the plan_accept path (v2 P4-b): the server translates the
// proposal's in-plan deps indices into fresh ids as earlier entries
// land, backfills plan_id/project_key/version/req, then hands each
// entry over. Same gates as Create (cap, whole-set structure — the
// pre-populated fields ride THIS time) and the same assign-proposal
// semantics: the orchestrator never calls this, the server does, after
// the host accepted the plan.
func (e *Engine) CreatePlanned(by string, draft Task) Outcome {
	by = sanitizeName(by)
	draft.Title = clampRunes(strings.TrimSpace(draft.Title), MaxTitle)
	draft.Desc = verifyHint(draft.Title, clampRunes(strings.TrimSpace(draft.Desc), MaxDesc))
	draft.Assignee = sanitizeName(draft.Assignee)
	if draft.Title == "" {
		return denied(i18n.S("标题不能为空"))
	}
	if !e.reserveCap() {
		return denied(i18n.Sf("任务数已达上限 %d 条", MaxTasks))
	}

	ts := util.Now()
	ch := e.shardFor(SlotKey(draft.ProjectKey))
	ch.mu.Lock()
	out := e.createPlannedLocked(by, draft, ts, ch)
	if out.Denied {
		e.releaseCap()
	}
	ch.mu.Unlock()
	e.saveShardAfter(ch)
	return out
}

// createPlannedLocked is CreatePlanned's locked core (the shared
// staging face AcceptCreatePlanned rides the same body): clamps are
// the caller's; callers hold ch.mu and have already reserved the cap
// (a structural denial returns it themselves).
func (e *Engine) createPlannedLocked(by string, draft Task, ts int64, ch *shard) Outcome {
	t := &Task{
		ID:        fmt.Sprintf("t_%02d", ch.nextFor()),
		Title:     draft.Title,
		Desc:      draft.Desc,
		Assignee:  draft.Assignee,
		Status:    StatusTodo,
		CreatedBy: by,
		CreatedTS: ts,
		UpdatedTS: ts,
		// the ten-field extension, verbatim from the accepted entry
		ProjectKey:  draft.ProjectKey,
		Req:         draft.Req,
		Version:     draft.Version,
		PlanID:      draft.PlanID,
		StartTS:     draft.StartTS,
		EndTS:       draft.EndTS,
		Milestone:   draft.Milestone,
		ProgressPct: draft.ProgressPct,
	}
	if len(draft.Deps) > 0 {
		t.Deps = append([]string(nil), draft.Deps...)
	}
	// the same whole-set structural gate Create runs, with the real
	// ten-field projection this time (deps translated to fresh ids must
	// resolve against the just-created earlier siblings).
	set := ch.structureSetLocked(nil, Task{})
	set = append(set, tenFieldProjection(t))
	if err := CheckStructure(set, e.projectKeysList()); err != nil {
		return denied(err.Error())
	}
	ch.tasks = append(ch.tasks, t)
	ch.gen++

	var out Outcome
	switch {
	case draft.Assignee == "", draft.Assignee == by, e.rankLocked(by) > e.rankLocked(draft.Assignee):
		t.log(ts, by, taskCreatedNote(draft.Assignee))
		out = Outcome{Event: "created"}
		out.Text = i18n.Sf("%s 创建了任务「%s」", by, draft.Title)
		if draft.Assignee != "" {
			out.Text += i18n.Sf(" → 指派给 %s", draft.Assignee)
		} else {
			out.Text += i18n.S("（任务池，待认领）")
		}
	default:
		t.Pending = &Proposal{
			By: by, Patch: Patch{Assignee: draft.Assignee},
			Need: []string{draft.Assignee}, Kind: ProposalAssign, TS: ts,
		}
		t.log(ts, by, i18n.Sf("创建并提名 %s 接收（等待确认）", draft.Assignee))
		out = Outcome{Event: "created", Waiting: draft.Assignee}
		out.Text = i18n.Sf("%s 提名 %s 接收任务「%s」，等待确认", by, draft.Assignee, draft.Title)
	}
	out.Task = copyTask(t)
	return out
}

// Update applies p to the task under the §6 permission model: host and
// strictly-higher ranks act directly on lower-ranked owners; everyone
// else negotiates through a pending proposal. Pool tasks may be updated
// or claimed by anyone. projectKey is the shelf the bare id resolves
// in (v2.10: ids are per-project; the room's own project — the lobby's
// "" spelling is the lobby).
func (e *Engine) Update(projectKey, by, id string, p Patch) Outcome {
	by = sanitizeName(by)
	p = sanitizePatch(p)
	if p.Empty() {
		return denied(i18n.S("没有可更新的字段"))
	}
	if p.Status != "" && !validStatus(p.Status) {
		return denied(i18n.Sf("非法状态 %q（todo|doing|done|cancelled）", p.Status))
	}
	p.Assignee = sanitizeName(p.Assignee)

	ch := e.shardFor(SlotKey(projectKey))
	var moved *Task // the patch re-keyed this task onto another shelf
	ch.mu.Lock()
	out := func() Outcome {
		defer ch.mu.Unlock()
		t := ch.findLocked(id)
		if t == nil {
			return denied(i18n.Sf("任务不存在: %s", id))
		}
		if t.Status == StatusDone || t.Status == StatusCancelled {
			return denied(i18n.Sf("任务已结束（%s），不能再更新", statusLabel(t.Status)))
		}
		if t.Pending != nil {
			return denied(i18n.S("已有待确认变更，请等待确认/拒绝或房主仲裁"))
		}

		hr := e.rankLocked(by)
		need := e.needForLocked(by, hr, t, p)

		ts := util.Now()
		if len(need) == 0 {
			sched, reason := e.checkPatchLocked(ch, t, p)
			if reason != "" {
				return denied(reason)
			}
			summary := applyPatch(t, by, p, sched, ts)
			ch.gen++
			if SlotKey(t.ProjectKey) != ch.key {
				moved = t
			}
			return Outcome{Event: "updated", Task: copyTask(t),
				Text: i18n.Sf("%s 更新任务「%s」：%s", by, t.Title, summary)}
		}
		// proposals validate at submission too — a structurally illegal
		// patch never sits in anyone's confirmation queue.
		if _, reason := e.checkPatchLocked(ch, t, p); reason != "" {
			return denied(reason)
		}
		t.Pending = &Proposal{By: by, Patch: p, Need: need, Kind: ProposalChange, TS: ts}
		ch.gen++
		return Outcome{Event: "proposal", Task: copyTask(t),
			Text:    i18n.Sf("%s 申请变更任务「%s」：%s（等待 %s 确认）", by, t.Title, patchSummary(p), strings.Join(need, "、")),
			Waiting: strings.Join(need, "、")}
	}()
	e.saveShardAfter(ch)
	if moved != nil {
		e.relocate(moved, ch.key)
	}
	return out
}

// needForLocked computes who must confirm this patch from whom before
// it may apply. Empty = direct effect. Callers must hold e.mu.
func (e *Engine) needForLocked(by string, hr int, t *Task, p Patch) []string {
	if by == e.local {
		return nil // the host outranks everyone
	}
	self := t.Assignee == by
	pool := t.Assignee == ""
	claim := pool && p.Assignee == by
	reassign := p.Assignee != "" && p.Assignee != t.Assignee

	var need []string
	add := func(name string) {
		if name != "" && name != by {
			need = append(need, name)
		}
	}
	switch {
	case !reassign && (self || pool || claim):
		return nil // own task / pool upkeep / claiming
	case !reassign:
		// someone else's task: strictly higher acts directly
		if hr > e.rankLocked(t.Assignee) {
			return nil
		}
		add(t.Assignee)
	default:
		nr := e.rankLocked(p.Assignee)
		if self || pool || claim {
			// handing the task to N: only N (same rank or higher) accepts
			if nr >= hr {
				add(p.Assignee)
			}
		} else if hr > e.rankLocked(t.Assignee) {
			// higher rank redirecting a lower's task; only a same-or-
			// higher N needs to accept the new assignment
			if nr >= hr {
				add(p.Assignee)
			}
		} else {
			// negotiation: current owner plus (same-or-higher) target
			add(t.Assignee)
			if nr >= hr {
				add(p.Assignee)
			}
		}
	}
	return need
}

// Confirm records by's acceptance. When everyone in Need has accepted,
// the proposed patch applies and the proposal clears. projectKey is
// the shelf the bare id resolves in (v2.10).
func (e *Engine) Confirm(projectKey, by, id string) Outcome {
	by = sanitizeName(by)
	ch := e.shardFor(SlotKey(projectKey))
	var moved *Task
	ch.mu.Lock()
	out := func() Outcome {
		defer ch.mu.Unlock()
		t := ch.findLocked(id)
		if t == nil {
			return denied(i18n.Sf("任务不存在: %s", id))
		}
		p := t.Pending
		if p == nil {
			return denied(i18n.S("该任务没有待确认变更"))
		}
		if !util.Contains(p.Need, by) {
			return denied(i18n.Sf("你不是该变更的需确认人（需: %s）", strings.Join(p.Need, "、")))
		}
		if util.Contains(p.Got, by) {
			return denied(i18n.Sf("你已确认过，等待: %s", strings.Join(remaining(p), "、")))
		}
		// every confirmation re-validates the pending patch: the world may
		// have moved since submission (a child task appeared under this one,
		// the attached project archived…). A refusal keeps the proposal
		// pending — the host's arbitration is the way out.
		sched, reason := e.checkPatchLocked(ch, t, p.Patch)
		if reason != "" {
			return denied(i18n.Sf("变更已无法生效（可请房主仲裁拒绝）：%s", reason))
		}

		ts := util.Now()
		p.Got = append(p.Got, by)
		if len(remaining(p)) > 0 {
			ch.gen++
			return Outcome{Event: "proposal", Task: copyTask(t),
				Text:    i18n.Sf("%s 已确认任务「%s」的变更，剩余等待: %s", by, t.Title, strings.Join(remaining(p), "、")),
				Waiting: strings.Join(remaining(p), "、")}
		}

		summary := applyPatch(t, p.By, p.Patch, sched, ts)
		t.Pending = nil
		ch.gen++
		if SlotKey(t.ProjectKey) != ch.key {
			moved = t
		}
		return Outcome{Event: "confirmed", Task: copyTask(t),
			Text: i18n.Sf("%s 确认，任务「%s」变更生效：%s", by, t.Title, summary)}
	}()
	e.saveShardAfter(ch)
	if moved != nil {
		e.relocate(moved, ch.key)
	}
	return out
}

// Decline discards the pending change (by anyone in Need). Declining
// an "assign" proposal cancels the just-created task. projectKey is
// the shelf the bare id resolves in (v2.10).
func (e *Engine) Decline(projectKey, by, id string) Outcome {
	by = sanitizeName(by)
	ch := e.shardFor(SlotKey(projectKey))
	ch.mu.Lock()
	out := func() Outcome {
		defer ch.mu.Unlock()
		t := ch.findLocked(id)
		if t == nil {
			return denied(i18n.Sf("任务不存在: %s", id))
		}
		p := t.Pending
		if p == nil {
			return denied(i18n.S("该任务没有待确认变更"))
		}
		if !util.Contains(p.Need, by) {
			return denied(i18n.Sf("你不是该变更的需确认人（需: %s）", strings.Join(p.Need, "、")))
		}
		out, _ := e.finishLocked(ch, t, by, false, "")
		return out
	}()
	e.saveShardAfter(ch)
	return out
}

// Arbitrate is the host bypass: approve applies the pending patch at
// once (skipping remaining confirmations), reject discards it.
// projectKey is the shelf the bare id resolves in (v2.10).
func (e *Engine) Arbitrate(projectKey, id string, approve bool) Outcome {
	ch := e.shardFor(SlotKey(projectKey))
	var moved *Task
	ch.mu.Lock()
	out := func() Outcome {
		defer ch.mu.Unlock()
		t := ch.findLocked(id)
		if t == nil {
			return denied(i18n.Sf("任务不存在: %s", id))
		}
		if t.Pending == nil {
			return denied(i18n.S("该任务没有待确认变更"))
		}
		out, mv := e.finishLocked(ch, t, e.local, approve, i18n.S("（房主仲裁）"))
		moved = mv
		return out
	}()
	e.saveShardAfter(ch)
	if moved != nil {
		e.relocate(moved, ch.key)
	}
	return out
}

// finishLocked applies or discards t's proposal and reports it,
// returning the task too when the applied patch re-keyed it onto
// another shelf (the caller relocates it after the lock is released).
// Callers must hold ch.mu.
func (e *Engine) finishLocked(ch *shard, t *Task, by string, approve bool, suffix string) (Outcome, *Task) {
	p := t.Pending
	ts := util.Now()
	if approve {
		sched, reason := e.checkPatchLocked(ch, t, p.Patch)
		if reason != "" {
			return denied(i18n.Sf("变更已无法生效：%s", reason)), nil
		}
		summary := applyPatch(t, p.By, p.Patch, sched, ts)
		t.Pending = nil
		ch.gen++
		var moved *Task
		if SlotKey(t.ProjectKey) != ch.key {
			moved = t
		}
		return Outcome{Event: "confirmed", Task: copyTask(t),
			Text: i18n.Sf("%s 确认，任务「%s」变更生效：%s%s", by, t.Title, summary, suffix)}, moved
	}
	t.Pending = nil
	if p.Kind == ProposalAssign {
		t.Status = StatusCancelled
		t.UpdatedTS = ts
		t.log(ts, by, i18n.Sf("拒绝接收，任务取消%s", suffix))
		ch.gen++
		return Outcome{Event: "declined", Task: copyTask(t),
			Text: i18n.Sf("%s 拒绝接收任务「%s」，任务已取消%s", by, t.Title, suffix)}, nil
	}
	t.log(ts, by, i18n.Sf("拒绝变更申请%s", suffix))
	ch.gen++
	return Outcome{Event: "declined", Task: copyTask(t),
		Text: i18n.Sf("%s 拒绝了 %s 对任务「%s」的变更申请%s", by, p.By, t.Title, suffix)}, nil
}

// ProposalStaleAfter is how long a pending proposal may sit before the
// sweep may release it — and even then ONLY the jammed shapes: the
// patch no longer validates (the world moved under it) or nobody in
// Need is still around. A merely old, still-valid, still-crewed
// proposal is a negotiation, not a jam, and stays.
const ProposalStaleAfter = 24 * time.Hour

// SweepStaleProposals releases projectKey's jammed pending proposals:
// aged past ProposalStaleAfter AND (the patch no longer validates OR
// nobody in Need is still around — around nil skips that test). The
// release rides the decline semantics (an assign proposal's
// just-created task cancels with it); each outcome's Text is the room
// line, the caller owns the broadcast. Hygiene, not autonomy: the
// engine's beat calls it regardless of the autopilot switches. An
// empty projectKey sweeps every shelf (the pre-shard global pass).
func (e *Engine) SweepStaleProposals(projectKey string, now time.Time, around func(string) bool) []Outcome {
	var out []Outcome
	scope := e.shardSnapshot()
	if projectKey != "" {
		scope = []*shard{e.shardFor(SlotKey(projectKey))}
	}
	for _, ch := range scope {
		var moved []*Task // release patches that re-keyed onto another shelf
		ch.mu.Lock()
		func() {
			defer ch.mu.Unlock()
			for _, t := range ch.tasks {
				if t.Pending == nil || SlotKey(t.ProjectKey) != ch.key {
					continue
				}
				p := t.Pending
				if wait := now.Unix() - p.TS; wait < int64(ProposalStaleAfter/time.Second) {
					continue // not aged: a negotiation, not a jam
				}
				reason := ""
				if _, why := e.checkPatchLocked(ch, t, p.Patch); why != "" {
					reason = why
				} else if around != nil && len(p.Need) > 0 {
					still := false
					for _, n := range p.Need {
						if around(n) {
							still = true
							break
						}
					}
					if !still {
						reason = i18n.S("需确认人已全部不在本项目")
					}
				}
				if reason == "" {
					continue
				}
				wait := util.HumanWait(now.Unix() - p.TS)
				ts := now.Unix()
				t.Pending = nil
				if p.Kind == ProposalAssign {
					t.Status = StatusCancelled
					t.UpdatedTS = ts
					t.log(ts, e.local, i18n.Sf("系统出清：指派提案挂起 %s 后无法再生效（%s），任务取消", wait, reason))
					out = append(out, Outcome{Event: "declined", Task: copyTask(t),
						Text: i18n.Sf("[看护] 任务「%s」的指派提案已挂起 %s 且无法再生效（%s）——已自动出清并取消任务；如仍需要请重新指派", t.Title, wait, reason)})
				} else {
					t.log(ts, e.local, i18n.Sf("系统出清：变更提案挂起 %s 后无法再生效（%s），自动拒绝", wait, reason))
					out = append(out, Outcome{Event: "declined", Task: copyTask(t),
						Text: i18n.Sf("[看护] 任务「%s」的变更提案（%s 提出）已挂起 %s 且无法再生效（%s）——已自动出清；提出人可重新发起", t.Title, p.By, wait, reason)})
				}
				ch.gen++
				if SlotKey(t.ProjectKey) != ch.key {
					moved = append(moved, t)
				}
			}
		}()
		e.saveShardAfter(ch)
		for _, t := range moved {
			e.relocate(t, ch.key)
		}
	}
	return out
}

// SystemNote appends a provenance line to a task's log (server-side
// gates' bookkeeping — v2.8 关单提交对账等系统事实). Unlike Update it
// may write a finished task (the note lands right after the done flip)
// and never broadcasts — the caller owns the room line. projectKey is
// the shelf the bare id resolves in (v2.10). Unknown id or empty note:
// silent no-op (garnish must not break the business event).
func (e *Engine) SystemNote(projectKey, id, note string) {
	if note == "" {
		return
	}
	ch := e.shardFor(SlotKey(projectKey))
	ch.mu.Lock()
	t := ch.findLocked(id)
	if t != nil {
		t.log(util.Now(), "系统", note)
		ch.gen++
	}
	ch.mu.Unlock()
	if t != nil {
		e.saveShardAfter(ch)
	}
}

// RemapReq rewrites the Req foreign key of projectKey's tasks per
// mapping — the renumber migrations' companion face (requirement ids
// changed underneath the tasks they mothered).
func (e *Engine) RemapReq(projectKey string, mapping map[string]string) int {
	if len(mapping) == 0 {
		return 0
	}
	slot := SlotKey(projectKey)
	ch := e.shardFor(slot)
	ch.mu.Lock()
	n := 0
	for _, t := range ch.tasks {
		if t.Req == "" || SlotKey(t.ProjectKey) != slot {
			continue
		}
		if nn, ok := mapping[t.Req]; ok {
			t.Req = nn
			n++
		}
	}
	if n > 0 {
		ch.gen++
	}
	ch.mu.Unlock()
	if n > 0 {
		e.saveShardAfter(ch)
	}
	return n
}

// taskTokenRE matches a whole t_NN token in free text (same width as
// patch validation's ^t_\d{1,4}$): the single-pass swap in
// RenumberApply rides on it. One pass beats chained ReplaceAll — a
// renumber map can have a value that is also a key (t_02→t_01 with
// t_03→t_02), and chained whole-string replaces would re-replace the
// just-written token.
var taskTokenRE = regexp.MustCompile(`t_\d{1,4}`)

// swapTaskTokens rewrites every t_NN token of str that rides in
// mapping, in one pass.
func swapTaskTokens(str string, mapping map[string]string) string {
	if str == "" || len(mapping) == 0 {
		return str
	}
	return taskTokenRE.ReplaceAllStringFunc(str, func(tok string) string {
		if nn, ok := mapping[tok]; ok {
			return nn
		}
		return tok
	})
}

// RenumberPlan computes projectKey's compact renumber map (old id →
// new id) with NO mutation: entries renumber t_01.. in insertion
// order, each already-compact entry keeping its slot — the
// requirements/meetings discipline verbatim. The v2.10 exemption of
// task ids is repealed (v2.11 号段完全重排): the shelf is a closed
// world (CheckStructure keeps parent/deps same-shelf), so a compact
// renumber plus reference rewrite leaves nothing dangling inside the
// studio's own storage. Empty map = nothing to do.
func (e *Engine) RenumberPlan(projectKey string) map[string]string {
	ch := e.shardFor(SlotKey(projectKey))
	ch.mu.Lock()
	defer ch.mu.Unlock()
	next := 1
	plan := map[string]string{}
	for _, t := range ch.tasks {
		if SlotKey(t.ProjectKey) != ch.key {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(t.ID, "t_%d", &n); err != nil {
			continue // hand-edited non t_NN id: not ours to renumber
		}
		if n == next {
			next++
			continue
		}
		plan[t.ID] = fmt.Sprintf("t_%02d", next)
		next++
	}
	return plan
}

// RenumberApply rewrites ids on projectKey's shelf per mapping and
// resets that shelf's counter past the new maximum. Same-shelf
// structural references (Parent, Deps, the pending proposal's patch
// deps) and prose (title/desc/progress, log notes) ride along via the
// single-pass token swap. Callers pass a filtered RenumberPlan —
// entries absent from the mapping keep their numbers.
func (e *Engine) RenumberApply(projectKey string, mapping map[string]string) error {
	if len(mapping) == 0 {
		return nil
	}
	ch := e.shardFor(SlotKey(projectKey))
	ch.mu.Lock()
	max := 0
	for _, t := range ch.tasks {
		if SlotKey(t.ProjectKey) != ch.key {
			continue
		}
		if nn, ok := mapping[t.ID]; ok {
			t.ID = nn
		}
		if t.Parent != "" {
			if nn, ok := mapping[t.Parent]; ok {
				t.Parent = nn
			}
		}
		for i, d := range t.Deps {
			if nn, ok := mapping[d]; ok {
				t.Deps[i] = nn
			}
		}
		t.Title = swapTaskTokens(t.Title, mapping)
		t.Desc = swapTaskTokens(t.Desc, mapping)
		t.Progress = swapTaskTokens(t.Progress, mapping)
		for i := range t.Log {
			t.Log[i].Note = swapTaskTokens(t.Log[i].Note, mapping)
		}
		if t.Pending != nil {
			t.Pending.Patch.Deps = swapTaskTokens(t.Pending.Patch.Deps, mapping)
			t.Pending.Patch.Note = swapTaskTokens(t.Pending.Patch.Note, mapping)
		}
		var n int
		if _, err := fmt.Sscanf(t.ID, "t_%d", &n); err == nil && n > max {
			max = n
		}
	}
	ch.next = max + 1
	ch.gen++
	ch.mu.Unlock()
	e.saveShardAfter(ch)
	return nil
}

// DeleteProject wipes one project's task shelf entirely — the project
// reset's ledger half: the tasks, the per-project id counter AND the
// shelf FILE go (the shard resets in place — the map keeps the entry,
// so pointers concurrent readers hold stay valid — and numbering
// restarts from t_01, the first-open state). The lobby shelf refuses
// — its tasks belong to the whole studio and the whole-studio wipe
// is「重置工作室」's business. Returns how many tasks went; a failed
// file removal still leaves the in-memory purge standing and surfaces
// the error.
func (e *Engine) DeleteProject(projectKey string) (int, error) {
	slot := SlotKey(projectKey)
	if slot == LobbyKey {
		return 0, errors.New(i18n.S("Niuma_Studio 任务台账不属于任何单项目——整室清理走「重置工作室」"))
	}
	ch := e.shardFor(slot)
	ch.mu.Lock()
	kept := ch.tasks[:0]
	n := 0
	for _, t := range ch.tasks {
		if SlotKey(t.ProjectKey) == slot {
			n++
			continue
		}
		kept = append(kept, t) // a task in transit to another shelf rides along untouched
	}
	ch.tasks = kept
	ch.next = 0
	ch.gen++
	ch.mu.Unlock()
	if err := e.store.DeleteShelf(e.subject, slot); err != nil {
		return n, fmt.Errorf("%s: %w", i18n.Sf("删除任务架 %s", slot), err)
	}
	return n, nil
}

// --- helpers --------------------------------------------------------------

// findLocked resolves one bare id inside this shard — the ONE lookup
// the write paths trust (ids are per-project since v2.10, so a bare id
// means nothing without its shelf). The slot re-check skips tasks in
// transit (re-keyed, relocation still pending). Callers hold ch.mu.
func (ch *shard) findLocked(id string) *Task {
	for _, t := range ch.tasks {
		if t.ID == id && SlotKey(t.ProjectKey) == ch.key {
			return t
		}
	}
	return nil
}

// nextFor returns (and lazily starts) the shard's id counter. Callers
// hold ch.mu; the returned value is the number to stamp and the
// counter is consumed by the caller's increment.
func (ch *shard) nextFor() int {
	if ch.next < 1 {
		ch.next = 2
		return 1
	}
	n := ch.next
	ch.next = n + 1
	return n
}

// relocate splices a re-keyed task from oldSlot's shard onto its new
// shelf's. It runs AFTER the mutation lock is released (callers pass
// the shard they resolved in); between applyPatch and relocate the
// task is invisible on both shelves and on neither duplicated — boot
// re-homes any task by its own project key, whatever file it rode in
// on. moveMu first, then the two shard locks in sorted-key order, is
// the only place two shard locks are held together, so no lock-order
// cycle can form. Writing the (possibly now-empty) old shelf back
// also closes the JSON-era resurrection window (a moved last task
// used to leave a stale shelf file behind).
func (e *Engine) relocate(t *Task, oldSlot string) {
	newSlot := SlotKey(t.ProjectKey)
	if newSlot == oldSlot {
		return
	}
	e.moveMu.Lock()
	defer e.moveMu.Unlock()
	a, b := e.shardFor(oldSlot), e.shardFor(newSlot)
	first, second := a, b
	if first.key > second.key {
		first, second = b, a
	}
	first.mu.Lock()
	second.mu.Lock()
	a.removePtr(t)
	if !b.containsPtr(t) {
		b.tasks = append(b.tasks, t)
		b.gen++
	}
	second.mu.Unlock()
	first.mu.Unlock()
	e.saveShardAfter(a)
	e.saveShardAfter(b)
}

// removePtr drops the task from the shard by pointer identity (a
// re-keyed task's slot no longer names its home). Callers hold ch.mu.
func (ch *shard) removePtr(t *Task) {
	for i, u := range ch.tasks {
		if u == t {
			ch.tasks = append(ch.tasks[:i], ch.tasks[i+1:]...)
			ch.gen++
			return
		}
	}
}

func (ch *shard) containsPtr(t *Task) bool {
	for _, u := range ch.tasks {
		if u == t {
			return true
		}
	}
	return false
}

// applyPatch mutates t (log appended, UpdatedTS refreshed) and returns
// a human summary of what changed. sched is the patch's pre-parsed
// scheduling projection (checkPatchLocked's output) — applyPatch trusts
// it, it never re-validates.
func applyPatch(t *Task, by string, p Patch, sched schedPatch, ts int64) string {
	var parts []string
	switch {
	case p.Status != "" && p.Status != t.Status:
		parts = append(parts, i18n.Sf("状态 %s→%s", statusLabel(t.Status), statusLabel(p.Status)))
		t.Status = p.Status
		// the structured work clock: stamped at the hop itself so the
		// gantt's real-span read never scrapes the note text (see
		// Task.DoingTS/DoneTS — first doing wins, done is terminal).
		if p.Status == StatusDoing && t.DoingTS == 0 {
			t.DoingTS = ts
		}
		if p.Status == StatusDone {
			t.DoneTS = ts
		}
	case p.Status != "":
		parts = append(parts, i18n.Sf("状态保持 %s", statusLabel(t.Status)))
	}
	if p.Progress != "" {
		t.Progress = p.Progress
		parts = append(parts, i18n.Sf("进度 %s", p.Progress))
	}
	if p.Assignee != "" && p.Assignee != t.Assignee {
		if t.Assignee == "" {
			parts = append(parts, i18n.Sf("%s 认领", p.Assignee))
		} else {
			parts = append(parts, i18n.Sf("负责人 → %s", p.Assignee))
		}
		t.Assignee = p.Assignee
	}
	// the six scheduling keys: snapshot, apply, then describe the diff
	// (排期 → … · 依赖 +t_85 · 挂靠 …) — the log line the gantt-era audit
	// trail reads. Priority rides the same snapshot/diff (优先级 高→紧急).
	before := Task{ProjectKey: t.ProjectKey, Version: t.Version, StartTS: t.StartTS,
		EndTS: t.EndTS, Deps: t.Deps, Milestone: t.Milestone, ProgressPct: t.ProgressPct,
		Priority: t.Priority}
	sched.applyTo(t)
	parts = append(parts, schedParts(before, *t)...)

	t.UpdatedTS = ts
	summary := strings.Join(parts, " · ")
	if p.Note != "" {
		t.log(ts, by, p.Note)
		if summary == "" {
			summary = i18n.Sf("备注 %s", util.FirstLine(p.Note))
		}
	} else {
		t.log(ts, by, summary)
	}
	return summary
}

// checkPatchLocked is the v2 P4-a write gate: it parses the patch's six
// text keys, enforces the leaf-only progress rule (B3: a task with
// children never takes a direct progress/pct write — non-leaf progress
// is the children's aggregate, derived, never stored) and runs the
// whole-set CheckStructure over the prospective ledger (start≤end, deps
// resolvable/acyclic, project attachments known). The returned sched
// feeds applyPatch; a non-empty reason denies with nothing mutated.
// Callers must hold ch.mu.
func (e *Engine) checkPatchLocked(ch *shard, t *Task, p Patch) (schedPatch, string) {
	sched, err := parseSchedKeys(p)
	if err != nil {
		return sched, err.Error()
	}
	if (p.Pct != "" || p.Progress != "") && ch.hasChildrenLocked(t.ID) {
		return sched, i18n.S("非叶任务不可直写进度（进度只可落叶，非叶进度由子任务聚合派生）")
	}
	cand := tenFieldProjection(t)
	sched.applyTo(&cand)
	if err := CheckStructure(ch.structureSetLocked(t, cand), e.projectKeysList()); err != nil {
		return sched, err.Error()
	}
	return sched, ""
}

// hasChildrenLocked reports whether any task in the shard parents on
// id — the non-leaf test for the leaf-only progress rule. Ids are
// per-project (v2.10), so the scan never crosses shelves. Callers
// hold ch.mu.
func (ch *shard) hasChildrenLocked(id string) bool {
	for _, t := range ch.tasks {
		if t.Parent == id && SlotKey(t.ProjectKey) == ch.key {
			return true
		}
	}
	return false
}

// tenFieldProjection copies just the structural fields CheckStructure
// reads — building a whole-ledger projection per write stays cheap
// (no logs, no prose ride along). Callers hold e.mu.
func tenFieldProjection(t *Task) Task {
	return Task{ID: t.ID, ProjectKey: t.ProjectKey, Parent: t.Parent,
		StartTS: t.StartTS, EndTS: t.EndTS, ProgressPct: t.ProgressPct, Deps: t.Deps}
}

// structureSetLocked projects this shard's ledger for CheckStructure —
// ids are per-project since v2.10, so structure validation must never
// mix shelves: two projects' t_01 are strangers, not duplicates. With
// t (when non-nil and found) replaced by cand — the prospective state
// a patch would produce. Callers hold ch.mu.
func (ch *shard) structureSetLocked(t *Task, cand Task) []Task {
	out := make([]Task, 0, len(ch.tasks)+1)
	for _, tt := range ch.tasks {
		if SlotKey(tt.ProjectKey) != ch.key {
			continue // in transit: not this shelf's to validate
		}
		if tt == t {
			out = append(out, cand)
			continue
		}
		out = append(out, tenFieldProjection(tt))
	}
	return out
}

// firstLine clips a note to its first line for one-line summaries.
func taskCreatedNote(assignee string) string {
	if assignee == "" {
		return i18n.S("创建任务（任务池）")
	}
	return i18n.Sf("创建任务，指派给 %s", assignee)
}

func (t *Task) log(ts int64, by, note string) {
	if note == "" {
		return
	}
	t.Log = append(t.Log, LogEntry{TS: ts, By: by, Note: clampRunes(note, MaxNote)})
	if over := len(t.Log) - MaxLog; over > 0 {
		t.Log = append([]LogEntry(nil), t.Log[over:]...)
	}
}

func patchSummary(p Patch) string {
	var parts []string
	if p.Status != "" {
		parts = append(parts, i18n.Sf("状态→%s", statusLabel(p.Status)))
	}
	if p.Progress != "" {
		parts = append(parts, i18n.Sf("进度 %s", p.Progress))
	}
	if p.Assignee != "" {
		parts = append(parts, i18n.Sf("负责人→%s", p.Assignee))
	}
	if p.Note != "" {
		parts = append(parts, i18n.Sf("备注 %s", p.Note))
	}
	// the six scheduling keys ride the same one-liner (raw input text —
	// the applied log line carries the formatted summary).
	if v := strings.TrimSpace(p.Project); v != "" {
		if v == "-" {
			parts = append(parts, i18n.S("挂靠清除"))
		} else {
			parts = append(parts, i18n.Sf("挂靠→%s", v))
		}
	}
	if v := strings.TrimSpace(p.Version); v != "" {
		if v == "-" {
			parts = append(parts, i18n.S("版本清除"))
		} else {
			parts = append(parts, i18n.Sf("版本→%s", v))
		}
	}
	if v := strings.TrimSpace(p.Start); v != "" {
		parts = append(parts, "start "+v)
	}
	if v := strings.TrimSpace(p.End); v != "" {
		parts = append(parts, "end "+v)
	}
	if v := strings.TrimSpace(p.Deps); v != "" {
		if v == "-" {
			parts = append(parts, i18n.S("依赖清空"))
		} else {
			parts = append(parts, i18n.Sf("依赖→%s", v))
		}
	}
	if v := strings.TrimSpace(p.Milestone); v != "" {
		parts = append(parts, i18n.Sf("里程碑 %s", v))
	}
	if v := strings.TrimSpace(p.Pct); v != "" {
		parts = append(parts, "pct "+v+"%")
	}
	if v := strings.TrimSpace(p.Priority); v != "" {
		if v == "-" {
			parts = append(parts, i18n.S("优先级清除"))
		} else {
			parts = append(parts, i18n.Sf("优先级→%s", PriorityLabel(v)))
		}
	}
	return strings.Join(parts, " · ")
}

// remaining lists Need members who have not confirmed yet.
func remaining(p *Proposal) []string {
	var out []string
	for _, n := range p.Need {
		if !util.Contains(p.Got, n) {
			out = append(out, n)
		}
	}
	return out
}

func validStatus(s string) bool {
	switch s {
	case StatusTodo, StatusDoing, StatusDone, StatusCancelled:
		return true
	}
	return false
}

// statusLabel maps a status to its Chinese board/CLI label.
func statusLabel(s string) string {
	switch s {
	case StatusDoing:
		return i18n.S("进行中")
	case StatusDone:
		return i18n.S("已完成")
	case StatusCancelled:
		return i18n.S("已取消")
	default:
		return i18n.S("待处理")
	}
}

// StatusLabel exposes the Chinese label for CLI/UI rendering.
func StatusLabel(s string) string { return statusLabel(s) }

// RankLabel renders a rank for board rows: 房主 for the host, else Lv.N.
func RankLabel(name string, rank int) string {
	if rank == RankHost {
		return i18n.S("房主")
	}
	return fmt.Sprintf("Lv.%d", rank)
}

func sanitizeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		out = append(out, r)
	}
	s := strings.TrimSpace(string(out))
	if r := []rune(s); len(r) > maxNameLen {
		s = string(r[:maxNameLen])
	}
	return s
}

func sanitizePatch(p Patch) Patch {
	p.Status = strings.TrimSpace(p.Status)
	p.Progress = clampRunes(strings.TrimSpace(p.Progress), MaxProgress)
	p.Note = clampRunes(strings.TrimSpace(p.Note), MaxNote)
	p.Assignee = strings.TrimSpace(p.Assignee)
	// the six scheduling keys: trim only — patch.go validates (or the
	// write path denies); version's length clamp happens at parse.
	p.Project = strings.TrimSpace(p.Project)
	p.Version = strings.TrimSpace(p.Version)
	p.Start = strings.TrimSpace(p.Start)
	p.End = strings.TrimSpace(p.End)
	p.Deps = strings.TrimSpace(p.Deps)
	p.Milestone = strings.TrimSpace(p.Milestone)
	p.Pct = strings.TrimSpace(p.Pct)
	p.Priority = strings.TrimSpace(p.Priority)
	return p
}

func clampRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}

func copyTask(t *Task) Task {
	out := Task{ID: t.ID, Title: t.Title, Desc: t.Desc, Assignee: t.Assignee,
		Status: t.Status, Progress: t.Progress, CreatedBy: t.CreatedBy,
		CreatedTS: t.CreatedTS, UpdatedTS: t.UpdatedTS}
	out.ProjectKey, out.Parent, out.Req = t.ProjectKey, t.Parent, t.Req
	out.Version, out.PlanID = t.Version, t.PlanID
	out.StartTS, out.EndTS = t.StartTS, t.EndTS
	out.Milestone, out.ProgressPct = t.Milestone, t.ProgressPct
	out.Priority = t.Priority
	// 工作钟随副本走（试用 B 抓出的漏拷：DoingTS/DoneTS 不进副本，
	// 所有读面（Get/List/Outcome.Task）都回 0，甘特实际跨度只能退回
	// 刮日志——结构化落点的意义就没了）。
	out.DoingTS, out.DoneTS = t.DoingTS, t.DoneTS
	if len(t.Deps) > 0 {
		out.Deps = append([]string(nil), t.Deps...)
	}
	if len(t.Log) > 0 {
		out.Log = append([]LogEntry(nil), t.Log...)
	}
	if t.Pending != nil {
		p := *t.Pending
		p.Need = append([]string(nil), t.Pending.Need...)
		p.Got = append([]string(nil), t.Pending.Got...)
		out.Pending = &p
	}
	return out
}

// saveShardAfter persists the touched shelf AFTER its lock is released
// (snapshot under the lock, atomic save outside it — the fsync that
// used to run inside one engine-wide critical section is the IO this
// split evicted; the snapshot is deep-copied, so a concurrent mutation
// can never tear a persisted document). saveMu orders concurrent
// writers per shelf; the generation check drops a slower writer whose
// snapshot a newer one has already superseded. Each shelf's envelope
// carries that project's id high-water, so a deleted max-number task
// never gets its number re-issued after a restart. Persistence
// failures are logged but do not abort the in-memory mutation (the
// A-族 discipline, phase1-store-semantics.md §1.6 — saved stays
// behind, so the next successful save catches the durable side up).
func (e *Engine) saveShardAfter(ch *shard) {
	ch.mu.Lock()
	gen := ch.gen
	doc := ch.shelfDocLocked()
	ch.mu.Unlock()
	ch.saveMu.Lock()
	defer ch.saveMu.Unlock()
	if gen <= ch.saved {
		return
	}
	if err := e.store.SaveShelf(e.subject, ch.key, doc); err != nil {
		log.Printf("save tasks shelf (%s): %v", ch.key, err)
		return
	}
	ch.saved = gen
}

// shelfDocLocked deep-copies the shard's document — the copy is what
// leaves the critical section. Callers hold ch.mu.
func (ch *shard) shelfDocLocked() *Shelf {
	f := &Shelf{Next: ch.next}
	if f.Next < 1 {
		f.Next = 1
	}
	if len(ch.tasks) > 0 {
		f.Tasks = make([]*Task, 0, len(ch.tasks))
		for _, t := range ch.tasks {
			tc := copyTask(t)
			f.Tasks = append(f.Tasks, &tc)
		}
	}
	return f
}

// saveRanksAfter is saveShardAfter's twin for the rank registry:
// snapshot under rankMu, atomic save outside it, generation-checked
// (persist（saveShardAfter 同款原子写）：truncate-write 写中途崩溃
// 撕出的半截 JSON 会让下次开架拒开——引擎连同调度器起不来).
func (e *Engine) saveRanksAfter() {
	e.rankMu.Lock()
	gen := e.rankGen
	snap := make(map[string]int, len(e.ranks))
	for k, v := range e.ranks {
		snap[k] = v
	}
	e.rankMu.Unlock()
	e.rankSaveMu.Lock()
	defer e.rankSaveMu.Unlock()
	if gen <= e.rankSaved {
		return
	}
	if err := e.store.SaveRanks(e.subject, snap); err != nil {
		log.Printf("save ranks: %v", err)
		return
	}
	e.rankSaved = gen
}
