package util

// The boot-time CLI reachability probe. Seated members always get the
// absolute exe path stamped into their handoff prompts (agents.
// SelfExe), but the HOST's own terminals and self-opened AI sessions
// can only find niuma through PATH — and when it isn't there, those
// sessions silently degrade into improvising over ~/.niuma raw state
// (the book-project incident: a fresh session in the project workspace
// answered "niuma CLI 未安装，改从 ~/.niuma 数据目录直读" — an
// environment fact the host learned only by bumping into it). This
// probe turns that surprise into a boot-time notice: announce the fact
// before it gets hit, not after.

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// NiumaOnPath answers where a plain shell on this machine resolves the
// niuma CLI; "" = nowhere. Chain: this process's PATH first (a
// terminal launch is the honest case), then the usual bin homes a GUI
// launch's bare PATH misses (/usr/local/bin, /opt/homebrew/bin), then
// a login shell's own resolution on darwin — the same tier
// zcode.resolveNode uses for node, covering PATH edits a GUI launch
// never sees (nvm/volta/mise style).
func NiumaOnPath() string {
	var shell func() string
	if runtime.GOOS == "darwin" {
		shell = darwinShellCLI
	}
	return niumaOnPathWith(exec.LookPath, statExists, shell)
}

// niumaOnPathWith is NiumaOnPath's injectable core (tests). A nil
// shell probe counts as "not applicable" — same as a non-darwin boot.
func niumaOnPathWith(look func(string) (string, error), stat func(string) error, shell func() string) string {
	if p, err := look("niuma"); err == nil && p != "" {
		return p
	}
	for _, cand := range []string{"/usr/local/bin/niuma", "/opt/homebrew/bin/niuma"} {
		if stat(cand) == nil {
			return cand
		}
	}
	if shell != nil {
		if p := shell(); p != "" {
			return p
		}
	}
	return ""
}

func statExists(p string) error {
	_, err := os.Stat(p)
	return err
}

// darwinShellCLI asks a login zsh where IT resolves niuma — the
// closest proxy for the user's interactive terminal. Bounded to 3s and
// only reached when everything cheaper already missed; the answer must
// be one clean path or it is discarded.
func darwinShellCLI() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/zsh", "-lc", "command -v niuma").Output()
	p := strings.TrimSpace(string(out))
	if err != nil || p == "" || strings.ContainsAny(p, "\n\r ") {
		return ""
	}
	return p
}

// GoRunTempExe reports whether exe is a `go run` scratch binary
// (…/go-buildNNN/b001/exe/niuma): linking THAT into a bin home would
// dangle on the next tmp clean, so the notice must say "build a real
// binary first" instead of handing out a symlink command.
func GoRunTempExe(exe string) bool {
	return strings.Contains(exe, "go-build")
}

// CLIMissingNotice composes the boot notice for a missing CLI. The
// returned template doubles as the i18n key (Sf discipline: zh verbatim,
// en dictionary entry with the same verbs); args fill it.
func CLIMissingNotice(exe string) (string, []any) {
	if GoRunTempExe(exe) {
		return "[环境] 本机 PATH 上没有 niuma 命令，且当前进程是 go run 的临时二进制（link 它会悬空）——先落一个正式二进制：go build -o /usr/local/bin/niuma，此后任何文件夹里的终端与会话都能直连本工作室；在座成员不受影响（调度器发的是绝对路径）。", nil
	}
	return "[环境] 本机 PATH 上没有 niuma 命令——终端与其他会话里直接运行 niuma 会扑空（在座成员不受影响，调度器发的是绝对路径）。要装上：ln -s \"%s\" /usr/local/bin/niuma（Apple Silicon 也可 /opt/homebrew/bin），装好后任何文件夹里的会话都能直连本工作室。", []any{exe}
}
