//go:build windows

package agent

import "syscall"

// processAlive reports whether the PID belongs to a live process (#3642,
// #3678). Windows has no kill(pid,0) probe: open the process handle with
// PROCESS_QUERY_LIMITED_INFORMATION instead. ERROR_ACCESS_DENIED means the
// process exists but is owned by another user - still alive (same
// semantics as the Unix EPERM branch).
func processAlive(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return err == syscall.ERROR_ACCESS_DENIED
	}
	syscall.CloseHandle(h)
	return true
}
