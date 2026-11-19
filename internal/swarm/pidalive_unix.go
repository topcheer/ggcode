//go:build !windows

package swarm

import "syscall"

// pidAlive probes a same-host process with signal 0. EPERM means the process
// exists but is owned by another user — still alive.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
