//go:build darwin

package shell

import (
	"fmt"
	"os/exec"

	"github.com/WWestC/Niuma_Studio/server"
)

// FocusApp raises the already-running app's window (the bundle
// relaunch path): the pid comes from the port discovery file, the
// raise itself is a System Events frontmost switch by unix id —
// exact, even with same-named processes around, and it works for a
// bundle-launched or terminal-launched instance alike.
func FocusApp(port int) {
	pid, ok := server.InstancePID(port)
	if !ok {
		return
	}
	script := fmt.Sprintf(
		`tell application "System Events" to set frontmost of (first process whose unix id is %d) to true`, pid)
	// best-effort nicety: a focus failure (headless session, System
	// Events permission denied) must not fail the relaunch
	_ = exec.Command("osascript", "-e", script).Run()
}
