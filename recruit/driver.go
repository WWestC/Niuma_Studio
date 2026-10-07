// Package recruit drives GUI-native session creation for the hiring
// pipeline (v0.5 M2): the onboarding brief rides the clipboard into
// ZCode's own 新建任务 flow. Everything here is the productized form
// of the C1 spike (11 real runs, 2 recovered by retry, 0 mis-pastes).
package recruit

import (
	"errors"
	"fmt"
	"github.com/WWestC/Niuma_Studio/zcode"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/util"
)

// C1-measured timings: below these the paste can land before the
// composer is ready and silently vanish.
const (
	delayActivate = 1000 * time.Millisecond // app activate → menu click
	delayComposer = 3000 * time.Millisecond // menu click → paste (new-task view ready)
	delaySend     = 1200 * time.Millisecond // paste → Return
	delayVerify   = 2000 * time.Millisecond // composer row lands in the index late
)

// injectionScript is the C1 §1 sequence verbatim (menu click beats
// Cmd+N — focus is uncertain, the menu never misses).
const injectionScript = `tell application "%s" to activate
delay %.1f
tell application "System Events"
	tell process "%s"
		click menu item "新建任务" of menu 1 of menu bar item "文件" of menu bar 1
	end tell
	delay %.1f
	keystroke "v" using command down
	delay %.1f
	keystroke return
end tell`

// Driver launches one new GUI session by injecting the brief. The
// zero value is not usable; RunDrive (cli) wires the defaults.
type Driver struct {
	AppName   string // GUI app to drive, "ZCode"
	BriefPath string // onboarding brief file (multi-line is fine)
	TaskDB    string // tasks-index.sqlite used for objective verification
	Attempts  int    // whole-sequence retries; C1 recommends 3
	DryRun    bool   // print the sequence instead of running it

	// injectables (tests); nil → real osascript / shell / clock
	RunScript func(script string) error
	Shell     func(name string, args ...string) (string, error)
	Verify    func() (bool, string) // verify 的注入缝（测试）；nil = 原生查任务索引
	Sleep     func(time.Duration)
	Printf    func(string, ...any)
}

// ErrNoWindow fails the run before anything is touched: with every
// window closed the menu click is silently inert and no reopen path
// produces a window (C1 §2.1/§5.1 — a human must open one).
var ErrNoWindow = errors.New("ZCode has no open window — open one manually, then retry")

// Run executes the drive: preflight → clipboard backup → (inject →
// verify) × attempts → clipboard restore. The clipboard is restored
// even when everything fails (respect the operator's environment).
func (d *Driver) Run() error {
	if runtime.GOOS != "darwin" {
		return errors.New("recruit: the GUI injection path is macOS-only")
	}
	if d.AppName == "" {
		d.AppName = "ZCode"
	}
	if d.TaskDB == "" {
		return errors.New("recruit: TaskDB (tasks-index.sqlite path) is required")
	}
	if d.Attempts <= 0 {
		d.Attempts = 3
	}
	brief, err := os.ReadFile(d.BriefPath)
	if err != nil {
		return fmt.Errorf("recruit: read brief: %w", err)
	}
	head := firstLine(string(brief))
	if head == "" {
		return errors.New("recruit: brief is empty")
	}

	script := fmt.Sprintf(injectionScript, d.AppName,
		delayActivate.Seconds(), d.AppName, delayComposer.Seconds(), delaySend.Seconds())

	if d.DryRun {
		d.printf("# recruit drive — dry run (nothing executed, brief body withheld)\n")
		d.printf("# 0. back up the operator's clipboard (unpredictable temp leaf, deleted right after restore)\npbpaste > $TMPDIR/dh_recruit_clipboard_XXXX.bak\n")
		d.printf("# 1. load the brief (content stays in the file, not in this log)\npbcopy < %s\n", d.BriefPath)
		d.printf("# 2. GUI: new task + paste + send\nosascript <<'EOF'\n%s\nEOF\n", script)
		d.printf("# 3. verify — latest running row's title starts with the brief's first line (read from the brief, never printed)\n")
		d.printf("# 4. restore the operator's clipboard\npbcopy < $TMPDIR/dh_recruit_clipboard_XXXX.bak\n")
		d.printf("# retry: on verify failure re-run from step 1 (the clipboard must be re-loaded); up to %d attempts.\n", d.Attempts)
		return nil
	}

	// preflight: a windowless GUI makes the menu click a no-op
	if n, err := d.windowCount(); err != nil {
		return fmt.Errorf("recruit: window preflight: %w", err)
	} else if n == "0" {
		return ErrNoWindow
	}

	// 剪贴板备份（安全核查修复）：操作者的整个剪贴板常含密码——
	// 不可预测的临时名（CreateTemp，0600）替代旧固定名
	// /tmp/dh_recruit_clipboard.bak（可预测名＋跟随符号链接＋永不删
	// 除，是经典的共享机器 symlink 目标与滞留密钥）；恢复成功即删，
	// 只有恢复失败才留盘并告知路径。
	backupPath, err := d.writeClipBackup(d.shellOut("pbpaste"))
	if err != nil {
		return fmt.Errorf("recruit: save clipboard backup: %w", err)
	}
	defer func() {
		if err := d.pasteFile(backupPath); err != nil {
			d.printf("recruit: clipboard restore failed: %v (backup kept at %s — restore manually, then delete)\n", err, backupPath)
			return
		}
		_ = os.Remove(backupPath)
	}()

	for attempt := 1; attempt <= d.Attempts; attempt++ {
		// C1 §4: every retry re-loads the brief — a restore in between
		// (or anything else touching the clipboard) would paste stale
		if err := d.pasteFile(d.BriefPath); err != nil {
			return fmt.Errorf("recruit: load brief into clipboard: %w", err)
		}
		d.printf("recruit: attempt %d/%d — activating %s…\n", attempt, d.Attempts, d.AppName)
		if err := d.runScript(script); err != nil {
			d.printf("recruit: injection error: %v (retrying)\n", err)
		}
		d.sleep(delayVerify)
		if ok, row := d.verify(); ok {
			d.printf("recruit: session launched — %s\n", row)
			return nil
		}
		d.printf("recruit: attempt %d not verified (no fresh running row starting with %q)\n", attempt, head)
	}
	return fmt.Errorf("recruit: %d attempts failed — check the C1 preconditions (window open, display awake, Accessibility granted); the operator's clipboard has been restored from the backup (and the backup deleted)",
		d.Attempts)
}

// verify reports whether the task index has a fresh running row whose
// title starts with the brief's first line (C1 §1.4 — objective,
// never eyeballed).
func (d *Driver) verify() (bool, string) {
	if d.Verify != nil {
		return d.Verify()
	}
	taskID, status, createdAt, ok := zcode.RunningTaskByTitleHead(d.TaskDB, d.BriefPath)
	if !ok {
		return false, ""
	}
	return true, taskID + " " + status + " " + createdAt
}

// windowCount returns the app's open-window count as a string.
func (d *Driver) windowCount() (string, error) {
	script := fmt.Sprintf(`tell application "System Events" to tell process "%s" to return (count of windows)`, d.AppName)
	out, err := d.shell("osascript", "-e", script)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// pasteFile loads a file into the clipboard through the shell —
// pbcopy only reads stdin (the spike's original form).
func (d *Driver) pasteFile(path string) error {
	if _, err := d.shell("/bin/sh", "-c", "pbcopy < "+quote(path)); err != nil {
		return err
	}
	return nil
}

// writeClipBackup persists the clipboard backup to an unpredictable
// 0600 temp leaf and returns its path (安全核查修复——见 Run 内注释).
func (d *Driver) writeClipBackup(content string) (string, error) {
	f, err := os.CreateTemp(os.TempDir(), "dh_recruit_clipboard_*.bak")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

func (d *Driver) printf(format string, args ...any) {
	if d.Printf != nil {
		d.Printf(format, args...)
	}
}

// --- real-world plumbing (swapped out in tests) -------------------------

func (d *Driver) runScript(script string) error {
	if d.RunScript != nil {
		return d.RunScript(script)
	}
	cmd := exec.Command("osascript", "-")
	cmd.Stdin = strings.NewReader(script)
	return cmd.Run()
}

func (d *Driver) shell(name string, args ...string) (string, error) {
	if d.Shell != nil {
		return d.Shell(name, args...)
	}
	out, err := util.HideConsole(exec.Command(name, args...)).Output()
	return string(out), err
}

func (d *Driver) shellOut(name string) string {
	out, _ := d.shell(name)
	return out
}

func (d *Driver) sleep(dur time.Duration) {
	if d.Sleep != nil {
		d.Sleep(dur)
	} else {
		time.Sleep(dur)
	}
}

// firstLine returns the brief's first non-empty line — the composer
// takes it as the task title, so verification matches on it.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// quote wraps a path in single quotes for /bin/sh.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
