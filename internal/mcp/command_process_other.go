//go:build !darwin && !linux

package mcp

import "os/exec"

func configureMCPCommandProcess(cmd *exec.Cmd) {
}
