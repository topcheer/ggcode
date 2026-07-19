//go:build !darwin && !linux && !windows

package mcp

import "os/exec"

func configureMCPCommandProcess(cmd *exec.Cmd) {
}

// killProcessGroup kills the process on non-Unix platforms.
// Windows doesn't support process groups the same way; cmd.Process.Kill suffices.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
