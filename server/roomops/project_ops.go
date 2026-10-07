package roomops

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server/gitops"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// handleProjects serves GET /projects (every state, the switcher's and
// the selector's data source) and POST /projects (立项 — always lands
// draft; activate is the only way out).
func (rc *Face) HandleProjects(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/projects" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		rc.HandleProjectCreate(w, r)
		return
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		return
	}
	if rc.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	type verOut struct {
		Name    string `json:"name"`
		StartTS int64  `json:"start_ts,omitempty"`
		EndTS   int64  `json:"end_ts,omitempty"`
		Goal    string `json:"goal,omitempty"`
	}
	type projSummary struct {
		Key       string   `json:"key"`
		Name      string   `json:"name"`
		Status    string   `json:"status"`
		Workspace string   `json:"workspace"`
		Desc      string   `json:"desc,omitempty"`
		Versions  []verOut `json:"versions,omitempty"`
		CreatedBy string   `json:"created_by,omitempty"`
		CreatedTS int64    `json:"created_ts,omitempty"`
		// BranchLinks mirrors the v2.7 版本管理 bindings (branch → 需求/
		// 版本) — the overview's version chips annotate their bound
		// branch from this, no extra git round-trip.
		BranchLinks map[string]projects.BranchLink `json:"branch_links,omitempty"`
		// AutoPilot mirrors the project's r_19 双开关之或（自动补货 ∨
		// 自动推进——the room pill's badge source）；absent when no
		// staffing store is wired.
		AutoPilot bool `json:"autopilot,omitempty"`
		// Paused mirrors the room's pause switch (POST /p/{key}/pause)
		// — the switcher row's pause/play button state; absent when no
		// staffing store is wired.
		Paused bool `json:"paused,omitempty"`
	}
	out := []projSummary{}
	for _, p := range rc.Stores.ProjectStore.List() {
		vers := make([]verOut, 0, len(p.Versions))
		for _, v := range p.Versions {
			vers = append(vers, verOut{Name: v.Name, StartTS: v.StartTS, EndTS: v.EndTS, Goal: v.Goal})
		}
		out = append(out, projSummary{Key: p.Key, Name: p.Name, Status: p.Status,
			Workspace: p.Workspace, Desc: p.Desc, Versions: vers,
			CreatedBy: p.CreatedBy, CreatedTS: p.CreatedTS,
			BranchLinks: p.BranchLinks,
			AutoPilot:   rc.Stores.StaffStore != nil && rc.Stores.StaffStore.SettingsOf(p.Key).AnyAutoOn(),
			Paused:      rc.Stores.StaffStore != nil && rc.Stores.StaffStore.SettingsOf(p.Key).Paused})
	}
	httputil.WriteJSONZip(w, r, out)
}

// handleProjectCreate is POST /projects: the 立项 face. The store
// validates (slug, name, absolute workspace, version list) and caps the
// count; a refusal is a 400 whose body is the reason. v2.14：载荷可带
// git_plan——落册后即刻对工作区执行 git 引导（自动 init/基线分支/钩子
// 直装，见 gitboot.go 的红线），结果随应答的 git_boot 字段如实带回；
// 引导失败不炸立项（草稿已在册，notes 指路补法）。
func (rc *Face) HandleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if rc.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Key       string             `json:"key"`
		Name      string             `json:"name"`
		Desc      string             `json:"desc"`
		Workspace string             `json:"workspace"`
		By        string             `json:"by"`
		Versions  []projects.Version `json:"versions"`
		GitPlan   *projects.GitPlan  `json:"git_plan"`
		// GitDisabled（两级门顶层）：立项表单的总开关关着时上送——引导
		// 与 git 面整面停；缺省 false＝开（存量口径）。
		GitDisabled bool `json:"git_disabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	p, err := rc.Stores.ProjectStore.Create(projects.Project{
		Key:         strings.TrimSpace(body.Key),
		Name:        body.Name,
		Desc:        body.Desc,
		Workspace:   strings.TrimSpace(body.Workspace),
		Versions:    body.Versions,
		CreatedBy:   body.By,
		GitPlan:     body.GitPlan,
		GitDisabled: body.GitDisabled,
	})
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	boot := rc.GitBootstrap(p)
	if p.GitPlan != nil {
		// 引导摘要进房间一行（做了/跳过了什么，房主在办公室里看得见）。
		if hub := rc.RoomHub(p.Key); hub != nil && len(boot.Notes) > 0 {
			hub.System(i18n.Sf("【版本】立项 git 引导：%s", strings.Join(boot.Notes, "；")))
		}
	}
	rc.Hub.System(i18n.Sf("新项目已立项：%s（%s，草稿）——项目管理页完善信息后开张", p.Name, p.Key))
	httputil.WriteJSON(w, struct {
		projects.Project
		GitBoot gitops.BootResult `json:"git_boot"`
	}{p, boot})
}

// preflightFinding is one risk item the 立项 form shows BEFORE the
// creating POST: level "error" previews a store refusal (the POST that
// follows WILL bounce), level "warn" is an advisory the form turns into
// an acknowledge gate — allowed, but only with the risk on screen.
type preflightFinding struct {
	Level string `json:"level"` // "error" | "warn"
	Code  string `json:"code"`
	Text  string `json:"text"`
}

// handleProjectPreflight is POST /projects/preflight: the 立项 face's
// advance risk brief (v2.13). Store-side refusals (bad/duplicate key,
// cap, non-absolute or root workspace, a folder held by a live project)
// come back as errors; the fs facts the store cannot see — a missing
// birth directory (the dispatcher never MkdirAlls one), the user's home
// itself, the studio's own ~/.niuma, nesting under a sibling project, a
// non-repo workspace — ride as warns. Read-only and idempotent: calling
// it twice is free, and the creating POST still enforces everything the
// store enforces (the preflight is the preview, never the gatekeeper).
func (rc *Face) HandleProjectPreflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if rc.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Key       string `json:"key"`
		Name      string `json:"name"`
		Workspace string `json:"workspace"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	key := strings.TrimSpace(body.Key)
	ws := filepath.Clean(strings.TrimSpace(body.Workspace))
	findings := []preflightFinding{}
	add := func(level, code, text string) {
		findings = append(findings, preflightFinding{Level: level, Code: code, Text: text})
	}

	// —— store-refusal preview (error)：随后的 POST /projects 必被拒 ——
	if !projects.ValidKey(key) {
		add("error", "bad_key", i18n.Sf("非法项目 key %q（[a-z0-9_-]{1,32}，创建后不可改名）", key))
	}
	if strings.TrimSpace(body.Name) == "" {
		add("error", "bad_name", i18n.S("项目名称不能为空"))
	}
	if rc.Stores.ProjectStore.Exists(key) {
		add("error", "dup_key", i18n.Sf("项目 key 已存在: %s", key))
	}
	if len(rc.Stores.ProjectStore.List()) >= projects.MaxProjects {
		add("error", "cap", i18n.Sf("项目数已达上限 %d 个——归档或清理后再立项", projects.MaxProjects))
	}
	if !filepath.IsAbs(ws) {
		add("error", "bad_ws", i18n.Sf("workspace 必须是绝对路径: %q", ws))
	} else {
		if filepath.Dir(ws) == ws {
			add("error", "ws_root", i18n.Sf("workspace 不能是文件系统根目录（%s）——成员会在整棵树上读写文件，请指定本项目的专用目录", ws))
		}
		for _, p := range rc.Stores.ProjectStore.List() {
			if p.Key == key || p.Workspace == "" {
				continue
			}
			other := filepath.Clean(p.Workspace)
			if other != ws {
				continue
			}
			if p.Status == projects.StatusArchived {
				// 归档 holders 不拦创建（迁移路径），但复活时会撞上——提前说
				add("warn", "ws_archived", i18n.Sf("workspace 与已归档项目 %s 相同——该项目复活后两个项目会共用一个目录，成员会话互相踩踏", p.Key))
				continue
			}
			add("error", "ws_dup", i18n.Sf("workspace 已被项目 %s 占用——两个在册项目共用一个目录，成员会话会互相踩踏（目录与项目是一对一的隔离绑定）", p.Key))
		}
	}

	// —— fs 事实预告（warn）：store 看不到的、允许但该知晓的事 ——
	home, _ := os.UserHomeDir()
	if home != "" && ws == filepath.Clean(home) {
		add("warn", "ws_home", i18n.S("workspace 就是用户主目录——AI 成员会在整个主目录里读写文件、执行命令（~/.ssh、浏览器配置等敏感内容都在射程内），强烈建议改用本项目专用目录"))
	}
	if root, err := projects.RootDir(); err == nil && root != "" && pathUnder(ws, filepath.Clean(root)) {
		add("warn", "ws_niuma", i18n.S("workspace 在 ~/.niuma 里——这是工作室自己的状态目录（聊天历史、房间快照、成员工作树都在此），成员会话会踩到它们"))
	}
	for _, p := range rc.Stores.ProjectStore.List() {
		if p.Key == key || p.Status == projects.StatusArchived || p.Workspace == "" {
			continue
		}
		other := filepath.Clean(p.Workspace)
		if pathUnder(ws, other) || pathUnder(other, ws) {
			add("warn", "ws_nested", i18n.Sf("workspace 与项目 %s 的目录嵌套（父子关系）——会话 CLI 按 cwd 祖先链定缺省项目范围，两个项目会互相遮蔽", p.Key))
		}
	}
	if filepath.IsAbs(ws) && filepath.Dir(ws) != ws {
		switch fi, err := os.Stat(ws); {
		case err != nil && errors.Is(err, os.ErrNotExist):
			add("warn", "ws_missing", i18n.S("目录还不存在——未选「自动 git init」时请先手动建好目录再开张，否则成员会话可能起不来"))
		case err != nil:
			add("warn", "ws_stat", i18n.Sf("目录读不了：%v", err))
		case !fi.IsDir():
			add("warn", "ws_notdir", i18n.S("该路径不是目录（是个文件？）——成员会话无法落在此处"))
		}
	}

	// —— git 事实（v2.14 立项引导）：仓库在不在/根在哪/当前分支——表单
	//  据此自动填基线分支、亮「可自动 init/外层仓库停用」。外层仓库形
	//  态（根 != 工作区，或工作区是链接工作树）是红线预警：自动引导一
	//  律不动外层仓库（那可能是用户别的项目的地界）。
	gf := gitops.ProbeGitFacts(r.Context(), ws)
	if gf.OuterRepo {
		add("warn", "ws_outer_repo", i18n.Sf("工作区位于外层 git 仓库内（根：%s）——自动 git 引导（init/自动建枝）不会动外层仓库，以免影响别的项目；需要分支隔离请为本项目单独建仓", gf.Root))
	} else if gf.IsRepo && gf.Branch == "" {
		add("warn", "ws_detached", i18n.S("工作区当前是游离 HEAD——基线分支请手填一个已存在的分支名"))
	} else if !gf.IsRepo {
		add("warn", "ws_nogit", i18n.S("工作区不是 git 仓库——分支隔离（成员专属工作树）无从落树；可在下方勾选「自动 git init」或稍后手工建仓"))
	}

	blocking := false
	for _, f := range findings {
		if f.Level == "error" {
			blocking = true
			break
		}
	}
	httputil.WriteJSON(w, map[string]any{"findings": findings, "blocking": blocking, "git": gf})
}

// pathUnder reports whether path is parent itself or lives under parent
// (a clean-path prefix with a separator boundary; cross-platform via
// filepath.Rel).
func pathUnder(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// handleProjectLifecycle is POST /p/{key}/lifecycle: one endpoint, the
// body's action picks the transition (activate | archive | reactivate).
// A store refusal is a 400; the room-set consequences run in the
// projectArchive/projectOpen helpers below.
func (rc *Face) HandleProjectLifecycle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if rc.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	var (
		p   projects.Project
		err error
	)
	switch body.Action {
	case "activate", "reactivate":
		if body.Action == "activate" {
			p, err = rc.Stores.ProjectStore.Activate(key)
		} else {
			p, err = rc.Stores.ProjectStore.Reactivate(key)
		}
		if err == nil {
			rc.projectOpen(key, p)
		}
	case "archive":
		p, err = rc.Stores.ProjectStore.Archive(key)
		if err == nil {
			rc.projectArchive(key, p)
		}
	default:
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("未知 action：%s（activate | archive | reactivate）", body.Action))
		return
	}
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, p)
}

// projectOpen runs the activate/reactivate consequences: the room
// instantiates NOW (a draft's first open must not wait for a dial — the
// registry listener brings the aggregator along, the keeper set catches
// the project on its next round), the establishment table converges
// onto its system-post floor (编制 is mandatory per project — the rows
// exist from the very first beat, and the recruiter loop births their
// holders right behind them), the fleet hook starts the dispatcher,
// and both rooms hear the news.
func (rc *Face) projectOpen(key string, p projects.Project) {
	hub := rc.Hub
	roomOpen := false
	if rc.Registry != nil {
		if h, err := rc.Registry.Hub(key); err == nil {
			hub = h
			roomOpen = true
			h.System(i18n.Sf("项目房已开张：%s（%s）。历史与任务台账原样都在——开工吧", p.Name, p.Key))
		} else {
			log.Printf("project %s open: room: %v — 房间等首次拨号时再实例化", key, err)
		}
	}
	// 项目编制表就位（必须面）：开张即带齐三行系统岗——编排者/HR 分项
	// 目各一套（不同项目的 HR 互相独立），小助手全工作室唯一。表缺失则
	// 整张补建、缺行则尾行追加；已就位一字不写。编排者/HR 由招聘循环自
	// 动出生到岗（EnsureRoom 尾部点火），缺岗由看护照看。
	if rc.Stores.Docs != nil {
		touched, err := kb.EnsureProjectEstablishment(rc.Stores.Docs, key, "开张")
		switch {
		case err != nil:
			log.Printf("project %s open: establishment: %v（表未带齐系统岗，看护报警口径照旧）", key, err)
		case len(touched) > 0 && roomOpen:
			hub.System(i18n.S("[编制] 项目编制表已就位：系统岗 编排者/HR（必须）已登记，将自动招聘到岗（每项目各一套）——小助手全工作室唯一，随调度常驻"))
		}
	}
	if rc.Fleet != nil {
		rc.Fleet.ProjectActivated(key, hub)
	}
	rc.Hub.System(i18n.Sf("项目已开张：%s（%s）——侧栏出现它的房间，编制与排期就位", p.Name, p.Key))
}

// projectArchive runs the archive consequences, in the order the room
// facts demand: the farewell line lands in history BEFORE the unload,
// the final roster snapshot persists for a future reactivate's
// forensics, the dispatcher retires first (its clocks must not fire
// into a freezing room), then the occupying staffing rows fold to
// offboard, then the registry drops the room.
func (rc *Face) projectArchive(key string, p projects.Project) {
	if rc.Registry != nil {
		if hub, ok := rc.Registry.Rooms()[key]; ok {
			hub.System(i18n.S("项目已归档封存：房间冻结、编制就地离编，任务台账只读保留——复活后历史原样回来"))
			rc.SnapshotTo(rc.Registry.SnapshotPath(key), hub)
		}
	}
	if rc.Fleet != nil {
		rc.Fleet.ProjectArchived(key)
	}
	if rc.Stores.StaffStore != nil {
		for _, e := range rc.Stores.StaffStore.ListByProject(key) {
			if !e.Occupying() {
				continue
			}
			if _, err := rc.Stores.StaffStore.Offboard(key, e.Person); err != nil {
				log.Printf("archive %s: offboard %s: %v", key, e.Person, err)
			}
		}
	}
	if rc.Registry != nil && key != chat.LobbyKey {
		if err := rc.Registry.Unload(key); err != nil {
			log.Printf("archive %s: unload: %v（未实例化的房间本就无可卸载）", key, err)
		}
	}
	rc.Hub.System(i18n.Sf("项目已归档封存：%s（%s）——房间已撤，复活入口在项目管理页", p.Name, p.Key))
}

// handleProjectPatch is PATCH /p/{key}: the content-edit envelope
// (name/desc/versions, the store's per-status workspace boundary
// decides the rest). A refusal is a 400; the edited entity rides back.
func (rc *Face) HandleProjectPatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "PATCH only", http.StatusMethodNotAllowed)
		return
	}
	if rc.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Name        string             `json:"name"`
		Desc        string             `json:"desc"`
		Workspace   string             `json:"workspace"`
		Versions    []projects.Version `json:"versions"`
		GitDisabled *bool              `json:"git_disabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	p, err := rc.Stores.ProjectStore.Update(r.PathValue("key"), projects.Patch{
		Name:        body.Name,
		Desc:        body.Desc,
		Workspace:   body.Workspace,
		Versions:    body.Versions,
		GitDisabled: body.GitDisabled,
	})
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, p)
}
