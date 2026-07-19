//go:build !windows

package session

import (
	"os"
	"strconv"
	"syscall"

	"github.com/topcheer/ggcode/internal/debug"
)

// TryAcquireSessionLock attempts to acquire an exclusive flock on the
// session's lock file. Returns a *SessionLock where Acquired()==true
// on success, or Acquired()==false if another process already holds it.
func TryAcquireSessionLock(storeDir, sessionID string) (*SessionLock, error) {
	lockPath := LockFilePath(storeDir, sessionID)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	// Non-blocking exclusive lock.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Another process holds the lock.
		pid := readLockPIDFromFile(f)
		f.Close()
		return &SessionLock{
			storeDir:  storeDir,
			sessionID: sessionID,
			acquired:  false,
			holderPID: pid,
		}, nil
	}

	// Write our PID.
	f.Truncate(0)
	f.Seek(0, 0)
	f.WriteString(strconv.FormatInt(int64(os.Getpid()), 10))
	f.Sync()
	// NOTE: f is intentionally NOT closed — keeping it open holds the lock.

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

// Release releases the session lock, closes the underlying file descriptor,
// and removes the lock file.
func (l *SessionLock) Release() {
	if l == nil || !l.acquired || l.file == nil {
		return // not our lock to release
	}
	// Remove the file WHILE holding the lock, then unlock and close.
	// This prevents a race where another process creates+locks a new file
	// between our unlock and remove (same pattern as IsSessionLocked).
	lockPath := LockFilePath(l.storeDir, l.sessionID)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		debug.Log("session-lock", "failed to remove lock file %s: %v", lockPath, err)
	}
	syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	l.file.Close()
	l.file = nil
}

// IsSessionLocked checks if a session is locked by another process.
// If a stale lock file exists (no active flock), it is silently removed.
func IsSessionLocked(storeDir, sessionID string) bool {
	lockPath := LockFilePath(storeDir, sessionID)
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		return false // no lock file = not locked
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		return true // locked by another process
	}

	// We acquired the flock — the file is stale (no process holds it).
	// Remove the file WHILE holding the lock, then unlock and close.
	// This prevents a race where another process creates+locks a new file
	// between our unlock and remove.
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
