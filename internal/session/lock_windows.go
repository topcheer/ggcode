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

const (
	lockPIDOffset = 32
	lockPIDMaxLen = 16
)

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

func (l *SessionLock) Acquired() bool {
	return l != nil && l.acquired
}

func (l *SessionLock) HolderPID() int {
	if l == nil {
		return 0
	}
	return l.holderPID
}

func (l *SessionLock) Release() {
	if l == nil || !l.acquired || l.file == nil {
		return
	}
	unlockFileEx(syscall.Handle(l.file.Fd()), 1, 0)
	l.file.Close()
	l.file = nil

	lockPath := LockFilePath(l.storeDir, l.sessionID)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		debug.Log("session-lock", "failed to remove lock file %s: %v", lockPath, err)
	}
}

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

	unlockFileEx(syscall.Handle(f.Fd()), 1, 0)
	f.Close()
	if err := os.Remove(lockPath); err != nil {
		debug.Log("session", "stale-lock cleanup: remove %s failed: %v", lockPath, err)
	}
	return false
}

func readLockPIDFromFile(f *os.File) int {

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
