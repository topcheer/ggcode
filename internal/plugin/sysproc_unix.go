//go:build unix

package plugin

import (
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// cancelProcessGroup kills the entire process group (negative PID) so that
// orphaned grandchildren (e.g., "sh -c sleep 30" → sh + sleep) are cleaned
// up and CombinedOutput can return instead of blocking forever.
func cancelProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
