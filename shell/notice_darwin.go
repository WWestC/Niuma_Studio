//go:build darwin

package shell

import (
	"fmt"
	"os/exec"
	"strings"
)

// UserNotice posts a macOS notification for a start path that has no
// window of its own (the bundle relaunch, a refused second instance):
// those paths' stdout/stderr are invisible to a double-clicking user,
// so the only visible channel is an OS banner. osascript's display
// notification needs no setup (unlike the UNUserNotificationCenter
// machinery the main window uses — that one is bound to the webview's
// lifetime, which these short-lived processes never reach). Best
// effort, same rule as FocusApp: a notice failure must never fail the
// start path it decorates.
func UserNotice(title, body string) {
	if title == "" || body == "" {
		return
	}
	esc := func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		return strings.ReplaceAll(s, `"`, `\"`)
	}
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, esc(body), esc(title))
	_ = exec.Command("osascript", "-e", script).Run()
}
