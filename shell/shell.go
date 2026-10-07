// Package shell is the app's native window face (v2 P6): ONE process,
// ONE window. The room (server, registry, dispatcher — the whole Go
// brain) and the management window live in the same process: the
// Ebiten pixel window retired, its room renders as the canvas board
// inside the window's /app page, and the dual-window/dual-process
// machinery (the old --app/--shell siblings, the spike verdict's
// workaround) went with it. Electron-style: native shell, web
// rendering, single Dock tile, closing the window quits the app.
package shell

import (
	"os"
	"path/filepath"
	"strings"
)

// inBundlePath reports whether exe lives inside a macOS .app bundle
// (…/Foo.app/Contents/MacOS/exe) — Launch Services runs bundle
// executables from there, and that context marks a user-facing launch
// (double-click), not a terminal restart.
func inBundlePath(exe string) bool {
	// ToSlash：路径形状是 macOS 概念（.app 包），Windows 检出的反斜杠
	// 路径不该让判断失真——测试在三个平台上跑同一份断言。
	return strings.HasSuffix(filepath.ToSlash(filepath.Dir(exe)), ".app/Contents/MacOS")
}

// InBundle reports whether this process was launched from a .app
// bundle. main uses it for the relaunch rule: a bundle start against
// an already-running healthy room focuses that app instead of taking
// the room over (probe first, never a second one).
func InBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return inBundlePath(exe)
}

// bundleOfficeHome is BundleOfficeHome's pure half: from a binary
// inside <home>/X.app/Contents/MacOS it climbs to <home> — the office
// checkout the bundle ships from — but only when <home> really is one
// (a go.mod beside the .app; a relocated bundle sitting in
// /Applications or ~/Applications fails the check and yields ""). The
// gate matters: an ungated climb would offer /Applications as the
// members' workspace, where birthed sessions can't write.
func bundleOfficeHome(exe string) string {
	if !inBundlePath(exe) {
		return ""
	}
	home := exe
	for i := 0; i < 4; i++ { // MacOS → Contents → X.app → <home>
		home = filepath.Dir(home)
	}
	if home == string(filepath.Separator) {
		return ""
	}
	if _, err := os.Stat(filepath.Join(home, "go.mod")); err != nil {
		return ""
	}
	return home
}

// BundleOfficeHome resolves the office home for a bundle (double-click)
// launch: the directory holding the .app when that directory is a
// niuma checkout. "" when not applicable — the caller then degrades to
// its own fallback.
func BundleOfficeHome() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return bundleOfficeHome(exe)
}
