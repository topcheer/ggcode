//go:build !windows

package tui

import (
	"os/exec"
	"syscall"
)

// statuslineProcAttr puts the script shell in its own process group so the
// whole tree (shell + whatever the script forks) can be killed on timeout.
func statuslineProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// statuslineCancelKill kills the process group led by cmd, not just the
// direct child: `sh -c script` may fork the real script as a grandchild that
// inherits the stdout pipe, and killing only the shell leaves the grandchild
// alive holding the pipe open (CI: an 80ms timeout waited out a `sleep 5`).
func statuslineCancelKill(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd.Process.Kill()
}
