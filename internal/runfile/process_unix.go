//go:build unix

package runfile

import "syscall"

// processExists checks if a process with the given PID is running.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	// #799: EPERM means the process EXISTS but belongs to another user
	// (shared-HOME/sudo layouts); treating it as dead made callers delete
	// live instances' port files cross-user.
	return err == nil || err == syscall.EPERM
}
