package zcode

// discover_test.go — pins the install-layout discovery against the
// desktop's packaging contract (electron-builder.config.js): the fixed
// identities (ZCode / ZCode Preview), the per-platform bundle path
// inside an install, the sibling Electron main binary, and the drift
// walk. Fakes are built for the RUNNING platform's layout, so every
// platform's suite exercises its own truth.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeInstall builds a temp install root in the running platform's
// packaged layout — the CLI bundle plus the Electron main binary — and
// answers (root, bundle, exe). Bare files suffice: discovery only stats.
func fakeInstall(t *testing.T) (root, bundle, exe string) {
	t.Helper()
	base := t.TempDir()
	switch runtime.GOOS {
	case "darwin":
		root = filepath.Join(base, "ZCode.app")
		bundle = filepath.Join(root, "Contents", "Resources", "glm", "zcode.cjs")
		exe = filepath.Join(root, "Contents", "MacOS", "ZCode")
	case "linux":
		root = filepath.Join(base, "zcode")
		bundle = filepath.Join(root, "resources", "glm", "zcode.cjs")
		exe = filepath.Join(root, "zcode")
	default: // windows
		root = filepath.Join(base, "ZCode")
		bundle = filepath.Join(root, "resources", "glm", "zcode.cjs")
		exe = filepath.Join(root, "ZCode.exe")
	}
	for _, p := range []string{bundle, exe} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fake\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, bundle, exe
}

// TestFindBundleIn pins the fixed-layout hit: the packaged bundle path
// inside a standard install root resolves without any walk.
func TestFindBundleIn(t *testing.T) {
	root, bundle, _ := fakeInstall(t)
	if got := findBundleIn(root); got != bundle {
		t.Fatalf("standard install miss: want %q, got %q", bundle, got)
	}
}

// TestInstallRootOf pins bundle → install-root derivation for the
// running platform, including the miss case (a bare CLI path with no
// install around it).
func TestInstallRootOf(t *testing.T) {
	root, bundle, _ := fakeInstall(t)
	if got := installRootOf(bundle); got != root {
		t.Fatalf("root derivation: want %q, got %q", root, got)
	}
	if got := installRootOf(filepath.Join(t.TempDir(), "zcode.cjs")); got != "" {
		t.Fatalf("a bare CLI path must derive no root, got %q", got)
	}
}

// TestElectronExeIn pins the desktop's own pairing: the install's
// Electron main binary is found by its packaged name, and a re-branded
// zcode-named exe still pairs via the sweep.
func TestElectronExeIn(t *testing.T) {
	_, bundle, exe := fakeInstall(t)
	if got := electronExeIn(installRootOf(bundle)); got != exe {
		t.Fatalf("packaged exe miss: want %q, got %q", exe, got)
	}
	if got := electronExeIn(t.TempDir()); got != "" {
		t.Fatalf("an empty root must answer no exe, got %q", got)
	}
	// a re-branded main binary (zcode-named, right suffix) still pairs
	brandedRoot := t.TempDir()
	_, ext := electronExeNames()
	exeDir := brandedRoot
	if runtime.GOOS == "darwin" {
		exeDir = filepath.Join(brandedRoot, "Contents", "MacOS")
	}
	branded := filepath.Join(exeDir, "ZCodeNext"+ext)
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(branded, []byte("fake\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := electronExeIn(brandedRoot); got != branded {
		t.Fatalf("re-branded exe miss: want %q, got %q", branded, got)
	}
}

// TestElectronMainExe pins the plain-Node-mode gate: only a
// zcode-named main executable counts; a plain node never does.
func TestElectronMainExe(t *testing.T) {
	_, _, exe := fakeInstall(t)
	if !electronMainExe(exe) {
		t.Fatalf("%q is the install's Electron main binary — must run as node", exe)
	}
	for _, plain := range []string{"/usr/local/bin/node", `C:\Program Files\nodejs\node.exe`} {
		if electronMainExe(plain) {
			t.Fatalf("%q is a plain node — must not enter Electron mode", plain)
		}
	}
}

// TestResolveNodePrefersInstallExe pins the pairing's precedence: with
// no env override, the bundle's own install yields its Electron main
// binary — even on machines where a plain node sits on PATH (the
// runtime the CLI was bundled for beats whatever node happens to be
// installed).
func TestResolveNodePrefersInstallExe(t *testing.T) {
	_, bundle, exe := fakeInstall(t)
	if got := resolveNode(bundle); got != exe {
		t.Fatalf("install exe must win: want %q, got %q", exe, got)
	}
}

// TestBundleHomesPreviewIdentity pins that both product identities
// (ZCode and ZCode Preview) carry a fixed home on every platform — a
// Preview install must be found without any scan.
func TestBundleHomesPreviewIdentity(t *testing.T) {
	homes := BundleHomes()
	joined := strings.Join(homes, string(filepath.ListSeparator))
	if !strings.Contains(joined, "ZCode Preview") && !strings.Contains(joined, "zcode-preview") {
		t.Fatalf("no Preview home on %s: %v", runtime.GOOS, homes)
	}
	for _, h := range homes {
		if !strings.HasSuffix(strings.ToLower(h), "zcode.cjs") {
			t.Fatalf("every home must name a zcode.cjs, got %q", h)
		}
	}
}

// TestParseRegSZLine pins the reg-query output parse: the value's data
// tail comes back whole (spaces included), foreign lines stay "".
func TestParseRegSZLine(t *testing.T) {
	got := parseRegSZLine("    InstallLocation    REG_SZ    C:\\Program Files\\ZCode", "InstallLocation")
	if got != `C:\Program Files\ZCode` {
		t.Fatalf("data tail: got %q", got)
	}
	if got := parseRegSZLine("    DisplayName    REG_SZ    ZCode", "InstallLocation"); got != "" {
		t.Fatalf("foreign value must stay empty, got %q", got)
	}
	if got := parseRegSZLine("unfinished line REG_SZ", "unfinished"); got != "" {
		t.Fatalf("no data tail must stay empty, got %q", got)
	}
}

// TestInstallRootFromUninstall pins the dir derivation from every
// UninstallString shape reg exports: quoted (with or without args) and
// bare — and the refusal of anything not ending in .exe.
func TestInstallRootFromUninstall(t *testing.T) {
	cases := map[string]string{
		`"C:\Program Files\ZCode\Uninstall ZCode.exe"`:        `C:\Program Files\ZCode`,
		`"C:\Program Files\ZCode\Uninstall ZCode.exe" /S _?=`: `C:\Program Files\ZCode`,
		`C:\ZCode\Uninstall.exe`:                              `C:\ZCode`,
		`"C:\Program Files\ZCode\uninstall-log.txt"`:          "",
		``: "",
	}
	for in, want := range cases {
		if got := installRootFromUninstall(in); got != want {
			t.Fatalf("installRootFromUninstall(%q): want %q, got %q", in, want, got)
		}
	}
}

// TestRegistryInstallRootsNonWindows pins the tier's off-switch: away
// from windows the registry is never consulted.
func TestRegistryInstallRootsNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows runs the real reg query")
	}
	if got := registryInstallRoots(); got != nil {
		t.Fatalf("non-windows must answer no registry roots, got %v", got)
	}
}

// TestCliBundleHomes pins the standalone-CLI homes: both the server
// deploy and the runtime-install layouts live under the user's home —
// the runtime install under "agent", not "glm".
func TestCliBundleHomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows UserHomeDir reads this
	homes := cliBundleHomes()
	want := []string{
		filepath.Join(home, ".zcode", "server", "agents", "glm", "zcode.cjs"),
		filepath.Join(home, ".zcode", "runtime", "current", "agent", "zcode.cjs"),
	}
	if len(homes) != len(want) {
		t.Fatalf("want %d homes, got %v", len(want), homes)
	}
	for i, w := range want {
		if homes[i] != w {
			t.Fatalf("home[%d]: want %q, got %q", i, w, homes[i])
		}
	}
}

// TestMissingSelfCheckNonWindows pins the note's silence off windows
// (and that the memoized wrapper answers the same "" twice).
func TestMissingSelfCheckNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows runs the real reg queries")
	}
	if got := MissingSelfCheck(); got != "" || MissingSelfCheck() != "" {
		t.Fatalf("non-windows must answer no self-check note, got %q", got)
	}
}

// TestLnkTargetPaths pins the .lnk payload extraction: drive-absolute
// targets come back whole (spaces included) from both the ASCII and
// UTF-16LE runs, garbage and relative segments stay out.
func TestLnkTargetPaths(t *testing.T) {
	ascii := "junk\x00C:\\Program Files\\ZCode\\ZCode.exe\x00more junk\x00"
	utf16le := make([]byte, 0, 64)
	for _, r := range "D:\\Apps 我的应用\\zcode\\zcode.exe\x00relative\\path" {
		utf16le = append(utf16le, byte(r), byte(r>>8))
	}
	var got []string
	for _, p := range lnkTargetPaths([]byte(ascii + string(utf16le))) {
		got = append(got, p)
	}
	want := []string{
		`C:\Program Files\ZCode\ZCode.exe`,
		`D:\Apps 我的应用\zcode\zcode.exe`,
	}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("path[%d]: want %q, got %q", i, w, got[i])
		}
	}
}

// TestBundleCache pins the remember/cached roundtrip and the
// self-healing stale case: a remembered path that no longer exists is
// rejected, so an uninstalled ZCode falls back to the live tiers.
func TestBundleCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	fake := filepath.Join(home, "ZCode.app", "Contents", "Resources", "glm", "zcode.cjs")
	if err := os.MkdirAll(filepath.Dir(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fake, []byte("fake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cachedBundle(); got != "" {
		t.Fatalf("nothing remembered yet, got %q", got)
	}
	rememberBundle(fake)
	if got := cachedBundle(); got != fake {
		t.Fatalf("remembered path must hit, want %q, got %q", fake, got)
	}
	if err := os.Remove(fake); err != nil {
		t.Fatal(err)
	}
	if got := cachedBundle(); got != "" {
		t.Fatalf("a stale remembered path must be rejected, got %q", got)
	}
}

// TestDriveSkipDir pins the sweep's pruning list: system trees are
// skipped, everything else (including an install's own dirs) walked.
func TestDriveSkipDir(t *testing.T) {
	for _, skip := range []string{"Windows", "ProgramData", "$RECYCLE.BIN", "Temp"} {
		if !driveSkipDir(skip) {
			t.Fatalf("%q must be pruned from the sweep", skip)
		}
	}
	for _, walk := range []string{"Users", "Program Files", "ZCode", "apps", "soft"} {
		if driveSkipDir(walk) {
			t.Fatalf("%q must stay in the sweep", walk)
		}
	}
}

// TestShortcutAndProcessTiersNonWindows pins the off-switch shared by
// the exec-free shortcut scan and the process sweep.
func TestShortcutAndProcessTiersNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows runs the real scans")
	}
	if got := shortcutInstallRoots(); got != nil {
		t.Fatalf("non-windows must answer no shortcut roots, got %v", got)
	}
	if got := processInstallRoots(); got != nil {
		t.Fatalf("non-windows must answer no process roots, got %v", got)
	}
}

// TestZcodeKeysFromRegListing pins the DisplayName enumeration parse:
// a zcode mention yields its HKEY_ block, foreign DisplayNames and
// bare key headers don't.
func TestZcodeKeysFromRegListing(t *testing.T) {
	dump := "\r\n" +
		"HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\{abcd}\r\n" +
		"    DisplayName    REG_SZ    Some Other App\r\n" +
		"\r\n" +
		"HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\legacy.app\r\n" +
		"    DisplayName    REG_SZ    ZCode\r\n" +
		"    InstallLocation    REG_SZ    D:\\apps\\ZCode\r\n" +
		"\r\n" +
		"HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\dev.zcode.app\r\n" +
		"    DisplayName    REG_SZ    zcode preview\r\n"
	keys := zcodeKeysFromRegListing(dump)
	want := []string{
		`HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Uninstall\legacy.app`,
		`HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\dev.zcode.app`,
	}
	if len(keys) != len(want) {
		t.Fatalf("want %d keys, got %v", len(want), keys)
	}
	for i, w := range want {
		if keys[i] != w {
			t.Fatalf("key[%d]: want %q, got %q", i, w, keys[i])
		}
	}
}
