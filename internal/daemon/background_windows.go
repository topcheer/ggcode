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

// flockNonBlocking acquires a non-blocking exclusive lock via LockFileEx,
// the Windows analogue of flock(LOCK_EX|LOCK_NB). The lock is released
// automatically when the handle closes, matching the flock lifetime
// semantics used by the daemon PID slot (#574 Bug D).
//
// Lock placement: unlike Unix flock (a whole-file advisory lock that never
// blocks read/write), a byte-range lock placed over the file content would
// make every independent reader (os.ReadFile in ReadPIDFile —
// CheckExistingDaemon, CleanupDaemon) fail with "locked a portion of the
// file", deadlocking daemon startup on Windows. We therefore lock a
// sentinel byte far past EOF instead: content I/O is never covered, so
// reads and writes behave exactly like Unix, while mutual exclusion between
// daemons is preserved (all daemons lock the same well-known offset).
// Returns ERROR_LOCK_VIOLATION when another process holds the lock.
func flockNonBlocking(f *os.File) error {
	const sentinelOffset = 1 << 40 // far beyond any real PID-file size
	var ol windows.Overlapped
	ol.OffsetHigh = uint32(sentinelOffset >> 32)
	ol.Offset = uint32(sentinelOffset & 0xFFFFFFFF)
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		lockFileExclusive|lockFileFailImmediately,
		0, // reserved
		1, // lock 1 sentinel byte
		0,
		&ol,
	)
}

// openPIDFile opens the PID file with full share access including
// FILE_SHARE_DELETE. Rationale: Unix flock lives on the inode, so a locked
// PID file can still be unlinked and the name recreated — every cleanup
// path (#552-C fork failure, #552-E ownership, CheckExistingDaemon stale
// removal) relies on that. Windows refuses DeleteFile on a handle without
// FILE_SHARE_DELETE, which broke all of those paths while the lock was
// held. Sharing delete restores the Unix semantics: os.Remove succeeds
// under the lock and the name is immediately reusable (verified on
// windows/amd64: remove + same-name recreate both succeed with
// LockFileEx held).
func openPIDFile(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS, // create if missing; truncation happens under the lock
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
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
