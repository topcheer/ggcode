package util

import (
	"os"
)

// IsProcessAlive checks if a process with the given PID is still running.
// On Windows, os.FindProcess always succeeds, so liveness is decided by
// isProcessRunningWindows (process_windows_helper.go): SYNCHRONIZE handle
// + non-blocking wait, with ACCESS_DENIED (exists, other user) counted
// alive and INVALID_PARAMETER (pid does not exist) counted dead (#1723).
// When truly indeterminate, the helper reports alive (when in doubt,
// alive - see #1535).
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Liveness is delegated to the handle-based helper
	// (non-blocking wait on a SYNCHRONIZE handle).
	return isProcessRunningWindows(proc)
}

// IsProcessAliveProc checks if the given os.Process is still running.
func IsProcessAliveProc(proc *os.Process) bool {
	if proc == nil {
		return false
	}
	return IsProcessAlive(proc.Pid)
}
