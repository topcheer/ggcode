package swarm

import (
	"path/filepath"
	"sync"
)

// Process-level owner session for prompt-surface filtering (#5837).
//
// The Manager knows its session, but prompt building has no Manager
// instance. SetProcessOwnerSession is called once at process bootstrap
// (root.go, right after Manager.SetOwnerSession) so SummarizePersistedTeams
// can filter to this session's (plus adoptable legacy) teams without
// threading a session ID through the prompt-building call chain.
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

// SetProcessWorkspace records the active workspace for the prompt-surface
// team scan (#5851-followup): teams live under <workspace>/.ggcode/swarm.
var procWorkspace string

func SetProcessWorkspace(dir string) {
	procSessionMu.Lock()
	procWorkspace = dir
	procSessionMu.Unlock()
}

func processWorkspaceRoot() string {
	procSessionMu.RLock()
	defer procSessionMu.RUnlock()
	if procWorkspace == "" {
		return ""
	}
	return procWorkspace + string(filepath.Separator) + ".ggcode"
}

// processOwnerSession returns the recorded session ID ("" when unset).
func processOwnerSession() string {
	procSessionMu.RLock()
	defer procSessionMu.RUnlock()
	return procOwnerSess
}
