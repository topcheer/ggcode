package plugin

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue4071UnregisterSitesUseOwnedNames(t *testing.T) {
	src, err := os.ReadFile("mcp_loader.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)

	if !strings.Contains(text, "func (m *MCPPlugin) OwnedToolNames()") {
		t.Fatal("OwnedToolNames accessor missing (#4071)")
	}
	if strings.Count(text, ".OwnedToolNames()") < 4 {
		t.Fatal("the 4 indirect unregister paths must enumerate OwnedToolNames() (#4071)")
	}
	for _, m := range regexp.MustCompile(`range (previous|plugin|p)\.Info\(\)\.ToolNames`).FindAllString(text, -1) {
		t.Fatalf("unregister loop still keys on the Info() display snapshot: %q (#4071)", m)
	}

	for _, m := range regexp.MustCompile(`range\s+(\w+)\.ToolNames\(\)`).FindAllStringSubmatch(text, -1) {
		t.Fatalf("unregister-site still enumerates full ToolNames() via %q (#4071/#1594-A regression)", m[0])
	}

	if n := strings.Count(text, ".RegisteredNames()"); n < 5 {
		t.Fatalf("expected >=5 RegisteredNames() unregister sites, found %d", n)
	}
}

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

	second := mcp.NewAdapter("github", nil, tools)
	if err := second.RegisterTools(registry); err != nil {
		t.Fatalf("second register: %v", err)
	}
	if len(second.RegisteredNames()) != 0 {
		t.Fatalf("collision-skipped adapter must own nothing, got %v", second.RegisteredNames())
	}
	if len(second.ToolNames()) != 1 {
		t.Fatal("fixture premise: ToolNames still lists the definition (the trap)")
	}

	for _, tn := range second.RegisteredNames() {
		registry.Unregister(tn)
	}
	if _, ok := registry.Get(first.RegisteredNames()[0]); !ok {
		t.Fatal("sibling's tool was unregistered by the skipped adapter (#4071 core regression)")
	}
}
