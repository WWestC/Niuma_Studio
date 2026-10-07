//go:build windows

package util

import (
	"os/exec"
	"syscall"
)

// HideConsole stops Windows from flashing a console window each time the
// app spawns a console-subsystem child (python3 / powershell / reg / git /
// node / taskkill …). The app itself is a windowsgui binary with no
// console of its own, and a console child of a console-less parent gets a
// fresh VISIBLE console that dies with it — every keeper beat and member
// spawn used to strobe a terminal on screen. CREATE_NO_WINDOW gives the
// child a hidden console instead; piped stdio is unaffected, and the
// child's own children inherit the hidden console. Wrap every internal
// exec: HideConsole(exec.CommandContext(ctx, …)).
func HideConsole(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	return cmd
}
