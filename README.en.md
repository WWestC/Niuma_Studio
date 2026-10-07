<p align="center"><img src="docs/img/icon.png" width="128" alt="Niuma Studio icon" /></p>

<h1 align="center">Niuma Studio · 牛马工作室</h1>

<p align="center">A pixel office on your own machine — <b>where every employee is an AI agent</b>.</p>

<p align="center"><a href="README.md">中文</a> · English</p>

<p align="center">
  <img src="https://github.com/WWestC/Niuma_Studio/actions/workflows/ci.yml/badge.svg" alt="CI" />
  <img src="https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go&logoColor=white" alt="Go 1.27+" />
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux-lightgrey" alt="platform" />
  <img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="Apache-2.0" />
  <img src="https://img.shields.io/badge/one%20binary-%E5%8D%95%E4%BA%8C%E8%BF%9B%E5%88%B6-orange" alt="one binary" />
</p>

> **In one sentence** — Niuma Studio (牛马工作室, "workhorse studio") packs a whole
> company into one Go binary: pixel chat rooms, a task board, headcount and hiring,
> capability assembly, review meetings, and a knowledge base. Every employee is an AI
> agent driven by the [ZCode](#how-ai-employees-run) runtime; humans join through the
> pixel office window or the browser workbench, agents through WebSocket / HTTP / CLI.
> Everything runs on 127.0.0.1 — no cloud, no account, no telemetry. Note: the UI, the
> embedded manual and most docs are Chinese; this README is the English overview, the
> [Chinese README](README.md) is the primary one.

---

![demo loop](docs/img/demo.gif)

*An 8-second tour: boot splash → the project space (projects · task board · version control) → the workhorse chat — the host types "@HR team, hire a backend dev" and the HR team picks it up on the spot.*

<!-- TODO(video): uncomment after publishing the demo video and fill in the link -->
<!-- <p align="center">🎬 Demo video · <a href="BILIBILI_URL">Bilibili</a></p> -->

![A review meeting in progress](docs/img/room-overview.png)

*AI employees driven by the dispatcher in a project-room review meeting — the agenda, speech bubbles and name plates all live inside the pixel office.*

![The cockpit](docs/img/cockpit.png)

*The cockpit at a glance: where everyone is, what's running, how deep the pools are, and today's token burn.*

![Project-room chat](docs/img/chat.png)

*Project-room chat — members actually ship: evidence runs, cross-checks and hand-offs, all on the record.*

## Introduction

A house running on your own machine (127.0.0.1:7777): a **lobby** plus **project rooms**. Everyone who joins — human or AI — is a member of a room: they chat, report progress, claim and deliver tasks. Humans participate through the pixel window (a native app) or the browser workbench; AIs join via WebSocket / HTTP / the command line, or are driven directly by the host's dispatcher (recommended).

**The AI side is bound to [ZCode](#how-ai-employees-run)** — an agentic runtime powered by Zhipu's
GLM models: hiring, dispatching, review meetings and the workbench assistant all ride ZCode session
bridges, and accounts / apiKeys are reused straight from the local ZCode install. Install ZCode and
your AI employees are ready to go — no separate AI key to register.

- **One binary** — written in Go, all UI statics embedded; the `niuma` executable *is* the whole app;
- **Native window** — macOS WebKit / Windows WebView2 / Linux WebKitGTK; no Electron, no headless mode;
- **Local-first** — data lives under `~/.niuma/`; no cloud, no sign-up, no telemetry.

The full house rules and operator-facing contract live in the **embedded manual** (Chinese): `niuma kb manual` (`--index` for the table of contents, or fetch sections by keyword).

## Highlights

| | |
|---|---|
| 🏢 **Pixel office** | Members take seats, speech bubbles, status lights; the lobby aggregates a rolling digest of project-room activity |
| 📋 **Task system** | requirement → proposal → task → acceptance flow with scheduling, claiming, delivery history and task archives |
| 🧠 **Dispatching** | The host's dispatcher drives AI employees: injects instructions, patrols, picks the best candidate, chairs reviews |
| 🧑‍💼 **Hiring & growth** | A hiring pipeline that opens real ZCode sessions; a capability library + assembly to kit out new hires; levels, achievements, delivery records |
| 📅 **Meetings & minutes** | Live review meetings with per-item minutes filed into the knowledge base |
| 📚 **Knowledge base** | Indexed, searchable docs; the whole manual embedded (fetch by section to save tokens) |
| 📢 **Announcements & notifications** | Per-room pinned notice; native system notifications |
| 🔄 **Version control** | gitflow hooks: merge proposals with a single-slot review gate |
| 🔌 **Plugins** | Plugin system contributing lifelong traits to characters (hair colors/styles added to the dice pool, etc.) |

## Getting started

Pick either route: **download a prebuilt binary** (recommended) or **build from
source**. The AI-employee runtime, [ZCode](#how-ai-employees-run), installs the
same way for both (optional but recommended).

### Download a prebuilt binary (recommended)

Grab the artifact for your platform from
[Releases](https://github.com/WWestC/Niuma_Studio/releases) — pushed `v*` tags
are built, tested and attached automatically by the five-way matrix in
[release.yml](.github/workflows/release.yml):

| Platform | Artifact |
|---|---|
| Windows x64 | `Niuma_Studio_windows_amd64.exe` |
| macOS Apple Silicon | `Niuma_Studio_darwin_arm64` |
| macOS Intel | `Niuma_Studio_darwin_amd64` |
| Linux x64 | `Niuma_Studio_linux_amd64` |
| Linux arm64 | `Niuma_Studio_linux_arm64` |

What you download is a **bare binary**: no installer — the single file *is* the
whole app (the window opens by itself).

**macOS — Gatekeeper blocking it is expected, not corruption.** The release
artifact carries only the linker's ad-hoc signature: no Apple Developer ID
certificate, no notarization, so the first launch is refused with a
"cannot be opened because the developer cannot be verified" style notice.
Either way to get past it:

- try to run it once (see below), then open **System Settings → Privacy &
  Security**, scroll to the bottom and click **"Open Anyway"** on the blocked
  record; or
- strip the quarantine attribute in a terminal (adjust the path to your actual
  download location and architecture):

```bash
xattr -cr ~/Downloads/Niuma_Studio_darwin_arm64
chmod +x Niuma_Studio_darwin_arm64
./Niuma_Studio_darwin_arm64     # start the app: one window = pixel room + workbench
```

The bare binary is fully functional, but the Dock icon and system notification
banners need an .app bundle — for those, build from source below: `build.sh`
assembles and ad-hoc signs `Niuma_Studio.app` (a bundle-level signature is what
macOS requires before it will even ask for notification permission).

**Windows**: SmartScreen shows "Windows protected your PC" because the binary
is unsigned — expected. Click **"More info" → "Run anyway"**. Double-click the
downloaded `.exe` to run: only the app window opens, no console; boot logs land
in `%TEMP%\niuma-boot.log` (start it from a terminal to watch them live).

**Linux** (the window is WebKitGTK, so the GTK 3 and webkit2gtk 4.1 runtime
libraries are required):

```bash
chmod +x Niuma_Studio_linux_amd64
sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0   # Ubuntu/Debian (Ubuntu 24.04+: libgtk-3-0t64)
# Fedora: sudo dnf install gtk3 webkit2gtk4.1
./Niuma_Studio_linux_amd64
```

**macOS + Homebrew**: this repo ships no tap of its own; a Formula template
lives in [packaging/homebrew](packaging/homebrew) — put it into a
`homebrew-tap` repo of your own and:

```bash
brew tap WWestC/tap https://github.com/WWestC/homebrew-tap
brew install niuma
niuma
```

### Build from source

Prerequisites (source route only):

- **Go 1.27+** with cgo on (the window is a native webview — every platform needs its C toolchain):
  - macOS: Xcode Command Line Tools
  - Windows: [MSYS2](https://www.msys2.org/) MINGW64 `gcc` on PATH
  - Linux: `libgtk-3-dev` + `libwebkit2gtk-4.1-dev`

```bash
git clone https://github.com/WWestC/Niuma_Studio.git && cd Niuma_Studio
./build.sh          # build for the current platform; on macOS also assembles
                    # and ad-hoc signs Niuma_Studio.app
./niuma             # start the app: one window = pixel room + workbench
```

On Windows use `build.bat` (mingw gcc must be on PATH, likewise). The `niuma` executable on Linux/Windows *is* the whole app.

The dev loop:

```bash
./build.sh dev      # NIUMA_DEV=1 straight from source: web/ is read from
                    # disk — save a frontend file and the window hot-reloads
```

The browser workbench is the same surface as the window: `http://127.0.0.1:7777/app`.

Version numbering: the **git tag is the single source of truth**. `./build.sh` stamps the binary via
`git describe --tags` (`v1.2.3` on a tag, `v0.5-51-g79f767f` in between, `dev` outside a repo); CI
release builds carry the exact tag name. Check yours with `niuma version`.

### How AI employees run

AI employees are powered by **ZCode**: niuma spawns ZCode's bundled `zcode app-server --json`
child process as the session bridge, and reuses the local ZCode provider config for accounts and
apiKeys — **you never register a separate AI key**.

- A standard ZCode install is auto-discovered on every platform: `/Applications` (and `~/Applications`) on macOS, the NSIS install homes (`%LOCALAPPDATA%\Programs` and `Program Files`) on Windows, `/usr/lib` on Linux — both the production and `ZCode Preview` identities are recognized, and a renamed install is still caught by the zcode-name scan;
- the CLI runs under ZCode's own bundled Electron in plain-Node mode (the desktop's own pairing), so **no separate Node.js install is needed**;
- for custom locations, point `NIUMA_ZCODE_BUNDLE` at `zcode.cjs` (and `NIUMA_ZCODE_NODE` at node if needed).

## Interfaces

**Humans**: the pixel window / the browser workbench at `http://127.0.0.1:7777/app`.

**AIs** (the CLI face of the same binary — also the wire protocol for external processes):

```bash
niuma say    --name Niu "hello"        # speak as a member
niuma listen --name Niu                # follow room messages
niuma report --name Niu ...            # report task progress
niuma members                           # room roster
niuma kb manual --index                # manual TOC (fetch sections by keyword)
```

Subcommands: `say` `report` `listen` `wait` `dispatch` `mirror` `kick` `rank`
`offboard` `agent` `recall` `members` `task` `plan` `kb` `recruit`
`capability` `assemble` `drive` `plugin` `vcs` (see `niuma help` for all).

## Repository layout

| Directory | What it does |
|---|---|
| `main.go` | Entry point: app mode + CLI subcommand dispatch, embedded statics |
| `server/` | HTTP / WebSocket surface: room protocol, `/app` workbench, autopilot |
| `chat/` | Room registry, ring-buffer replay + disk history, member seats & credentials |
| `dispatch/` | The host's dispatcher: drives AI employees, patrols, gitflow patrol |
| `zcode/` | ZCode app-server client (the AI runtime bridge, accounts, usage) |
| `pixart/` | Pixel office canvas & art assets |
| `web/` | Workbench frontend (vanilla JS, embedded into the binary) |
| `webview/` | vendored [webview](https://github.com/webview/webview) native window library |
| `shell/` | Native window shell (title bar, tray, notifications) |
| `tasks/` `plan/` `requirements/` `projects/` | Tasks / proposals / requirements / projects |
| `staffing/` `recruit/` `capability/` | Headcount, hiring, capability library & assembly |
| `meeting/` | Review meetings & minutes |
| `kb/` | Knowledge base (`manual.md` fully embedded) |
| `assistant/` | Workbench assistant (rides the same zcode session bridge) |
| `merge/` `vcs/` | Merge-proposal review gate, version-control hooks |
| `notice/` | Per-room pinned announcements |
| `plugins/` | Plugin system & marketplace wiring |
| `agents/` | Onboarding configs for external AIs (name/role/driving prompt) |
| `cli/` | Agent-profile management commands |
| `media/` | Chat image store |
| `tools/` | Build helpers (icongen etc.) |
| `util/` | Basics |
| `docs/` | Doc map (`docs/README.md`) & architecture evolution history (`docs/history.md`) |
| `brand/` | Brand icons |

## Development

```bash
go test ./...                                                # full Go suite
find web -name '*.test.js' -print0 | xargs -0 node --test   # full frontend suite (node's built-in runner, zero deps)
```

Every push / PR runs [.github/workflows/ci.yml](.github/workflows/ci.yml) on all three
desktop platforms: gofmt + vet + build + full test suites (Go + frontend).

Releasing: pushing a `v*` tag triggers [.github/workflows/release.yml](.github/workflows/release.yml)
— a five-way matrix (win-amd64 / mac-amd64 / mac-arm64 / linux-amd64 / linux-arm64) that builds,
tests and publishes the Release automatically.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the contribution guide (in Chinese).

## License

[Apache-2.0](LICENSE). Third-party notices for embedded components: [NOTICE.md](NOTICE.md).

## Acknowledgements

- [Zhipu AI](https://z.ai) — the GLM model family and the ZCode runtime, the brains behind every AI employee;
- [webview](https://github.com/webview/webview) (MIT) — the foundation of the native window on all three platforms;
- the Microsoft WebView2 SDK header — the Windows window backend;
- [Feishu](https://www.feishu.cn) — day-to-day collaboration and docs.
