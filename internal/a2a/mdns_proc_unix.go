//go:build linux

package a2a

import (
	"os/exec"
	"syscall"
)

// setProcessGroup configures the child to die when the parent exits.
// On Linux we use Pdeathsig so SIGKILL of ggcode also kills avahi-publish.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}
}

func killProcessGroup(pid int) {
	syscall.Kill(pid, syscall.SIGTERM)
}
