//go:build windows

package daemon

import (
	"fmt"
	"os"
	"syscall"

	"github.com/topcheer/ggcode/internal/util"
	"golang.org/x/sys/windows"
)

// LockFileEx flags (from winbase.h).
const (
	lockFileExclusive       = 0x00000002 // LOCKFILE_EXCLUSIVE_LOCK
	lockFileFailImmediately = 0x00000001 // LOCKFILE_FAIL_IMMEDIATELY
)

// flockNonBlocking acquires a non-blocking exclusive lock on the first byte
// of f via LockFileEx, the Windows analogue of flock(LOCK_EX|LOCK_NB).
// The lock is released automatically when the handle closes, matching the
// flock lifetime semantics used by the daemon PID slot (#574 Bug D).
// Returns ERROR_LOCK_VIOLATION when another process holds the lock.
func flockNonBlocking(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		lockFileExclusive|lockFileFailImmediately,
		0, // reserved
		1, // lock 1 byte at offset 0
		0,
		&ol,
	)
}

func newBackgroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

func checkProcessAlive(proc *os.Process) error {
	if proc == nil || proc.Pid <= 0 {
		return fmt.Errorf("invalid process")
	}
	// os.FindProcess on Windows always succeeds even for dead PIDs,
	// so we must actively check liveness via OpenProcess.
	if !util.IsProcessAlive(proc.Pid) {
		return fmt.Errorf("process %d is not running", proc.Pid)
	}
	return nil
}
