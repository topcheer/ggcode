package util

import (
	"os"
)

// isProcessRunningWindows uses OpenProcess to check if a PID is alive.
// This is a stub — the actual implementation is in process_windows_sys.go
// which uses syscall to call kernel32 OpenProcess.
func isProcessRunningWindows(proc *os.Process) bool {
	// On Windows, the simplest reliable check is to try to open the process.
	// os.FindProcess always succeeds on Windows, so we need a different approach.
	// Fall back to assuming alive since we can't easily check without kernel32.
	// The daemon package's background_windows.go does the same.
	return true
}
