//go:build windows

package util

import (
	"os"

	"golang.org/x/sys/windows"
)

// isProcessRunningWindows uses OpenProcess to check if a PID is alive.
// On Windows, os.FindProcess always succeeds and Signal is not reliable,
// so we call OpenProcess with SYNCHRONIZE access and check for errors.
func isProcessRunningWindows(proc *os.Process) bool {
	if proc == nil || proc.Pid <= 0 {
		return false
	}

	// SYNCHRONIZE (0x100000) is sufficient to open a handle to any process.
	// If the process has exited, OpenProcess returns ERROR_INVALID_PARAMETER.
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(proc.Pid))
	if err != nil {
		return false
	}
	windows.CloseHandle(handle)
	return true
}
