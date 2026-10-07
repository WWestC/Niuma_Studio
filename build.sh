#!/usr/bin/env bash
# Build niuma（牛马工作室）for the current platform.
# The app window is native on every platform (macOS/WebKit, Windows/
# WebView2, Linux/WebKitGTK — 和 ZCode 一样), so cgo is always on:
# Xcode/clang on macOS, mingw-w64 gcc on Windows, and gtk+-3.0 +
# webkit2gtk-4.1 dev packages on Linux. Cross-compiling needs the
# target's cgo toolchain (see .github/workflows/release.yml for the
# reliable CI matrix build).
# NOTE: do NOT strip symbols with -ldflags "-s -w" — symbol-stripped
# binaries trip Windows Smart App Control heuristics and get blocked.
# (-ldflags "-X main.version=…" is fine and used below — injecting a
# string, not stripping anything.)
set -euo pipefail
cd "$(dirname "$0")"

command -v go >/dev/null || { echo "error: Go toolchain not found in PATH"; exit 1; }
export CGO_ENABLED=1

# ./build.sh dev — the developer loop: run straight from source with
# NIUMA_DEV=1. Web assets are served from the repo's web/ (no rebuild
# for frontend edits — save a file and the window reloads itself over
# /app/__reload); Go changes still need a re-run (Ctrl+C quits clean —
# the exit choreography folds every child process, so relaunching is
# always a clean start).
if [ "${1:-}" = "dev" ]; then
  shift
  echo "dev mode: NIUMA_DEV=1 go run . $*  （web/ 直读磁盘，改完存盘窗口自动重载）"
  NIUMA_DEV=1 exec go run . "$@"
fi

out=niuma
case "$(go env GOOS)" in
  windows) out=niuma.exe ;;
esac

# version stamp: the git tag is the single source of truth —
# v1.2.3 on a tag, v0.5-51-g79f767f between tags, "dev" outside a
# repo. Injected via -X; same string lands in Info.plist below.
ver=$(git describe --tags --always --dirty 2>/dev/null || echo dev)

# Windows: fold the brand icon into the exe as resources — same
# discipline as the .icns below, the brand PNGs stay the single
# source of truth (icongen renders the .ico, windres compiles app.rc
# into a .syso that go build auto-links; see the .rc for the slot
# rationale). amd64 only: the one Windows target this repo ships.
if [ "$(go env GOOS)" = "windows" ] && [ "$(go env GOARCH)" = "amd64" ]; then
  go run ./tools/icongen -ico brand/icon-inverted.ico
  windres -O coff packaging/windows/app.rc -o rsrc_windows_amd64.syso
fi

# -H windowsgui (Windows only): the exe is a GUI-subsystem program —
# double-clicking allocates NO console window (the app is its window;
# boot logs land in %TEMP%\niuma-boot.log). Terminal use keeps its
# output via the AttachConsole shim in boot_console_windows.go.
gui=
if [ "$(go env GOOS)" = "windows" ]; then
  gui="-H windowsgui"
fi

go build -trimpath -ldflags "$gui -X main.version=$ver" -o "$out" .
echo "built $out v$ver ($(wc -c <"$out") bytes)"
echo "run : ./$out            (the app: one window, room + workbench)"
echo "cli : ./$out say --name Bot \"hello\""

# macOS: also assemble the .app bundle (v2 P6 单进程单窗口) — one app,
# one process, one window: the room serves and the native window shows
# /app (management boards + the pixel room canvas board together). A
# relaunch while the app is running just focuses it. On Windows the
# mingw-built niuma.exe is already the whole app (WebView2 window);
# on Linux likewise (WebKitGTK window) — no headless mode anywhere.
if [ "$(go env GOOS)" = "darwin" ]; then
  app=Niuma_Studio.app
  rm -rf "$app"
  mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
  cp "$out" "$app/Contents/MacOS/$out"
  go run ./tools/icongen "$app/Contents/Resources/AppIcon.iconset"
  iconutil -c icns "$app/Contents/Resources/AppIcon.iconset"
  rm -rf "$app/Contents/Resources/AppIcon.iconset"
  # unquoted heredoc on purpose: ${ver} must expand so the bundle
  # carries the same stamped version as the binary.
  cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>              <string>Niuma Studio</string>
  <key>CFBundleDisplayName</key>       <string>牛马工作室</string>
  <key>CFBundleIdentifier</key>        <string>local.niuma</string>
  <key>CFBundleVersion</key>           <string>${ver}</string>
  <key>CFBundleShortVersionString</key><string>${ver}</string>
  <key>CFBundlePackageType</key>       <string>APPL</string>
  <key>CFBundleExecutable</key>        <string>niuma</string>
  <key>CFBundleIconFile</key>          <string>AppIcon</string>
  <key>LSMinimumSystemVersion</key>    <string>11.0</string>
  <key>NSHighResolutionCapable</key>   <true/>
</dict>
</plist>
PLIST
  # Bundle 级 ad-hoc 签名——通知授权的前置条件：链接器只给 Mach-O 盖
  # ad-hoc 章（Identifier=a.out、Info.plist 不绑定、资源不封印），
  # 这样的 bundle 在 macOS 眼里是无效身份：UNUserNotificationCenter 的
  # 授权请求会被毫秒级拒绝（didGrant:0 hasError:1，连「想给你发通知」
  # 的系统问询都不弹），通知横幅全灭（2026-10-02 弹窗失灵之夜的元凶）。
  # 签 bundle（Identifier=local.niuma、资源封印、Info.plist 绑定）后
  # 系统才肯问用户。ad-hoc 够本地用；每次重打包 CDHash 变，系统可能
  # 重新问一次授权，答「允许」即可。
  if command -v codesign >/dev/null; then
    codesign --force --sign - --identifier local.niuma "$app"
    echo "signed: $app (ad-hoc bundle 签名——系统通知可用的前提)"
  else
    echo "warn: codesign 不可用——bundle 未签名，系统通知横幅将不可用" >&2
  fi
  echo "bundle: $app (双击即整个应用：房间＋工作台一个窗口，Dock 单图标)"
fi
