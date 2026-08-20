//go:build windows

package im

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	imKernel32         = syscall.NewLazyDLL("kernel32.dll")
	imProcLockFileEx   = imKernel32.NewProc("LockFileEx")
	imProcUnlockFileEx = imKernel32.NewProc("UnlockFileEx")
)

// lockBindingsFile acquires a blocking exclusive lock on the bindings lock file.
// This serializes read-modify-write cycles across multiple ggcode processes
// that share the same im-bindings.json file. The returned cleanup function
// releases the lock and closes the file handle.
func lockBindingsFile(bindingsPath string) (func(), error) {
	lockPath := bindingsPath + ".flock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	// Blocking exclusive lock (no LOCKFILE_FAIL_IMMEDIATELY).
	const lockfileExclusiveLock = 0x00000002
	if err := imLockFileEx(syscall.Handle(f.Fd()), lockfileExclusiveLock, 0, 1, 0); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		imUnlockFileEx(syscall.Handle(f.Fd()), 1, 0)
		f.Close()
	}, nil
}

func imLockFileEx(handle syscall.Handle, flags, reserved uint32, length uint32, offset uint32) error {
	var ol syscall.Overlapped
	ol.Offset = offset

	r1, _, e1 := syscall.SyscallN(
		imProcLockFileEx.Addr(),
		uintptr(handle),
		uintptr(flags),
		uintptr(reserved),
		uintptr(length),
		uintptr(0),
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		return e1
	}
	return nil
}

func imUnlockFileEx(handle syscall.Handle, length uint32, offset uint32) error {
	var ol syscall.Overlapped
	ol.Offset = offset

	// Win32: BOOL UnlockFileEx(HANDLE hFile, DWORD dwReserved, DWORD
	// nNumberOfBytesToUnlockLow, DWORD nNumberOfBytesToUnlockHigh,
	// LPOVERLAPPED lpOverlapped). #755: a stray leading uintptr(0) used to
	// put NULL in hFile (ERROR_INVALID_HANDLE) and the handle in dwReserved
	// -- the explicit unlock never worked; release relied solely on the
	// implicit Close() semantics.
	r1, _, e1 := syscall.SyscallN(
		imProcUnlockFileEx.Addr(),
		uintptr(handle),
		uintptr(0), // dwReserved, must be zero
		uintptr(length),
		uintptr(0), // nNumberOfBytesToUnlockHigh
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		return e1
	}
	return nil
}
