//go:build windows

package daemon

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func processCmdline(pid int) string {
	if pid <= 0 {
		return ""
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return ""
	}
	for {
		if int(pe.ProcessID) == pid {
			return windows.UTF16ToString(pe.ExeFile[:])
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			return ""
		}
	}
}
