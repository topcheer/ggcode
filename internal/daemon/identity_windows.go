//go:build windows

package daemon

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// processCmdline returns a best-effort executable image name for pid on
// Windows (#431). A full command line needs the WMI/NTQSI toolchain, but the
// process IMAGE NAME (e.g. "ggcode.exe") is available via a toolhelp32
// snapshot and is enough to distinguish a reused PID running an unrelated
// binary from our daemon. Returns "" when inspection fails — callers treat
// that as "identity unknown" and keep the signal-0 verdict (#412).
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
