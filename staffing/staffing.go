// Package staffing is the v2 project staffing table (PRD §4.1): one
// row = one person in one project in one role, persisted agents.Store
// style in ~/.niuma/staffing.json. The person's global
// profile stays in agents; the per-project session binding's single
// source of truth lives HERE. Phase-1 product rule: a person holds at
// most one occupying row at a time (borrowing = leave one, join
// another); the data model itself stays multi-row ready — dropping
// the check is the phase-2 unthrottling.
package staffing

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
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// Entry states. onboard is joining (row written, birth/seating still
// in flight); active is on staff; offboard has left (left_ts stamped).
const (
	StateOnboard  = "onboard"
	StateActive   = "active"
	StateOffboard = "offboard"
)

const (
	maxPersonRunes = 24 // the member-name cap, house style
	maxRoleRunes   = 24 // matches chat's role cap, house style
)

// reasoningPattern is the thinking-intensity token rule — the same
// shape capability.ModelTier enforces on the pack slot, so the seat
// tier accepts exactly what a composed fabric could carry.
var reasoningPattern = regexp.MustCompile(`^[a-z0-9]{1,24}$`)

// Entry is one staffing row: one person, one project, one role. The
// (project_key, person) pair is unique; project_role defaults to the
// profile's Role at the wiring layer (empty here means "not set").
// Skills/MCPs are the seat-level capability assembly — non-empty
// fully replaces the profile defaults (Q18). Model is the seat-level
// model slot: "providerId/modelId" (or a bare model id) chosen at
// birth/招牛马 or re-picked later; empty = no seat pick, the process
// default chain decides.
type Entry struct {
	ProjectKey  string `json:"project_key"`
	Person      string `json:"person"`
	ProjectRole string `json:"project_role,omitempty"`
	// PostKey 是行在本项目编制表的岗位锚（分项目根治的第一键）：
	// 出生/召回按 --post 落键，看护与补员按它对账——身份串降级为
	// 显示文本，「·主笔」尾缀、空 project_role 不再读作缺岗（book
	// 实录：写手/质检全员在座、报警恒 编制 N/在岗 0）。空＝未锚
	// （存量行——对账回退身份串精确匹配，adopt 时按角色一次性锚定
	// 迁移）。离编保留、Join 复职随行（与 ProjectRole 同纪律）。
	PostKey   string   `json:"post_key,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
	Skills    []string `json:"skills,omitempty"`
	MCPs      []string `json:"mcps,omitempty"`
	// LegacyPacks is the retired capability-pack assembly
	// (pre-v2.13): read-only, folded into Skills once at load and
	// never written back once cleared.
	LegacyPacks []string `json:"packs,omitempty"`
	Model       string   `json:"model,omitempty"`
	// Reasoning 是座位级模型档的思考强度（ZCode 的 reasoningLevel，
	// 如 low/high/max）：空＝模型缺省档。模型留空时非空的强度挂在默认
	// 链解析出的模型上（ZCode 拾取器口径——默认模型也是真模型，档照
	// 挑）；两槽皆空＝清档。离编保留、复职随 Join 恢复。
	Reasoning string `json:"reasoning,omitempty"`
	State     string `json:"state"`
	JoinedTS  int64  `json:"joined_ts,omitempty"`
	LeftTS    int64  `json:"left_ts,omitempty"`
	// Branch 是座位级分支槽（v2.7.1 分支隔离）：非空＝该成员隔离——会
	// 话生在自己的专属工作树（~/.niuma/wt/<项目>/<人名>）驻该分支；
	// 空＝共享项目主工作树（现状语义，存量项目零变化）。离编保留
	//（拉回复职即恢复隔离）。
	Branch string `json:"branch,omitempty"`
	// Look 是终身层形象（r_05，t_126）：发色/发型/肤色/饰品——招募时
	// 生成一次落档、终身不变（重置工厂除外）。空＝尚未生成（渲染走
	// 旧的两维名字哈希，兼容存量）。
	Look *Look `json:"look,omitempty"`
}

// Look is the lifetime look layer: the member's physiological facts,
// rolled once at recruitment and never re-rolled (design/r05-avatar
// §一). Shirt/hair COLORS stay where they are (Member.Color/Hair 的
// 名字哈希)——Look 只加形状维度。
type Look struct {
	HairStyle   string    `json:"hair_style"`            // F1–F8（pixart.Hair*）
	Skin        string    `json:"skin"`                  // 3 档肤色 hex
	Accessories []AccItem `json:"accessories,omitempty"` // 0–2 件
}

// AccItem is one accessory: slot + style 库键（t_128 wardrobe.go 的
// Accessories Key）＋color（旧档案兼容，新版档案只填 Style——颜色以
// 库定义为准，服务器渲染侧 Style 优先）。
type AccItem struct {
	Slot  string `json:"slot"` // pixart.Acc*
	Style string `json:"style,omitempty"`
	Color string `json:"color,omitempty"`
}

// Occupying reports whether the row holds a headcount in its project.
// onboard counts: joining occupies the slot from the moment the row
// appears — the phase-1 one-person-one-room rule counts these.
func (e Entry) Occupying() bool {
	return e.State == StateOnboard || e.State == StateActive
}

// Store is the persisted staffing table, agents.Store style: one
// mutex, atomic full-file rewrite, empty path = in-memory only. A
// corrupt file surfaces as an Open error; the caller decides how to
// degrade. The per-project behavior switches (P5-a, settings.go) ride
// in the same store via a companion settings file.
type Store struct {
	mu           sync.Mutex
	path         string
	settingsPath string
	list         []Entry
	settings     map[string]Settings
	rev          uint64 // bumped on every mutation, for change detection
}

// DefaultPath returns the conventional v2 location:
// ~/.niuma/staffing.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma", "staffing.json"), nil
}

// Open loads the store from path. A missing file starts empty.
func Open(path string) (*Store, error) {
	s := &Store{path: path, settingsPath: settingsPathOf(path)}
	if path == "" {
		s.loadSettings()
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.loadSettings()
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i := range s.list {
		s.list[i].foldLegacyPacks()
	}
	s.loadSettings()
	return s, nil
}

// foldLegacyPacks folds the retired packs references into Skills once
// (order: skills first, then packs) and clears the legacy slot so the
// next save drops it. References whose pack never became a skill
// degrade at compose time — the standing missing-key discipline.
func (e *Entry) foldLegacyPacks() {
	if len(e.LegacyPacks) == 0 {
		return
	}
	e.Skills = dedupeKeys(append(e.Skills, e.LegacyPacks...))
	e.LegacyPacks = nil
}

// Join writes the (project, person) row as a fresh onboard entry (the
// 招募 flow's staffing step). An existing offboard row is reinstated
// in place (joined_ts refreshed, left_ts cleared, role/session/packs
// kept where the call passes them empty) — the 归还 flow. Phase-1
// rule: the person must hold no occupying row anywhere, including
// onboard ones.
func (s *Store) Join(projectKey, person, projectRole, sessionID string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	person = sanitizePerson(person)
	projectRole = sanitizeRole(projectRole)
	if !projects.ValidKey(projectKey) {
		return Entry{}, errors.New(i18n.Sf("非法项目 key %q", projectKey))
	}
	if person == "" {
		return Entry{}, errors.New(i18n.S("牛马名不能为空"))
	}
	i := s.findIndexLocked(projectKey, person)
	if i >= 0 && s.list[i].Occupying() {
		return Entry{}, errors.New(i18n.Sf("%s 已在项目 %s 的编制中", person, projectKey))
	}
	if occ, ok := s.occupancyOfLocked(person); ok {
		return Entry{}, errors.New(i18n.Sf("%s 当前已在项目 %s 编制（一期一人一时一房：先离编再入编）", person, occ.ProjectKey))
	}
	e := Entry{ProjectKey: projectKey, Person: person, State: StateOnboard, JoinedTS: util.Now()}
	if projectRole != "" {
		e.ProjectRole = projectRole
	} else if i >= 0 {
		e.ProjectRole = s.list[i].ProjectRole
	}
	// 岗位锚随复职保留：召回（Recall→Birth→Join）不洗掉行已锚定的
	// 岗位——补员对账靠它认人。
	if i >= 0 && s.list[i].PostKey != "" {
		e.PostKey = s.list[i].PostKey
	}
	if sessionID != "" {
		e.SessionID = sessionID
	} else if i >= 0 {
		e.SessionID = s.list[i].SessionID
	}
	if i >= 0 {
		if s.list[i].Skills != nil {
			e.Skills = append([]string(nil), s.list[i].Skills...)
		}
		if s.list[i].MCPs != nil {
			e.MCPs = append([]string(nil), s.list[i].MCPs...)
		}
	}
	if i >= 0 && s.list[i].Model != "" {
		e.Model = s.list[i].Model
		e.Reasoning = s.list[i].Reasoning
	}
	if i >= 0 {
		s.list[i] = e
	} else {
		s.list = append(s.list, e)
	}
	s.rev++
	s.saveLocked()
	return e, nil
}

// MarkActive promotes onboard→active: birth done, seat taken. The
// only other way a row reaches active is Reactivate-style rejoining
// followed by this.
func (s *Store) MarkActive(projectKey, person string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionLocked(projectKey, person, StateOnboard, StateActive,
		i18n.S("仅 onboard 编制可转 active"))
}

// Offboard releases the row (onboard|active→offboard) and stamps
// left_ts — the staffing step of the five-step offboard pipeline,
// scoped to this one project (the global profile archives only when
// every row is offboard).
func (s *Store) Offboard(projectKey, person string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, i, err := s.entryLocked(projectKey, person)
	if err != nil {
		return Entry{}, err
	}
	if e.State == StateOffboard {
		return Entry{}, errors.New(i18n.Sf("%s 在项目 %s 已离编", person, projectKey))
	}
	e.State = StateOffboard
	e.LeftTS = util.Now()
	s.list[i] = e
	s.rev++
	s.saveLocked()
	return e, nil
}

// BindSession records the member's ZCode session binding for this
// project — the binding's single source of truth (agents.SessionID is
// retired in v2). An empty id clears it. Only occupying rows rebind;
// an offboard row's session is history, a returning member carries the
// binding through Join.
func (s *Store) BindSession(projectKey, person, sessionID string) (Entry, error) {
	return s.mutateOccupyingLocked(projectKey, person, func(e *Entry) {
		e.SessionID = sessionID
	})
}

// SetRole overwrites the project role (@组唤醒 and establishment
// reconciliation resolve against it). Only occupying rows rebind —
// same rule as BindSession.
func (s *Store) SetRole(projectKey, person, role string) (Entry, error) {
	role = sanitizeRole(role)
	return s.mutateOccupyingLocked(projectKey, person, func(e *Entry) {
		e.ProjectRole = role
	})
}

// SetPost anchors the row to its establishment post key（岗位锚）：看护
// 与补员按它对账，身份串降级为显示文本。Same occupancy rule as
// SetRole — an offboard row keeps its anchor for the recall path, and
// Join carries it through reinstatement.
func (s *Store) SetPost(projectKey, person, postKey string) (Entry, error) {
	postKey = strings.TrimSpace(postKey)
	if postKey != "" && len([]rune(postKey)) > 64 {
		return Entry{}, errors.New(i18n.Sf("岗位锚非法: %q（≤64 字符）", postKey))
	}
	return s.mutateOccupyingLocked(projectKey, person, func(e *Entry) {
		e.PostKey = postKey
	})
}

// SetSkills overwrites the seat-level skill assembly (Q18: a
// non-empty list wholly replaces the profile default, an empty one
// falls back to it at compose time). Only occupying rows rewrite —
// an offboard row's assembly is history; reinstatement carries it
// through Join. SetMCPs follows the same rule for the server list.
func (s *Store) SetSkills(projectKey, person string, keys []string) (Entry, error) {
	return s.mutateOccupyingLocked(projectKey, person, func(e *Entry) {
		e.Skills = dedupeKeys(keys)
	})
}

// SetMCPs overwrites the seat-level MCP server assembly. Same
// occupancy rule as SetSkills.
func (s *Store) SetMCPs(projectKey, person string, keys []string) (Entry, error) {
	return s.mutateOccupyingLocked(projectKey, person, func(e *Entry) {
		e.MCPs = dedupeKeys(keys)
	})
}

// SetModel overwrites the seat-level model tier: the pick
// ("providerId/modelId" or a bare id; empty = the pack slot ⊕ process
// default chain decides the model) plus its thinking intensity
// (reasoning, the CLI's reasoningLevel vocabulary; empty = the model's
// own default tier). A reasoning on an empty pick rides the chain's
// model — the ZCode picker's shape, where the default model is a real
// model whose tiers stay pickable; clearing both slots clears the
// tier. Same occupancy rule as SetSkills — the pick rides through Join
// on reinstatement.
func (s *Store) SetModel(projectKey, person, model, reasoning string) (Entry, error) {
	model = strings.TrimSpace(model)
	reasoning = strings.TrimSpace(reasoning)
	if model != "" && (len([]rune(model)) > 160 || strings.ContainsAny(model, " \t")) {
		return Entry{}, errors.New(i18n.Sf("座位模型非法: %q（≤160 字符，不含空白）", model))
	}
	if reasoning != "" && !reasoningPattern.MatchString(reasoning) {
		return Entry{}, errors.New(i18n.Sf("思考强度非法: %q（^[a-z0-9]{1,24}$，空=模型缺省档）", reasoning))
	}
	return s.mutateOccupyingLocked(projectKey, person, func(e *Entry) {
		e.Model, e.Reasoning = model, reasoning
	})
}

// SetBranch overwrites the seat-level branch slot（v2.7.1 分支隔离）：
// empty = clear（回共享主工作树）。名字过 vcs.ValidRefName（Unicode
// 放宽版——中文成员名即分支名）。Same occupancy rule as SetModel —
// the pick rides through offboard/recall。
func (s *Store) SetBranch(projectKey, person, branch string) (Entry, error) {
	branch = strings.TrimSpace(branch)
	if branch != "" && !vcs.ValidRefName(branch) {
		return Entry{}, vcs.RefNameError("分支名", branch)
	}
	return s.mutateOccupyingLocked(projectKey, person, func(e *Entry) {
		e.Branch = branch
	})
}

// dedupeKeys normalizes an asset-key list: trimmed, empties dropped,
// first occurrence kept. Assembly order is load-bearing (Compose
// concatenates in order), so no sorting.
func dedupeKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// transitionLocked moves the (project, person) row from state from to
// to, refusing anything else. Callers hold s.mu.
func (s *Store) transitionLocked(projectKey, person, from, to, refusal string) (Entry, error) {
	e, i, err := s.entryLocked(projectKey, person)
	if err != nil {
		return Entry{}, err
	}
	if e.State != from {
		return Entry{}, errors.New(i18n.Sf("%s（%s 在 %s 当前 %s）", refusal, person, projectKey, e.State))
	}
	e.State = to
	s.list[i] = e
	s.rev++
	s.saveLocked()
	return e, nil
}

// mutateOccupyingLocked applies fn to the row and persists, requiring
// the row to be occupying (onboard/active). Callers hold s.mu.
func (s *Store) mutateOccupyingLocked(projectKey, person string, fn func(*Entry)) (Entry, error) {
	e, i, err := s.entryLocked(projectKey, person)
	if err != nil {
		return Entry{}, err
	}
	if !e.Occupying() {
		return Entry{}, errors.New(i18n.Sf("%s 在项目 %s 已离编（重新入编时携带新值）", person, projectKey))
	}
	fn(&e)
	s.list[i] = e
	s.rev++
	s.saveLocked()
	return e, nil
}

// RemoveRow deletes the (project, person) row outright — the bulk
// dismiss/reset half. Unlike Offboard this leaves no history row: the
// person's chapter in THIS project is over (「清退」是删除不是离编
// 留档); rows the person holds elsewhere, if any, stand untouched.
// Reports whether a row existed.
func (s *Store) RemoveRow(projectKey, person string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findIndexLocked(projectKey, sanitizePerson(person))
	if i < 0 {
		return false
	}
	s.list = append(s.list[:i], s.list[i+1:]...)
	s.rev++
	s.saveLocked()
	return true
}

// Get returns one row by exact (project, person) pair.
func (s *Store) Get(projectKey, person string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.findIndexLocked(projectKey, sanitizePerson(person)); i >= 0 {
		return s.list[i], true
	}
	return Entry{}, false
}

// List returns a copy of every row in insertion order (all states —
// the raw store view).
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.list...)
}

// ListByProject returns the rows of one project, all states.
func (s *Store) ListByProject(projectKey string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, 4)
	for _, e := range s.list {
		if e.ProjectKey == projectKey {
			out = append(out, e)
		}
	}
	return out
}

// ListByPerson returns one person's rows across projects, all states —
// the person-page "在哪些项目任何职" join's staffing half.
func (s *Store) ListByPerson(person string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	person = sanitizePerson(person)
	out := make([]Entry, 0, 2)
	for _, e := range s.list {
		if e.Person == person {
			out = append(out, e)
		}
	}
	return out
}

// OccupancyOf returns the person's occupying row, if any — the phase-1
// ≤1 invariant exposed as a query (Join enforces it; this is how the
// rest of the system reads it).
func (s *Store) OccupancyOf(person string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.occupancyOfLocked(sanitizePerson(person))
	return e, ok
}

// Rev returns the mutation counter; callers use it to detect changes
// without comparing row bodies.
func (s *Store) Rev() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

func (s *Store) occupancyOfLocked(person string) (Entry, bool) {
	for _, e := range s.list {
		if e.Person == person && e.Occupying() {
			return e, true
		}
	}
	return Entry{}, false
}

func (s *Store) entryLocked(projectKey, person string) (Entry, int, error) {
	person = sanitizePerson(person)
	i := s.findIndexLocked(projectKey, person)
	if i < 0 {
		return Entry{}, -1, errors.New(i18n.Sf("编制行不存在: %s 在 %s", person, projectKey))
	}
	return s.list[i], i, nil
}

func (s *Store) findIndexLocked(projectKey, person string) int {
	for i, e := range s.list {
		if e.ProjectKey == projectKey && e.Person == person {
			return i
		}
	}
	return -1
}

// saveLocked writes the JSON snapshot atomically (temp file beside the
// target, then rename), creating the parent directory on first save.
// Persistence failures are logged but do not abort the in-memory
// mutation.
func (s *Store) saveLocked() {
	if s.path == "" {
		return
	}
	if err := persist.SaveJSON(s.path, s.list, 0o644); err != nil {
		log.Printf("save staffing file: %v", err)
	}
}

// sanitizePerson strips control characters, trims and clamps a person
// name — the member-name discipline (names are single-line ≤24 runes
// everywhere in the house).
func sanitizePerson(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		out = append(out, r)
	}
	s := strings.TrimSpace(string(out))
	if r := []rune(s); len(r) > maxPersonRunes {
		s = string(r[:maxPersonRunes])
	}
	return s
}

// sanitizeRole clamps a role: control characters become spaces, the
// ends trim (a role must match exactly for @组唤醒), then clamp.
func sanitizeRole(role string) string {
	out := make([]rune, 0, len(role))
	for _, r := range role {
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		out = append(out, r)
	}
	s := strings.TrimSpace(string(out))
	if r := []rune(s); len(r) > maxRoleRunes {
		return string(r[:maxRoleRunes])
	}
	return s
}

// ── 终身层生成器（r_05 §二/§六，t_126）──────────────────────────────

// hairColorTable 是发色概率表（§二）：黑 99%＋9 稀有色 1%。
// 权重单位 1/10000。
var hairColorTable = []struct {
	Hex       string
	PerMyriad int
}{
	{"#3b2f2f", 9900}, // 黑（深棕黑，现有基色）
	{"#7a4a2b", 30},   // 棕
	{"#a8763e", 25},   // 深栗
	{"#57534e", 15},   // 灰
	{"#d9b380", 10},   // 亚麻金
	{"#8c3b3b", 8},    // 酒红
	{"#e8e4de", 6},    // 银白
	{"#2f3b46", 3},    // 蓝黑
	{"#e89bb0", 2},    // 粉
	{"#4a7d52", 1},    // 绿
}

// hairStylePool 是发型池（§二；t_128 并轨后键空间扩到 wardrobe.go 的
// 50 款）：F1–F8 锚点沿用设计稿权重；三轴网格派生款（f#s#v# 键）与
// 背面变体款各分小权重——50 款人人可抽，锚点款仍占多数保辨识基线。
var hairStylePool = []struct {
	Key    string
	Weight int
}{
	{"F1", 26}, // standard 圆盖头
	{"F2", 10}, // fringe 留海
	{"F3", 10}, // sidepart 分缝
	{"F4", 8},  // buzz 削薄
	{"F5", 8},  // bob 鬓角
	{"F6", 6},  // ponytail 马尾
	{"F7", 6},  // spiky 尖刺
	{"F8", 8},  // long 长发
	{"ponytail-fringe", 1}, {"ponytail-side", 1}, {"ponytail-full", 1}, {"ponytail-longside", 1},
	{"long-fringe", 1}, {"long-flat", 1}, {"long-full", 1},
	{"spiky-fringe", 1}, {"spiky-side", 1},
	{"buzz-longside", 1},
	{"f0s0v2", 1}, {"f0s1v0", 1}, {"f0s1v2", 1}, {"f0s2v0", 1}, {"f0s2v2", 1},
	{"f1s1v0", 1}, {"f1s1v2", 1}, {"f1s2v1", 1},
	{"f2s0v2", 1}, {"f2s1v0", 1}, {"f2s2v1", 1},
	{"f3s1v1", 1}, {"f3s2v0", 1},
}

// skinTones 3 档肤色（§一）。
var skinTones = []string{"#f0c8a0", "#e8b88c", "#d9a878"}

// accessoryPool 饰品池（§三挂点，抽取概率 0–2 件；t_128 并轨后条目＝
// wardrobe.go 的 52 件库选样，Style 存库键、Color 由库定义——AccItem
// 不再自带颜色语义，但字段保留兼容存量档案）。
var accessoryPool = []struct {
	Slot    string
	Style   string
	PerCent int // 单件佩戴概率
}{
	{"glasses", "glasses-box-black", 12},
	{"glasses", "glasses-round-black", 6},
	{"glasses", "glasses-rimless-gold", 4},
	{"earring", "earring-gold-stud", 6},
	{"earring", "earring-silver-stud", 4},
	{"hairpin", "clip-blue", 4},
	{"hairpin", "clip-red", 4},
	{"hairpin", "tie-band-black", 3},
	{"tie", "tie-navy", 4},
	{"scarf", "scarf-red", 3},
	{"badge", "badge-blue", 5},
}

// RollLook rolls one member's lifetime layer (发色/发型/肤色/饰品).
// seed 作确定性输入（名字）——同名字重放同结果，便于测试与锦定。
func RollLook(name string) *Look {
	h := fnv32(name)
	rng := func(n uint32) uint32 { h = h*1664525 + 1013904223; return h % n }
	// 发型：加权池（基表＋插件贡献的并集快照，lookaddon.go）
	pool := styleRollPool()
	hs := 0
	for _, p := range pool {
		hs += p.Weight
	}
	pick := rng(uint32(hs))
	var style string
	for _, p := range pool {
		if pick < uint32(p.Weight) {
			style = p.Key
			break
		}
		pick -= uint32(p.Weight)
	}
	// 肤色：3 档
	skin := skinTones[rng(3)]
	// 饰品：逐款独立掷概率，至多 2 件（同挂点天然互斥——池内同挂点
	// 款式互斥由「每挂点已中即跳过」保证）
	var acc []AccItem
	slotHit := map[string]bool{}
	for _, a := range accessoryPool {
		if len(acc) >= 2 {
			break
		}
		if slotHit[a.Slot] {
			continue
		}
		if rng(100) < uint32(a.PerCent) {
			acc = append(acc, AccItem{Slot: a.Slot, Style: a.Style})
			slotHit[a.Slot] = true
		}
	}
	return &Look{HairStyle: style, Skin: skin, Accessories: acc}
}

// RollHairColor 按发色概率表掷一发色（黑 99%＋9 稀有色 1%）。
// 供 joinLocked 首次入座时替换名字哈希发色（Hair 字段语义升级为
// 「终身层真源」，但值域向后兼容 hairPalette 的现有 8 色＋2 稀有）。
func RollHairColor(name string) string {
	h := fnv32(name + ":hair")
	table := colorRollTable() // 基表＋插件贡献的并集快照（lookaddon.go）
	total := 0
	for _, t := range table {
		total += t.PerMyriad
	}
	pick := h % uint32(total)
	for _, t := range table {
		if pick < uint32(t.PerMyriad) {
			return t.Hex
		}
		pick -= uint32(t.PerMyriad)
	}
	return table[0].Hex
}

// AnchoredLook 为存量成员锦定观感（§六·4）：发型选最接近现状款、
// 肤色继承现有基色、饰品从轻——上线日无人「突然换脸」。
func AnchoredLook(style string) *Look {
	return &Look{HairStyle: style, Skin: "#f0c8a0"}
}

// fnv32 是名字哈希（与 chat.paletteFor 的稳定哈希语义一致——但独立
// 实现避免包依赖）。
func fnv32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// SetLook 落档终身层（幂等：已有 Look 不覆盖——「终身锁定」由调用
// 侧保证只在新成员首次入座时调）。
func (s *Store) SetLook(projectKey, person string, look *Look) (Entry, error) {
	return s.mutateOccupyingLocked(projectKey, person, func(e *Entry) {
		if e.Look == nil && look != nil {
			e.Look = look
		}
	})
}
