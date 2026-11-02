package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestIssue4948_SetProviderResetsServerToolSearch pins the #4948 fix:
// serverToolSearch is cross-run Agent state (set when a run sees
// ServerToolSearchActive on the then-current provider). A provider hot-swap
// must reset it, or markServerDeferred keeps flagging every MCP schema
// defer_loading against a consumer that no longer exists - the schemas ride
// each request deferred forever. Clearing is unconditional: run-setup
// re-derives the flag when the new provider supports the beta.
func TestIssue4948_SetProviderResetsServerToolSearch(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	a.serverToolSearch = true // simulate a run on a server-tool-search provider
	a.SetProvider(&mockProvider{})
	if a.serverToolSearch {
		t.Fatalf("serverToolSearch survived SetProvider: MCP schemas would stay deferred against a provider without server-side tool search (#4948)")
	}
}
