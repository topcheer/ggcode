//go:build !windows

package tool

import "syscall"

// processAlive reports whether pid currently exists (signal 0 probe).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
