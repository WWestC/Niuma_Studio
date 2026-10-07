package zcode

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/WWestC/Niuma_Studio/util"
)

// DesktopListProvider is the provider value the ZCode desktop task
// list filters on — its own SQL carries the rule ("ZCode Agent 列表
// 只接受当前 glm provider"), so a row stamped with the personal
// provider UUID is filtered out of the sidebar even though it sits
// in the index. This is the DISPLAY column only; the session's real
// model calls keep using the personal provider from provider_config.
const DesktopListProvider = "glm"

// TaskIndexEntry is one sidebar registration: everything the ZCode
// desktop task list needs to show — and reopen — a session the room
// birthed headlessly.
type TaskIndexEntry struct {
	// WorkspacePath is the row's workspace_key — where the sidebar
	// LOOKS for it: the desktop list only shows rows whose key matches
	// a workspace the app has open, so the room registers employees
	// under the HOST workspace (the office's own folder), not their
	// birth workspace. The session's real cwd lives in the session
	// itself; reopening follows the task_id.
	WorkspacePath string
	SessionID     string // sess_... — the row's task_id
	Title         string
	Mode          string // plan|build|edit|yolo, as birthed
	Provider      string // "" leaves the column empty
	Model         string // "" leaves the column empty
	Status        string // sidebar status chip; employees stay running
}

// RegisterTaskIndex mirrors a headless-born session into the ZCode
// desktop task index (~/.zcode/v2/tasks-index.sqlite). That database
// belongs to the desktop shell — the app-server protocol exposes no
// write RPC for it — so the room writes its own row through the same
// python3+sqlite3 seam the recruit verifier rides: WAL-safe via
// busy_timeout, own-row by task_id (INSERT OR IGNORE plus an UPDATE
// matched on the session id — the desktop's timeline sync rewrites
// born rows and drops the dhEmployee marker, so a guarded UPDATE
// no-ops exactly when it must not; the task_id IS the room-driven
// session id, unique by construction, the ArchiveTaskIndexForce
// lesson). Registering an ALREADY-KNOWN session (the assistant's
// cross-boot adoption) therefore restores its row in place — pinned,
// un-archived — instead of growing a second one. Best-effort by
// contract: an error means "not visible in the sidebar", never a
// failed birth.
func RegisterTaskIndex(e TaskIndexEntry) error {
	if e.WorkspacePath == "" || e.SessionID == "" || e.Title == "" {
		return fmt.Errorf("zcode: task index entry needs workspace, session and title")
	}
	if e.Mode == "" {
		e.Mode = "yolo"
	}
	if e.Status == "" {
		e.Status = "running"
	}
	dbPath, err := HomePathV2("tasks-index.sqlite")
	if err != nil {
		return fmt.Errorf("zcode: task index path: %w", err)
	}
	if err := registerTaskIndexAt(dbPath, e); err != nil {
		return err
	}
	go revealWorkspace(e.WorkspacePath)
	return nil
}

// registerTaskIndexAt is RegisterTaskIndex over an explicit db (tests).
func registerTaskIndexAt(dbPath string, e TaskIndexEntry) error {
	db, err := openStoreDB(dbPath)
	if err != nil {
		return fmt.Errorf("zcode: task index upsert: %w", err)
	}
	defer db.Close()
	now := time.Now().UnixMilli()
	meta, err := json.Marshal(map[string]any{
		"taskId": e.SessionID, "dhEmployee": true, "title": e.Title,
		"titleOverridden": true, "workspacePath": e.WorkspacePath,
		"createdAt": now, "updatedAt": now, "mode": e.Mode,
		"model": e.Model, "provider": e.Provider, "status": e.Status,
	})
	if err != nil {
		return fmt.Errorf("zcode: task index upsert: %w", err)
	}
	// the retired seam's four statements, verbatim — fold stray siblings
	// first (so the pin cannot lift them back), fold displaced twins by
	// title, then refresh+pin the home row
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("zcode: task index upsert: %w", err)
	}
	stmts := []struct {
		sql  string
		args []any
	}{
		{"insert or ignore into tasks" +
			" (workspace_key, workspace_path, workspace_identity, task_id," +
			"  title, task_status, provider, mode, model, created_at," +
			"  updated_at, unread_at, pinned, archived, deleted," +
			"  title_overridden, meta_json, searchable_text," +
			"  cron_automation_id, last_unread_at, off_peak_task_id," +
			"  migration_source, forked_from_task_id)" +
			" values (?,?,NULL,?,?,?,?,?,?,?,?,NULL,1,0,0,1,?,?,NULL,0,NULL,NULL,NULL)",
			[]any{e.WorkspacePath, e.WorkspacePath, e.SessionID, e.Title, e.Status,
				e.Provider, e.Mode, e.Model, now, now, string(meta), e.Title}},
		{"update tasks set pinned=0, archived=1," +
			" task_status='completed', updated_at=?," +
			" meta_json=case when json_valid(meta_json)" +
			"  then json_set(meta_json, '$.status', 'completed')" +
			"  else meta_json end" +
			" where task_id=? and workspace_key != ?",
			[]any{now, e.SessionID, e.WorkspacePath}},
		{"update tasks set pinned=0, archived=1," +
			" task_status='completed', updated_at=?," +
			" meta_json=case when json_valid(meta_json)" +
			"  then json_set(meta_json, '$.status', 'completed')" +
			"  else meta_json end" +
			" where title=? and task_id != ? and archived=0",
			[]any{now, e.Title, e.SessionID}},
		{"update tasks set title=?, task_status=?, provider=?, mode=?," +
			" model=?, updated_at=?, meta_json=?," +
			" pinned=1, archived=0" +
			" where task_id=? and workspace_key=?",
			[]any{e.Title, e.Status, e.Provider, e.Mode, e.Model, now,
				string(meta), e.SessionID, e.WorkspacePath}},
	}
	for _, st := range stmts {
		if _, err := tx.Exec(st.sql, st.args...); err != nil {
			tx.Rollback()
			return fmt.Errorf("zcode: task index upsert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("zcode: task index upsert: %w", err)
	}
	return nil
}

// --- the sidebar reveal nudge ---------------------------------------------
//
// 直写 tasks-index.sqlite 对桌面端是「写了但没人知道」：ZCode 的任务列表
// 是纯事件驱动的内存缓存，只在自家 runtime 的 workspace_task_list_changed
// / 用户操作 / 重启时重读库——没有文件监听，也没有轮询（开源版
// zcodeTaskIndexSyncer.ts 佐证）。外部进程唯一借得到的入口是让桌面端
// 「打开」这个工作区：目标工作区还不是标签页时，新增标签改变列表的
// workspace scopes，触发一次全量重读——刚注册的行随即可见；已是标签
// 页则按 tab 去重静默收场。打开有两条等价通道，但待遇不同：zcode://
// workspace/open 深链在 ZCode ≥3.14 每次都弹「只打开你信任来源的文件
// 夹」一次性确认框（深链 handler 无条件 confirm，没有白名单也没有
// 「记住」），而第二实例 --open-workspace argv 走 handleOpenWorkspacePath
// ——同一条 OpenWorkspacePath 路由，零弹窗（Windows 右键「在 ZCode 中
// 打开」注册的就是这个形状）。所以 nudge 优先拉起一个短命第二实例带参
// 投递，单实例锁内转发完即退。这条通道只在 Windows/Linux 干净；macOS
// 上两代形状都试过并双双阵亡：裸 exec 内层二进制＝没登记的启动，Dock
// 给转发进程立幽灵 tile 且退了不清（v2.6 回归）；改经 `open -n` 走
// LaunchServices 后进程侧干净了（拉起→转发→亚秒退出），但 Dock 仍把
// 这次 launch 的 tile 留成常驻——房主实测每开一次工作室，拓展坞多一
// 枚 Z 图标。故 macOS 默认不投：侧栏行照常落库，浮现交给桌面端下一
// 次自然刷新（与红点同一语义）；设置窗·连接面板的「ZCode 侧栏即时
// 刷新」开关（真源 reveal.json，与传统 NIUMA_REVEAL=link 同一词表）
// 显式选回深链（零进程零图标，代价是 ZCode ≥3.14 每次投递弹一次性信
// 任框）。找不到运行中的二进制才退回老深链（会弹框，聊胜于无）。只
// 在桌面端已在运行时触发（绝不替用户拉起 ZCode），10 秒节流；投不投
// 由 reveal.go 的模式门统一裁决（设置文件 > env > 平台默认，off 整体
// 关闭）。

var revealLast atomic.Int64 // unix millis of the last fired nudge

const revealEvery = 10 * time.Second

// revealWorkspace fires the reveal nudge for one workspace,
// best-effort and throttled: a burst of births (boot backfill, quick
// hires) shares one nudge.
func revealWorkspace(workspace string) { reveal(workspace, false) }

// RevealWorkspaceForce is the same nudge with the shared throttle set
// aside — the rank-change tap's companion: a title rewrite is rare and
// user-initiated, and must not silently lose its nudge to a birth burst
// that just consumed the 10s window (a stale Lv.N sitting on screen is
// exactly the complaint the rewrite exists to answer).
func RevealWorkspaceForce(workspace string) { reveal(workspace, true) }

func reveal(workspace string, force bool) {
	// 模式门（真源在 reveal.go：设置文件 > env > 平台默认）——off 全
	// 平台不投；darwin 只在显式 link（设置窗开关或 NIUMA_REVEAL=link）
	// 时投：第二实例的两代形状都在 Dock 留 tile（裸 exec＝幽灵，open
	// -n＝launch tile 常驻），默认整条砍掉——行照常落库，侧栏等桌面端
	// 下次自然刷新；其余平台默认投（零弹窗的第二实例通道）。
	if workspace == "" || !RevealDelivers(RevealMode(), runtime.GOOS) {
		return
	}
	if !desktopRunning() {
		return
	}
	now := time.Now().UnixMilli()
	last := revealLast.Load()
	if force {
		revealLast.Store(now) // 强制揭示也占窗：紧随的出生爆发不再补枪
	} else if now-last < revealEvery.Milliseconds() || !revealLast.CompareAndSwap(last, now) {
		return
	}
	var cmd *exec.Cmd
	if argv := nudgeArgv(zcodeDesktopBinary(), workspace); argv != nil {
		// 无弹窗通道：短命第二实例带 --open-workspace 投递，单实例锁
		// 转发完即退（对正在运行的桌面端等同一次「打开工作区」）。
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd = util.HideConsole(exec.CommandContext(ctx, argv[0], argv[1:]...))
	} else {
		// 兜底老深链：刷新效果相同，但 ZCode ≥3.14 会对它弹一次性
		// 确认框——只在定位不到运行中二进制时才走到这。
		link := "zcode://workspace/open?path=" + url.QueryEscape(workspace)
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", "-g", link) // -g: 只递消息不抢前台
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
		default:
			cmd = exec.Command("xdg-open", link)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		revealLast.Store(0) // 失败不占节流窗，下一次注册再试
		log.Printf("zcode: 侧栏揭示投递失败（不影响注册）: %v: %s", err, trimTail(string(out)))
	}
}

// desktopRunning reports whether the ZCode desktop app is up — the nudge
// taps a running app, never launches one.
func desktopRunning() bool {
	switch runtime.GOOS {
	case "darwin":
		// 主进程 argv0 落在 .app/Contents/MacOS 下；Helper 子进程都在
		// Frameworks/ 下，这个 -f 模式不会误伤（pgrep 也不会匹配自己）。
		if exec.Command("pgrep", "-f", "ZCode.app/Contents/MacOS").Run() == nil {
			return true
		}
		// ZCode 3.14.4+ 的 Electron 会把主进程 argv 改写成裸应用名
		// （ps 里只剩 "ZCode"，不含任何包路径），按路径找会误判「没在
		// 跑」而让整个揭示哑火——LaunchServices 才是权威事实源：它登记
		// 的是它亲手拉起的应用，与进程后来怎么改名无关。
		return lsDesktopRunning()
	case "windows":
		out, err := util.HideConsole(exec.Command("tasklist", "/FI", "IMAGENAME eq ZCode.exe", "/FO", "CSV", "/NH")).Output()
		return err == nil && strings.Contains(string(out), "ZCode.exe")
	default:
		return exec.Command("pgrep", "-x", "zcode").Run() == nil
	}
}

// lsDesktopRunning asks LaunchServices whether the ZCode desktop app is
// up: lsappinfo prints the app's ASN when it runs, empty when it doesn't
// (exit is 0 either way — the output, not the status, carries the
// answer). Darwin-only companion of desktopRunning.
func lsDesktopRunning() bool {
	out, err := exec.Command("/usr/bin/lsappinfo", "find", `bundleID="dev.zcode.app"`).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// lsRunningAppBundle returns the bundle path of the RUNNING ZCode
// desktop app straight from LaunchServices (empty = not running /
// unavailable) — the one install the user actually opened, however the
// process renamed itself in ps.
func lsRunningAppBundle() string {
	find, err := exec.Command("/usr/bin/lsappinfo", "find", `bundleID="dev.zcode.app"`).Output()
	if err != nil {
		return ""
	}
	asn := strings.TrimSpace(string(find))
	if asn == "" {
		return ""
	}
	info, err := exec.Command("/usr/bin/lsappinfo", "info", "-only", "bundlepath", asn).Output()
	if err != nil {
		return ""
	}
	return parseLSBundlePath(string(info))
}

// parseLSBundlePath pulls the bundle path out of one lsappinfo line:
// "LSBundlePath"="/Applications/ZCode.app" → /Applications/ZCode.app.
// Empty string for anything unrecognised.
func parseLSBundlePath(info string) string {
	const key = `"LSBundlePath"=`
	s := strings.TrimSpace(info)
	if !strings.HasPrefix(s, key) {
		return ""
	}
	return strings.Trim(s[len(key):], `"`)
}

// zcodeDesktopBinary locates the running desktop app's main executable:
// the dialog-free nudge spawns a short-lived second instance of exactly
// that binary — not some other install the user never opened. Empty
// string means "not found, fall back to the deep link".
func zcodeDesktopBinary() string {
	switch runtime.GOOS {
	case "darwin":
		// ps 全量 comm 挑第一条主进程路径：Helper 都在 Frameworks/ 下，
		// 与 desktopRunning 的 pgrep 模式同一边界，不会捞错。
		if out, err := exec.Command("ps", "-axo", "comm=").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if line = strings.TrimSpace(line); strings.Contains(line, "ZCode.app/Contents/MacOS/") {
					return line
				}
			}
		}
		// 3.14.4+ 主进程在 ps 里只剩裸名（见 desktopRunning 的注）——问
		// LaunchServices 要正在运行的那份 .app 的包路径，投递永远拉用户
		// 开着的同一个安装；/Applications 猜测只是最后手段。
		if p := lsRunningAppBundle(); p != "" {
			return filepath.Join(p, "Contents", "MacOS", "ZCode")
		}
		fallback := "/Applications/ZCode.app/Contents/MacOS/ZCode"
		if _, err := os.Stat(fallback); err == nil {
			return fallback
		}
		return ""
	case "windows":
		// 与发现层同源（含 ZCode Preview 与改名装机位）；右键集成写进
		// 注册表的是标准位，但投递只要求拿到同一份可执行文件。
		return electronExeIn(installRootOf(FindBundle()))
	default:
		// /proc/<pid>/exe 指回正在运行的那个二进制（AppImage 的解包
		// 挂载位也成立）；-o 取最老的进程，主进程先于一切子进程。
		out, err := exec.Command("pgrep", "-ox", "zcode").Output()
		if err != nil {
			return ""
		}
		if exe, err := os.Readlink("/proc/" + strings.TrimSpace(string(out)) + "/exe"); err == nil {
			return exe
		}
		return ""
	}
}

// nudgeArgv shapes the dialog-free channel for the platforms where it
// is clean — the argv that spawns the short-lived second instance
// carrying --open-workspace, or nil when the running app couldn't be
// located (caller falls back to the deep link). Windows/Linux exec the
// running binary itself. macOS has no seat: BOTH second-instance
// shapes leave Dock tiles behind on current systems (see reveal's
// darwin verdict), so the darwin guard upstream routes to the deep
// link (NIUMA_REVEAL=link) or skips delivery entirely.
func nudgeArgv(bin, workspace string) []string {
	if bin == "" || runtime.GOOS == "darwin" {
		return nil
	}
	return []string{bin, "--open-workspace", workspace}
}

// ArchiveTaskIndex folds a room-owned sidebar row away (offboard):
// archived=1, status completed. Guarded by the dhEmployee marker —
// GUI-owned rows are never touched — and idempotent: a row that was
// never registered is a silent no-op.
func ArchiveTaskIndex(workspacePath, sessionID string) error {
	if workspacePath == "" || sessionID == "" {
		return fmt.Errorf("zcode: task index archive needs workspace and session")
	}
	dbPath, err := HomePathV2("tasks-index.sqlite")
	if err != nil {
		return fmt.Errorf("zcode: task index path: %w", err)
	}
	return archiveTaskIndexAt(dbPath, workspacePath, sessionID)
}

// archiveTaskIndexAt is ArchiveTaskIndex over an explicit db (tests).
func archiveTaskIndexAt(dbPath, workspacePath, sessionID string) error {
	db, err := openStoreDB(dbPath)
	if err != nil {
		return fmt.Errorf("zcode: task index archive: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		"update tasks set pinned=0, archived=1, task_status='completed',"+
			" updated_at=?, meta_json=json_set(meta_json, '$.status', 'completed')"+
			" where workspace_key=? and task_id=?"+
			" and json_extract(meta_json, '$.dhEmployee') = 1",
		time.Now().UnixMilli(), workspacePath, sessionID); err != nil {
		return fmt.Errorf("zcode: task index archive: %w", err)
	}
	return nil
}

// ArchiveTaskIndexForce folds one session's sidebar row by task_id
// alone — no dhEmployee guard. The guarded ArchiveTaskIndex above lost
// a real fight: the ZCode desktop's timeline sync REWRITES a born
// session's row (its own meta shape, no dhEmployee) seconds after
// birth, so the own-row guard stops matching and the fold no-ops —
// the close-time archive left "running" ghosts. The task_id IS the
// room-driven session id, unique by construction, so matching on it
// alone stays inside our own rows; the GUI-ownership protection the
// guard gave up only ever mattered for rows the room did not birth.
func ArchiveTaskIndexForce(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("zcode: task index archive needs a session")
	}
	dbPath, err := HomePathV2("tasks-index.sqlite")
	if err != nil {
		return fmt.Errorf("zcode: task index path: %w", err)
	}
	return archiveTaskIndexForceAt(dbPath, sessionID)
}

// archiveTaskIndexForceAt is ArchiveTaskIndexForce over an explicit db (tests).
func archiveTaskIndexForceAt(dbPath, sessionID string) error {
	db, err := openStoreDB(dbPath)
	if err != nil {
		return fmt.Errorf("zcode: task index archive: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		"update tasks set pinned=0, archived=1, task_status='completed',"+
			" updated_at=?, meta_json=json_set(meta_json, '$.status', 'completed')"+
			" where task_id=?",
		time.Now().UnixMilli(), sessionID); err != nil {
		return fmt.Errorf("zcode: task index archive: %w", err)
	}
	return nil
}

// MarkTaskUnread stamps a member's sidebar row unread — the red-dot
// half of「在 ZCode 里看见」: every line the room hears from a
// member's seat dots their pinned row, so a return to the desktop
// names who spoke without opening the web app. The SAME pass mirrors
// the session's visible chat text (user inputs + assistant text
// replies, the desktop syncer's own shaping) into the row's
// searchable_text — native sidebar rows carry their whole
// conversation there, so content search finds them with snippets;
// room-born rows would otherwise match their title alone. Column-only
// on the dot half: the desktop's reader takes unread_at straight off
// the row (rowToMeta: unreadAt = r.unread_at), and leaving meta_json
// alone keeps the stamp out of the timeline rewriter's fights.
// updated_at rides along so the row also floats to its group's top
// (the list sorts updated_at DESC) — the desktop's own merge is
// max()-shaped, so a monotonic bump never contradicts it. Matched by
// task_id alone for the ArchiveTaskIndexForce reason: the rewrite may
// have moved the row off the birth workspace_key. Best-effort, same
// as the register: a failed stamp costs a dot and the search text,
// never the turn. What this can NOT do: make the desktop render it —
// the list cache has no watcher, so the dot (and the fresh search
// text) waits for the next sidebar touch (search, task open, tab
// add).
func MarkTaskUnread(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("zcode: task index unread stamp needs a session")
	}
	dbPath, err := HomePathV2("tasks-index.sqlite")
	if err != nil {
		return fmt.Errorf("zcode: task index path: %w", err)
	}
	// 对话正文源：CLI/app-server 共用的消息库。取不到路径（无 home）
	// 就退化为只盖红点——回填是增益不是前提。
	clap := ""
	if p, err := HomePath(filepath.Join("cli", "db", "db.sqlite")); err == nil {
		clap = p
	}
	return markTaskUnreadAt(dbPath, clap, sessionID)
}

// markTaskUnreadAt is MarkTaskUnread over an explicit db (tests).
func markTaskUnreadAt(dbPath, clap, sessionID string) error {
	db, err := openStoreDB(dbPath)
	if err != nil {
		return fmt.Errorf("zcode: task index unread: %w", err)
	}
	defer db.Close()
	now := time.Now().UnixMilli()
	if _, err := db.Exec(
		"update tasks set unread_at=?, last_unread_at=?, updated_at=?"+
			" where task_id=?", now, now, now, sessionID); err != nil {
		return fmt.Errorf("zcode: task index unread: %w", err)
	}
	// the search half is best-effort and NEVER rolls the dot back (the
	// seam lost both legs when this one errored — a schema without
	// searchable_text now survives with the dot alone, as its comment
	// always claimed)
	if text := clapSearchText(clap, sessionID); text != "" {
		_, _ = db.Exec(
			"update tasks set searchable_text=?, updated_at=? where task_id=?",
			text, now, sessionID)
	}
	return nil
}

// clapSearchText mirrors the session's visible chat text out of the
// CLI/app-server message store with the desktop syncer's own shaping:
// user messages keep ALL their text parts, assistant messages only the
// LAST text part (the visible-final reply), model-only turns and
// compact summaries stay out, parts join with "\n" and the total caps
// at 200k chars (buildSearchableTextFromSnapshot's own limit). "" on
// any miss — the dot half never depends on it.
func clapSearchText(clap, sessionID string) string {
	if clap == "" {
		return ""
	}
	if _, err := os.Stat(clap); err != nil {
		return ""
	}
	src, err := openStoreDB(clap)
	if err != nil {
		return ""
	}
	defer src.Close()
	type partRow struct {
		mid  string
		data string
	}
	msgRows, err := src.Query(
		"select id, data from message where session_id=?"+
			" order by sequence, time_created, id", sessionID)
	if err != nil {
		return ""
	}
	var msgs []struct{ id, data string }
	for msgRows.Next() {
		var id, data string
		if msgRows.Scan(&id, &data) == nil {
			msgs = append(msgs, struct{ id, data string }{id, data})
		}
	}
	msgRows.Close()
	partRows, err := src.Query(
		"select message_id, data from part where session_id=?"+
			" and json_extract(data, '$.type')='text'"+
			" order by message_id, sequence, time_created, id", sessionID)
	if err != nil {
		return ""
	}
	texts := map[string][]string{}
	for partRows.Next() {
		var pr partRow
		if partRows.Scan(&pr.mid, &pr.data) != nil {
			continue
		}
		var d struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(pr.data), &d) != nil {
			continue
		}
		if t := strings.TrimSpace(d.Text); t != "" {
			texts[pr.mid] = append(texts[pr.mid], t)
		}
	}
	partRows.Close()
	const cap200k = 200000
	var out []string
	total := 0
	for _, m := range msgs {
		var d struct {
			Role     string `json:"role"`
			Summary  *any   `json:"summary"`
			Metadata *struct {
				Visibility string `json:"visibility"`
			} `json:"metadata"`
			Semantics *struct {
				Kind string `json:"kind"`
			} `json:"semantics"`
		}
		if json.Unmarshal([]byte(m.data), &d) != nil {
			continue
		}
		if d.Role != "user" && d.Role != "assistant" {
			continue
		}
		if d.Metadata != nil && d.Metadata.Visibility == "model-only" {
			continue
		}
		if d.Semantics != nil && d.Semantics.Kind == "compact_summary" || d.Summary != nil {
			continue
		}
		chunk := texts[m.id]
		if d.Role == "assistant" && len(chunk) > 0 {
			chunk = chunk[len(chunk)-1:]
		}
		for _, t := range chunk {
			out = append(out, t)
			total += len(t)
			if total >= cap200k {
				break
			}
		}
		if total >= cap200k {
			break
		}
	}
	text := strings.Join(out, "\n")
	if len(text) > cap200k {
		text = text[:cap200k]
	}
	return text
}

func trimTail(s string) string {
	if len(s) > 200 {
		return s[len(s)-200:]
	}
	return s
}
