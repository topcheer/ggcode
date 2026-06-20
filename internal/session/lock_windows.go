//go:build windows

package session

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
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
			holderPID: pid,
		}, nil
	}

	f.Truncate(0)
	f.Seek(0, 0)
	f.WriteString(strconv.FormatInt(int64(os.Getpid()), 10))
	f.Sync()

	return &SessionLock{
		storeDir:  storeDir,
		sessionID: sessionID,
		holderPID: 0,
		file:      f,
	}, nil
}

// Acquired reports whether this lock was successfully acquired (true)
// or whether another process holds it (false).
func (l *SessionLock) Acquired() bool {
	return l != nil && l.holderPID == 0
}

// HolderPID returns the PID of the process holding the lock, or 0 if
// we hold it or if the PID could not be determined.
func (l *SessionLock) HolderPID() int {
	if l == nil {
		return 0
	}
	return l.holderPID
}

// Release releases the session lock and closes the underlying file handle.
func (l *SessionLock) Release() {
	if l == nil || l.holderPID != 0 || l.file == nil {
		return
	}
	unlockFileEx(syscall.Handle(l.file.Fd()), 1, 0)
	l.file.Close()
	l.file = nil
}

// IsSessionLocked checks if a session is locked by another process.
func IsSessionLocked(storeDir, sessionID string) bool {
	lockPath := LockFilePath(storeDir, sessionID)
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()

	err = lockFileEx(syscall.Handle(f.Fd()), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0)
	if err != nil {
		return true
	}
	unlockFileEx(syscall.Handle(f.Fd()), 1, 0)
	return false
}

func readLockPIDFromFile(f *os.File) int {
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return 0
	}
	return parsePID(data)
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
