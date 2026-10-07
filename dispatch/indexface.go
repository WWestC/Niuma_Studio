package dispatch

// indexface.go — 桌面侧栏行（任务索引）的登记/刷新/归档面。拆自
// dispatcher.go（v2.15 结构整理）。

import (
	"fmt"
	"log"
	"sync"

	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// taskIndexTitle is the sidebar label of a member's pinned entry:
// 【项目】牛马-岗位-Lv.N (v2 P7 — the room's own naming contract). The
// project slot falls back to the key, the role slot drops when empty,
// and the rank slot drops when the registry is unwired.
func taskIndexTitle(project, name, role string, rank int) string {
	if project == "" {
		project = chat.LobbyKey
	}
	out := "【" + project + "】" + name
	if role != "" {
		out += "-" + role
	}
	if rank > 0 {
		out += fmt.Sprintf("-Lv.%d", rank)
	}
	return out
}

// IndexWorkspace is the workspace the sidebar rows call home — the
// fleet's host workspace with the v1 fallback. Exported for the
// settings toggle's heal nudge (Fleet.RevealIndexWorkspaces): one
// force reveal per distinct workspace brings every already-pinned row
// back on screen at once.
func (d *Dispatcher) IndexWorkspace() string {
	if d.cfg.IndexWorkspace != "" {
		return d.cfg.IndexWorkspace
	}
	return d.cfg.Workspace
}

// rankOf reads the member's rank through the wired registry (0 = omit).
func (d *Dispatcher) rankOf(name string) int {
	if d.cfg.RankOf == nil {
		return 0
	}
	return d.cfg.RankOf(name)
}

// registerTaskIndex mirrors a member's session into the ZCode desktop
// task index (pinned) so a headless birth still shows in the app's
// left sidebar. The index is another app's database — best-effort by
// contract: a failure costs visibility, never the birth, so it logs
// and returns the error for the caller to decide on a notice. force
// additionally fires an UNTHROTTLED reveal nudge — the rank-change
// tap's shape: rare, user-initiated, must not ride inside (and lose
// to) a birth burst's shared 10s throttle window.

func (d *Dispatcher) registerTaskIndex(name, role, sessionID string, fabric *capability.EffectiveFabric, force bool) error {
	model := d.cfg.Model
	if fabric != nil && fabric.Model != nil {
		model = fabric.Model.ID
	}
	reg := d.registerIndex // test seam; nil = the real desktop upsert
	if reg == nil {
		reg = taskIndexRegister
	}
	if err := reg(zcode.TaskIndexEntry{
		WorkspacePath: d.IndexWorkspace(),
		SessionID:     sessionID,
		Title:         taskIndexTitle(d.cfg.ProjectName, name, role, d.rankOf(name)),
		Mode:          "yolo",
		Provider:      zcode.DesktopListProvider,
		Model:         model,
		Status:        "running",
	}); err != nil {
		log.Printf("[调度] %s 注册桌面任务索引失败（不影响出生）：%v", name, err)
		return err
	}
	if force && d.registerIndex == nil {
		// 强制揭示只在真实路径发——测试缝换掉了行落库，深链也一并不发
		//（开着桌面跑 go test 不该被连戳几次 zcode:// 链接）。
		taskIndexReveal(d.IndexWorkspace())
	}
	return nil
}

// registerTaskIndexAsync is registerTaskIndex off the birth critical
// path: the upsert rides a python3+sqlite3 spawn（百毫秒级），出生不必
// 排队等它。公告（announceTaskIndex）挪进后台任务——行真正落库后才播
// 报，比同步版更贴事实；揭示深链（revealWorkspace）也在落库后由
// zcode.RegisterTaskIndex 自己触发。
func (d *Dispatcher) registerTaskIndexAsync(name, role, sessionID string, fabric *capability.EffectiveFabric) {
	go util.Guard("dispatch: task index register", func() {
		if err := d.registerTaskIndex(name, role, sessionID, fabric, false); err != nil {
			return // registerTaskIndex 已记日志，出生不受影响
		}
		d.announceTaskIndex(name, role)
	})
}

// announceTaskIndex tells the room where the member's ZCode entry
// landed and how to flush the desktop's list cache — the one seam
// users otherwise trip over: rows written by the room do not make
// ZCode's in-memory timeline refetch on their own (search or any
// task-list touch does). Recorded, not live-only: the hint must
// still be there when someone opens the room later.
func (d *Dispatcher) announceTaskIndex(name, role string) {
	d.hub.SystemRecorded(i18n.Sf(
		"[调度] %s 的会话已注册进 ZCode 左侧任务列表并置顶（「%s」）；若 ZCode 未立刻显示，在侧栏搜索「%s」或新建任意任务即可刷新",
		name, taskIndexTitle(d.cfg.ProjectName, name, role, d.rankOf(name)), name))
}

// archiveTaskIndex folds the member's sidebar row away on offboard —
// and at app close (Fleet.ArchiveTaskIndexes). Force-fold by task_id:
// the desktop's timeline sync rewrites born rows (dropping the
// dhEmployee marker), so the guarded variant no-ops on rewritten rows
// and "running" ghosts would survive the close.
func (d *Dispatcher) archiveTaskIndex(name, sessionID string) {
	if sessionID == "" {
		return
	}
	fold := d.archiveIndex // test seam; nil = the real desktop fold
	if fold == nil {
		fold = taskIndexArchive
	}
	if err := fold(sessionID); err != nil {
		log.Printf("[调度] %s 归档桌面任务索引失败（不影响离编）：%v", name, err)
	}
}

// stampRowUnread dots the member's ZCode sidebar row — the reply half
// of the sidebar contract (birth pins the row; a reply keeps it loud):
// every line the room hears from a member's seat — turn reply, 收到
// receipt, ask, failure notice — leaves an unread mark, so an operator
// returning to the desktop sees WHO spoke without opening the web app.
// name and sessionID pass by value: the stamp rides its own goroutine
// (the python3 spawn is 百毫秒级 — registerTaskIndexAsync's lesson —
// and must never sit on the turn path), and the member struct belongs
// to the dispatcher's locked world. Best-effort by the register's
// contract: a failed stamp logs and costs a dot, never a reply.
func (d *Dispatcher) stampRowUnread(name, sessionID string) {
	if sessionID == "" {
		return
	}
	stamp := d.stampIndex // test seam; nil = the real desktop stamp
	// r_35 t_144：挂 wg（Stop 收拢——裸 goroutine 生命周期纪律补漏；
	// 取证实录：测试世界不触发本路径〔探针零 fire〕，预防性修复非四红因果）
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		util.Guard("dispatch: sidebar unread stamp", func() {
			if stamp == nil {
				stamp = taskIndexUnread
			}
			if err := stamp(sessionID); err != nil {
				log.Printf("[调度] %s 侧栏未读戳失败（不影响回合）：%v", name, err)
			}
		})
	}()
}

// RefreshIndexTitle re-registers one managed member's sidebar row —
// the rank-change tap: the title's Lv.N slot reads the registry live,
// a rank_set lands on the row at once instead of at the next boot's
// backfill. No-op for unmanaged names (offboarded, other rooms). The
// member's birth fabric rides along — a rank refresh must not clobber
// the row's model column back to the process default — and the reveal
// nudge is FORCED past the shared throttle (see registerTaskIndex).
func (d *Dispatcher) RefreshIndexTitle(name string) {
	m := d.lookup(name)
	if m == nil {
		return
	}
	d.mu.Lock()
	fabric := m.fab.fabric
	d.mu.Unlock()
	if err := d.registerTaskIndex(m.name, m.role, d.sessionOf(m), fabric, true); err != nil {
		log.Printf("[调度] %s 刷新侧栏标题失败：%v", name, err)
	}
}

// ArchiveTaskIndexes folds EVERY managed member's sidebar row away —
// the project-archive flow's half (归档即全离： no "running" ghosts of
// a frozen room stay pinned in the list). Individual departures keep
// the watchSeat path; this is the wholesale fold.
func (d *Dispatcher) ArchiveTaskIndexes() {
	d.mu.Lock()
	type row struct{ name, sid string }
	rows := make([]row, 0, len(d.members))
	for _, m := range d.members {
		rows = append(rows, row{m.name, m.sessionID})
	}
	d.mu.Unlock()
	for _, r := range rows {
		d.archiveTaskIndex(r.name, r.sid)
	}
}

// BackfillTaskIndex mirrors already-bound sessions into the desktop
// task index — the catch-up for employees born before the sidebar
// registration existed. Fleet.start calls it on every boot; the
// upsert is idempotent so repeat passes are free. Offboarded rows
// self-heal the other way: a lingering sidebar row gets archived.
// Registrations run with bounded concurrency: each upsert is its own
// python3 spawn, and a big team used to pay them strictly serially.
func (d *Dispatcher) BackfillTaskIndex() {
	type reg struct{ name, role, sid string }
	var regs []reg
	if d.staff != nil {
		for _, row := range d.staff.ListByProject(d.projectKey) {
			if row.SessionID == "" {
				continue
			}
			if !row.Occupying() {
				d.archiveTaskIndex(row.Person, row.SessionID)
				continue
			}
			role := row.ProjectRole
			if role == "" {
				if cfg, ok := d.store.Get(row.Person); ok {
					role = cfg.Role
				}
			}
			regs = append(regs, reg{row.Person, role, row.SessionID})
		}
	} else if d.store != nil {
		for _, cfg := range d.store.List() {
			if cfg.SessionID == "" {
				continue
			}
			if cfg.Archived {
				d.archiveTaskIndex(cfg.Name, cfg.SessionID)
				continue
			}
			regs = append(regs, reg{cfg.Name, cfg.Role, cfg.SessionID})
		}
	}
	// 4 路并发把串行墙钟砍掉大半（sqlite WAL + busy_timeout 在库里自然
	// 串写），又不至于一次拉起一支 python 大军。
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, r := range regs {
		wg.Add(1)
		sem <- struct{}{}
		go func(r reg) {
			defer wg.Done()
			defer func() { <-sem }()
			util.Guard("dispatch: task index backfill", func() {
				d.registerTaskIndex(r.name, r.role, r.sid, nil, false)
			})
		}(r)
	}
	wg.Wait()
}
