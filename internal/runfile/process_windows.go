//go:build windows

package runfile

import (
	"fmt"
	"syscall"
)

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

		if err == syscall.ERROR_ACCESS_DENIED {
			return true
		}
		return false
	}
	defer syscall.CloseHandle(handle)

	if event, waitErr := syscall.WaitForSingleObject(handle, 0); waitErr == nil {

		return event == syscall.WAIT_TIMEOUT
	}

	var exitCode uint32
	if err := syscall.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == 259
}

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
