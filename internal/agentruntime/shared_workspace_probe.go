package agentruntime

import "sync"

var (
	sharedWorkspaceProbeMu sync.RWMutex
	sharedWorkspaceProbe   func() string
)

func SetSharedWorkspaceProbe(fn func() string) {
	sharedWorkspaceProbeMu.Lock()
	defer sharedWorkspaceProbeMu.Unlock()
	sharedWorkspaceProbe = fn
}

func sharedWorkspaceAlertText() string {
	sharedWorkspaceProbeMu.RLock()
	fn := sharedWorkspaceProbe
	sharedWorkspaceProbeMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn()
}
