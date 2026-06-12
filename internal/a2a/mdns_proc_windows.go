//go:build windows

package a2a

import (
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {
	// Windows doesn't support process groups the same way.
	// cmd.Process.Kill() will be used instead.
}

func killProcessGroup(pid int) {
	// Windows: handled via cmd.Process.Kill() in stop()
}
