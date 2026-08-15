//go:build windows

package cron

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	cronKernel32     = syscall.NewLazyDLL("kernel32.dll")
	cronLockFileEx   = cronKernel32.NewProc("LockFileEx")
	cronUnlockFileEx = cronKernel32.NewProc("UnlockFileEx")
)

const (
	cronLockfileExclusiveLock   = 0x00000002
	cronLockfileFailImmediately = 0x00000001
)

// acquireMigrationLock takes a non-blocking exclusive LockFileEx on path.
// Returns a release func and true on success; (nil, false) when another
// process holds the lock (#414).
func acquireMigrationLock(path string) (func(), bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	var ol syscall.Overlapped
	r1, _, e1 := syscall.SyscallN(
		cronLockFileEx.Addr(),
		uintptr(syscall.Handle(f.Fd())),
		cronLockfileExclusiveLock|cronLockfileFailImmediately,
		0, 1, 0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		f.Close()
		_ = e1
		return nil, false
	}
	return func() {
		var ol2 syscall.Overlapped
		syscall.SyscallN(
			cronUnlockFileEx.Addr(),
			uintptr(syscall.Handle(f.Fd())),
			0, 1, 0,
			uintptr(unsafe.Pointer(&ol2)),
		)
		f.Close()
	}, true
}
