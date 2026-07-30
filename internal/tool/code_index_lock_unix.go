// Code index cross-process locking — Unix implementation using flock.
//
//go:build !windows

package tool

import (
	"os"
	"syscall"
)

// lockFileExcl acquires a non-blocking exclusive lock on the file.
// Returns true on success, false if another process holds the lock.
func lockFileExcl(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// unlockFileExcl releases the exclusive lock.
func unlockFileExcl(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
