//go:build windows

package runfile

import (
	"os"
	"syscall"
)

// processExists checks if a process with the given PID is running.
// On Windows, syscall.Kill is not available; we use OpenProcess instead.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	handle, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var exitCode uint32
	err = syscall.GetExitCodeProcess(handle, &exitCode)
	if err != nil {
		return false
	}
	return exitCode == 259 // STILL_ACTIVE
}

// PID is a convenience helper for the current process.
var _ = os.Getpid
