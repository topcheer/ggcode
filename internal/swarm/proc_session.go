package swarm

import (
	"sync"
)

// Process-level owner session for prompt-surface filtering (#5837).
//
// The Manager knows its session, but prompt building has no Manager
// instance. SetProcessOwnerSession is called once at process bootstrap
// (root.go, right after the swarm manager is constructed with the
// confirmed session id, #5860) so SummarizePersistedTeams can filter to
// this session's namespace without threading a session ID through the
// prompt-building call chain.
var (
	procSessionMu sync.RWMutex
	procOwnerSess string
)

// SetProcessOwnerSession records the active session ID for surface
// filtering. Empty (unset) keeps the unfiltered legacy behavior.
func SetProcessOwnerSession(sessionID string) {
	procSessionMu.Lock()
	procOwnerSess = sessionID
	procSessionMu.Unlock()
}

// processOwnerSession returns the recorded session ID ("" when unset).
func processOwnerSession() string {
	procSessionMu.RLock()
	defer procSessionMu.RUnlock()
	return procOwnerSess
}
