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
		// #1723 case 1: distinguish the errors. ACCESS_DENIED (5) means the
		// process exists but belongs to another user/elevation - treating it
		// as dead made daemon slot checks delete the PID file and fork a
		// second daemon (#1535). ERROR_INVALID_PARAMETER (87) is what Windows
		// returns for a NONEXISTENT pid - judging it alive wedged the slot
		// forever after a daemon panic (CheckExistingDaemon -> alive ->
		// empty cmdline -> same helper again). Everything else: when in
		// doubt, alive.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false
		}
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
