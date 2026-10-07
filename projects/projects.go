// Package projects is the v2 project registry (PRD §4.2): Project
// entities — key slug + Version light entities + the
// draft→active→archived lifecycle — persisted agents.Store style in
// ~/.niuma/projects.json. Pure model layer (P1): nothing
// wires into server/cli/ui yet; per-project Hub instantiation is P3.
package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// Project lifecycle states. draft is recorded-but-not-open (no room,
// no staffing, no tasks); active is open for business; archived is
// frozen — revivable, or deletable by the explicit Delete verb (the
// registry row dies; the workspace and its git history never do).
const (
	StatusDraft    = "draft"
	StatusActive   = "active"
	StatusArchived = "archived"
)

// LobbyName is the lobby's display name; legacyLobbyName is the v1
// display name EnsureLobby upgrades from (a lobby still carrying it was
// never renamed by hand — the rename entry point rewrites the store,
// and a hand-edited custom name is left alone).
const (
	LobbyName       = "Niuma_Studio"
	legacyLobbyName = "大厅" // v1 显示名，仅供 EnsureLobby 迁移匹配
)

// LobbyKey is the reserved key of the default project (the lobby): it
// is born active with a first-boot-frozen workspace and can never be
// archived — the Ebiten window, the mirror channel and the CLI default
// route all anchor on it.
const LobbyKey = "default"

// Field limits (PRD §4.2 / model §4.1). MaxProjects is the ★B5
// miscreation cap.
const (
	MaxProjects         = 32
	maxNameRunes        = 48
	maxDescRunes        = 2000
	maxVersionNameRunes = 48
	maxByRunes          = 24 // created_by: the member-name cap, house style
)

var keyPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// ValidKey reports whether key is a legal project slug: url- and
// filename-safe [a-z0-9_-], 1–32 runes. The key is the immutable
// primary key — history/inbox/staffing/tasks all foreign-key on it,
// renaming it would be changing the project's identity.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// Version is a light entity inside one project (M1): "a name with a
// start and an end" — no id of its own; tasks attach via project_key +
// version name. Windows are Unix seconds; end_ts 0 = open-ended.
type Version struct {
	Name    string `json:"name"`
	StartTS int64  `json:"start_ts,omitempty"`
	EndTS   int64  `json:"end_ts,omitempty"`
	Goal    string `json:"goal,omitempty"`
}

// Valid checks the version's own fields. Name uniqueness is a
// project-level check (Project.Validate), not the version's.
func (v Version) Valid() error {
	if n := len([]rune(v.Name)); n == 0 {
		return errors.New(i18n.S("版本名不能为空"))
	} else if n > maxVersionNameRunes {
		return errors.New(i18n.Sf("版本名超长：%d rune（上限 %d）", n, maxVersionNameRunes))
	}
	if v.EndTS != 0 && v.StartTS > v.EndTS {
		return errors.New(i18n.Sf("版本 %s 的窗口起点晚于终点", v.Name))
	}
	return nil
}

// GitPlan is the 立项-time git bootstrap plan (v2.14): what the studio
// was asked to set up when the project was created. InitIfMissing +
// BaseBranch drive the create-time bootstrap (auto git init with one
// empty root commit, and the baseline every branch cuts from);
// AutoSeatBranch lives on for the seat face — a member assigned a branch
// that doesn't exist yet gets it cut from BaseBranch instead of a
// refusal. The whole family is additive-only: the studio never deletes
// branches or worktrees, and never touches a repository other than the
// one rooted exactly at the project's workspace.
type GitPlan struct {
	InitIfMissing  bool   `json:"init_if_missing,omitempty"`  // 工作区无仓库时自动 git init（含空首提交）
	BaseBranch     string `json:"base_branch,omitempty"`      // 基线分支（空＝main；引导与座位建枝的出发点）
	AutoSeatBranch bool   `json:"auto_seat_branch,omitempty"` // 成员设分支时，缺失分支从基线自动新建
	// AutoSeat（主动档）：成员出生（及开关打开时的存量在编成员）自动
	// 各驻专属枝 wt/<人名>——分支自基线切出、会话落专属工作树。与
	// AutoSeatBranch（被动档：设分支时缺失才补）互补，缺省 false。
	AutoSeat bool `json:"auto_seat,omitempty"`
}

// BaseOrDefault is the plan's effective baseline: the stored name, or
// the conventional "main" when absent. nil-safe（无计划也是 main）。
func (g *GitPlan) BaseOrDefault() string {
	if g == nil || g.BaseBranch == "" {
		return "main"
	}
	return g.BaseBranch
}

// Project is one project entry. Workspace is where member sessions are
// born; it locks the moment the project activates (moving it would
// move live session contexts — §3.2's "会话永不跨项目搬迁").
type Project struct {
	Key         string                `json:"key"`
	Name        string                `json:"name"`
	Workspace   string                `json:"workspace"`
	Desc        string                `json:"desc,omitempty"`
	Status      string                `json:"status"`
	Versions    []Version             `json:"versions,omitempty"`
	CreatedBy   string                `json:"created_by,omitempty"`
	CreatedTS   int64                 `json:"created_ts,omitempty"`
	BranchLinks map[string]BranchLink `json:"branch_links,omitempty"` // v2.7 版本管理：分支↔需求/版本绑定（key=分支名）
	GitPolicy   *vcs.Policy           `json:"git_policy,omitempty"`   // v2.7 版本管理：commit 规范策略（nil=默认档）
	GitPlan     *GitPlan              `json:"git_plan,omitempty"`     // v2.14 立项 git 引导计划（nil=未引导）
	// GitDisabled（两级门顶层）：true＝该项目的 git 版本管理整面关闭
	//（面板只余「已关闭」卡、全部 git 写面 409、座位建枝/合并流/提交播
	// 报全停）。缺省 false＝开启——存量项目零迁移零变化；缺省开的另一
	// 层理由：git 面本来就只看「工作区是不是仓库」，关与不开是新给的
	// 显式控制，不是既有行为的缺省。
	GitDisabled bool `json:"git_disabled,omitempty"`
}

// Validate checks the entity's own fields: key slug, non-empty name,
// absolute workspace, and a per-project-unique well-formed version
// list. The lifecycle status is the state machine's business
// (Activate/Archive/Reactivate), not this check's.
func (p Project) Validate() error {
	if !ValidKey(p.Key) {
		return errors.New(i18n.Sf("非法项目 key %q（[a-z0-9_-]{1,32}，创建后不可改名）", p.Key))
	}
	if n := len([]rune(p.Name)); n == 0 {
		return errors.New(i18n.S("项目名称不能为空"))
	} else if n > maxNameRunes {
		return errors.New(i18n.Sf("项目名称超长：%d rune（上限 %d）", n, maxNameRunes))
	}
	if n := len([]rune(p.Desc)); n > maxDescRunes {
		return errors.New(i18n.Sf("项目描述超长：%d rune（上限 %d）", n, maxDescRunes))
	}
	if !filepath.IsAbs(p.Workspace) {
		return errors.New(i18n.Sf("workspace 必须是绝对路径: %q", p.Workspace))
	}
	if p.GitPlan != nil && p.GitPlan.BaseBranch != "" && !vcs.ValidRefName(p.GitPlan.BaseBranch) {
		return vcs.RefNameError(i18n.S("基线分支名"), p.GitPlan.BaseBranch)
	}
	seen := make(map[string]struct{}, len(p.Versions))
	for _, v := range p.Versions {
		if err := v.Valid(); err != nil {
			return fmt.Errorf("%s: %w", i18n.Sf("项目 %s", p.Key), err)
		}
		if _, dup := seen[v.Name]; dup {
			return errors.New(i18n.Sf("项目 %s 的版本名重复: %s", p.Key, v.Name))
		}
		seen[v.Name] = struct{}{}
	}
	return nil
}

// sanitize clamps the prose fields (the house style clamps display
// text, rejects identifiers) and normalizes workspace. Key and version
// names are never rewritten — they are validated or rejected.
func (p Project) sanitize() Project {
	p.Name = clampRunes(strings.TrimSpace(p.Name), maxNameRunes)
	p.Desc = clampRunes(strings.TrimSpace(p.Desc), maxDescRunes)
	p.Workspace = strings.TrimSpace(p.Workspace)
	p.CreatedBy = sanitizeBy(p.CreatedBy)
	for i, v := range p.Versions {
		v.Goal = clampRunes(strings.TrimSpace(v.Goal), maxDescRunes)
		p.Versions[i] = v
	}
	return p
}

// Store is the persisted project list, agents.Store style: one mutex,
// atomic full-file rewrite (temp+rename), empty path = in-memory only.
// A corrupt file surfaces as an Open error; how to degrade is the
// caller's call (the v1 mains log-and-fallback / backup-and-reset).
type Store struct {
	mu   sync.Mutex
	path string
	list []Project
	rev  uint64 // bumped on every mutation, for change detection
}

// DefaultPath returns the conventional v2 location:
// ~/.niuma/projects.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma", "projects.json"), nil
}

// RootDir returns the v2 single-root directory ~/.niuma —
// the parent of projects.json and the home of the per-room persistence
// slots (history/<key>.jsonl, room/<key>.json, P3-a).
func RootDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma"), nil
}

// degradedWorkspace is stableWorkspace's last-resort member workspace
// (~/.niuma/workspace, main's fallback when neither the cwd nor the
// bundle location yields a home). As a store value it is always that
// fallback's residue, never a deliberate freeze — EnsureLobby's repair
// treats it exactly like root poison. "" when even the root can't be
// resolved (nothing to compare against).
func degradedWorkspace() string {
	dir, err := RootDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "workspace")
}

// Open loads the store from path. A missing file starts empty.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// Create registers a new project in draft (M1 draft actions: create
// always lands draft; activate is the only way out of it). Initial
// versions may ride along. The store stamps Status and CreatedTS; the
// key is immutable from here on. Workspace guards (v2.13): a filesystem
// root is refused outright (the lobby's own repair machinery documents
// what a root-homed project costs — members write files all over the
// tree), and a folder already held by a NON-archived project is refused
// too (the v2.9 isolation table maps workspace→project one-to-one;
// two live projects on one folder would silently rebind and mix member
// sessions). Archived holders don't count — their folder is legitimately
// reusable for the archive→recreate migration path.
func (s *Store) Create(p Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p = p.sanitize().clone() // 入账的 links map 不与调用者手里的那份共享
	p.Status = StatusDraft
	p.CreatedTS = util.Now()
	if err := p.Validate(); err != nil {
		return Project{}, err
	}
	if s.findIndexLocked(p.Key) >= 0 {
		return Project{}, errors.New(i18n.Sf("项目 key 已存在: %s", p.Key))
	}
	if len(s.list) >= MaxProjects {
		return Project{}, errors.New(i18n.Sf("项目数已达上限 %d 个", MaxProjects))
	}
	if isRootPath(p.Workspace) {
		return Project{}, errors.New(i18n.Sf("workspace 不能是文件系统根目录（%s）——成员会在整棵树上读写文件，请指定本项目的专用目录", p.Workspace))
	}
	if other, dup := s.workspaceConflictLocked(p.Workspace, p.Key); dup {
		return Project{}, errors.New(i18n.Sf("workspace 已被项目 %s 占用——两个在册项目共用一个目录，成员会话会互相踩踏（目录与项目是一对一的隔离绑定）", other))
	}
	s.list = append(s.list, p)
	s.rev++
	s.saveLocked()
	return p.clone(), nil
}

// isRootPath reports whether ws is a filesystem root ("/", "C:\") — the
// directory that is its own parent. EnsureLobby's root-poison repair
// documents the cost of homing members there; Create/Update refuse it up
// front so the poison can never be registered deliberately. (Only the
// user-facing write paths check this — Validate stays fs-agnostic and
// the lobby's boot-time repair keeps its own machinery.)
func isRootPath(ws string) bool {
	return ws != "" && filepath.Dir(ws) == ws
}

// workspaceConflictLocked reports the non-archived project already
// living on workspace ws (selfKey excluded). Callers hold s.mu.
func (s *Store) workspaceConflictLocked(ws, selfKey string) (string, bool) {
	for _, p := range s.list {
		if p.Key == selfKey || p.Status == StatusArchived {
			continue
		}
		if p.Workspace == ws {
			return p.Key, true
		}
	}
	return "", false
}

// Patch is the project-content edit envelope (house Patch idiom):
// zero fields keep the stored value. Versions is nil = keep, non-nil
// (including empty) = replace the whole list. The key is never
// editable. Edit rights follow the status: draft edits everything but
// the key; active edits content but NOT workspace; archived edits
// nothing.
type Patch struct {
	Name      string    `json:"name,omitempty"`
	Desc      string    `json:"desc,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
	Versions  []Version `json:"versions,omitempty"`
	// GitDisabled（两级门顶层，指针区分「没送」与 false）：编辑项目面
	// 的总开关镜像——细项（基线/自动建枝）仍归 git/settings 写面。
	GitDisabled *bool `json:"git_disabled,omitempty"`
}

// Update applies a content patch under the per-status edit boundary.
// A rejected patch leaves the store untouched.
func (s *Store) Update(key string, p Patch) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findIndexLocked(key)
	if i < 0 {
		return Project{}, errors.New(i18n.Sf("项目不存在: %s", key))
	}
	cur := s.list[i]
	switch cur.Status {
	case StatusArchived:
		return Project{}, errors.New(i18n.Sf("项目 %s 已归档，全只读", key))
	case StatusActive:
		if p.Workspace != "" {
			return Project{}, errors.New(i18n.Sf("项目 %s 已开张，workspace 不可改（会话已开在该路径上；确需迁移＝归档后另建）", key))
		}
	}
	// Workspace guards mirror Create's (v2.13): only a CHANGING workspace
	// can newly collide — a patch restating the current value passes.
	if ws := strings.TrimSpace(p.Workspace); ws != "" && ws != cur.Workspace {
		if isRootPath(ws) {
			return Project{}, errors.New(i18n.Sf("workspace 不能是文件系统根目录（%s）——成员会在整棵树上读写文件，请指定本项目的专用目录", ws))
		}
		if other, dup := s.workspaceConflictLocked(ws, key); dup {
			return Project{}, errors.New(i18n.Sf("workspace 已被项目 %s 占用——两个在册项目共用一个目录，成员会话会互相踩踏（目录与项目是一对一的隔离绑定）", other))
		}
	}
	if p.Name != "" {
		cur.Name = clampRunes(strings.TrimSpace(p.Name), maxNameRunes)
	}
	if p.Desc != "" {
		cur.Desc = clampRunes(strings.TrimSpace(p.Desc), maxDescRunes)
	}
	if p.Workspace != "" {
		cur.Workspace = strings.TrimSpace(p.Workspace)
	}
	if p.Versions != nil {
		cur.Versions = p.Versions
	}
	if p.GitDisabled != nil {
		cur.GitDisabled = *p.GitDisabled
	}
	cur = cur.sanitize()
	if err := cur.Validate(); err != nil {
		return Project{}, err
	}
	s.list[i] = cur
	s.rev++
	s.saveLocked()
	return cur.clone(), nil
}

// Activate opens the project (draft→active) — the room instantiates,
// staffing may join (P3 wiring). Only draft projects activate.
func (s *Store) Activate(key string) (Project, error) {
	return s.transitionLocked(key, StatusDraft, StatusActive,
		i18n.S("仅 draft 项目可开张"))
}

// Archive freezes the project (active→archived): room frozen, staffing
// released, tasks sealed (P3/P4 wiring). The lobby can never be
// archived; draft projects do not archive either — they never opened,
// there is nothing to freeze (an abandoned draft just sits in the
// registry at zero cost).
func (s *Store) Archive(key string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findIndexLocked(key)
	if i < 0 {
		return Project{}, errors.New(i18n.Sf("项目不存在: %s", key))
	}
	if key == LobbyKey {
		return Project{}, errors.New(i18n.S("Niuma_Studio 不可归档（进程常驻房：黑板窗、mirror 通道、CLI 缺省路由都锚定它）"))
	}
	return s.transitionLocked(key, StatusActive, StatusArchived,
		i18n.S("仅 active 项目可归档（draft 未开张不经归档）"))
}

// Reactivate revives an archived project (archived→active) — the
// archive is a freeze, not a delete.
func (s *Store) Reactivate(key string) (Project, error) {
	return s.transitionLocked(key, StatusArchived, StatusActive,
		i18n.S("仅 archived 项目可复活"))
}

// Delete removes the project's registry row entirely — the one
// irreversible lifecycle verb (archive is the reversible one). Only a
// draft (never opened) or archived (frozen, crowd already released)
// project deletes: an active project must archive first, which retires
// its room and folds its staffing — delete then only sweeps residue.
// The lobby is process-constant and refuses. Deletion is the REGISTRY
// row's death only: the workspace directory, the git repository,
// branches and worktrees all stay untouched — the studio never deletes
// the user's files (the git-plan red line); re-registering the same
// folder under a new key is legal afterwards.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == LobbyKey {
		return errors.New(i18n.S("Niuma_Studio 不可删除（进程常驻房：像素窗、mirror 通道、CLI 缺省路由都锚定它）"))
	}
	i := s.findIndexLocked(key)
	if i < 0 {
		return errors.New(i18n.Sf("项目不存在: %s", key))
	}
	switch s.list[i].Status {
	case StatusActive:
		return errors.New(i18n.Sf("项目 %s 开张中——先归档（房间冻结、成员离编）再删除", key))
	case StatusDraft, StatusArchived:
	default:
		return errors.New(i18n.Sf("项目 %s 状态异常（%s）——不可删除", key, s.list[i].Status))
	}
	s.list = append(s.list[:i], s.list[i+1:]...)
	s.rev++
	s.saveLocked()
	return nil
}

// EnsureLobby guarantees the default project exists and is active (v2
// P3-a boot): the lobby is BORN active — the process's own常驻房 has
// no pre-room stage — with its workspace frozen on first boot (later
// boots keep the stored value; an active workspace is immutable
// anyway). The lobby bypasses the MaxProjects cap: it is
// process-constant and boot-critical, not a created project.
// Idempotent; returns the lobby entry.
func (s *Store) EnsureLobby(workspace string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.findIndexLocked(LobbyKey); i >= 0 {
		p := s.list[i]
		if p.Status != StatusActive {
			// A lobby sitting in draft (a hand-registered key) or archived
			// (only a hand-edited file can do that — Archive refuses it) is
			// forced back: the lobby is process-constant.
			p.Status = StatusActive
			s.list[i] = p
			s.rev++
			s.saveLocked()
		}
		// Display-name upgrade: a lobby still carrying the legacy 「大厅」
		// title takes the Niuma_Studio name (the lobby IS the studio's own
		// project now). Only the exact legacy title migrates — a renamed
		// lobby is a deliberate user pick and stays.
		if p.Name == legacyLobbyName {
			p.Name = LobbyName
			s.list[i] = p
			s.rev++
			s.saveLocked()
		}
		// Root-poison repair: a lobby frozen at the filesystem root (or
		// empty — a hand-mangled file) is never a deliberate pick; it is
		// the residue of a GUI launch, whose process cwd IS "/". Members
		// homed there write files at the root and ZCode grows a stray
		// "/" project, so the freeze yields and rewrites to the caller's
		// resolved workspace. The DEGRADED residue — stableWorkspace's
		// own ~/.niuma/workspace fallback — repairs by the same rule:
		// it is always our artifact, never a user pick, and without this
		// clause a once-degraded lobby would freeze there forever even
		// after a bundle launch can name the real office home. Every
		// other frozen value stays immutable.
		if ws := strings.TrimSpace(workspace); p.Workspace == "/" || p.Workspace == "" || p.Workspace == degradedWorkspace() {
			if ws != "" && ws != "/" && ws != degradedWorkspace() && filepath.IsAbs(ws) {
				p.Workspace = ws
				s.list[i] = p
				s.rev++
				s.saveLocked()
			}
		}
		return p.clone(), nil
	}
	p := Project{
		Key:       LobbyKey,
		Name:      LobbyName,
		Workspace: strings.TrimSpace(workspace),
		Status:    StatusActive,
		CreatedTS: util.Now(),
	}
	p = p.sanitize()
	if err := p.Validate(); err != nil {
		return Project{}, fmt.Errorf("%s: %w", i18n.S("Niuma_Studio 初始实体非法（workspace 需绝对路径）"), err)
	}
	s.list = append(s.list, p)
	s.rev++
	s.saveLocked()
	return p.clone(), nil
}

// transitionLocked moves key from status from to to. Callers hold
// s.mu (Archive takes the lock itself to front the lobby guard).
func (s *Store) transitionLocked(key, from, to, refusal string) (Project, error) {
	i := s.findIndexLocked(key)
	if i < 0 {
		return Project{}, errors.New(i18n.Sf("项目不存在: %s", key))
	}
	p := s.list[i]
	if p.Status != from {
		return Project{}, errors.New(i18n.Sf("%s（%s 当前 %s）", refusal, key, p.Status))
	}
	p.Status = to
	s.list[i] = p
	s.rev++
	s.saveLocked()
	return p.clone(), nil
}

// CurrentVersion is the schedule-side "current version" judgment
// (v2 P4-a, orchestration §3.4): the version whose window holds now;
// when none does, the nearest future version (smallest start_ts ahead);
// nil when nothing qualifies — the gantt's default view anchor and the
// patrol clock's shared context. Containing windows resolve in list
// order (overlapping windows are a modeling error the store's own
// checks leave to the operator).
func CurrentVersion(p Project, now int64) *Version {
	var future *Version
	for i := range p.Versions {
		v := p.Versions[i]
		if v.StartTS <= now && (v.EndTS == 0 || now <= v.EndTS) {
			return &p.Versions[i]
		}
		if v.StartTS > now && (future == nil || v.StartTS < future.StartTS) {
			future = &p.Versions[i]
		}
	}
	return future
}

// Get returns one project by exact key.
func (s *Store) Get(key string) (Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.findIndexLocked(key); i >= 0 {
		return s.list[i].clone(), true
	}
	return Project{}, false
}

// List returns a copy of the projects in insertion order (all states —
// the raw store view).
func (s *Store) List() []Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Project, 0, len(s.list))
	for _, p := range s.list {
		out = append(out, p.clone())
	}
	return out
}

// Exists reports whether the key is registered — the task-side
// project_key attachment check consumes this (P4 wiring).
func (s *Store) Exists(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findIndexLocked(key) >= 0
}

// Rev returns the mutation counter; callers use it to detect changes
// without comparing project bodies.
func (s *Store) Rev() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

func (s *Store) findIndexLocked(key string) int {
	for i, p := range s.list {
		if p.Key == key {
			return i
		}
	}
	return -1
}

// saveLocked writes the JSON snapshot atomically (temp file beside the
// target, then rename — chat/history.go style), creating the parent
// directory on first save. Persistence failures are logged but do not
// abort the in-memory mutation.
func (s *Store) saveLocked() {
	if s.path == "" {
		return
	}
	b, err := json.MarshalIndent(s.list, "", "  ")
	if err == nil {
		err = persist.Save(s.path, b, 0o644)
	}
	if err != nil {
		log.Printf("save projects file: %v", err)
	}
}

// sanitizeBy clamps a provenance name (created_by): single line,
// trimmed, ≤24 runes — the member-name cap, house style.
func sanitizeBy(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		out = append(out, r)
	}
	s := strings.TrimSpace(string(out))
	if r := []rune(s); len(r) > maxByRunes {
		s = string(r[:maxByRunes])
	}
	return s
}

func clampRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}
