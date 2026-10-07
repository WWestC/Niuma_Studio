//go:build !windows

package util

import "os/exec"

// HideConsole is a Windows-only concern (see console_windows.go — the
// windowsgui app has no console for children to share). Elsewhere
// children inherit the terminal normally; pass the cmd through as-is.
func HideConsole(cmd *exec.Cmd) *exec.Cmd { return cmd }
