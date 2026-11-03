//go:build !windows

package tui

import (
	"os/exec"
	"syscall"
)

func statuslineProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func statuslineCancelKill(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd.Process.Kill()
}
