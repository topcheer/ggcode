//go:build windows

package tui

import (
	"os/exec"
	"syscall"
)

func statuslineProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

func statuslineCancelKill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
