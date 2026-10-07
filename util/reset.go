package util

// reset.go — the studio's factory reset, data half (the choreography
// lives in main: POST /reset only signals; the wipe runs AFTER the
// exit-orderly teardown so nothing flushes a snapshot back over a
// cleaned slate). Scope is every piece of studio-authored state under
// the home directory:
//
//   ~/.niuma/            the v2 root — rooms & chat history, seats,
//                        notices, plans, staffing, projects, meetings,
//                        requirements, capabilities, audit, the
//                        assistant's session pin, the degraded
//                        workspace residue, and the v3 docs database
//                        (~/.niuma/kb: log.jsonl + snapshot.json)
//   ~/.niuma_kb/         the legacy pre-v3 markdown tree — imported
//                        once into ~/.niuma/kb at boot; wiping both
//                        means the next boot re-seeds exactly like a
//                        first install
//   ~/.niuma_kb.imported-*  the one-shot import's timestamped backups
//   ~/.niuma_agents.json AI onboarding profiles (档案)
//   ~/.niuma_tasks.json  the task ledger (flat legacy location)
//   ~/.niuma_ranks.json  the rank registry (flat legacy location)
//   ~/.niuma_blackboard.md  the legacy import source — must go too,
//                        else the "fresh" boot re-imports the old
//                        announcement as the lobby notice
//   ~/.niuma_token_*     per-name seat tokens (cli/seat.go)
//   ~/.niuma_wait_*      per-name wait cursors (cli/wait.go)
//
// Deliberately NOT touched: ~/.niuma_port (runtime discovery — the
// server removes it on Close), the members' workspace directories (a
// project's workspace is wherever the owner pointed it — possibly the
// office checkout itself; deliverables are the owner's files), and
// ZCode's own stores under ~/.zcode (the sidebar rows are archived
// through the sanctioned fold, never by reaching into another app's
// index).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// studioDataPaths enumerates every studio-authored path under home, in
// wipe order. Globs (tokens/cursors) are expanded by the caller.
func studioDataPaths(home string) (dirs, files, globs []string) {
	return []string{
		filepath.Join(home, ".niuma"),
		filepath.Join(home, ".niuma_kb"),
	}, []string{
		filepath.Join(home, ".niuma_agents.json"),
		filepath.Join(home, ".niuma_tasks.json"),
		filepath.Join(home, ".niuma_ranks.json"),
		filepath.Join(home, ".niuma_blackboard.md"),
	}, []string{
		filepath.Join(home, ".niuma_token_*"),
		filepath.Join(home, ".niuma_wait_*"),
		filepath.Join(home, ".niuma_kb.imported-*"),
	}
}

// WipeStudioData removes every piece of studio-authored state under
// the current user's home, returning what it removed. Best effort per
// entry: a locked file is reported, not fatal — the reset proceeds
// (the relaunch re-seeds whatever it finds absent). An unresolvable
// home wipes nothing and errors.
func WipeStudioData() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return wipeStudioDataAt(home)
}

// wipeStudioDataAt is WipeStudioData over an explicit home (tests).
func wipeStudioDataAt(home string) ([]string, error) {
	if home == "" || filepath.Clean(home) == string(filepath.Separator) {
		return nil, fmt.Errorf("拒绝在不安全的家目录 %q 下擦除", home)
	}
	dirs, files, globs := studioDataPaths(home)
	var removed []string
	var errs []error
	for _, d := range dirs {
		if _, err := os.Stat(d); err != nil {
			continue // absent: nothing to remove (already fresh)
		}
		if err := os.RemoveAll(d); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", d, err))
			continue
		}
		removed = append(removed, d)
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if err := os.Remove(f); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f, err))
			continue
		}
		removed = append(removed, f)
	}
	for _, g := range globs {
		hits, _ := filepath.Glob(g)
		for _, f := range hits {
			if err := os.Remove(f); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", f, err))
				continue
			}
			removed = append(removed, f)
		}
	}
	return removed, joinErrs(errs)
}

func joinErrs(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return fmt.Errorf("%s", strings.Join(msgs, "; "))
}

// RelaunchSelf starts one detached copy of this executable with the
// same arguments and environment — the reset/rebuild relay: the old
// process exits right after, the copy boots into the freshly-seeded
// studio (or the freshly-swapped binary). The caller MUST have
// released the port (srv.Close) before spawning: the child then finds
// a free port and a clean discovery file, no multi-open refusal, no
// takeover dance. A failure is reported (the caller logs and exits
// anyway — the data is already reset; a manual relaunch lands in the
// same fresh state).
//
// macOS bundle starts relay through LaunchServices instead (see
// openBundleScript): a bare exec of the inner bundle binary is an
// unregistered launch and the Dock tiles it with a generic/ghost icon
// — the settings-rebuild relay was the invisible culprit behind the
// 「应用图标坏了」sightings.
func RelaunchSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("解析自身路径失败: %w", err)
	}
	if script, ok := openBundleScript(exe, os.Getpid(), os.Args[1:]); ok {
		// 输出丢弃：替身要等本进程退场才被 open 拉起，继承的管道届时
		// 已随父侧关闭，写它会 SIGPIPE 杀掉接力 sh；open 的成败无人接收。
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Stdin = nil
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("拉起替身进程失败: %w", err)
		}
		// 不 Wait：接力 sh 必须等本进程死后才去 open——本进程立刻退场
		return nil
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// stdin 丢弃：替身不经任何终端互动起步，别继承可能关闭的管道
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("拉起替身进程失败: %w", err)
	}
	// 不 Wait：父进程马上退场，替身交给系统收养
	return nil
}

// openBundleScript is RelaunchSelf's macOS bundle half: when the exe
// lives inside Foo.app, the relay goes through Launch Services —
// /usr/bin/open on the bundle — because a bare exec of the inner
// binary is an unregistered launch and the Dock tiles it with a
// generic/ghost icon instead of the bundle's (the v2.6 reveal-channel
// lesson; the settings-rebuild relay carried the same defect — every
// 「重新编译并重启」left the Dock icon broken). open must fire only
// AFTER the old process is gone: Launch Services would otherwise see
// the still-dying registered instance and merely "reopen" it, and the
// studio would never come back — hence the /bin/sh one-liner that
// polls the parent pid first, then execs open. ok is false on other
// platforms and for terminal/dev runs (exe outside any bundle):
// those keep the direct-exec relay, where no Dock identity is at
// stake.
func openBundleScript(exe string, pid int, args []string) (string, bool) {
	if runtime.GOOS != "darwin" {
		return "", false
	}
	macOS := filepath.Dir(exe) // …/Foo.app/Contents/MacOS
	if !strings.HasSuffix(macOS, ".app/Contents/MacOS") {
		return "", false
	}
	bundle := filepath.Dir(filepath.Dir(macOS)) // …/Foo.app
	var b strings.Builder
	fmt.Fprintf(&b, "while kill -0 %d 2>/dev/null; do sleep 0.1; done; exec /usr/bin/open %q", pid, bundle)
	if len(args) > 0 { // --args 之后整段原样归应用
		b.WriteString(" --args")
		for _, a := range args {
			fmt.Fprintf(&b, " %q", a)
		}
	}
	return b.String(), true
}
