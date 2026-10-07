//go:build !windows

package agents

import (
	"os/exec"
	"syscall"
)

// setNewGroup puts the agent in its own process group so Stop can
// terminate the whole tree (the agent spawns children such as its
// listen process).
func setNewGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killTree terminates the agent's whole process group.
func killTree(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
