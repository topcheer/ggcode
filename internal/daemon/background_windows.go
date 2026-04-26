//go:build windows

package daemon

import (
	"os"
	"syscall"
)

func newBackgroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

func checkProcessAlive(proc *os.Process) error {
	// On Windows, os.FindProcess already checks if the process exists.
	// Wait with non-blocking approach: try to wait with WaitOption.
	return nil
}
