package util

import (
	"os"
)

// IsProcessAlive checks if a process with the given PID is still running.
// On Windows, os.FindProcess always succeeds, so we use a non-blocking
// wait attempt via the process handle to determine liveness.
// If we can't determine, we return false (assume dead).
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows, try to get the process exit status.
	// If the process is still running, this will return ERROR_INVALID_PARAMETER.
	return isProcessRunningWindows(proc)
}

// IsProcessAliveProc checks if the given os.Process is still running.
func IsProcessAliveProc(proc *os.Process) bool {
	if proc == nil {
		return false
	}
	return IsProcessAlive(proc.Pid)
}
