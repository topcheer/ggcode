package session

import (
	"os"
	"strconv"
)

// SessionLock represents an exclusive lock on a session, preventing
// concurrent access from multiple processes (CLI instances, desktop app).
// The lock is automatically released when the process exits (kernel
// releases flocks on file descriptor close), making it crash-safe.
type SessionLock struct {
	storeDir  string
	sessionID string
	holderPID int      // 0 = we hold it; >0 = PID of holder
	file      *os.File // kept open to hold the flock (unix) or lock (windows)
}

// LockFilePath returns the path to the lock file for a session.
func LockFilePath(storeDir, sessionID string) string {
	return storeDir + "/" + sessionID + ".lock"
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
