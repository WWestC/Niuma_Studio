package zcode

// discover.go — where ZCode installs live on this machine, and the
// runtime that executes the CLI found there. Layout facts come from
// the desktop's own packaging (electron-builder.config.js +
// desktop-product-identity.mjs):
//   - the CLI ships as extraResources glm/ → <install>/resources/glm/
//     zcode.cjs on win/linux, <App>.app/Contents/Resources/glm/zcode.cjs
//     on darwin;
//   - installs are named ZCode or ZCode Preview (two side-by-side
//     identities): NSIS per-user (%LOCALAPPDATA%\Programs) or
//     per-machine (Program Files) on Windows, /usr/lib/<name> for
//     deb/rpm/pacman on Linux, /Applications on darwin;
//   - NO standalone Node ships with the app — the desktop runs the CLI
//     with its own Electron main binary under ELECTRON_RUN_AS_NODE=1
//     (without it the child comes up as a Chromium app and wedges at
//     GPU init). The bridge mirrors that pairing, so a machine with
//     ZCode installed but no Node.js still works.

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
)

// fixedInstallRoots lists this platform's standard install directories,
// both product identities in probe order (ZCode first — the public
// flavor, Preview second — the side-by-side internal one).
func fixedInstallRoots() []string {
	switch runtime.GOOS {
	case "windows":
		var roots []string
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			roots = append(roots,
				filepath.Join(la, "Programs", "ZCode"),
				filepath.Join(la, "Programs", "ZCode Preview"))
		}
		return append(roots,
			filepath.Join(`C:\Program Files`, "ZCode"),
			filepath.Join(`C:\Program Files`, "ZCode Preview"),
			filepath.Join(`C:\Program Files (x86)`, "ZCode"),
			filepath.Join(`C:\Program Files (x86)`, "ZCode Preview"))
	case "linux":
		return []string{"/usr/lib/zcode", "/usr/lib/zcode-preview", "/opt/ZCode"}
	default: // darwin
		roots := []string{"/Applications/ZCode.app", "/Applications/ZCode Preview.app"}
		if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots,
				filepath.Join(home, "Applications", "ZCode.app"),
				filepath.Join(home, "Applications", "ZCode Preview.app"))
		}
		return roots
	}
}

// scanParents lists the directories whose immediate children get
// scanned for zcode-named installs — the drift tier that catches a
// renamed, recased, or re-versioned install dir without walking the
// whole drive. One readdir each, never deeper.
func scanParents() []string {
	switch runtime.GOOS {
	case "windows":
		var parents []string
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			parents = append(parents, filepath.Join(la, "Programs"))
		}
		return append(parents, `C:\Program Files`, `C:\Program Files (x86)`)
	case "linux":
		return []string{"/usr/lib", "/opt"}
	default: // darwin
		parents := []string{"/Applications"}
		if home, err := os.UserHomeDir(); err == nil {
			parents = append(parents, filepath.Join(home, "Applications"))
		}
		return parents
	}
}

// scanInstallRoots answers zcode-named subdirectories of the scan
// parents — the drift tier that catches a renamed, recased, or
// re-versioned install dir without walking the whole drive. One
// readdir each, never deeper; fixed roots are skipped (case-folded —
// Windows dirs fold case).
func scanInstallRoots() []string {
	seen := map[string]bool{}
	for _, root := range fixedInstallRoots() {
		seen[strings.ToLower(filepath.Clean(root))] = true
	}
	var found []string
	for _, parent := range scanParents() {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.Contains(strings.ToLower(e.Name()), "zcode") {
				continue
			}
			p := filepath.Join(parent, e.Name())
			key := strings.ToLower(filepath.Clean(p))
			if !seen[key] {
				seen[key] = true
				found = append(found, p)
			}
		}
	}
	return found
}

// cliBundleHomes names the standalone-CLI installs (no desktop): the
// server deploy (~/.zcode/server/agents/glm) and the runtime install
// (~/.zcode/runtime/current/agent — the install script's tarball
// layout, "agent" not "glm"). Both ship zcode.cjs under their own
// layout and run on a system node.
func cliBundleHomes() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".zcode", "server", "agents", "glm", "zcode.cjs"),
		filepath.Join(home, ".zcode", "runtime", "current", "agent", "zcode.cjs"),
	}
}

// MissingSelfCheck answers the missing panel's self-identifying note —
// what discovery actually saw on this machine, so a failure report
// carries the facts (and this build's vintage, by the note's presence)
// without anyone asking. Memoized: the boot gate polls fast, the reg
// queries must not ride every beat. "" when there is nothing to add.
func MissingSelfCheck() string {
	selfCheckOnce.Do(func() { selfCheckNote = missingSelfCheck() })
	return selfCheckNote
}

var (
	selfCheckOnce sync.Once
	selfCheckNote string
)

func missingSelfCheck() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	if n := len(registryInstallRoots()) + len(enumRegistryInstallRoots()) +
		len(shortcutInstallRoots()) + len(processInstallRoots()); n > 0 {
		return i18n.Sf("（自检：注册表/开始菜单/开机进程共 %d 条 ZCode 线索但其中无 zcode.cjs；可设 NIUMA_ZCODE_BUNDLE 指路）", n)
	}
	if n := len(scanInstallRoots()); n > 0 {
		return i18n.Sf("（自检：装机位扫描到 %d 个 zcode 字样目录但其中无 zcode.cjs；可设 NIUMA_ZCODE_BUNDLE 指路）", n)
	}
	return i18n.Sf("（自检：装机位、开始菜单、开机进程与注册表均未发现 ZCode——ZCode 必须装在本机（Niuma 所在的这台），装在别的电脑上不算；非常规位置则设 NIUMA_ZCODE_BUNDLE 指向 zcode.cjs）")
}

// shortcutInstallRoots pulls install roots out of Start Menu .lnk files
// — name-independent of the install dir: the shortcut's payload carries
// the real target path even when the folder was renamed or the shortcut
// rebranded. Both Start Menu homes (per-user Roaming, per-machine
// ProgramData), every .lnk read (a few dozen small files, no exec).
func shortcutInstallRoots() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	menus := []string{
		filepath.Join(`C:\ProgramData`, "Microsoft", "Windows", "Start Menu", "Programs"),
	}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		menus = append(menus, filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs"))
	}
	var roots []string
	for _, menu := range menus {
		_ = filepath.WalkDir(menu, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".lnk") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			for _, target := range lnkTargetPaths(data) {
				if strings.Contains(strings.ToLower(target), "zcode") {
					roots = append(roots, filepath.Dir(target))
				}
			}
			return nil
		})
	}
	return roots
}

// lnkPathRe matches a drive-absolute path inside a decoded segment —
// the path may sit mid-segment when neighboring .lnk fields bleed into
// the same NUL-split run, so the match anchors on the drive letter, not
// the segment start. ':' is excluded past the drive (paths carry it
// only there), control/forbidden chars end the run.
var lnkPathRe = regexp.MustCompile(`[A-Za-z]:[\\/][^:\x00-\x1f<>|?*"]{1,200}`)

// lnkTargetPaths extracts candidate absolute target paths from raw .lnk
// bytes. The local-base-path field stores NUL-terminated strings in
// ASCII and UTF-16LE; both encodings are split on NUL and searched for
// drive-absolute paths. Pure for tests.
func lnkTargetPaths(b []byte) []string {
	var out []string
	seen := map[string]bool{}
	collect := func(seg string) {
		for _, m := range lnkPathRe.FindAllString(seg, -1) {
			if s := strings.TrimSpace(m); s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	for _, seg := range strings.Split(string(b), "\x00") {
		collect(seg)
	}
	// UTF-16LE runs at BOTH byte phases — a .lnk's string offset isn't
	// guaranteed even, so an odd-headed payload needs the shifted walk
	for _, phase := range []int{0, 1} {
		var seg []uint16
		flush := func() {
			if len(seg) > 0 {
				collect(string(utf16.Decode(seg)))
				seg = seg[:0]
			}
		}
		for i := phase; i+1 < len(b); i += 2 {
			if v := uint16(b[i]) | uint16(b[i+1])<<8; v == 0 {
				flush()
			} else {
				seg = append(seg, v)
			}
		}
		flush()
	}
	return out
}

// processInstallRoots answers the install dirs of RUNNING zcode
// processes — the tier that sees through any renamed directory while
// the app is open. powershell exec is heavy, so negatives cache for a
// minute (the bridge retries every 10s; a freshly opened ZCode shows up
// on the next sweep).
func processInstallRoots() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	procMu.Lock()
	defer procMu.Unlock()
	if time.Since(procAt) < time.Minute {
		return procRoots
	}
	procAt = time.Now()
	procRoots = nil
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := util.HideConsole(exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
		"Get-Process | Where-Object {$_.Path} | ForEach-Object {$_.Path}")).Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || !strings.Contains(strings.ToLower(line), "zcode") {
			continue
		}
		dir := filepath.Dir(line)
		if !seen[dir] {
			seen[dir] = true
			procRoots = append(procRoots, dir)
		}
	}
	return procRoots
}

var (
	procMu    sync.Mutex
	procAt    time.Time
	procRoots []string
)

// driveBundlePath sweeps every fixed drive depth-bounded for the
// zcode.cjs FILE — the last net: it needs neither a known install name
// nor a registry entry nor a running process. ASYNC on purpose: the
// sweep costs seconds to a minute, far too slow to sit inside a boot
// retry — the first call merely launches it in the background and
// answers "" until it lands; the bridge's 10s retry picks the result
// up on a later tick (and the boot door shows SweepInProgress, so the
// wait reads as searching, not stuck).
func driveBundlePath() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	driveMu.Lock()
	defer driveMu.Unlock()
	if !driveStarted {
		driveStarted = true
		go func() {
			found := sweepDrivesForBundle()
			driveMu.Lock()
			driveFound, driveSwept = found, true
			driveMu.Unlock()
		}()
	}
	return driveFound
}

// SweepInProgress reports whether the one-shot drive sweep is still
// running — the boot door turns this into "正在全盘搜索" instead of the
// missing panel.
func SweepInProgress() bool {
	driveMu.Lock()
	defer driveMu.Unlock()
	return driveStarted && !driveSwept
}

func sweepDrivesForBundle() string {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	found := ""
	for c := 'C'; c <= 'Z' && found == ""; c++ {
		root := string(c) + `:\`
		if _, err := os.Stat(root); err != nil {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable subtrees skipped, not fatal
			}
			if ctx.Err() != nil {
				return fs.SkipAll
			}
			if e.IsDir() {
				if driveSkipDir(e.Name()) {
					return fs.SkipDir
				}
				if rel, rerr := filepath.Rel(root, p); rerr == nil &&
					strings.Count(rel, string(filepath.Separator)) >= 9 {
					return fs.SkipDir
				}
				return nil
			}
			if e.Name() == "zcode.cjs" {
				found = p
				return fs.SkipAll
			}
			return nil
		})
	}
	return found
}

// driveSkipDir names the subtrees no CLI install lives in — skipping
// them keeps the sweep inside its timeout budget. Pure for tests.
func driveSkipDir(name string) bool {
	switch strings.ToLower(name) {
	case "windows", "programdata", "$recycle.bin", "perflogs",
		"system volume information", "temp", "tmp":
		return true
	}
	return false
}

var (
	driveMu      sync.Mutex
	driveStarted bool
	driveSwept   bool
	driveFound   string
)

// bundleCachePath is ~/.niuma/zcode_bundle — the last discovery's win,
// so a restart never pays the deep tiers again. A plain file (not
// persist.Save's atomic dance): a torn write worst-cases into a
// missing path, the stat check rejects it, and the tiers self-heal.
func bundleCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".niuma", "zcode_bundle")
}

func cachedBundle() string {
	p := bundleCachePath()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	if s := strings.TrimSpace(string(b)); s != "" && fileExists(s) {
		return s
	}
	return ""
}

func rememberBundle(p string) {
	if cp := bundleCachePath(); cp != "" {
		_ = os.MkdirAll(filepath.Dir(cp), 0o755)
		_ = os.WriteFile(cp, []byte(p), 0o644)
	}
}

// registryInstallRoots asks the Windows uninstall registry where ZCode
// installed itself — the tier that catches NSIS's custom install dir.
// electron-builder registers under Uninstall\{appId} (both flavors,
// productName spellings ride along); InstallLocation names the install
// dir, an UninstallString yields its parent. nil on non-windows.
func registryInstallRoots() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	const prefix = `Software\Microsoft\Windows\CurrentVersion\Uninstall`
	var roots []string
	for _, hive := range []string{"HKCU", `HKLM\SOFTWARE`, `HKLM\SOFTWARE\WOW6432Node`} {
		for _, name := range []string{"dev.zcode.app", "dev.zcode.app.preview", "ZCode", "ZCode Preview"} {
			key := hive + `\` + prefix + `\` + name
			if loc := regQuerySZ(key, "InstallLocation"); loc != "" {
				roots = append(roots, loc)
			}
			if us := regQuerySZ(key, "UninstallString"); us != "" {
				if dir := installRootFromUninstall(us); dir != "" {
					roots = append(roots, dir)
				}
			}
		}
	}
	return roots
}

// enumRegistryInstallRoots sweeps every Uninstall subkey's DisplayName
// for a zcode mention — closing the hole the four fixed key names can't
// see (an older build's appId nobody remembers). One reg exec per hive,
// miss-path only; the block parsing is pure for tests.
func enumRegistryInstallRoots() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	const uninstall = `Software\Microsoft\Windows\CurrentVersion\Uninstall`
	var roots []string
	for _, hive := range []string{"HKCU", `HKLM\SOFTWARE`, `HKLM\SOFTWARE\WOW6432Node`} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, err := util.HideConsole(exec.CommandContext(ctx, "reg", "query", hive+`\`+uninstall, "/s", "/v", "DisplayName")).Output()
		cancel()
		if err != nil {
			continue
		}
		for _, key := range zcodeKeysFromRegListing(string(out)) {
			if loc := regQuerySZ(key, "InstallLocation"); loc != "" {
				roots = append(roots, loc)
			}
			if us := regQuerySZ(key, "UninstallString"); us != "" {
				if dir := installRootFromUninstall(us); dir != "" {
					roots = append(roots, dir)
				}
			}
		}
	}
	return roots
}

// zcodeKeysFromRegListing walks a `reg query /s /v DisplayName` dump:
// HKEY_… key headers alternate with value lines; a DisplayName carrying
// a zcode mention yields its key. Pure for tests.
func zcodeKeysFromRegListing(dump string) []string {
	var keys []string
	key := ""
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(line, "HKEY_") {
			key = line
			continue
		}
		if v := parseRegSZLine(line, "DisplayName"); v != "" &&
			strings.Contains(strings.ToLower(v), "zcode") && key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// regQuerySZ reads one REG_SZ value via reg.exe (always on PATH in
// System32). A missing key/value errors and answers "". Best-effort
// tier: paths with consecutive spaces would fold — acceptable here.

// regQuerySZ reads one REG_SZ value via reg.exe (always on PATH in
// System32). A missing key/value errors and answers "". Best-effort
// tier: paths with consecutive spaces would fold — acceptable here.
func regQuerySZ(key, value string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := util.HideConsole(exec.CommandContext(ctx, "reg", "query", key, "/v", value)).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v := parseRegSZLine(line, value); v != "" {
			return v
		}
	}
	return ""
}

// parseRegSZLine pulls the data tail off one reg-query output line
// ("    NAME    REG_SZ    data…"). Pure for tests.
func parseRegSZLine(line, valueName string) string {
	fields := strings.Fields(line)
	for i := 0; i+2 < len(fields); i++ {
		if strings.EqualFold(fields[i], valueName) && strings.EqualFold(fields[i+1], "REG_SZ") {
			return strings.Join(fields[i+2:], " ")
		}
	}
	return ""
}

// installRootFromUninstall derives the install dir from an
// UninstallString — quoted path taken whole, bare path cut at the
// first space (args), one level up from the Uninstall helper exe.
// Pure for tests.
func installRootFromUninstall(us string) string {
	s := strings.TrimSpace(us)
	if strings.HasPrefix(s, `"`) {
		if end := strings.Index(s[1:], `"`); end >= 0 {
			s = s[1 : 1+end]
		}
	} else if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	if s == "" || !strings.HasSuffix(strings.ToLower(s), ".exe") {
		return ""
	}
	// cut at the last separator by hand — a windows path must parse the
	// same on every host the tests run on
	if i := strings.LastIndexAny(s, `\/`); i > 0 {
		return s[:i]
	}
	return ""
}

// bundleInRoot answers the CLI bundle path inside an install root for
// this platform's packaged layout.
func bundleInRoot(root string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(root, "Contents", "Resources", "glm", "zcode.cjs")
	}
	return filepath.Join(root, "resources", "glm", "zcode.cjs")
}

// BundleHomes lists, in probe order, the standard CLI bundle homes of
// the running platform — never empty (the boot gate's missing panel
// shows homes[0], the room notice joins the rest).
func BundleHomes() []string {
	homes := make([]string, 0, 4)
	for _, root := range fixedInstallRoots() {
		homes = append(homes, bundleInRoot(root))
	}
	return homes
}

// findBundleIn probes the given install roots: the fixed bundle path
// first, then the depth-bounded walk of each root (a desktop update
// that moved the CLI inside its install still gets found). "" = miss.
func findBundleIn(roots ...string) string {
	for _, root := range roots {
		if p := bundleInRoot(root); fileExists(p) {
			return p
		}
	}
	return walkForBundle(roots...)
}

// FindBundle locates the zcode CLI bundle: an explicit
// NIUMA_ZCODE_BUNDLE wins, then the LAST DISCOVERY'S REMEMBERED PATH
// (stat-checked — an uninstalled ZCode self-heals into a fresh sweep
// of the tiers), then the platform's install-root tiers one by one —
// fixed identities, zcode-named siblings, the standalone-CLI homes,
// the uninstall registry (fixed keys, then full enumeration), Start
// Menu payloads, running processes, and the async drive sweep. Every
// win is remembered, so restarts cost a single stat. "" = not found;
// the caller decides how to say so.
func FindBundle() string {
	if p := findBundleLive(); p != "" {
		rememberBundle(p)
		return p
	}
	return ""
}

func findBundleLive() string {
	if p := util.Env("ZCODE_BUNDLE"); p != "" {
		if fileExists(p) {
			return p
		}
	}
	if p := cachedBundle(); p != "" {
		return p
	}
	tried := map[string]bool{}
	probe := func(roots []string) string {
		var fresh []string
		for _, root := range roots {
			key := strings.ToLower(filepath.Clean(root))
			if root != "" && !tried[key] {
				tried[key] = true
				fresh = append(fresh, root)
			}
		}
		return findBundleIn(fresh...)
	}
	for _, tier := range []func() []string{fixedInstallRoots, scanInstallRoots} {
		if p := probe(tier()); p != "" {
			return p
		}
	}
	// standalone-CLI homes are bundle paths outright (their own layout)
	for _, p := range cliBundleHomes() {
		if fileExists(p) {
			return p
		}
	}
	if p := probe(registryInstallRoots()); p != "" {
		return p
	}
	if p := probe(enumRegistryInstallRoots()); p != "" {
		return p
	}
	if p := probe(shortcutInstallRoots()); p != "" {
		return p
	}
	if p := probe(processInstallRoots()); p != "" {
		return p
	}
	if p := driveBundlePath(); p != "" {
		return p
	}
	return ""
}

// walkForBundle sweeps the given roots depth-bounded for a zcode.cjs —
// drift insurance, not a general search: dirs beyond the CLI's known
// nesting (plus slack) are pruned; unreadable subtrees are skipped,
// not fatal.
func walkForBundle(roots ...string) string {
	found := ""
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil || found != "" {
				return fs.SkipAll
			}
			if e.IsDir() {
				if rel, rerr := filepath.Rel(root, p); rerr == nil && strings.Count(rel, string(filepath.Separator)) >= 6 {
					return fs.SkipDir
				}
				return nil
			}
			if e.Name() == "zcode.cjs" {
				found = p
				return fs.SkipAll
			}
			return nil
		})
		if found != "" {
			break
		}
	}
	return found
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// installRootOf walks a bundle path back to its install root — the
// parent of the resources dir on win/linux, the .app bundle on darwin.
// "" = an unrecognized layout (e.g. an env override at a bare CLI).
func installRootOf(bundle string) string {
	dir := filepath.Dir(bundle)
	if runtime.GOOS == "darwin" {
		return darwinAppBundleOf(dir)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if strings.EqualFold(filepath.Base(d), "resources") {
			return filepath.Dir(d)
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

// darwinAppBundleOf walks up from dir to the enclosing .app bundle;
// "" when there is none (the caller keeps dir itself as the base).
func darwinAppBundleOf(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if strings.HasSuffix(filepath.Base(d), ".app") {
			return d
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

// electronExeIn answers the install's own Electron main binary — the
// runtime the desktop itself pairs the CLI with. Exact identity names
// first, then a zcode-named sweep of the binary's directory (a
// re-branded exe still pairs). "" = no Electron here (a bare CLI).
func electronExeIn(root string) string {
	if root == "" {
		return ""
	}
	names, ext := electronExeNames()
	exeDir := root
	if runtime.GOOS == "darwin" {
		exeDir = filepath.Join(root, "Contents", "MacOS")
	}
	for _, n := range names {
		if p := filepath.Join(exeDir, n+ext); fileExists(p) {
			return p
		}
	}
	entries, err := os.ReadDir(exeDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if !e.IsDir() && strings.Contains(name, "zcode") && (ext == "" || strings.HasSuffix(name, ext)) {
			return filepath.Join(exeDir, e.Name())
		}
	}
	return ""
}

// electronExeNames answers the packaged main-binary names and platform
// executable suffix (productName from desktop-product-identity.mjs).
func electronExeNames() (names []string, ext string) {
	switch runtime.GOOS {
	case "windows":
		return []string{"ZCode", "ZCode Preview"}, ".exe"
	case "linux":
		return []string{"zcode", "zcode-preview"}, ""
	default: // darwin
		return []string{"ZCode", "ZCode Preview"}, ""
	}
}

// electronMainExe reports whether the resolved runtime path is an
// Electron main binary by name — the gate for the plain-Node mode the
// pairing requires (Start sets ELECTRON_RUN_AS_NODE for it; a plain
// node ignores that env harmlessly, but keep it Electron-only anyway).
func electronMainExe(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if !strings.Contains(base, "zcode") {
		return false
	}
	return runtime.GOOS != "windows" || strings.HasSuffix(base, ".exe")
}
