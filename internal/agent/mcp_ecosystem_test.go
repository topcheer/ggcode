package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// fakeMCPRuntimeForEcosystem implements tool.MCPRuntime for testing.
type fakeMCPRuntimeForEcosystem struct {
	snapshots []tool.MCPServerSnapshot
}

func (f fakeMCPRuntimeForEcosystem) SnapshotMCP() []tool.MCPServerSnapshot {
	return f.snapshots
}

func (f fakeMCPRuntimeForEcosystem) GetPrompt(ctx context.Context, server, name string, args map[string]interface{}) (*tool.MCPPromptResult, error) {
	return nil, nil
}

func (f fakeMCPRuntimeForEcosystem) ReadResource(ctx context.Context, server, uri string) (*tool.MCPResourceResult, error) {
	return nil, nil
}

func TestMCPEcosystemHealthy(t *testing.T) {
	s := newMCPEcosystemState()
	snapshots := []tool.MCPServerSnapshot{
		{Name: "server-a", Connected: true, ToolNames: []string{"mcp__server-a__search"}},
		{Name: "server-b", Connected: true, ToolNames: []string{"mcp__server-b__fetch"}},
	}
	msg := s.analyzeMCPEcosystem(snapshots)
	if msg != "" {
		t.Fatalf("expected empty message for healthy ecosystem, got: %s", msg)
	}
}

func TestMCPEcosystemNoServers(t *testing.T) {
	s := newMCPEcosystemState()
	msg := s.analyzeMCPEcosystem(nil)
	if msg != "" {
		t.Fatalf("expected empty message when no MCP servers, got: %s", msg)
	}
}

func TestMCPEcosystemFailedServer(t *testing.T) {
	s := newMCPEcosystemState()
	snapshots := []tool.MCPServerSnapshot{
		{Name: "failed-srv", Connected: false, Error: "connection refused"},
		{Name: "ok-srv", Connected: true, ToolNames: []string{"mcp__ok-srv__tool1"}},
	}
	msg := s.analyzeMCPEcosystem(snapshots)
	if msg == "" {
		t.Fatal("expected warning for failed server")
	}
	if !strings.Contains(msg, "failed-srv") {
		t.Errorf("message should mention failed server name, got: %s", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("message should include error detail, got: %s", msg)
	}
}

func TestMCPEcosystemOAuthIssue(t *testing.T) {
	s := newMCPEcosystemState()
	snapshots := []tool.MCPServerSnapshot{
		{Name: "oauth-srv", Connected: false, Error: "OAuth authentication required: token expired"},
	}
	msg := s.analyzeMCPEcosystem(snapshots)
	if msg == "" {
		t.Fatal("expected warning for OAuth issue")
	}
	if !strings.Contains(msg, "authentication") || !strings.Contains(msg, "oauth-srv") {
		t.Errorf("message should mention auth issue and server name, got: %s", msg)
	}
}

func TestMCPEcosystemEmptyServer(t *testing.T) {
	s := newMCPEcosystemState()
	snapshots := []tool.MCPServerSnapshot{
		{Name: "empty-srv", Connected: true, ToolNames: nil, PromptNames: nil, ResourceNames: nil},
	}
	msg := s.analyzeMCPEcosystem(snapshots)
	if msg == "" {
		t.Fatal("expected warning for empty server")
	}
	if !strings.Contains(msg, "empty-srv") {
		t.Errorf("message should mention empty server name, got: %s", msg)
	}
	if !strings.Contains(msg, "no tools") {
		t.Errorf("message should explain the issue, got: %s", msg)
	}
}

func TestMCPEcosystemToolConflict(t *testing.T) {
	s := newMCPEcosystemState()
	snapshots := []tool.MCPServerSnapshot{
		{Name: "srv-a", Connected: true, ToolNames: []string{"mcp__srv-a__search"}},
		{Name: "srv-b", Connected: true, ToolNames: []string{"mcp__srv-b__search"}},
	}
	msg := s.analyzeMCPEcosystem(snapshots)
	if msg == "" {
		t.Fatal("expected warning for tool conflict")
	}
	if !strings.Contains(msg, "conflict") {
		t.Errorf("message should mention conflict, got: %s", msg)
	}
	if !strings.Contains(msg, "search") {
		t.Errorf("message should mention conflicting tool name, got: %s", msg)
	}
}

func TestMCPEcosystemConflictDetection(t *testing.T) {
	// No conflicts
	snapshots := []tool.MCPServerSnapshot{
		{Name: "srv-a", Connected: true, ToolNames: []string{"mcp__srv-a__tool1"}},
		{Name: "srv-b", Connected: true, ToolNames: []string{"mcp__srv-b__tool2"}},
	}
	conflicts := detectToolConflicts(snapshots)
	if len(conflicts) != 0 {
		t.Fatalf("expected 0 conflicts, got %d", len(conflicts))
	}

	// With conflict
	snapshots = []tool.MCPServerSnapshot{
		{Name: "srv-a", Connected: true, ToolNames: []string{"mcp__srv-a__shared"}},
		{Name: "srv-b", Connected: true, ToolNames: []string{"mcp__srv-b__shared"}},
	}
	conflicts = detectToolConflicts(snapshots)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].toolName != "shared" {
		t.Errorf("expected tool name 'shared', got %q", conflicts[0].toolName)
	}
}

func TestMCPEcosystemConflictSkipsDisconnected(t *testing.T) {
	// Disconnected servers should not contribute to conflicts
	snapshots := []tool.MCPServerSnapshot{
		{Name: "srv-a", Connected: true, ToolNames: []string{"mcp__srv-a__shared"}},
		{Name: "srv-b", Connected: false, ToolNames: []string{"mcp__srv-b__shared"}},
	}
	conflicts := detectToolConflicts(snapshots)
	if len(conflicts) != 0 {
		t.Fatalf("expected 0 conflicts (server-b disconnected), got %d", len(conflicts))
	}
}

func TestMCPEcosystemExtractBaseName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"mcp__server-a__search", "search"},
		{"mcp__server-b__fetch_page", "fetch_page"},
		{"non-mcp-tool", "non-mcp-tool"},
		{"", ""},
		{"mcp__only", "mcp__only"},
	}
	for _, tc := range tests {
		got := extractMCPToolBaseName(tc.input)
		if got != tc.expected {
			t.Errorf("extractMCPToolBaseName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestMCPEcosystemReset(t *testing.T) {
	s := newMCPEcosystemState()
	s.fired = true
	s.reset()
	if s.fired {
		t.Fatal("reset should clear fired flag")
	}
}

func TestMCPEcosystemFiresOnce(t *testing.T) {
	a := &Agent{
		mcpEcosystem: newMCPEcosystemState(),
		mcpRuntime: fakeMCPRuntimeForEcosystem{
			snapshots: []tool.MCPServerSnapshot{
				{Name: "failed", Connected: false, Error: "conn refused"},
			},
		},
	}

	// Iteration 1 should be skipped (too early)
	msg := a.maybeWarnMCP(1)
	if msg != "" {
		t.Fatal("iteration 1 should not fire (too early)")
	}

	// Iteration 2 should fire
	msg = a.maybeWarnMCP(2)
	if msg == "" {
		t.Fatal("iteration 2 should fire for failed server")
	}

	// Subsequent calls should not fire again
	msg = a.maybeWarnMCP(3)
	if msg != "" {
		t.Fatal("should not fire more than once per run")
	}
}

func TestMCPEcosystemNilRuntime(t *testing.T) {
	a := &Agent{
		mcpEcosystem: newMCPEcosystemState(),
		mcpRuntime:   nil,
	}
	msg := a.maybeWarnMCP(2)
	if msg != "" {
		t.Fatal("should return empty when mcpRuntime is nil")
	}
}
