package util

import (
	"os"
)

func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	return isProcessRunningWindows(proc)
}

func IsProcessAliveProc(proc *os.Process) bool {
	if proc == nil {
		return false
	}
	return IsProcessAlive(proc.Pid)
}
