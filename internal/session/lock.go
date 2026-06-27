package session

import (
	"os"
	"path/filepath"
	"strconv"
)

// SessionLock represents an exclusive lock on a session, preventing
// concurrent access from multiple processes (CLI instances, desktop app).
// The lock is automatically released when the process exits (kernel
// releases flocks on file descriptor close), making it crash-safe.
type SessionLock struct {
	storeDir  string
	sessionID string
	acquired  bool     // true = we hold the lock; false = held by another process
	holderPID int      // PID of the holder when acquired==false (0 if unknown)
	file      *os.File // kept open to hold the flock (unix) or lock (windows)
}

// LockFilePath returns the path to the lock file for a session.
func LockFilePath(storeDir, sessionID string) string {
	return filepath.Join(storeDir, sessionID+".lock")
}

// CleanupStaleLocks scans the sessions directory for lock files whose
// owning process has exited. This handles the case where a process was
// killed (SIGKILL, panic, power loss) before Release() could run — the
// kernel releases the flock but the file remains.
//
// Safe to call at startup. Lock files held by a live process are left alone.
func CleanupStaleLocks(storeDir string) {
	matches, err := filepath.Glob(filepath.Join(storeDir, "*.lock"))
	if err != nil || len(matches) == 0 {
		return
	}
	for _, path := range matches {
		// IsSessionLocked handles stale detection + removal internally.
		// We just need to call it for each session ID.
		base := filepath.Base(path)
		sessionID := base[:len(base)-len(".lock")]
		IsSessionLocked(storeDir, sessionID) //nolint:errcheck — best effort
	}
}

// parsePID parses a PID from raw bytes.
func parsePID(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := len(data)
	if n > 16 {
		n = 16
	}
	pid, _ := strconv.Atoi(string(data[:n]))
	return pid
}
