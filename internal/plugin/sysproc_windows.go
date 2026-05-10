//go:build windows

package plugin

import (
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

// On Windows there is no process-group kill with negative PID.
// Fall back to killing the direct child process.
func cancelProcessGroup(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
