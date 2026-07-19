//go:build darwin || linux

package mcp

import (
	"os/exec"
	"syscall"
)

func configureMCPCommandProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// killProcessGroup kills the entire process group led by cmd.
// Setsid ensures the child is a session leader, so PGID == PID.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
