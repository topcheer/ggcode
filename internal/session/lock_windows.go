//go:build windows

package session

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/topcheer/ggcode/internal/debug"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

// #1305: byte offset where the holder's PID is stored. LockFileEx locks
// byte 0 only; on Windows byte-range locks are MANDATORY, so reading
// offset 0 from another handle fails with ERROR_LOCK_VIOLATION and the
// holder PID came back 0 every time. Storing the PID outside the locked
// byte keeps diagnostics readable through a separate handle (same fix as
// internal/knight/lock_windows.go).
const (
	lockPIDOffset = 32
	lockPIDMaxLen = 16
)

// TryAcquireSessionLock attempts to acquire an exclusive lock on the
// session's lock file. Returns a *SessionLock where Acquired()==true
// on success, or Acquired()==false if another process already holds it.
func TryAcquireSessionLock(storeDir, sessionID string) (*SessionLock, error) {
	lockPath := LockFilePath(storeDir, sessionID)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	const length = 1
	err = lockFileEx(syscall.Handle(f.Fd()), lockfileExclusiveLock|lockfileFailImmediately, 0, length, 0)
	if err != nil {
		pid := readLockPIDFromFile(f)
		f.Close()
		return &SessionLock{
			storeDir:  storeDir,
			sessionID: sessionID,
			acquired:  false,
			holderPID: pid,
		}, nil
	}

	// #1305: write the PID OUTSIDE the locked byte (offset 32) so a second
	// process can read it through its own handle without hitting the
	// mandatory byte-range lock. Byte 0 stays locked as the lock itself.
	var buf [lockPIDMaxLen]byte
	n := copy(buf[:], []byte(strconv.FormatInt(int64(os.Getpid()), 10)))
	_, _ = f.WriteAt(buf[:n], lockPIDOffset)
	_ = f.Sync()

	return &SessionLock{
		storeDir:  storeDir,
		sessionID: sessionID,
		acquired:  true,
		file:      f,
	}, nil
}

// Acquired reports whether this lock was successfully acquired (true)
// or whether another process holds it (false).
func (l *SessionLock) Acquired() bool {
	return l != nil && l.acquired
}

// HolderPID returns the PID of the process holding the lock, or 0 if
// we hold it or if the PID could not be determined.
func (l *SessionLock) HolderPID() int {
	if l == nil {
		return 0
	}
	return l.holderPID
}

// Release releases the session lock, closes the underlying file handle,
// and removes the lock file.
func (l *SessionLock) Release() {
	if l == nil || !l.acquired || l.file == nil {
		return
	}
	unlockFileEx(syscall.Handle(l.file.Fd()), 1, 0)
	l.file.Close()
	l.file = nil

	// Best-effort: remove the lock file so it doesn't linger.
	lockPath := LockFilePath(l.storeDir, l.sessionID)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		debug.Log("session-lock", "failed to remove lock file %s: %v", lockPath, err)
	}
}

// IsSessionLocked checks if a session is locked by another process.
// If a stale lock file exists (no active lock), it is silently removed.
func IsSessionLocked(storeDir, sessionID string) bool {
	lockPath := LockFilePath(storeDir, sessionID)
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		return false
	}

	err = lockFileEx(syscall.Handle(f.Fd()), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0)
	if err != nil {
		f.Close()
		return true
	}

	// We acquired the lock — the file is stale (no process holds it).
	// #481: on Windows, os.Remove under an open handle ALWAYS fails with
	// ERROR_SHARING_VIOLATION — Go's syscall.Open hardcodes sharemode
	// FILE_SHARE_READ|WRITE (no DELETE), and os.Remove uses plain
	// DeleteFileW. The unix-ported order (remove-while-holding) is
	// impossible here. Match Release()'s proven order instead: unlock,
	// close, THEN remove — and log failures instead of swallowing them.
	unlockFileEx(syscall.Handle(f.Fd()), 1, 0)
	f.Close()
	if err := os.Remove(lockPath); err != nil {
		debug.Log("session", "stale-lock cleanup: remove %s failed: %v", lockPath, err)
	}
	return false
}

func readLockPIDFromFile(f *os.File) int {
	// #1305: ReadAt at the PID offset - a plain ReadFile from offset 0
	// always touched the locked byte 0 and failed with
	// ERROR_LOCK_VIOLATION, so the holder PID was permanently 0.
	var buf [lockPIDMaxLen]byte
	n, err := f.ReadAt(buf[:], lockPIDOffset)
	if err != nil && n == 0 {
		return 0
	}
	return parsePID(buf[:n])
}

func lockFileEx(handle syscall.Handle, flags, reserved uint32, length uint32, offset uint32) error {
	var ol syscall.Overlapped
	ol.Offset = offset

	r1, _, e1 := syscall.SyscallN(
		procLockFileEx.Addr(),
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

func unlockFileEx(handle syscall.Handle, length uint32, offset uint32) error {
	var ol syscall.Overlapped
	ol.Offset = offset

	r1, _, e1 := syscall.SyscallN(
		procUnlockFileEx.Addr(),
		uintptr(handle),
		uintptr(0),
		uintptr(length),
		uintptr(0),
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		return e1
	}
	return nil
}
