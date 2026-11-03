package session

import (
	"os"
	"path/filepath"
	"strconv"
)

type SessionLock struct {
	storeDir  string
	sessionID string
	acquired  bool
	holderPID int
	file      *os.File
}

func (l *SessionLock) SessionID() string {
	if l == nil {
		return ""
	}
	return l.sessionID
}

func LockFilePath(storeDir, sessionID string) string {
	if !validSessionID(sessionID) {
		return filepath.Join(storeDir, "_invalid_id_.lock")
	}
	return filepath.Join(storeDir, sessionID+".lock")
}

func CleanupStaleLocks(storeDir string) {
	matches, err := filepath.Glob(filepath.Join(storeDir, "*.lock"))
	if err != nil || len(matches) == 0 {
		return
	}
	for _, path := range matches {

		base := filepath.Base(path)
		sessionID := base[:len(base)-len(".lock")]
		IsSessionLocked(storeDir, sessionID) //nolint:errcheck — best effort
	}
}

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
