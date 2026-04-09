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
