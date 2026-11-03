//go:build windows

package util

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	procLockFile   = kernel32.NewProc("LockFileEx")
	procUnlockFile = kernel32.NewProc("UnlockFileEx")
)

func FileLock(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	const (
		lockfileExclusiveLock   = 0x00000002
		lockfileFailImmediately = 0x00000001
	)
	if err := acquireWithRetry(func() error {
		return lockFileEx(syscall.Handle(f.Fd()), lockfileExclusiveLock|lockfileFailImmediately)
	}); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		unlockFileEx(syscall.Handle(f.Fd()))
		_ = f.Close()
	}, nil
}

func lockFileEx(handle syscall.Handle, flags uint32) error {
	var ol syscall.Overlapped
	r1, _, e1 := syscall.SyscallN(
		procLockFile.Addr(),
		uintptr(handle),
		uintptr(flags),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		return e1
	}
	return nil
}

func unlockFileEx(handle syscall.Handle) {
	var ol syscall.Overlapped
	ol.Offset = 0
	syscall.SyscallN(
		procUnlockFile.Addr(),
		uintptr(handle),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&ol)),
	)
}
