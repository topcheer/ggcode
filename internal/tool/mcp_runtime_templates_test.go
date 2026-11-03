package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeMCPRuntimeWithTemplates struct {
	fakeMCPRuntime
}

func (f fakeMCPRuntimeWithTemplates) SnapshotMCP() []MCPServerSnapshot {
	return []MCPServerSnapshot{{
		Name:                  "db-bridge",
		Connected:             true,
		ResourceNames:         []string{"docs"},
		ResourceTemplateNames: []string{"db://tables/{t}", "view (db://views/{v})"},
	}}
}

func TestListMCPCapabilitiesRendersResourceTemplates(t *testing.T) {
	tool := ListMCPCapabilitiesTool{Runtime: fakeMCPRuntimeWithTemplates{}}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "resource templates (expand via read_mcp_resource): db://tables/{t}, view (db://views/{v})") {
		t.Fatalf("templates line missing from capabilities output:\n%s", result.Content)
	}
}

func TestListMCPCapabilitiesOmitsTemplatesLineForLegacy(t *testing.T) {
	tool := ListMCPCapabilitiesTool{Runtime: fakeMCPRuntime{}}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Content, "resource templates") {
		t.Fatalf("legacy server must not render a templates line:\n%s", result.Content)
	}
}
