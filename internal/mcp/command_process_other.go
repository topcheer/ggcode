//go:build !darwin && !linux && !windows

package mcp

import "os/exec"

func configureMCPCommandProcess(cmd *exec.Cmd) {
}
