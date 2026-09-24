//go:build windows

package tui

import (
	"os/exec"
	"syscall"
)

// statuslineProcAttr is a no-op on Windows: `cmd /c` runs the script in the
// same process tree as the shell and job objects are not needed here.
func statuslineProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

// statuslineCancelKill uses the default single-process kill on Windows; the
// WaitDelay backstop in runStatuslineCommand releases pipes held by any
// survivor regardless of platform.
func statuslineCancelKill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
