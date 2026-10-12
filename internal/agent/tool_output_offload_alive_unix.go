//go:build !windows

package agent

import "syscall"

// processAlive reports whether the PID belongs to a live process (#3642,
// #3678). Signal 0 performs no delivery; EPERM means the process exists
// but is owned by another user - still alive.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
