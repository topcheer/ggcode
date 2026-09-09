//go:build windows

package util

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// isProcessRunningWindows checks whether the process is alive. On Windows
// os.FindProcess always succeeds, so we open a handle with SYNCHRONIZE
// access and do a non-blocking wait: a process object stays valid (and
// OpenProcess keeps succeeding) even after it exits, for as long as ANY
// handle to it remains open — so OpenProcess success alone is NOT liveness
// (#552-A: a killed+reaped child with a lingering parent handle was
// reported alive forever, wedging EnsureDaemonSlot). The signaled state of
// the handle is the actual liveness verdict.
func isProcessRunningWindows(proc *os.Process) bool {
	if proc == nil || proc.Pid <= 0 {
		return false
	}

	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(proc.Pid))
	if err != nil {
		// #1535: ACCESS_DENIED means the process exists but belongs to
		// another user/elevation - treating it as dead made daemon slot
		// checks delete the PID file and fork a second daemon.
		// #1723: only ACCESS_DENIED implies existence. A NONEXISTENT pid
		// returns ERROR_INVALID_PARAMETER (87) on Windows - returning true
		// for it wedged EnsureDaemonSlot forever on "daemon already
		// running" after a daemon panic/reap (the exact mirror of #552-A).
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return true
		}
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false
		}
		// Any other error: conservatively alive, per #1535's principle.
		return true
	}
	defer windows.CloseHandle(handle)

	// Non-blocking wait: WAIT_OBJECT_0 (signaled) = exited; WAIT_TIMEOUT = running.
	event, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		// Cannot determine: conservatively report alive so callers do not
		// treat a live process as dead.
		return true
	}
	return event == uint32(windows.WAIT_TIMEOUT)
}
