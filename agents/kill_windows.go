//go:build windows

package agents

import (
	"os/exec"
	"strconv"

	"github.com/WWestC/Niuma_Studio/util"
)

// setNewGroup is a no-op on Windows; killTree handles the tree via
// taskkill.
func setNewGroup(cmd *exec.Cmd) {}

// killTree terminates the agent and everything it spawned.
func killTree(cmd *exec.Cmd) {
	_ = util.HideConsole(exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))).Run()
}
