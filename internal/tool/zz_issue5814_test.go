package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// #5814: per-server fleet health must surface in list_mcp_capabilities so
// long-lived instances can audit dead/stale MCP servers without a tool call.

type issue5814Runtime struct {
	snaps []MCPServerSnapshot
}

func (r issue5814Runtime) SnapshotMCP() []MCPServerSnapshot { return r.snaps }

func (r issue5814Runtime) GetPrompt(ctx context.Context, server, name string, args map[string]interface{}) (*MCPPromptResult, error) {
	return nil, nil
}

func (r issue5814Runtime) ReadResource(ctx context.Context, server, uri string) (*MCPResourceResult, error) {
	return nil, nil
}

func TestIssue5814ListCapabilitiesRendersHealth(t *testing.T) {
	rt := issue5814Runtime{snaps: []MCPServerSnapshot{
		{
			Name:      "legacy-srv",
			Connected: true,
			ToolNames: []string{"t1"},
		},
		{
			Name:           "fleet-srv",
			Connected:      true,
			ToolNames:      []string{"t2"},
			Health:         "dead",
			LastProbeError: "probe timed out after 30s",
		},
		{
			Name:      "reinit-srv",
			Connected: true,
			ToolNames: []string{"t3"},
			Health:    "reiniting",
		},
	}}
	res, err := ListMCPCapabilitiesTool{Runtime: rt}.Execute(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := res.Content
	if !strings.Contains(out, "- legacy-srv (connected)\n") {
		t.Fatalf("legacy snapshot without health must render the old shape; got:\n%s", out)
	}
	if !strings.Contains(out, "- fleet-srv (connected, health: dead)\n") {
		t.Fatalf("dead server must render its health; got:\n%s", out)
	}
	if !strings.Contains(out, "  last probe: probe timed out after 30s\n") {
		t.Fatalf("last probe error must render for audits; got:\n%s", out)
	}
	if !strings.Contains(out, "- reinit-srv (connected, health: reiniting)\n") {
		t.Fatalf("reiniting server must render its health; got:\n%s", out)
	}
}
