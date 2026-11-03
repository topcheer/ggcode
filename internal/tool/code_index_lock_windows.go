//go:build windows

package tool

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock = 0x00000002
	lockfileFailImmediate = 0x00000001
)

type overlapped struct {
	Internal     uintptr
	InternalHigh uintptr
	DOffset      uint32
	DOffsetHigh  uint32
	hEvent       uintptr
}

func lockFileExcl(f *os.File) bool {
	ol := &overlapped{}
	r1, _, _ := procLockFileEx.Call(
		f.Fd(),
		lockfileExclusiveLock|lockfileFailImmediate,
		0,
		1,
		0,
		uintptr(unsafe.Pointer(ol)),
	)
	return r1 != 0
}

func unlockFileExcl(f *os.File) {
	ol := &overlapped{}
	_, _, _ = procUnlockFileEx.Call(
		f.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(ol)),
	)
}
