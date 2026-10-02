//go:build windows

package runfile

import (
	"fmt"
	"syscall"
)

// processExists checks if a process with the given PID is running.
// On Windows, syscall.Kill is not available; we use OpenProcess instead.
//
// #3079: the legacy GetExitCodeProcess==259 check was ambiguous - 259 is
// STILL_ACTIVE, but a process whose REAL exit code happened to be exactly
// 259 stayed "alive" forever. The wait state of the handle is unambiguous:
// WaitForSingleObject(handle, 0) returns WAIT_TIMEOUT while the process
// lives and WAIT_OBJECT_0 once it terminated, whatever its exit code was.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	const (
		PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
		SYNCHRONIZE                       = 0x00100000
	)
	handle, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION|SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// #1290: mirror the Unix #799 EPERM semantics. ERROR_ACCESS_DENIED
		// (cross-user, protected/elevated process under UAC integrity-level
		// gaps) means the process EXISTS but we may not query it - returning
		// false here made ReadAll delete LIVE instances' port files
		// (runas/schtask/shared-HOME layouts).
		if err == syscall.ERROR_ACCESS_DENIED {
			return true
		}
		return false
	}
	defer syscall.CloseHandle(handle)

	// Preferred: wait-state check, immune to the STILL_ACTIVE=259 exit-code
	// ambiguity (#3079).
	if event, waitErr := syscall.WaitForSingleObject(handle, 0); waitErr == nil {
		// WAIT_OBJECT_0 (signaled) = terminated; WAIT_TIMEOUT = still alive.
		return event == syscall.WAIT_TIMEOUT
	}
	// Degraded path (wait right unavailable, e.g. some protected processes
	// grant query but not synchronize): fall back to the legacy heuristic
	// rather than misreporting a live process as dead.
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == 259 // STILL_ACTIVE
}

// procStartTime returns the process-creation identity token (#1624 case C).
// Windows has no /proc; the token is the raw GetProcessTimes CreationTime
// FILETIME (opaque but unique per process start), which is all the
// PID-recycle check needs. A non-empty token here (re)activates the recycle
// fallback on Windows, which previously returned "" and degraded to plain
// liveness - a recycled PID kept its ghost port file until the number was
// reused by yet another process (#3079).
func procStartTime(pid int) string {
	if pid <= 0 {
		return ""
	}
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	handle, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(handle)
	var create, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &create, &exit, &kernel, &user); err != nil {
		return ""
	}
	return fmt.Sprintf("%d", uint64(create.HighDateTime)<<32|uint64(create.LowDateTime))
}
