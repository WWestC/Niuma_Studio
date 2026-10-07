// Package plugins implements the Niuma plugin system (v3 preview):
// manifest-driven plugins installed under ~/.niuma/plugins/. Form A
// (data plugins: wardrobe contributions) and form B (frontend ES-module
// boards) ride the same manifest; the host never recompiles for a
// plugin (Go buildmode=plugin is off the table — the app ships five
// platforms including Windows). Scheduling internals (dispatch /
// autopilot / fleet) are outside the plugin surface by design.
package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// APIVersion is the host-side plugin API this build speaks. Manifests
// declare a minimum via engine (">=1.0"); a plugin asking for a newer
// API than the host is refused at load, not at install — the same
// package keeps working after the host updates.
const APIVersion = "1.0"

// CoreBoards are the workbench's built-in board ids — plugin boards may
// never shadow them (route() falls back to #/room; a colliding id would
// hijack a core board's nav anchor).
var CoreBoards = []string{"room", "people", "project", "chat", "assistant", "kb"}

var (
	idRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}\.[a-z0-9][a-z0-9-]{0,31}$`)
	boardIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
	itemKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	// colorRE — theme var values are colors and nothing else: #hex
	// (3/6/8), rgb()/rgba() with sane numbers (alpha may be .5 shorthand —
	// the app's own CSS writes it that way), or transparent. A theme
	// paints; it never gets to smuggle arbitrary CSS values.
	colorRE = regexp.MustCompile(`^(#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})|rgba?\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*(,\s*(\.\d+|[01](\.\d+)?)\s*)?\)|transparent)$`)
)

// Board is one contributed workbench board: a title in the sidebar nav
// plus an ES module whose activate(niuma) export builds the view. Entry
// is a plugin-relative path served at /plugins/<id>/<entry>.
type Board struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Entry string `json:"entry"`
	Icon  string `json:"icon,omitempty"` // plugin-relative asset path; default puzzle icon when empty
}

// ThemeDef is one contributed color theme (形态 A)：an inline palette
// overlaying the built-in light/dark slots. Vars keys must come from
// ThemeSlots (the app's documented CSS slots) and values are colors —
// a theme paints, it never gets to inject arbitrary CSS.
type ThemeDef struct {
	ID   string            `json:"id"`
	Name string            `json:"name"`
	Vars map[string]string `json:"vars"`
}

// ThemeSlots is the themable surface — the :root CSS slots the app
// itself themes (light block + dark block in index.html). Anything
// else in a plugin theme's vars is rejected at parse time.
var ThemeSlots = []string{
	"--titlebar", "--board", "--board-1", "--board-2", "--board-3",
	"--chalk", "--chalk-dim", "--chalk-faint", "--chalk-off",
	"--line", "--line-2", "--line-hi",
	"--hover", "--hover-msg", "--active",
	"--gold", "--gold-deep", "--gold-dim", "--blue",
	"--bubble", "--bubble-me",
	"--sw-off", "--disable-fill", "--bad-dim",
}

// Contributes lists what the plugin adds to the host. Wardrobe
// entries are plugin-relative paths to JSON files (form A); boards
// and themes are declared inline.
type Contributes struct {
	Boards   []Board    `json:"boards,omitempty"`
	Wardrobe []string   `json:"wardrobe,omitempty"`
	Themes   []ThemeDef `json:"themes,omitempty"`
}

// Manifest is plugin.json — the one file every plugin must carry.
type Manifest struct {
	ID          string      `json:"id"`      // publisher.name, e.g. wwestc.chess
	Name        string      `json:"name"`    // display name (≤24 chars)
	Version     string      `json:"version"` // semver-ish x.y.z
	Desc        string      `json:"desc,omitempty"`
	Author      string      `json:"author,omitempty"`
	Engine      string      `json:"engine,omitempty"`      // minimum host API, ">=1.0"; empty = any
	Permissions []string    `json:"permissions,omitempty"` // from KnownPermissions; enforced by the niuma.* API
	Contributes Contributes `json:"contributes"`
}

// KnownPermissions — the grant list the frontend niuma.* API checks
// against. Anything else in a manifest is recorded as a problem (forward
// compat: a newer host may know it) but never blocks load.
var KnownPermissions = []string{
	"boards",        // register workbench boards
	"widgets",       // register HUD widgets (room overlay spots)
	"wardrobe",      // contribute wardrobe items (form A)
	"themes",        // contribute color themes (form A; declarative channel)
	"chat.read",     // subscribe to live chat frames
	"tasks.read",    // read the task ledger
	"projects.read", // read the project registry
	"ui.toast",      // raise toasts
	"market.admin",  // install/enable/remove plugins (the market board's grant — supply-chain power, first-party only in practice)
}

// ValidID reports whether s is a well-formed plugin id (publisher.name)
// — the asset face uses it as its first traversal guard (a valid id
// carries no path material at all).
func ValidID(s string) bool { return idRE.MatchString(s) }

// ParseManifest reads and validates one plugin.json. Problems are
// accumulated, not thrown, so `niuma plugin list` can show a broken
// plugin instead of hiding it; err is non-nil only when the manifest is
// unusable as a whole.
func ParseManifest(data []byte) (m *Manifest, problems []string, err error) {
	m = &Manifest{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, nil, errors.New(i18n.Sf("plugin.json 不是合法 JSON：%v", err))
	}
	if !idRE.MatchString(m.ID) {
		return m, []string{i18n.S("id 必须形如 publisher.name（小写字母/数字/连字符）")}, nil
	}
	if strings.TrimSpace(m.Name) == "" {
		problems = append(problems, i18n.S("name 缺失"))
	}
	if len([]rune(m.Name)) > 24 {
		problems = append(problems, i18n.S("name 超过 24 字"))
	}
	if m.Version == "" {
		problems = append(problems, i18n.S("version 缺失"))
	}
	if ok, p := engineOK(m.Engine); !ok {
		problems = append(problems, p)
	}
	known := map[string]bool{}
	for _, p := range KnownPermissions {
		known[p] = true
	}
	for _, p := range m.Permissions {
		if !known[p] {
			// 未知权限问题串被 Install 按子串「未知权限/unknown permission」
			// 判非致命（store.go 的针脚）——译文改字要连着那边一起改。
			problems = append(problems, i18n.Sf("未知权限 %q（仅记录，不拦截）", p))
		}
	}
	core := map[string]bool{}
	for _, b := range CoreBoards {
		core[b] = true
	}
	seen := map[string]bool{}
	for i, b := range m.Contributes.Boards {
		if !boardIDRE.MatchString(b.ID) {
			problems = append(problems, i18n.Sf("boards[%d].id %q 不合法（小写字母开头，≤24 字符）", i, b.ID))
			continue
		}
		if core[b.ID] || seen[b.ID] {
			problems = append(problems, i18n.Sf("板块 id %q 与内置板块或其他板块冲突", b.ID))
		}
		seen[b.ID] = true
		if strings.TrimSpace(b.Title) == "" {
			problems = append(problems, i18n.Sf("boards[%d].title 缺失", i))
		}
		if p := relPathProblem(b.Entry); p != "" {
			problems = append(problems, fmt.Sprintf("boards[%d].entry %s", i, p))
		}
		if p := relPathProblem(b.Icon); p != "" {
			problems = append(problems, fmt.Sprintf("boards[%d].icon %s", i, p))
		}
	}
	for i, p := range m.Contributes.Wardrobe {
		if p := relPathProblem(p); p != "" {
			problems = append(problems, fmt.Sprintf("wardrobe[%d] %s", i, p))
		}
	}
	// 主题（形态 A）：槽位必须来自 ThemeSlots、值必须是颜色——主题只
	// 许 painting，不许把任意 CSS 值塞进 style。
	slots := map[string]bool{}
	for _, s := range ThemeSlots {
		slots[s] = true
	}
	seenTheme := map[string]bool{}
	for i, t := range m.Contributes.Themes {
		where := fmt.Sprintf("themes[%d]", i)
		switch {
		case !boardIDRE.MatchString(t.ID):
			problems = append(problems, i18n.Sf("%s.id %q 不合法（小写字母开头，≤24 字符）", where, t.ID))
			continue
		case seenTheme[t.ID]:
			problems = append(problems, i18n.Sf("%s：主题 id %q 重复", where, t.ID))
		case strings.TrimSpace(t.Name) == "":
			problems = append(problems, i18n.Sf("%s.name 缺失", where))
		case len(t.Vars) == 0:
			problems = append(problems, i18n.Sf("%s：vars 一个槽都没给", where))
		}
		seenTheme[t.ID] = true
		for k, v := range t.Vars {
			switch {
			case !slots[k]:
				problems = append(problems, i18n.Sf("%s.vars：未知槽位 %q（可用槽位见 plugins.ThemeSlots）", where, k))
			case !colorRE.MatchString(v):
				problems = append(problems, i18n.Sf("%s.vars：%s 的值 %q 不是颜色（#hex / rgb()/rgba() / transparent）", where, k, v))
			}
		}
	}
	return m, problems, nil
}

// LoadManifestFile parses <dir>/plugin.json; a missing file is an error
// (the directory is then not a plugin — the store skips it with the
// reason recorded).
func LoadManifestFile(dir string) (*Manifest, []string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return nil, nil, errors.New(i18n.Sf("plugin.json 不可读：%v", err))
	}
	return ParseManifest(data)
}

// relPathProblem validates a plugin-relative asset path: relative, no
// "..", non-empty. Returns "" when fine.
func relPathProblem(p string) string {
	if p == "" {
		return "" // optional fields may be empty
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.Contains(p, "..") {
		return i18n.S("必须是插件目录内的相对路径（不带 / 开头、不含 ..）")
	}
	return ""
}

// engineOK checks the manifest's engine floor against APIVersion.
// Accepted spellings: "" (any), "1.0", ">=1.0", "^1.0".
func engineOK(engine string) (bool, string) {
	e := strings.TrimSpace(engine)
	if e == "" {
		return true, ""
	}
	e = strings.TrimPrefix(e, ">=")
	e = strings.TrimPrefix(e, "^")
	want, err := parseMinor(e)
	if err != nil {
		return false, i18n.Sf("engine %q 无法解析（写法：>=1.0）", engine)
	}
	have, err := parseMinor(APIVersion)
	if err != nil || have < want {
		return false, i18n.Sf("需要宿主插件 API ≥ %s（本机为 %s）——升级牛马工作室后重试", e, APIVersion)
	}
	return true, ""
}

// parseMinor reads "major.minor" as a single sortable int (×100).
func parseMinor(v string) (int, error) {
	parts := strings.SplitN(strings.TrimSpace(v), ".", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("want major.minor, got %q", v)
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || maj < 0 || min < 0 || min > 99 {
		return 0, fmt.Errorf("bad version %q", v)
	}
	return maj*100 + min, nil
}
