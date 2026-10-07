package dispatch

// Fleet is the v2 P3-b per-project dispatcher set: ONE Dispatcher per
// active project room, each wired to its own Hub (routing isolation is
// structural — a room's OnSay feed only ever reaches that room's
// dispatcher, whose registry is that project's staffing rows: A-C1 by
// construction, not routing discipline). Birth workspaces come from
// the projects store — the sole source. The Fleet is also the routing
// layer for everything that used to talk to the one dispatcher: the
// HTTP face (an optional project field, absent = the lobby, so every
// legacy caller keeps its exact behavior) and recall (by staffing
// occupancy).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// Fleet owns the per-project dispatchers. The zero value is not
// usable — StartFleet builds it.
type Fleet struct {
	mu     sync.Mutex
	bridge Bridge
	pool   *BridgePool
	agents *agents.Store
	projs  *projects.Store
	staff  *staffing.Store
	base   Config
	byKey  map[string]*Dispatcher

	// indexWS is the workspace every project's sidebar row registers
	// under — the LOBBY project's frozen workspace (the office's home
	// folder, boot-stamped in the store), not the raw process cwd: a
	// bundle double-click starts with cwd=/, and the desktop list only
	// shows rows whose workspace_key matches an open workspace.
	indexWS string

	// The boot flagship recruiter's state (recruit.go): started once a
	// bridge attaches, progress snapshotted for the splash's gate.
	// recruitProg's zero-safe default is Done=true — a fleet that never
	// recruits (no docs face, inert) never gates the door. recruitWG
	// tracks the loop so Stop can close the door AND wait for the last
	// round to drain (a bare goroutine would race whatever the test
	// teardown writes next — the recruitTick swap's实录). recruitRunning
	// is the liveness flag the loop clears on exit — a settled loop may
	// be re-armed by a later project opening (KickProjectRecruit).
	recruitStop     chan struct{}
	recruitStopOnce sync.Once
	recruitWG       sync.WaitGroup
	recruitRunning  bool
	recruitProg     RecruitProgress

	// contPending holds restart-caught mid-turn names for rooms whose
	// dispatcher hasn't started yet (重启自动接续): ContinueInterrupted
	// stashes here when the room has no dispatcher to carry the poke, and
	// start() drains its key's stash the moment one comes up — whichever
	// order the boot walk and the snapshot restore land in.
	contMu      sync.Mutex
	contPending map[string][]string
}

// StartFleet builds one dispatcher per ACTIVE project with a room in
// rooms (the registry's instantiated hubs; main passes
// registry.Rooms()). base is the shared template — Model/Reasoning/
// Perm/InboxDir/capability wiring — each instance gets ProjectKey,
// the project's Workspace and a project-scoped seat-packs hook (the
// staffing row's Packs, CAP-M2's Q18 override) on top. A nil bridge
// yields an inert fleet (no dispatchers, refuses HTTP writes) — the
// room runs on, exactly like --no-dispatch.
func StartFleet(bridge Bridge, agentStore *agents.Store, projStore *projects.Store, staffStore *staffing.Store, rooms map[string]*chat.Hub, base Config) *Fleet {
	f := &Fleet{
		agents: agentStore, projs: projStore, staff: staffStore,
		base: base, byKey: map[string]*Dispatcher{},
		indexWS:     base.Workspace,
		recruitStop: make(chan struct{}),
		recruitProg: RecruitProgress{Done: true}, // ungated until a kick says otherwise
		contPending: map[string][]string{},
	}
	// The sidebar row's home prefers the lobby project's frozen
	// workspace — the office's own folder wherever the process was
	// started from.
	if p, ok := projStore.Get(chat.LobbyKey); ok && p.Workspace != "" {
		f.indexWS = p.Workspace
	}
	// 分支隔离（v2.7.1）的工作树根：~/.niuma/wt（projects.RootDir 家
	// 规）。解析失败留空——隔离座位在 birth 时如实报错，不静默回主树。
	if wtRoot, err := projects.RootDir(); err == nil {
		f.base.WorktreeRoot = filepath.Join(wtRoot, "wt")
	}
	if bridge == nil || projStore == nil {
		return f
	}
	// one app-server child, N dispatchers: the shared bridge fans
	// hooks/reverse requests out instead of last-writer-wins.
	bridge = newSharedBridge(bridge)
	f.bridge = bridge
	for _, p := range projStore.List() {
		if p.Status != projects.StatusActive {
			continue // draft never opened, archived frozen: no room, no dispatcher
		}
		hub, ok := rooms[p.Key]
		if !ok {
			continue
		}
		f.start(p, hub)
	}
	return f
}

// Attach wires a bridge that appeared AFTER boot (the boot gate's
// install-while-waiting recovery: ZCode missing at start, installed
// while the splash door waits) and runs StartFleet's boot walk for
// every active project. One-shot: a fleet that already drives a
// bridge ignores the call, and so does the inert fleet StartFleet
// returns without a project store — the /dispatch face stays mounted
// either way (an empty fleet just refuses writes until Attach fills
// it).
func (f *Fleet) Attach(bridge Bridge, rooms map[string]*chat.Hub) bool {
	if bridge == nil || f.projs == nil {
		return false
	}
	f.mu.Lock()
	if f.bridge != nil {
		f.mu.Unlock()
		return false
	}
	// one app-server child, N dispatchers: the shared bridge fans
	// hooks/reverse requests out instead of last-writer-wins.
	f.bridge = newSharedBridge(bridge)
	f.mu.Unlock()
	for _, p := range f.projs.List() {
		if p.Status != projects.StatusActive {
			continue // draft never opened, archived frozen: no room, no dispatcher
		}
		if hub, ok := rooms[p.Key]; ok {
			f.start(p, hub)
		}
	}
	return true
}

// AttachPool is Attach's bridge-pool face (v3 SPOF 根治第一段): the
// pool's members own spawn/health/respawn, and every project room is
// routed to ONE sticky member (start() asks routeBridge) — a member's
// crash blinks only the rooms it carries. f.bridge (the assistant's
// driver and the HasBridge flag) pins the lobby's member, preserving
// the single-child semantics for every pool size. One-shot like
// Attach.
func (f *Fleet) AttachPool(p *BridgePool, rooms map[string]*chat.Hub) bool {
	if p == nil || f.projs == nil {
		return false
	}
	f.mu.Lock()
	if f.bridge != nil || f.pool != nil {
		f.mu.Unlock()
		return false
	}
	f.pool = p
	sb := p.Route(chat.LobbyKey)
	if sb == nil {
		f.pool = nil
		f.mu.Unlock()
		return false
	}
	f.bridge = sb
	f.mu.Unlock()
	for _, pr := range f.projs.List() {
		if pr.Status != projects.StatusActive {
			continue
		}
		if hub, ok := rooms[pr.Key]; ok {
			f.start(pr, hub)
		}
	}
	return true
}

// routeBridge is start()'s per-project bridge: the pool's sticky
// assignment when one is attached, the single shared bridge otherwise.
// Callers hold f.mu.
func (f *Fleet) routeBridge(projectKey string) Bridge {
	if f.pool != nil {
		if sb := f.pool.Route(projectKey); sb != nil {
			return sb
		}
	}
	return f.bridge
}

// ReplaceBridge swaps the fleet's DEAD bridge for a live re-spawn (the
// legacy single-child face — with a pool attached, the swap lands on
// the dead member and the pool keeps owning respawn). Every dispatcher
// keeps its sharedBridge pointer — the swap re-points the fan-out at
// the new child and reinstalls the wire hooks on it; cold member
// sessions re-resume through the dispatchers' existing -32004 path.
// Returns false when there is no bridge to replace (use Attach) or the
// fleet is inert.
func (f *Fleet) ReplaceBridge(nb Bridge) bool {
	if nb == nil {
		return false
	}
	f.mu.Lock()
	pool, bridge := f.pool, f.bridge
	f.mu.Unlock()
	if pool != nil {
		return pool.SwapDead(nb)
	}
	sb, ok := bridge.(*sharedBridge)
	if !ok {
		return false
	}
	sb.swap(nb)
	return true
}

// start wires one project's dispatcher (idempotent).
func (f *Fleet) start(p projects.Project, hub *chat.Hub) *Dispatcher {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.byKey[p.Key]; ok {
		return d
	}
	cfg := f.base
	cfg.ProjectKey = p.Key
	if p.Workspace != "" {
		cfg.Workspace = p.Workspace // the project store is the sole source
	}
	// The sidebar's naming (v2 P7): the title's 【项目】 slot drinks from
	// the project store, and the row itself lives under the OFFICE home
	// workspace — the desktop's sidebar only lists rows whose
	// workspace_key matches a workspace the app has open, so project
	// employees register where the operator actually watches the office
	// (the lobby's own folder), not under their birth workspace.
	cfg.ProjectName = p.Name
	if cfg.ProjectName == "" {
		cfg.ProjectName = p.Key
	}
	cfg.IndexWorkspace = f.indexWS
	cfg.StaffStore = f.staff
	// 房间暂停的活读探针：staffing 设置里的 Paused 位（SetPaused 写，
	// POST /p/{key}/pause 的写面）——翻转即时生效，无需重启调度器。
	if cfg.PauseProbe == nil && f.staff != nil {
		key := p.Key
		cfg.PauseProbe = func() bool { return f.staff.SettingsOf(key).Paused }
	}
	// git 总开关的活读探针（两级门顶层）：项目注册表的 GitDisabled 位
	//（SetGitSettings 写，POST /p/{key}/git/settings 写面）——出生建树
	// 用它把关分支隔离座位，翻转即时生效。
	if cfg.GitOffProbe == nil && f.projs != nil {
		key := p.Key
		cfg.GitOffProbe = func() bool {
			cp, ok := f.projs.Get(key)
			return ok && cp.GitDisabled
		}
	}
	// 自动驻分支的活读探针（主动档）：GitPlan.AutoSeat 位＋基线——座位
	// 空的成员出生时自动各驻 wt/<人名>（总开关关着视为关）。
	if cfg.AutoSeatProbe == nil && f.projs != nil {
		key := p.Key
		cfg.AutoSeatProbe = func() (bool, string) {
			cp, ok := f.projs.Get(key)
			if !ok || cp.GitDisabled || cp.GitPlan == nil || !cp.GitPlan.AutoSeat {
				return false, ""
			}
			return true, cp.GitPlan.BaseOrDefault()
		}
	}
	if cfg.StaffingAssembly == nil && f.staff != nil {
		key := p.Key
		cfg.StaffingAssembly = func(person string) ([]string, []string) {
			if row, ok := f.staff.Get(key, person); ok {
				return row.Skills, row.MCPs
			}
			return nil, nil
		}
	}
	if cfg.StaffingModel == nil && f.staff != nil {
		key := p.Key
		cfg.StaffingModel = func(person string) (string, string) {
			if row, ok := f.staff.Get(key, person); ok {
				return row.Model, row.Reasoning
			}
			return "", ""
		}
	}
	d := Start(hub, f.agents, f.routeBridge(p.Key), cfg)
	f.byKey[p.Key] = d
	// v2.9 项目隔离的会话锚：本房工作区（含无分支成员的共享树）记进
	// 工作区→项目绑定表，存量成员的专属工作树一并扫绑——成员会话的
	// CLI 从 cwd 祖先链查表定缺省项目范围。失败只影响读面缺省，照常
	// 起房（绑定下一次启动还会再试）。
	f.bindWorkspace(p.Key, cfg)
	// 重启自动接续的迟到消费：这间房的调度器现在才起（快照恢复先于
	// Attach、或房是运行中才开的），中断名单可能还压在桩上——此刻
	// 点名。一次性取走：Retire/重开不再吃冷饭。
	f.contMu.Lock()
	names := f.contPending[p.Key]
	delete(f.contPending, p.Key)
	f.contMu.Unlock()
	if len(names) > 0 {
		d.ContinueInterruptedNotes(names, nil) // 迟到消费不带 notes：冷启动兜底走 ContinueText 旧路
	}
	// Catch employees born before the sidebar registration existed —
	// idempotent, so every boot just re-affirms the rows.
	d.BackfillTaskIndex()
	return d
}

// bindWorkspace is start()'s isolation anchor (v2.9): the project
// workspace joins the workspace→project table (the session CLIs'
// scope default), and every branched member's EXISTING worktree is
// swept in too — trees born before the table existed keep their
// binding across restarts without waiting for a rebirth. Best-effort
// by design: a failed bind costs a scoped-read default, never a room.
func (f *Fleet) bindWorkspace(key string, cfg Config) {
	if cfg.Workspace == "" {
		return
	}
	if err := util.BindWorkspaceProject(cfg.Workspace, key); err != nil {
		log.Printf("[调度] 项目 %s 的工作区绑定失败（只影响 CLI 读面缺省）：%v", key, err)
	}
	if cfg.WorktreeRoot == "" || f.staff == nil {
		return
	}
	for _, row := range f.staff.ListByProject(key) {
		if row.Branch == "" {
			continue
		}
		wt := filepath.Join(cfg.WorktreeRoot, key, row.Person)
		if st, err := os.Stat(wt); err != nil || !st.IsDir() {
			continue
		}
		if err := util.BindWorkspaceProject(wt, key); err != nil {
			log.Printf("[调度] %s 的工作树 %s 绑定失败（只影响 CLI 读面缺省）：%v", row.Person, wt, err)
		}
	}
}

// SetSendWait retunes every dispatcher's injection-ack wait — the
// settings card's 「消息注入等待」 write face (POST /dispatch-wait
// lands here through main's hook). Live dispatchers flip in place (the
// next deliver reads the new value), and the fleet's base config
// carries it onto dispatchers born later (Attach after a --no-bridge
// boot, EnsureRoom for rooms activated after the tune).
func (f *Fleet) SetSendWait(dt time.Duration) {
	if dt <= 0 {
		dt = DefaultSendWait
	}
	f.mu.Lock()
	f.base.SendWait = dt
	live := make([]*Dispatcher, 0, len(f.byKey))
	for _, d := range f.byKey {
		live = append(live, d)
	}
	f.mu.Unlock()
	for _, d := range live {
		d.SetSendWait(dt)
	}
}

// noDispatcher is the routing layer's one miss line — 项目 %s 无调度器
// （<what>），what 点名这次丢了什么（公告/入库通知/巡报……），房间的
// 环境注记因此说得清损失。二十来个路由方法曾各自手拼这一句（一半
// fmt 一半 i18n，拼写随手）；现在共用一条，i18n 也只此一键。
func noDispatcher(project, what string) error {
	return errors.New(i18n.Sf("项目 %s 无调度器（%s）", project, i18n.S(what)))
}

// Get returns the project's dispatcher (nil when the project has no
// live dispatcher — unknown, draft, archived or dispatch disabled).
func (f *Fleet) Get(key string) *Dispatcher {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byKey[key]
}

// FlushProjectInbox drains the project's dispatcher's pending inbox
// writes (nil-safe — an absent dispatcher has nothing pending). The
// pre-wipe leg for WipeProjectLanes: a racing flusher write would
// resurrect the key the wipe just deleted.
func (f *Fleet) FlushProjectInbox(project string) {
	if d := f.Get(project); d != nil {
		d.FlushInbox()
	}
}

// EnsureRoom starts (or returns) the project's dispatcher for a room
// that appeared after boot — the activate/reactivate flow's fleet half
// (StartFleet's walk only covers the rooms that existed then). Same
// rules as the walk: unknown and non-active projects refuse, start is
// idempotent. A nil bridge (dispatch off) refuses like Get's nil.
func (f *Fleet) EnsureRoom(key string, hub *chat.Hub) (*Dispatcher, error) {
	if f.bridge == nil || f.projs == nil {
		return nil, fmt.Errorf("调度未启用")
	}
	if hub == nil {
		return nil, fmt.Errorf("项目 %s 无房间", key)
	}
	p, ok := f.projs.Get(key)
	if !ok {
		return nil, fmt.Errorf("项目不存在: %s", key)
	}
	if p.Status != projects.StatusActive {
		return nil, fmt.Errorf("项目 %s 未开张（%s）——无调度器", key, p.Status)
	}
	d := f.start(p, hub)
	// v2.5 分项目编制：房一开、调度器一上，本项目的系统岗（编排者/HR
	// 各一套）就开始自动招聘——已收摊的招聘循环为此重新点火（splash
	// 的门不受影响：大厅半场瞬时汇报）。
	f.KickProjectRecruit()
	return d, nil
}

// ContinueInterrupted routes the restart's interrupted seats to their
// rooms' dispatchers (重启自动接续): the snapshot's mid-turn members get
// their continue poke WITHOUT waiting for the owner to click the card —
// empty-lane members receive one ContinueText injection, parked-lane
// members ride their own restored lines. A room whose dispatcher isn't
// up yet (bridge late, room opened after boot) keeps its names stashed;
// start() drains them when its dispatcher comes online.
func (f *Fleet) ContinueInterrupted(seats []chat.InterruptedSeat) {
	if f == nil {
		return
	}
	byRoom := map[string][]string{}
	byRoomNotes := map[string]map[string]string{} // room → name → contextNotes (r_20)
	for _, s := range seats {
		key := s.Project
		if key == "" {
			key = chat.LobbyKey // the lobby seats carry no project tag
		}
		byRoom[key] = append(byRoom[key], s.Name)
		if s.ContextNotes != "" {
			if byRoomNotes[key] == nil {
				byRoomNotes[key] = map[string]string{}
			}
			byRoomNotes[key][s.Name] = s.ContextNotes
		}
	}
	for key, names := range byRoom {
		if d := f.Get(key); d != nil {
			d.ContinueInterruptedNotes(names, byRoomNotes[key])
			continue
		}
		f.contMu.Lock()
		f.contPending[key] = append(f.contPending[key], names...)
		// notes 不随 pending 缓存：调度器晚起时的兜底走 ContinueText
		// 旧路（要点只在即时接续路径上——快照还在盘上，可再触发）
		f.contMu.Unlock()
	}
}

// Retire stops and drops the project's dispatcher — the archive flow's
// fleet half: an archived project keeps no patrol clock, no birth face,
// no recall route (Fleet.Recall falls through Get's nil to the lobby).
// Reports whether a dispatcher actually retired; the lobby is
// process-constant and never retires here.
func (f *Fleet) Retire(key string) bool {
	if key == chat.LobbyKey {
		return false
	}
	f.mu.Lock()
	d, ok := f.byKey[key]
	delete(f.byKey, key)
	f.mu.Unlock()
	if !ok {
		return false
	}
	d.Stop()
	return true
}

// Lobby returns the lobby's dispatcher — the legacy no-project
// default face (nil when dispatch is off).
func (f *Fleet) Lobby() *Dispatcher {
	return f.Get(chat.LobbyKey)
}

// ResumeRoom re-kicks one room's lanes after an un-pause (the pause
// write face's ResumeSink): parked lines flow again, the meeting
// agenda's held windows release. A room with no dispatcher (dispatch
// off, room never instantiated) is a quiet no-op — the gates read the
// staffing switch live, so the next dispatcher to come up is never
// stuck. Idempotent.
func (f *Fleet) ResumeRoom(key string) {
	if d := f.Get(key); d != nil {
		// 恢复即找回：暂停房没有看护的自愈腿（整房跳过），boot 时
		// attach 失败的编制成员此刻补座——「恢复前一切保持现状」的
		// 现状里，小人本来就该在。
		d.ReadoptMissing()
		d.Resume()
	}
}

// HasBridge reports whether the fleet drives a live zcode bridge
// (dispatch on) — the UI assistant's availability check: it rides the
// same app-server child, so no bridge means no assistant.
func (f *Fleet) HasBridge() bool { return f.bridge != nil }

// SessionDriver is the bridge surface a non-dispatcher session owner
// (the UI assistant) may use: the driving verbs only. The hook slots
// (SetHooks/SetReverse) are deliberately absent — they belong to the
// shared fan-out, and Observe is the event path for such callers.
type SessionDriver interface {
	CreateSession(ctx context.Context, workspace, mode string) (string, error)
	SetModel(ctx context.Context, sessionID string, sel zcode.ModelSelection) error
	ResumeSession(ctx context.Context, sessionID string) (*zcode.ResumeResult, error)
	Subscribe(ctx context.Context, sessionID string) (*zcode.SubscribeResult, error)
	Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error)
}

// Driver hands out the shared bridge as a SessionDriver (nil when
// dispatch is off) for callers that drive their OWN sessions on the
// same app-server child.
func (f *Fleet) Driver() SessionDriver { return f.bridge }

// Observe registers a non-dispatcher bridge consumer (the UI
// assistant): every session/state event fans out to it alongside the
// dispatchers, and reverse requests for sessions it owns are answered
// by it FIRST — ahead of the dispatcher fallback. No-op without a
// bridge (dispatch off).
func (f *Fleet) Observe(o *Observer) {
	f.mu.Lock()
	bridge := f.bridge
	f.mu.Unlock()
	if sb, ok := bridge.(*sharedBridge); ok {
		sb.addObserver(o)
	}
}

// RankChanged routes a rank write to the member's dispatcher so their
// ZCode sidebar title (Lv.N slot) refreshes at once — the occupancy
// row names the room, a member without one falls back to the lobby
// (the v1 shape's binding lives there). Main wires this to the
// server's rank tap; a miss is silent (the next boot's backfill
// re-registers everyone anyway).
func (f *Fleet) RankChanged(person string) {
	d := f.Lobby()
	if f.staff != nil {
		if row, ok := f.staff.OccupancyOf(person); ok {
			d = f.Get(row.ProjectKey)
		}
	}
	if d != nil {
		d.RefreshIndexTitle(person)
	}
}

// Archive is the project-archive flow's fleet half (归档即全离):
// every managed member's sidebar row folds FIRST — while the
// dispatcher still knows them — then the dispatcher retires. Reports
// whether a dispatcher actually retired.
func (f *Fleet) Archive(key string) bool {
	if d := f.Get(key); d != nil {
		d.ArchiveTaskIndexes()
	}
	return f.Retire(key)
}

// ArchiveTaskIndexes folds EVERY dispatcher's sidebar rows — the
// app-close half of the sidebar contract (开着＝置顶在岗，关了＝归档
// 收摊): the next boot's adopt re-registers each returning member, so
// no "running" ghost rows outlive the app.
func (f *Fleet) ArchiveTaskIndexes() {
	f.mu.Lock()
	ds := make([]*Dispatcher, 0, len(f.byKey))
	for _, d := range f.byKey {
		ds = append(ds, d)
	}
	f.mu.Unlock()
	for _, d := range ds {
		d.ArchiveTaskIndexes()
	}
}

// RevealIndexWorkspaces force-reveals every live dispatcher's index
// workspace, DEDUPED — the settings toggle's healing half (设置窗·连
// 接面板「ZCode 侧栏即时刷新」打开的那一刻): the rows are usually
// already pinned in the desktop's index (boot backfill re-pinned them
// seconds after the restart folded them), what's missing is only the
// nudge that makes the desktop re-read — one reveal per DISTINCT
// workspace, not per room (every room's rows live under the same host
// workspace, and N deep-link deliveries would pop N trust dialogs on
// ZCode ≥3.14 for one user action). Nil-safe like the fleet's other
// taps: a --no-dispatch boot simply has nothing to reveal.
func (f *Fleet) RevealIndexWorkspaces() {
	if f == nil {
		return
	}
	f.mu.Lock()
	set := make(map[string]bool, len(f.byKey))
	for _, d := range f.byKey {
		set[d.IndexWorkspace()] = true
	}
	f.mu.Unlock()
	for ws := range set {
		if ws != "" {
			taskIndexReveal(ws)
		}
	}
}

// Recall rebirths the member per their staffing occupancy (v2 P3-b):
// the occupying row names the project whose dispatcher handles it; a
// member with no staffing row falls back to the lobby dispatcher —
// the v1 single-room behavior, unchanged. Reports whether handled.
func (f *Fleet) Recall(person string) bool {
	d := f.Lobby()
	if f.staff != nil {
		if row, ok := f.staff.OccupancyOf(person); ok {
			d = f.Get(row.ProjectKey)
		}
	}
	if d == nil {
		return false
	}
	return d.Recall(person)
}

// Announce routes one room announcement (飞书式群公告) to the
// project's dispatcher: wake=true delivers an addressed line to every
// member now (their turn replies land back in the room), wake=false
// parks it in the background lane. An error = no dispatcher for the
// room (dispatch off, or an embed with only the lobby's inert fleet)
// — the server turns it into an ambient note, never a refusal.
func (f *Fleet) Announce(project, from, content string, wake bool) error {
	if d := f.Get(project); d != nil {
		d.Announce(from, content, wake)
		return nil
	}
	return noDispatcher(project, "公告未注入牛马会话")
}

// CancelRebuildVote routes the AI-rebuild-vote switch's OFF leg (the
// server's RebuildVoteCancel hook, main-wired like NoticeWake) to the
// project's dispatcher: a ballot still open in that room is voided
// with a recorded line. Nil fleet (--no-dispatch) or nil dispatcher
// (room not live) = nothing to void.
func (f *Fleet) CancelRebuildVote(project string) {
	if f == nil {
		return
	}
	if d := f.Get(project); d != nil {
		d.CancelVote()
	}
}

// PlanLanded routes the plan-accept wake to the project's dispatcher
// (the server's PlanLanded hook, main-wired like Announce): each
// managed assignee hears the tasks now theirs, the orchestrator hears
// the acceptance with the full landing map. An error = no dispatcher
// for the room — the server turns it into an ambient note, never a
// refusal; the tasks are already in the ledger.
func (f *Fleet) PlanLanded(project, planID, planTitle, submitter string, landed []*tasks.Task) error {
	if d := f.Get(project); d != nil {
		d.PlanLanded(planID, planTitle, submitter, landed)
		return nil
	}
	return noDispatcher(project, "入库通知未注入牛马会话")
}

// SeatAck routes the seq-less CLI ack (niuma ack): the member's
// one-shot seat dials the room, the server's SeatAck tap lands here,
// and the project's dispatcher converts the turn's armed cargo into
// receipts. False = the member owes nothing (or no dispatcher for the
// room) — the CLI answers "无可回执" and that is not an error.
func (f *Fleet) SeatAck(project, name string) bool {
	if d := f.Get(project); d != nil {
		return d.SeatAck(name)
	}
	return false
}

// EstablishmentAdded routes the row face's hiring-desk wake (the
// server's EstablishmentAdded hook, main-wired like PlanLanded): a row
// that lands in a PROJECT goes to that project's own seated HR first
// （v2.5 分项目编制——编排者/HR 每个项目各一套，自己的加编自己跟进）,
// falling back to the lobby's HR (人事) when the project has none
// seated yet — the studio-level desk the keeper's project alarms land
// at too. The 【加编通知】 itself names the scope the row landed in,
// so HR always hears it at its own desk. An error = no dispatcher or
// no seated HR anywhere — the server turns it into the room's ambient
// note, never a refusal; the row is already written.
func (f *Fleet) EstablishmentAdded(project string, row kb.EstablishmentRow, prev int, by string) error {
	if project != chat.LobbyKey {
		if d := f.Get(project); d != nil && d.EstablishmentAdded(project, row, prev, by) {
			return nil
		}
	}
	d := f.Lobby()
	if d == nil {
		return errors.New(i18n.S("Niuma_Studio 无调度器（加编提醒未注入 HR 会话）"))
	}
	if !d.EstablishmentAdded(project, row, prev, by) {
		return errors.New(i18n.Sf("项目 %s 与 Niuma_Studio 均无在岗 HR（人事）——加编提醒无人可送，请房主在对话里 @HR 或先补齐 HR 岗", project))
	}
	return nil
}

// SeatBranch routes the git seat face's write (the server's
// DispatcherSeat hook, main-wired like BranchSwitched) to the project's
// dispatcher — staffing 落账、树内换枝/跨树重生、背景告知都在那里一
// 手经办。An error = no dispatcher（调度未开/项目未开张）——the server
// turns it into the click's refusal; the staffing row stays untouched
// (落账在调度器里，失败即回滚).
func (f *Fleet) SeatBranch(project, person, branch string) error {
	if d := f.Get(project); d != nil {
		return d.SeatBranch(person, branch)
	}
	return noDispatcher(project, "座位分支迁移未执行")
}

// BranchSwitched routes the git face's switch tap (the server's
// GitBranchSwitched hook, main-wired like EstablishmentAdded) to the
// project's dispatcher — the workspace's files just changed under every
// member working there. The note rides the background lane (no one is
// woken for it). An error = no dispatcher or no managed members — the
// server logs it; the checkout itself already happened.
func (f *Fleet) BranchSwitched(project, from, to string) error {
	if d := f.Get(project); d != nil {
		if !d.BranchSwitched(from, to) {
			return fmt.Errorf("项目 %s 没有调度器管理的成员（切分支告知无人可送）", project)
		}
		return nil
	}
	return noDispatcher(project, "切分支告知未注入成员会话")
}

// MergeLanded routes the merge-accept tap (the server's MergeLanded
// hook, v2.8 gitflow) to the project's dispatcher — the branch's owner
// hears their work reached the mainline (background lane, no wake). An
// error = no dispatcher or no seat on that branch; the server logs it,
// the merge itself already stands.
func (f *Fleet) MergeLanded(project, branch, into string) error {
	if d := f.Get(project); d != nil {
		return d.MergeLanded(branch, into)
	}
	return noDispatcher(project, "合并落地告知未送达分支主人")
}

// WorkspaceUpdated routes the git face's pull tap (the server's
// GitUpdated hook, v2.14 远端同步) to the project's dispatcher — the
// shared workspace's files just fast-forwarded under every shared-tree
// member working there. The note rides the background lane (no one is
// woken for it). An error = no dispatcher or no managed members — the
// server logs it; the fast-forward itself already landed.
func (f *Fleet) WorkspaceUpdated(project, branch string, incoming int) error {
	if d := f.Get(project); d != nil {
		if !d.WorkspaceUpdated(branch, incoming) {
			return fmt.Errorf("项目 %s 没有调度器管理的成员（拉取更新告知无人可送）", project)
		}
		return nil
	}
	return noDispatcher(project, "拉取更新告知未注入成员会话")
}

// AutoPilotPoke routes the autopilot engine's low-water stocking wake
// (全智能模式, r_17: busy or idle, the pool is restocked below the
// target line) to the project's dispatcher — the EstablishmentAdded
// shape: the server decides, the fleet routes, the dispatcher delivers
// the frozen 【自动驾驶·选题】 injection to the seated orchestrator.
// An error = no dispatcher or no seated orchestrator; the engine turns
// it into the room's ambient note, never a refusal.
func (f *Fleet) AutoPilotPoke(project string, openN int) error {
	if d := f.Get(project); d != nil {
		return d.AutoPilotPoke(openN)
	}
	return noDispatcher(project, "选题注入未送达编排者")
}

// AutoPilotDivert routes the【自动驾驶·拆解】wake (r_13 t_175) to the
// project's dispatcher — the stalled-backlog divert.
func (f *Fleet) AutoPilotDivert(project string, openN int) error {
	if d := f.Get(project); d != nil {
		return d.AutoPilotDivert(project, d.orchestratorName(), openN)
	}
	return noDispatcher(project, "拆解注入未送达编排者")
}

// AutoPilotPatrol routes the【全智能·产能巡报】to the project's seated
// HR (r_13 t_175). An error = no dispatcher or no seated HR. The HR
// post is studio-level (the EstablishmentAdded doctrine: 人事 sits in
// the lobby), so a project room without its own HR seat falls back to
// the LOBBY's HR — the snapshot carries the project's name, the
// studio-level HR broadcasts it like any other capacity news (复查#4:
// the fallback-less shape silently starved every project patrol).
func (f *Fleet) AutoPilotPatrol(project string, context string) error {
	d := f.Get(project)
	if d == nil {
		return noDispatcher(project, "巡报未送达")
	}
	hr := d.hrName()
	if hr == "" && project != chat.LobbyKey {
		if lobby := f.Lobby(); lobby != nil {
			if lobbyHR := lobby.hrName(); lobbyHR != "" {
				return lobby.AutoPilotPatrol(project, lobbyHR, context)
			}
		}
	}
	if hr == "" {
		return fmt.Errorf("项目 %s 无在岗人事（项目房与 Niuma_Studio 均无 HR 岗）——巡报无人可送", project)
	}
	return d.AutoPilotPatrol(project, hr, context)
}

// AutoPilotResume routes the engine's stalled-task wake (全智能模式's
// resume gate) to the project's dispatcher — the AutoPilotPoke shape.
// An error = no dispatcher, or the assignee is not a managed member
// (the host / a 待招 seat); the engine turns it into the room's
// ambient note, never a refusal.
func (f *Fleet) AutoPilotResume(project, assignee string, stalled []*tasks.Task) error {
	if d := f.Get(project); d != nil {
		return d.AutoPilotResume(assignee, stalled)
	}
	return errors.New(i18n.Sf("项目 %s 无调度器（续命注入未送达 %s）", project, assignee))
}

// Whisper routes the owner's 私信 (manual v0.10 M4 占位落地) to the
// project's dispatcher lane — main wires Server.Options.WhisperSink
// here. An error = no dispatcher, or the name is not a managed member;
// the seatless say+to face turns it into the composer's refusal, never
// a ghost receipt.
func (f *Fleet) Whisper(project, member, text string) error {
	if d := f.Get(project); d != nil {
		return d.Whisper(member, text)
	}
	return errors.New(i18n.Sf("项目 %s 无调度器（私信未送达 %s）", project, member))
}

// AnswerQuestion routes the owner's question-card answer (the
// workbench's answer verb, main-wired like PlanLanded) to the
// project's dispatcher. false = no dispatcher for the room, or no
// such open question (already answered, expired, a stale card) — the
// server turns that into the click's refusal note instead of a fake
// success.
func (f *Fleet) AnswerQuestion(project, qid, by string, answers map[string]string) bool {
	if d := f.Get(project); d != nil {
		return d.Answer(qid, by, answers)
	}
	return false
}

// MeetingCtl routes the chair's meeting_end/meeting_extend verbs (the
// server's MeetingCtl hook, main-wired like AnswerQuestion) to the
// project's dispatcher — the room's single live agenda. The returned
// string is the receipt (what the agenda actually did); an error = no
// dispatcher, not the chair, no live meeting or a window mid-switch —
// the server turns it into the verb's private denial.
func (f *Fleet) MeetingCtl(project, actor string, end bool, minutes int) (string, error) {
	if d := f.Get(project); d != nil {
		return d.MeetingCtl(actor, end, minutes)
	}
	return "", noDispatcher(project, "会议控制未送达")
}

// Stop tears every dispatcher down (tests, graceful shutdown). The
// recruiter loop goes first — close the door, then WAIT the in-flight
// round out (a half-dead fleet must not keep hiring, and the loop must
// not race whatever runs after Stop) — then the dispatchers drain.
func (f *Fleet) Stop() {
	f.recruitStopOnce.Do(func() { close(f.recruitStop) })
	f.recruitWG.Wait()
	f.mu.Lock()
	ds := make([]*Dispatcher, 0, len(f.byKey))
	for _, d := range f.byKey {
		ds = append(ds, d)
	}
	f.mu.Unlock()
	for _, d := range ds {
		d.Stop()
	}
}

// resolve maps an optional project key to its dispatcher; "" means
// the lobby (the legacy default).
func (f *Fleet) resolve(project string) (*Dispatcher, error) {
	if project == "" {
		project = chat.LobbyKey
	}
	if d := f.Get(project); d != nil {
		return d, nil
	}
	return nil, noDispatcher(project, "不存在、未开张或调度未启用")
}

// HTTPHandler serves the fleet's management face (mounted by the room
// server under /dispatch/): the v1 four endpoints with one additive
// routing key — POST bodies may carry "project" (and GET
// /dispatch?project=), absent = the lobby, so every legacy caller
// (cli dispatch …) keeps its exact behavior. Loopback-only by the
// server's binding.
func (f *Fleet) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		switch {
		case path == "/dispatch":
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			d, err := f.resolve(r.URL.Query().Get("project"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(d.Snapshot())
		case path == "/dispatch/lanestats":
			// L6 度量闭环的读面（v2.11）：车道投递的三分布＋溢出计数
			// （?project= 缺省大厅，同 /dispatch 的路由键）。汇聚窗/同人
			// 合并/并车阶梯这些旋钮按这组数调，不按感觉调。
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			d, err := f.resolve(r.URL.Query().Get("project"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(d.LaneStats())
		case path == "/dispatch/context":
			// P1 上下文用量仪表的读面（v2.11）：全舰队在座成员的活体上
			// 下文尺寸——最新一轮的 input_tokens（整个提示面的体量）、
			// 缓存一半、净新量、会话回合龄与全史消耗。一次台账查询全
			// 员返回；这是周期性会话折叠（P0）阈值的数据源，也是房主
			// 的「谁在膨胀」面。
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.ContextGauges())
		case path == "/dispatch/birth":
			f.op(func(body map[string]string) (string, error) {
				d, err := f.resolve(body["project"])
				if err != nil {
					return "", err
				}
				// --post（岗位锚）：身份可按表反查、行落锚——HR 不再
				// 手抄身份串（分项目根治的出生面）。
				return d.BirthPost(body["name"], body["post"], body["role"], body["prompt"], body["model"], body["reasoning"])
			}).ServeHTTP(w, r)
		case path == "/dispatch/model":
			// The seat-model write face (招牛马后的改档): {project?, name,
			// model, reasoning?} — model "providerId/modelId" or a bare id;
			// either slot may fly solo: empty model + a reasoning re-voices
			// the default chain's model intensity (the ZCode picker's
			// default-model shape), emptying both clears the pick back to
			// the default chain; reasoning is the CLI's reasoningLevel
			// vocabulary, "" = the model's default tier.
			f.op(func(body map[string]string) (string, error) {
				d, err := f.resolve(body["project"])
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(body["name"]) == "" {
					return "", errors.New(i18n.S("name 必填"))
				}
				if err := d.SetSeatModel(body["name"], body["model"], body["reasoning"]); err != nil {
					return "", err
				}
				return "model set", nil
			}).ServeHTTP(w, r)
		case path == "/dispatch/models":
			// The pick list the birth/change UIs offer, mirroring the
			// desktop ZCode picker's surface: account-plan faces first
			// (BigModel 团队 / Start Plan 免费 — composed from the CLI's
			// bundled builtin config, every visible face the desktop
			// registry keeps, undrivable ones annotated not dropped),
			// then every personal provider from
			// ~/.zcode/v2/provider_config.json (display name + models in
			// the operator's order), plus the process default the empty
			// pick falls back to. The levels side table carries each
			// model's thinking-intensity picks (the same bundled
			// modelRules the desktop picker reads), so the UIs offer
			// exactly what ZCode itself would. Served fleet-wide — the
			// providers are machine-global, not per-project.
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			f.mu.Lock()
			def, reasoning := f.base.Model, f.base.Reasoning
			f.mu.Unlock()
			providers := zcode.ListModelProviders()
			out := make([]map[string]any, 0, len(providers))
			levels := map[string][]string{}
			for _, p := range providers {
				for _, m := range p.Models {
					if _, ok := levels[m]; ok {
						continue
					}
					if lv := zcode.ReasoningLevels(m); len(lv) > 0 {
						levels[m] = lv
					}
				}
				out = append(out, map[string]any{
					"id": p.ProviderID, "name": p.ProviderName,
					"badge": p.Badge, "models": p.Models,
					"available": p.Available, "reason": p.Unavailable,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"default": def, "reasoning": reasoning, "providers": out,
				"levels": levels,
			})
		case path == "/dispatch/bind":
			f.op(func(body map[string]string) (string, error) {
				d, err := f.resolve(body["project"])
				if err != nil {
					return "", err
				}
				if err := d.Bind(body["name"], body["session"]); err != nil {
					return "", err
				}
				return "bound", nil
			}).ServeHTTP(w, r)
		case path == "/dispatch/steer":
			f.op(func(body map[string]string) (string, error) {
				d, err := f.resolve(body["project"])
				if err != nil {
					return "", err
				}
				if err := d.Steer(body["name"], body["text"]); err != nil {
					return "", err
				}
				return "steered", nil
			}).ServeHTTP(w, r)
		case path == "/dispatch/recall":
			// v2 P5-a: recall's HTTP face (was CLI-only). An explicit
			// project routes to that room's dispatcher; absent = the
			// staffing occupancy route (Fleet.Recall), so the thin CLI
			// and the app workbench share one path with the keeper's
			// auto-refill — dispatcher-managed rebirth, Birth's formula.
			f.op(func(body map[string]string) (string, error) {
				name := strings.TrimSpace(body["name"])
				if name == "" {
					return "", errors.New(i18n.S("name 必填"))
				}
				var ok bool
				if p := strings.TrimSpace(body["project"]); p != "" {
					d, err := f.resolve(p)
					if err != nil {
						return "", err
					}
					ok = d.Recall(name)
				} else {
					ok = f.Recall(name)
				}
				if !ok {
					return "", errors.New(i18n.Sf("召回 %s 失败（未建档或会话不可用，详见办公室系统消息）", name))
				}
				return "recalled " + name, nil
			}).ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// op adapts a body-keyed operation into a POST JSON handler (the
// Dispatcher.op shape, fleet-side).
func (f *Fleet) op(fn func(map[string]string) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			http.Error(w, i18n.Sf("载荷解析失败: %s", err), http.StatusBadRequest)
			return
		}
		out, err := fn(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(out))
	}
}
