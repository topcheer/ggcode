package knight

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/topcheer/ggcode/internal/debug"
)

// instanceLock provides cross-process mutual exclusion for Knight instances
// running in the same project directory. Only one ggcode process can hold
// the lock at a time; others gracefully skip Knight startup.
type instanceLock struct {
	file *os.File
	path string
}

// tryAcquireLock attempts to acquire an exclusive flock on the Knight lock file
// in the project's .ggcode/ directory. Returns the lock on success, or nil if
// another process already holds it (or on error).
//
// The lock is automatically released when the process exits (kernel releases
// all flocks on file descriptor close), so it's crash-safe.
func tryAcquireLock(projDir string) *instanceLock {
	lockDir := filepath.Join(projDir, ".ggcode")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		debug.Log("knight", "lock: cannot create dir %s: %v", lockDir, err)
		return nil
	}

	lockPath := filepath.Join(lockDir, "knight.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		debug.Log("knight", "lock: cannot open %s: %v", lockPath, err)
		return nil
	}

	// Non-blocking exclusive lock. If another process holds it, we get EWOULDBLOCK.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Another instance holds the lock — read its PID for the message
		pid := readLockPID(f)
		f.Close()
		debug.Log("knight", "lock: another instance already running (pid=%d)", pid)
		return nil
	}

	// Write our PID so other instances can report who holds the lock.
	f.Truncate(0)
	f.Seek(0, 0)
	f.WriteString(strconv.Itoa(os.Getpid()))
	f.Sync()

	debug.Log("knight", "lock: acquired (path=%s)", lockPath)
	return &instanceLock{file: f, path: lockPath}
}

// release unlocks and closes the lock file.
func (l *instanceLock) release() {
	if l.file != nil {
		syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		l.file.Close()
		debug.Log("knight", "lock: released (path=%s)", l.path)
	}
}

// readLockPID reads the PID from a lock file for informational purposes.
func readLockPID(f *os.File) int {
	data, err := os.ReadFile(f.Name())
	if err != nil || len(data) == 0 {
		return 0
	}
	pid, _ := strconv.Atoi(string(data[:min(len(data), 16)]))
	return pid
}

// LockHeldBy returns the PID of the process holding the Knight lock for the
// given project directory, or 0 if the lock is not held.
func LockHeldBy(projDir string) (int, error) {
	lockPath := filepath.Join(projDir, ".ggcode", "knight.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return 0, nil // no lock file
	}
	pid, _ := strconv.Atoi(string(data[:min(len(data), 16)]))
	if pid <= 0 {
		return 0, nil
	}
	// Verify the lock is actually held by checking with a non-blocking attempt
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0600)
	if err != nil {
		return pid, nil // can't open = stale
	}
	defer f.Close()

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Lock IS held — return the PID
		return pid, nil
	}
	// Lock is NOT held (stale file) — release and clean up
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return 0, nil
}

// FormatLockMessage returns a human-readable message about why Knight didn't start.
func FormatLockMessage(pid int) string {
	if pid > 0 {
		return fmt.Sprintf("knight: skipped — another instance (PID %d) already running in this workspace", pid)
	}
	return "knight: skipped — another instance already running in this workspace"
}
