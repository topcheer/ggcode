package plugin

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

// #4071: every registry-unregister path in the plugin loader must enumerate
// the adapter's OWNED names (RegisteredNames, #1594-A) - ToolNames() lists
// the full definition set, so a duplicate-name server whose RegisterTools
// calls were all collision-skipped would unregister the sibling's tools
// from the shared registry. This probe pins every site so a refactor cannot
// silently reintroduce the full-list unregister.
func TestIssue4071UnregisterSitesUseOwnedNames(t *testing.T) {
	src, err := os.ReadFile("mcp_loader.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)

	// Indirect unregister paths (Resurrect/Disconnect/Retry/reloadServers)
	// must use the owned-set accessor, NOT the Info() display snapshot.
	if !strings.Contains(text, "func (m *MCPPlugin) OwnedToolNames()") {
		t.Fatal("OwnedToolNames accessor missing (#4071)")
	}
	if strings.Count(text, ".OwnedToolNames()") < 4 {
		t.Fatal("the 4 indirect unregister paths must enumerate OwnedToolNames() (#4071)")
	}
	for _, m := range regexp.MustCompile(`range (previous|plugin|p)\.Info\(\)\.ToolNames`).FindAllString(text, -1) {
		t.Fatalf("unregister loop still keys on the Info() display snapshot: %q (#4071)", m)
	}

	// No unregister loop may enumerate ToolNames() anymore.
	for _, m := range regexp.MustCompile(`range\s+(\w+)\.ToolNames\(\)`).FindAllStringSubmatch(text, -1) {
		t.Fatalf("unregister-site still enumerates full ToolNames() via %q (#4071/#1594-A regression)", m[0])
	}

	// Direct sites carry the owned-set call (4 loops + 1 ForceReauth + snapshot).
	if n := strings.Count(text, ".RegisteredNames()"); n < 5 {
		t.Fatalf("expected >=5 RegisteredNames() unregister sites, found %d", n)
	}
}

// Functional ownership, the issue's duplicate-name scenario: two same-name
// servers share one registry; the second server's registrations all
// collision-skip, so it owns NOTHING - unregistering via its owned set must
// leave the sibling's tools registered.
func TestIssue4071SkippedAdapterOwnsNothing(t *testing.T) {
	registry := tool.NewRegistry()
	tools := []mcp.ToolDefinition{{Name: "echo"}}

	first := mcp.NewAdapter("github", nil, tools)
	if err := first.RegisterTools(registry); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if len(first.RegisteredNames()) != 1 {
		t.Fatalf("first adapter must own the tool, got %v", first.RegisteredNames())
	}

	second := mcp.NewAdapter("github", nil, tools) // same server name -> same ggcode tool name
	if err := second.RegisterTools(registry); err != nil {
		t.Fatalf("second register: %v", err)
	}
	if len(second.RegisteredNames()) != 0 {
		t.Fatalf("collision-skipped adapter must own nothing, got %v", second.RegisteredNames())
	}
	if len(second.ToolNames()) != 1 {
		t.Fatal("fixture premise: ToolNames still lists the definition (the trap)")
	}

	// The loader's unregister contract: iterate the OWNED set only.
	for _, tn := range second.RegisteredNames() {
		registry.Unregister(tn)
	}
	if _, ok := registry.Get(first.RegisteredNames()[0]); !ok {
		t.Fatal("sibling's tool was unregistered by the skipped adapter (#4071 core regression)")
	}
}
