//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// configureVerifyCommand sets up process-group isolation so that on
// timeout we can kill the entire group, preventing orphaned children
// from keeping stdout/stderr pipes open.
func configureVerifyCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			return syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
		}
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
