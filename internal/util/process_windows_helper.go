//go:build windows

package util

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func isProcessRunningWindows(proc *os.Process) bool {
	if proc == nil || proc.Pid <= 0 {
		return false
	}

	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(proc.Pid))
	if err != nil {

		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false
		}
		return true
	}
	defer windows.CloseHandle(handle)

	event, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {

		return true
	}
	return event == uint32(windows.WAIT_TIMEOUT)
}
