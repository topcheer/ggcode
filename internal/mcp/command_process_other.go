//go:build !darwin && !linux && !windows

package mcp

import "os/exec"

func configureMCPCommandProcess(cmd *exec.Cmd) {
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
