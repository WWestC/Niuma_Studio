package plugins

// store.go — 插件库的磁盘真相：~/.niuma/plugins/<id>/ 一目录一插件，
// 启用态落 ~/.niuma/plugins.json（原子整写、读失败降级全启用——与
// agents/staffing 等 store 同一条「房间必须能启动」纪律）。
//
// Load() 每次全量重扫（目录列表＋几份小 JSON，开销可忽略）：CLI 的
// enable/disable 与 GUI 的 /plugins.json 共用同一份真相，装机/启停后
// 无需重启进程；浏览器端刷新一次窗口即见新板块。

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
)

// Plugin is one installed plugin as the host sees it after a scan.
// Problems are human-readable, never fatal — a broken plugin shows up
// in `niuma plugin list` and /plugins.json instead of vanishing.
type Plugin struct {
	Manifest *Manifest `json:"manifest,omitempty"` // nil when plugin.json is missing/unparseable
	Dir      string    `json:"-"`                  // absolute install directory
	ID       string    `json:"id"`                 // dir name (== manifest id in a healthy install)
	Enabled  bool      `json:"enabled"`
	Problems []string  `json:"problems,omitempty"`
}

// Boards returns the manifest's boards — empty for a broken plugin.
func (p *Plugin) Boards() []Board {
	if p.Manifest == nil {
		return nil
	}
	return p.Manifest.Contributes.Boards
}

type stateFile struct {
	Enabled map[string]bool `json:"enabled"`
	// Removed is the uninstall tombstone: the factory seed respects it
	// (a user who removed niuma.market must not find it resurrected on
	// the next boot).
	Removed map[string]bool `json:"removed,omitempty"`
}

// Store manages the plugin root directory and the enable-state file.
// A zero-ish Store (root "") is inert: Load returns nil, handlers 404 —
// tests and degraded boots use that shape.
type Store struct {
	root      string
	statePath string

	mux sync.Mutex // serializes state-file read/modify/write

	// applied tracks the wardrobe sources the last ApplyWardrobe left
	// registered — the next apply diffs against it to withdraw what
	// disappeared (uninstalled or disabled plugins leave no residue in
	// the roll pools / render tables).
	applied map[string]bool
}

// DefaultPaths resolves ~/.niuma/plugins and ~/.niuma/plugins.json.
func DefaultPaths() (root, statePath string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(home, ".niuma", "plugins"),
		filepath.Join(home, ".niuma", "plugins.json"), nil
}

// Open brings up the store; it never fails (degrade-to-empty is the
// house rule) — root "" disables the whole plugin face.
func Open(root, statePath string) *Store {
	return &Store{root: root, statePath: statePath}
}

// Root returns the plugin root ("" = plugin face disabled).
func (s *Store) Root() string { return s.root }

// Load scans the plugin root. Result is sorted by id; cross-plugin
// board-id conflicts are resolved in favor of the earlier id (the
// loser's board is dropped and a problem recorded — duplicate DOM ids
// would otherwise hijack each other's nav anchors).
func (s *Store) Load() []*Plugin {
	if s.root == "" {
		return nil
	}
	state := s.readState()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil // no root yet: zero plugins, not an error
	}
	var out []*Plugin
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(s.root, e.Name())
		p := &Plugin{Dir: dir, ID: e.Name(), Enabled: true}
		m, problems, err := LoadManifestFile(dir)
		if err != nil {
			p.Problems = append(p.Problems, err.Error())
			p.Enabled = false // a plugin we can't even parse never runs
			out = append(out, p)
			continue
		}
		p.Manifest = m
		p.Problems = append(p.Problems, problems...)
		if m.ID != e.Name() {
			// The dir name is the addressing truth (asset URLs use it);
			// a divergent manifest id means a hand-copied install.
			p.Problems = append(p.Problems, i18n.Sf("目录名 %q 与 manifest id %q 不一致（以目录名为准）", e.Name(), m.ID))
		}
		if on, ok := state.Enabled[e.Name()]; ok {
			p.Enabled = on
		}
		if ok, _ := engineOK(m.Engine); !ok { // problems already carry the reason
			p.Enabled = false
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	// Cross-plugin board conflicts: earlier id (sorted) wins.
	taken := map[string]bool{}
	for _, b := range CoreBoards {
		taken[b] = true
	}
	for _, p := range out {
		if p.Manifest == nil {
			continue
		}
		kept := p.Manifest.Contributes.Boards[:0]
		for _, b := range p.Manifest.Contributes.Boards {
			if taken[b.ID] {
				p.Problems = append(p.Problems, i18n.Sf("板块 %q 与其他插件/内置板块撞 id，已让位", b.ID))
				continue
			}
			taken[b.ID] = true
			kept = append(kept, b)
		}
		p.Manifest.Contributes.Boards = kept
	}
	return out
}

// ApplyWardrobe registers every enabled plugin's wardrobe contributions
// into pixart/staffing (form A). Idempotent per source — re-Apply just
// replaces what a source previously contributed — and reconciling: a
// source that dropped out (uninstall, disable) has its contribution
// withdrawn, so the pools track the enabled set exactly.
func (s *Store) ApplyWardrobe(ps []*Plugin) {
	now := map[string]bool{}
	for _, p := range ps {
		if p.Manifest == nil || !p.Enabled || len(p.Manifest.Contributes.Wardrobe) == 0 {
			continue
		}
		for _, rel := range p.Manifest.Contributes.Wardrobe {
			src := "plugin:" + p.ID + ":" + rel
			now[src] = true
			problems := applyWardrobeFile(src, filepath.Join(p.Dir, rel))
			if len(problems) > 0 {
				p.Problems = append(p.Problems, problems...)
			}
		}
	}
	for src := range s.applied {
		if !now[src] {
			withdrawWardrobe(src)
		}
	}
	s.applied = now
}

// SetEnabled flips one plugin's enable state and persists it.
func (s *Store) SetEnabled(id string, on bool) error {
	if !idRE.MatchString(id) {
		return errors.New(i18n.Sf("插件 id %q 不合法", id))
	}
	if dir := filepath.Join(s.root, id); s.root != "" {
		if _, err := os.Stat(dir); err != nil {
			return errors.New(i18n.Sf("未安装插件 %s", id))
		}
	}
	s.mux.Lock()
	defer s.mux.Unlock()
	state := s.readStateLocked()
	if state.Enabled == nil {
		state.Enabled = map[string]bool{}
	}
	state.Enabled[id] = on
	return s.writeStateLocked(state)
}

// Install copies a source directory (an unpacked plugin) into the root.
// The manifest must parse and carry no fatal problems; an existing
// target is refused unless force (the remove→install pair is explicit).
// Returns the installed id.
func (s *Store) Install(srcDir string, force bool) (string, error) {
	if s.root == "" {
		return "", errors.New(i18n.S("插件目录不可用"))
	}
	m, problems, err := LoadManifestFile(srcDir)
	if err != nil {
		return "", err
	}
	fatal := false
	for _, p := range problems {
		// board/entry/icon problems make form B unusable; id/engine are
		// non-starters outright. Be strict: any manifest problem refuses
		// the install — authors fix their plugin.json first.
		// 针脚（与 manifest.go 的「未知权限」问题串互为耦合）：判定按
		// 两种语言的子串各认一次，改译文措辞要连着这里改。
		if !strings.Contains(p, "未知权限") && !strings.Contains(p, "unknown permission") {
			fatal = true
		}
		if ok, _ := engineOK(m.Engine); !ok {
			fatal = true
		}
	}
	if fatal {
		return "", errors.New(i18n.Sf("plugin.json 有问题，拒绝安装：%s", strings.Join(problems, "；")))
	}
	dst := filepath.Join(s.root, m.ID)
	if _, err := os.Stat(dst); err == nil && !force {
		return "", errors.New(i18n.Sf("插件 %s 已安装（先 remove 或 --force 覆盖）", m.ID))
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", err
	}
	if err := copyTree(srcDir, dst); err != nil {
		return "", errors.New(i18n.Sf("复制插件文件失败：%v", err))
	}
	return m.ID, nil
}

// Remove deletes one plugin's directory and drops a tombstone (the
// enable entry stays behind — it also remembers the user's choice on
// reinstall; the tombstone is what stops the factory seed).
func (s *Store) Remove(id string) error {
	if !idRE.MatchString(id) {
		return errors.New(i18n.Sf("插件 id %q 不合法", id))
	}
	if s.root == "" {
		return errors.New(i18n.S("插件目录不可用"))
	}
	dir := filepath.Join(s.root, id)
	if _, err := os.Stat(dir); err != nil {
		return errors.New(i18n.Sf("未安装插件 %s", id))
	}
	// Guard the blast radius: only the exact child dir ever goes.
	if filepath.Clean(dir) == filepath.Clean(s.root) {
		return fmt.Errorf("refusing to remove the plugin root itself")
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	s.mux.Lock()
	defer s.mux.Unlock()
	state := s.readStateLocked()
	if state.Removed == nil {
		state.Removed = map[string]bool{}
	}
	state.Removed[id] = true
	delete(state.Enabled, id)
	_ = s.writeStateLocked(state) // 墓碑落盘失败：预装可能复活，可接受的小概率
	return nil
}

// ShouldSeed reports whether the factory should (re)plant one builtin
// plugin: not installed and never removed by the user.
func (s *Store) ShouldSeed(id string) bool {
	if s.root == "" || !ValidID(id) {
		return false
	}
	if _, err := os.Stat(filepath.Join(s.root, id)); err == nil {
		return false // 已在
	}
	state := s.readState()
	return !state.Removed[id]
}

// ---------- state file ----------

func (s *Store) readState() stateFile {
	s.mux.Lock()
	defer s.mux.Unlock()
	return s.readStateLocked()
}

func (s *Store) readStateLocked() stateFile {
	var st stateFile
	if s.statePath == "" {
		return st
	}
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return st // absent/unreadable → defaults (everything enabled)
	}
	_ = json.Unmarshal(data, &st)
	return st
}

func (s *Store) writeStateLocked(st stateFile) error {
	if s.statePath == "" {
		return nil
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return persist.Save(s.statePath, data, 0o644)
}

// copyTree recursively copies src into dst (dst created; files 0644,
// dirs 0755 — plugin payloads are data + JS, nothing executable-bit
// sensitive in forms A/B).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks & friends: an install is flat data
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
