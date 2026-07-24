//go:build unix

package harness

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the harness subprocess in its own process group so
// that cancellation can kill the entire tree (including children spawned by
// the agent like `make`, `go test`, etc.).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalProcessGroup sends SIGKILL to the process group led by pid.
// With Setpgid, the child's PID equals its PGID, so we kill -PGID.
func signalProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
