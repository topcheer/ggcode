//go:build !darwin && !linux

package tool

import (
	"os/exec"
)

func sandboxWrap(cmd *exec.Cmd, workspace string, p *SandboxPolicy) (bool, error) {
	return false, errSandboxUnavailable
}
