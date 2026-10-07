@echo off
rem Build niuma for the current platform.
rem The app window is native WebView2 (和 ZCode 一样), so cgo is on and
rem mingw-w64 gcc must be in PATH (e.g. from MSYS2 MINGW64).
rem NOTE: do NOT strip symbols with -ldflags "-s -w" — symbol-stripped
rem binaries trip Windows Smart App Control heuristics and get blocked.
rem (-ldflags "-X main.version=…" is fine and used below.)
setlocal
where go >nul 2>nul || (echo error: Go toolchain not found in PATH & exit /b 1)
where gcc >nul 2>nul || (echo error: mingw-w64 gcc not found in PATH ^(MSYS2 MINGW64: cgo builds the WebView2 window^) & exit /b 1)
set CGO_ENABLED=1

rem App icon: fold the brand iconset into the exe as resources.
rem icongen renders a deterministic multi-size .ico from the brand
rem PNGs; windres (binutils — ships with the mingw cgo needs anyway)
rem compiles packaging/windows/app.rc into a .syso that go build
rem auto-links (see the .rc for the 1 / 32512 slot rationale).
rem amd64 only: that is the one Windows target this repo ships
rem (release.yml matrix); other GOARCH builds skip the icon and
rem still work.
set GOARCH_NOW=
for /f "delims=" %%A in ('go env GOARCH') do set "GOARCH_NOW=%%A"
if "%GOARCH_NOW%"=="amd64" (
  go run ./tools/icongen -ico brand\icon-inverted.ico || (echo error: icongen failed to render the brand .ico & exit /b 1)
  windres -O coff packaging\windows\app.rc -o rsrc_windows_amd64.syso || (echo error: windres failed to compile app.rc ^(mingw binutils missing?^) & exit /b 1)
) else (
  echo note: GOARCH %GOARCH_NOW% — skipping the app icon ^(amd64 only^)
)

rem version stamp: git tag is the single source of truth ("dev" outside a repo)
set VER=dev
for /f "delims=" %%V in ('git describe --tags --always --dirty 2^>nul') do set "VER=%%V"

rem -H windowsgui: the exe is a GUI-subsystem program — double-clicking
rem allocates NO console window (the app is its window; boot logs land
rem in %TEMP%\niuma-boot.log). Terminal use keeps its output via the
rem AttachConsole shim in boot_console_windows.go.
go build -trimpath -ldflags "-H windowsgui -X main.version=%VER%" -o niuma.exe .
if errorlevel 1 (echo build failed & exit /b 1)

for %%A in (niuma.exe) do echo built niuma.exe v%VER% ^(%%~zA bytes^)
echo run: niuma.exe          ^(the app: one window, room + workbench^)
echo cli: niuma.exe say --name Bot "hello"
