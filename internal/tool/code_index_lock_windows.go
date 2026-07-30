// Code index cross-process locking — Windows implementation using LockFileEx.
//
//go:build windows

package tool

import (
	"os"
	"syscall"
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

// lockFileExcl acquires a non-blocking exclusive lock on the file.
// Returns true on success, false if another process holds the lock.
func lockFileExcl(f *os.File) bool {
	r1, _, _ := procLockFileEx.Call(
		f.Fd(),
		lockfileExclusiveLock|lockfileFailImmediate,
		0, // reserved
		1, // numBytesLow
		0, // numBytesHigh
		0, // OVERLAPPED* — nil
	)
	return r1 != 0
}

// unlockFileEx releases the exclusive lock.
func unlockFileExcl(f *os.File) {
	_, _, _ = procUnlockFileEx.Call(
		f.Fd(),
		0, // reserved
		1, // numBytesLow
		0, // numBytesHigh
		0, // OVERLAPPED* — nil
	)
}
