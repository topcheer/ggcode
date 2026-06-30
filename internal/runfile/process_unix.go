//go:build unix

package runfile

import "syscall"

// processExists checks if a process with the given PID is running.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}
