//go:build windows

package agent

import (
	"syscall"
	"unsafe"
)

// diskUsageOS returns (free bytes, total bytes, ok) for the filesystem
// containing path, using GetDiskFreeSpaceEx.
func diskUsageOS(path string) (free, total uint64, ok bool) {
	var freeBytes, totalBytes, totalFreeBytes uint64
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	ret, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW").Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&freeBytes)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if ret == 0 {
		return 0, 0, false
	}
	return freeBytes, totalBytes, true
}
