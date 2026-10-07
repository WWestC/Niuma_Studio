package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/assistant"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/dispatch"
	"github.com/WWestC/Niuma_Studio/pixart"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/shell"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// boot_helpers.go — the composition root's support cast: name/path
// resolution, the ZCode bridge boot, staffing-look wiring, legacy
// migration. Split out of main.go (see also boot_fleet.go for the
// FleetAPI adapter).

func defaultName() string {
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "you"
}

// harvestStudioSessions collects the ZCode session ids the studio
// still knows — every staffing binding plus the 小助手's pinned
// session — for the reset's ZCode-side purge. MUST run before
// WipeStudioData: the records ARE the scope, and the wipe destroys
// them. The 小助手's live handle is private, so its pin file is read
// straight off disk (the same file the assistant adopts from).
func harvestStudioSessions(staffStore *staffing.Store) []string {
	seen := make(map[string]struct{})
	var ids []string
	add := func(sid string) {
		if sid == "" {
			return
		}
		if _, dup := seen[sid]; dup {
			return
		}
		seen[sid] = struct{}{}
		ids = append(ids, sid)
	}
	for _, row := range staffStore.List() {
		add(row.SessionID)
	}
	if b, err := os.ReadFile(assistant.DefaultSessionFile()); err == nil {
		var pin struct {
			Session string `json:"session"`
		}
		if json.Unmarshal(b, &pin) == nil {
			add(pin.Session)
		}
	}
	return ids
}

// purgeMemberSessionsDeep is the per-project dismiss/reset faces'
// ZCode leg (server.Options.PurgeMemberSessions): exactly the ids the
// server harvested from that project's staffing rows — the factory
// reset's marker/title scope arms stay OUT (they would take every
// project's members). Three moves like the reset's: the sidebar rows,
// the session transcripts, the per-session files. The bridge child
// may still be mid-turn on a kicked member; the WAL + busy_timeout
// seam (the register's) keeps the delete safe, and a session that
// somehow survives is desktop-side residue, never a failed dismiss.
func purgeMemberSessionsDeep(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := zcode.PurgeTaskRows(ids); err != nil {
		return fmt.Errorf("任务索引行清理: %w", err)
	}
	if err := zcode.PurgeStudioSessions(ids); err != nil {
		return fmt.Errorf("会话正文清理: %w", err)
	}
	return zcode.PurgeSessionFiles(ids)
}

// unionIDs merges two id lists order-preserving and deduped — the
// harvest ∪ the purge's own findings (the index rows carry ids the
// stores forgot: departed members, displaced twins).
func unionIDs(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	var out []string
	for _, list := range [][]string{a, b} {
		for _, id := range list {
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// openProjectStore opens the v2 project registry store
// (~/.niuma/projects.json). Never returns nil: an unreadable
// file logs and degrades to in-memory — the room must boot (the agents
// store's shape; the corrupt file is preserved for forensics).
func openProjectStore() *projects.Store {
	path, err := projects.DefaultPath()
	if err != nil {
		log.Printf("projects store path unavailable: %v", err)
		path = ""
	}
	store, err := projects.Open(path)
	if err != nil {
		log.Printf("projects store: %v — 以内存项目表运行（办公室照常，重启后仅剩 Niuma_Studio）", err)
		store, _ = projects.Open("")
	}
	return store
}

// v2Root returns the v2 single-root directory (~/.niuma) the
// registry persists room history and seat snapshots under; "" on an
// unavailable home dir (every room stays in memory — same degradation
// as the pre-v2 history path).
func v2Root() string {
	dir, err := projects.RootDir()
	if err != nil {
		log.Printf("v2 root unavailable: %v — 办公室历史将不落盘", err)
		return ""
	}
	return dir
}

// stableWorkspace is where member sessions call home when the process
// cwd can't be: a bundle double-click (Finder/Dock) starts with cwd
// "/", and a lobby frozen there drags every birth to the filesystem
// root — ZCode grows a stray "/" project and relative writes aim at /.
// The cwd stays authoritative for a terminal/dev launch; only a cwd
// that IS a filesystem root degrades — first to the bundle's own
// checkout (the .app ships from the office repo, so the home is three
// levels up and go.mod-gated), then to the stable ~/.niuma/workspace,
// created on demand.
func stableWorkspace() string {
	wd, _ := os.Getwd()
	if wd != "" && filepath.Dir(wd) != wd {
		return wd
	}
	if home := shell.BundleOfficeHome(); home != "" {
		return home
	}
	dir, err := projects.RootDir()
	if err != nil {
		log.Printf("workspace root unavailable: %v — 成员工作区退回进程目录 %q", err, wd)
		return wd
	}
	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		log.Printf("workspace dir %s: %v — 成员工作区退回进程目录 %q", ws, err, wd)
		return wd
	}
	return ws
}

func exeName() string {
	if len(os.Args) > 0 {
		return os.Args[0]
	}
	return "niuma"
}

// bridgePoolSize reads NIUMA_BRIDGE_POOL (1–8). Default and floor are
// 1 — the historical single app-server child, byte-for-byte.
func bridgePoolSize() int {
	n, err := strconv.Atoi(util.Env("BRIDGE_POOL"))
	if err != nil || n < 1 {
		return 1
	}
	if n > 8 {
		return 8
	}
	return n
}

// loopbackBind reports whether a NIUMA_ADDR value keeps the listener
// inside the machine (empty, localhost, a loopback literal). Anything
// else is a remote deployment and needs the gate token.
func loopbackBind(addr string) bool {
	a := strings.TrimSpace(addr)
	if a == "" || strings.EqualFold(a, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(a, "[]"))
	return ip != nil && ip.IsLoopback()
}

// startZCodeBridge spawns the zcode app-server child the dispatcher
// drives member sessions through. The client rides along so main's
// boot-gate status can watch its readiness; any failure (no node, no
// bundle) reports through fail: "" = started, "missing" = the bundle
// is absent, otherwise the Start error text. notify=false (the boot
// gate's quiet retries) posts no room notice — a re-attempt every 10s
// must not repeat the same line into the room.
func startZCodeBridge(hub *chat.Hub, wd string, notify bool) (bridge dispatch.Bridge, client *zcode.Client, fail string) {
	// FindBundle + Start 内部的 provider/node 发现共同构成自愈链：
	// 桌面端更新挪了 CLI、provider 配置或 node 不在 PATH，重拉都会
	// 重新发现，而不是永远撞同一堵墙。
	bundle := zcode.FindBundle()
	if bundle == "" {
		if notify {
			hub.System(fmt.Sprintf("[调度] 未找到 ZCode CLI（找过 %s；NIUMA_ZCODE_BUNDLE 可指定路径）%s——应用入口正在等待 ZCode，安装后将自动识别；房间服务照常运行", strings.Join(zcode.BundleHomes(), "、"), zcode.MissingSelfCheck()))
		}
		return nil, nil, "missing"
	}
	c, err := zcode.Start(zcode.Options{
		Bundle: bundle,
		Cwd:    wd,
		Stderr: log.New(os.Stderr, "[zcode] ", log.LstdFlags).Writer(),
	})
	if err != nil {
		if notify {
			hub.System(fmt.Sprintf("[调度] ZCode app-server 启动失败（%v）——应用入口将显示原因并持续等待", err))
		}
		return nil, nil, err.Error()
	}
	// 账号套餐面同步（同步、有限预算）：把本机可驱动的账号套餐（BigModel
	// 团队 / Start Plan 免费…）推进子进程的 provider registry——没有这一步
	// registry 不认识 account:* 模型，SetModel 选了也会被拒。必须在本函数
	// 返回（＝ OnSpawn 的预热探针/旗舰招聘开跑）之前完成：子进程按 stdin
	// 到达顺序处理请求，探针的 create/setModel 一旦先到，SetModel(account:*)
	// 就被「Provider Registry 中不存在」拒掉、被静默容忍后钉回会话默认模
	// 型——房主实录 2026-10-07：默认模型是已死的个人中转，预热挂满 90 秒
	// 封顶。同步等注册表推送先落，顺序才有保证；失败只落日志不进房（个人
	// provider 不受影响，缺的分组缘由见 kb/manual.md 模型清单一节）。池的
	// TrySpawn 跑在自己的 goroutine 上，这里的阻塞不卡启动门轮询。
	select {
	case <-c.ReadyDone():
	case <-time.After(30 * time.Second):
		log.Printf("[zcode] 账号套餐面同步放弃：子进程 30s 未就绪")
		return c, c, ""
	}
	{
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := c.SyncAccountProviders(ctx); err != nil {
			log.Printf("[zcode] 账号套餐面同步失败（补员清单将缺少套餐分组）：%v", err)
		} else {
			log.Printf("[zcode] 账号套餐面已同步")
		}
	}
	return c, c, ""
}

// lookServer builds the hub's LookFn (r_05 t_126): the lifetime look's
// single source is the staffing store — read the row's Look, roll and
// persist one when absent (SetLook's idempotence IS the lifetime lock:
// once written, never re-rolled), and anchor the five incumbent
// members to the standard look so nobody "changes face" on upgrade
// day. Project scope is the lobby's default row (members are
// studio-level people, the staffing default row carries them).
func lookServer(staff *staffing.Store) func(string) *chat.Look {
	anchored := map[string]bool{"小牛": true, "小马": true, "小猿": true, "小狐": true, "小鹿": true}
	return func(name string) *chat.Look {
		if staff == nil {
			return nil
		}
		e, ok := staff.Get(chat.LobbyKey, name)
		if ok && e.Look != nil {
			return lookWire(e.Look)
		}
		var look *staffing.Look
		if anchored[name] {
			look = staffing.AnchoredLook(pixart.HairStandard)
		} else {
			look = staffing.RollLook(name)
		}
		if _, err := staff.SetLook(chat.LobbyKey, name, look); err != nil {
			// 落档失败（未在编行等）——本回合回 nil 走旧两维渲染，
			// 下次入座再试；终身层绝不因落档失败而错配
			return nil
		}
		return lookWire(look)
	}
}

// lookWire projects the staffing look into the chat wire shape.
func lookWire(l *staffing.Look) *chat.Look {
	if l == nil {
		return nil
	}
	out := &chat.Look{HairStyle: l.HairStyle, Skin: l.Skin}
	for _, a := range l.Accessories {
		out.Accessories = append(out.Accessories, chat.LookAcc{Slot: a.Slot, Style: a.Style, Color: a.Color})
	}
	return out
}

func envOr(name, fallback string) string {
	if v := util.Env(name); v != "" {
		return v
	}
	return fallback
}

// migrateLegacyPacks is the one-shot retirement of the capability-pack
// era: when the old capabilities.json still stands beside the (not yet
// existing) skills.json, fold its prompt/manual-carrying packs into
// skills under the same keys and rename the old file .bak. Idempotent
// by construction — the renamed file never matches again. Model-only
// and tool-only packs have no skill shape and die with the format;
// profile/staffing references ride through the stores' own load-time
// fold (missing keys degrade, the standing discipline). Never fatal:
// the caller logs and the room boots.
func migrateLegacyPacks(skillsPath string) error {
	if skillsPath == "" {
		return nil
	}
	oldPath := filepath.Join(filepath.Dir(skillsPath), "capabilities.json")
	if _, err := os.Stat(skillsPath); err == nil {
		return nil // the skills library already stands — one-shot done
	} else if !os.IsNotExist(err) {
		return err
	}
	skills, err := capability.MigrateLegacyPacks(oldPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		return nil // nothing legacy on this machine
	}
	st, err := capability.Open(skillsPath, "")
	if err != nil {
		return err
	}
	n := 0
	for _, sk := range skills {
		if _, err := st.UpsertSkill(sk); err != nil {
			log.Printf("旧能力包 %s 未迁移：%v", sk.Key, err)
			continue
		}
		n++
	}
	if err := os.Rename(oldPath, oldPath+".bak"); err != nil {
		return fmt.Errorf("rename %s: %w", oldPath, err)
	}
	log.Printf("旧能力包已退役：迁移为技能 %d 个（%s → %s.bak；模型档/工具面槽随格式退役）", n, oldPath, oldPath)
	return nil
}

// quarantineSQLite moves a corrupt single-database file aside for
// forensics (语义对照表 §6 ①类): studio.db and its WAL/SHM siblings
// all take the .corrupt-<stamp> suffix — a stale -wal next to a
// renamed main file is its own little corruption trap. Best effort:
// a sibling that cannot move (locked, vanished) logs and leaves it;
// the caller's fresh Open recreates whatever it needs.
func quarantineSQLite(dbPath, stamp string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := dbPath + suffix
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, src+".corrupt-"+stamp); err != nil {
			log.Printf("sqlite 隔离失败（%s 留在原地）：%v", src, err)
		}
	}
}

// —— 单向桥的一次性导入戳（转正回落矩阵的地基）——
//
// 桥只在「首开」跑一次；戳文件在场＝JSON 时代已被消费过。损坏隔
// 离重开、手动删库都绝不回导：桥的源文件是切换时刻的旧快照，回
// 导＝静默复活陈旧数据（比空库更会说谎）。戳与 JSON 副本同根存
// 放（都在 v2 root 下），删除整个数据目录＝全新开始，戳与 JSON
// 一起消失，语义自洽。

// ledgerImportStampPath is the stamp file's location under the v2 root.
func ledgerImportStampPath(root string) string {
	return filepath.Join(root, ".ledger_imported")
}

// ledgerImportStampDone reports whether the JSON era was already
// consumed into a database (the bridge must not run again).
func ledgerImportStampDone(root string) bool {
	if root == "" {
		return true // no root, no bridge — the mem boot's shape
	}
	_, err := os.Stat(ledgerImportStampPath(root))
	return err == nil
}

// writeLedgerImportStamp marks the JSON era consumed (best-effort: a
// failed write only means the bridge re-runs next boot — its per-side
// emptiness gates make that a no-op, never a re-import).
func writeLedgerImportStamp(root string) {
	if root == "" {
		return
	}
	if err := os.WriteFile(ledgerImportStampPath(root),
		[]byte(fmt.Sprintf("%d\n", time.Now().Unix())), 0o644); err != nil {
		log.Printf("ledger import stamp: %v — 下轮启动桥按表空门幂等重查", err)
	}
}
