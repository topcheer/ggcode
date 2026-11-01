package agentruntime

import "sync"

// sharedWorkspaceProbe is registered by the process entrypoint (cmd/ggcode
// daemon) once the lanchat hub exists. It returns the shared-workspace
// alert text for the CURRENT workspace, or "" when this workspace has no
// concurrent lanchat agents (the common single-agent case).
//
// agentruntime cannot import internal/lanchat (hub lifecycle belongs to the
// daemon), so the daemon injects the closure here — the same pattern as
// GlobalSectionSnapshot's background collector.
var (
	sharedWorkspaceProbeMu sync.RWMutex
	sharedWorkspaceProbe   func() string
)

// SetSharedWorkspaceProbe installs (or replaces with nil) the probe that
// supplies shared-workspace collision text for system prompts.
func SetSharedWorkspaceProbe(fn func() string) {
	sharedWorkspaceProbeMu.Lock()
	defer sharedWorkspaceProbeMu.Unlock()
	sharedWorkspaceProbe = fn
}

// sharedWorkspaceAlertText returns the current alert text, or "" when no
// probe is registered or no collision exists.
func sharedWorkspaceAlertText() string {
	sharedWorkspaceProbeMu.RLock()
	fn := sharedWorkspaceProbe
	sharedWorkspaceProbeMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn()
}
