package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue4948_SetProviderResetsServerToolSearch(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	a.serverToolSearch = true
	a.SetProvider(&mockProvider{})
	if a.serverToolSearch {
		t.Fatalf("serverToolSearch survived SetProvider: MCP schemas would stay deferred against a provider without server-side tool search (#4948)")
	}
}
