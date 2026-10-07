//go:build !windows

package server

import (
	"os"
	"syscall"
)

// stdinIsInteractive reports whether stdin is the process's REAL
// terminal. ModeCharDevice alone was not enough: /dev/null — the stdin
// of headless AI scripts (nohup, cron, background launches) — is also
// a character device, which let a non-interactive start pass ARB-1 and
// take a healthy room over (验收甲's t_48 finding, reproduced twice).
// The tight test: stdin's device numbers must equal the controlling
// terminal's — a redirected /dev/null is a char device but a DIFFERENT
// one, and a process without a controlling terminal cannot open
// /dev/tty at all. Plain syscall.Stat_t, no ioctl, same on darwin and
// linux.
func stdinIsInteractive() bool {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return false // no controlling terminal: headless by definition
	}
	defer tty.Close()
	si, err1 := os.Stdin.Stat()
	ti, err2 := tty.Stat()
	if err1 != nil || err2 != nil {
		return false
	}
	a, ok1 := si.Sys().(*syscall.Stat_t)
	b, ok2 := ti.Sys().(*syscall.Stat_t)
	return ok1 && ok2 && a.Dev == b.Dev && a.Rdev == b.Rdev
}
