//go:build !windows

package util

import (
	"os"
	"syscall"
)

// IsProcessAlive checks if a process with the given PID is still running.
// On Unix, it sends signal 0 (no signal, just permission/existence check).
func IsProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// IsProcessAliveProc checks if the given os.Process is still running.
func IsProcessAliveProc(proc *os.Process) bool {
	if proc == nil || proc.Pid <= 0 {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
