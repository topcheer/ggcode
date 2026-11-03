//go:build !windows

package session

import (
	"fmt"
	"os"
	"strconv"
	"syscall"

	"github.com/topcheer/ggcode/internal/debug"
)

func TryAcquireSessionLock(storeDir, sessionID string) (*SessionLock, error) {
	lockPath := LockFilePath(storeDir, sessionID)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {

		pid := readLockPIDFromFile(f)
		f.Close()
		return &SessionLock{
			storeDir:  storeDir,
			sessionID: sessionID,
			acquired:  false,
			holderPID: pid,
		}, nil
	}

	for attempt := 0; attempt < 3; attempt++ {
		var fi os.FileInfo
		if fi, err = f.Stat(); err != nil {
			f.Close()
			return nil, err
		}
		pathFi, pathErr := os.Stat(lockPath)
		sameOS := os.SameFile(fi, pathFi)
		if pathErr == nil && sameOS {
			break
		}

		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			pid := readLockPIDFromFile(f)
			f.Close()
			return &SessionLock{
				storeDir:  storeDir,
				sessionID: sessionID,
				acquired:  false,
				holderPID: pid,
			}, nil
		}
	}

	fi, serr := f.Stat()
	if serr != nil {
		f.Close()
		return nil, fmt.Errorf("session lock %s: final stat failed: %w", lockPath, serr)
	}
	if pathFi, perr := os.Stat(lockPath); perr != nil || !os.SameFile(fi, pathFi) {
		f.Close()
		return nil, fmt.Errorf("session lock %s: unlink race persisted after retries", lockPath)
	}

	f.Truncate(0)
	f.Seek(0, 0)
	f.WriteString(strconv.FormatInt(int64(os.Getpid()), 10))
	f.Sync()

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

	lockPath := LockFilePath(l.storeDir, l.sessionID)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		debug.Log("session-lock", "failed to remove lock file %s: %v", lockPath, err)
	}
	syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	l.file.Close()
	l.file = nil
}

func IsSessionLocked(storeDir, sessionID string) bool {
	lockPath := LockFilePath(storeDir, sessionID)
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		return false
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		return true
	}

	_ = os.Remove(lockPath)
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
	return false
}

func readLockPIDFromFile(f *os.File) int {
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return 0
	}
	return parsePID(data)
}
