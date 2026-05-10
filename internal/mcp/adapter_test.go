package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestAdapterRegisterTools registers tools into a real registry and verifies names.
func TestAdapterRegisterToolsInRegistry(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "search", Description: "Search files", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "read", Description: "Read file", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	a := NewAdapter("filesystem", nil, tools)

	registry := tool.NewRegistry()
	if err := a.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	// Verify both tools are registered
	for _, td := range tools {
		fullName := "mcp__filesystem__" + td.Name
		registered, ok := registry.Get(fullName)
		if !ok {
			t.Errorf("tool %q not found in registry", fullName)
			continue
		}
		if registered.Name() != fullName {
			t.Errorf("tool name = %q, want %q", registered.Name(), fullName)
		}
		if registered.Description() != td.Description {
			t.Errorf("tool description = %q, want %q", registered.Description(), td.Description)
		}
	}
}

// TestAdapterToolConflict_TwoServersSameToolName verifies that when two adapters from
// different servers register a tool with the same local name, the second registration
// is skipped (name collision) but the first tool is preserved.
func TestAdapterToolConflict_TwoServersSameToolName(t *testing.T) {
	registry := tool.NewRegistry()

	// Server A registers "search"
	adapterA := NewAdapter("server-a", nil, []ToolDefinition{
		{Name: "search", Description: "Search from server A", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapterA.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools A: %v", err)
	}

	// Server B registers "search" — different server, different prefixed name, so no conflict
	adapterB := NewAdapter("server-b", nil, []ToolDefinition{
		{Name: "search", Description: "Search from server B", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapterB.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools B: %v", err)
	}

	// Both should exist with different prefixed names
	toolA, okA := registry.Get("mcp__server-a__search")
	toolB, okB := registry.Get("mcp__server-b__search")
	if !okA {
		t.Error("server-a search tool not found")
	}
	if !okB {
		t.Error("server-b search tool not found")
	}
	if okA && toolA.Description() != "Search from server A" {
		t.Errorf("server-a tool description = %q", toolA.Description())
	}
	if okB && toolB.Description() != "Search from server B" {
		t.Errorf("server-b tool description = %q", toolB.Description())
	}
}

// TestAdapterToolConflict_SamePrefixedNameSkipped verifies that if two adapters produce
// the same prefixed tool name (same server name + same tool name), the second one is
// skipped and the first is preserved.
func TestAdapterToolConflict_SamePrefixedNameSkipped(t *testing.T) {
	registry := tool.NewRegistry()

	// First adapter registers "search"
	adapter1 := NewAdapter("myserver", nil, []ToolDefinition{
		{Name: "search", Description: "First search tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapter1.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools 1: %v", err)
	}

	// Second adapter with the same server name tries to register the same tool name
	adapter2 := NewAdapter("myserver", nil, []ToolDefinition{
		{Name: "search", Description: "Second search tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapter2.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools 2: %v", err)
	}

	// The first tool should be preserved
	registered, ok := registry.Get("mcp__myserver__search")
	if !ok {
		t.Fatal("tool mcp__myserver__search not found in registry")
	}
	if registered.Description() != "First search tool" {
		t.Errorf("expected first tool preserved, got description %q", registered.Description())
	}
}

// TestAdapterToolConflict_MultipleToolsPartialConflict verifies that when some tools
// conflict and some don't, the non-conflicting tools are still registered.
func TestAdapterToolConflict_MultipleToolsPartialConflict(t *testing.T) {
	registry := tool.NewRegistry()

	// Register all tools from server A
	adapterA := NewAdapter("srv", nil, []ToolDefinition{
		{Name: "read", Description: "Read A", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "write", Description: "Write A", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapterA.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools A: %v", err)
	}

	// Register with conflict on "read" but new "delete"
	adapterB := NewAdapter("srv", nil, []ToolDefinition{
		{Name: "read", Description: "Read B", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "delete", Description: "Delete B", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapterB.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools B: %v", err)
	}

	// "read" should be the first registration
	readTool, ok := registry.Get("mcp__srv__read")
	if !ok {
		t.Fatal("mcp__srv__read not found")
	}
	if readTool.Description() != "Read A" {
		t.Errorf("read tool description = %q, want 'Read A'", readTool.Description())
	}

	// "write" should still exist
	writeTool, ok := registry.Get("mcp__srv__write")
	if !ok {
		t.Fatal("mcp__srv__write not found")
	}
	if writeTool.Description() != "Write A" {
		t.Errorf("write tool description = %q, want 'Write A'", writeTool.Description())
	}

	// "delete" should be registered
	deleteTool, ok := registry.Get("mcp__srv__delete")
	if !ok {
		t.Fatal("mcp__srv__delete not found")
	}
	if deleteTool.Description() != "Delete B" {
		t.Errorf("delete tool description = %q, want 'Delete B'", deleteTool.Description())
	}
}

// TestAdapterToolConflict_ErrorMessage verifies that tool name conflict produces an
// error from the registry containing the conflicting name.
func TestAdapterToolConflict_ErrorMessage(t *testing.T) {
	registry := tool.NewRegistry()

	// Register first tool
	adapter1 := NewAdapter("conflict-srv", nil, []ToolDefinition{
		{Name: "do_thing", Description: "Original", InputSchema: json.RawMessage(`{}`)},
	})
	if err := adapter1.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools 1: %v", err)
	}

	// Try registering directly with the registry to observe the error
	conflictingTool := &mcpTool{
		name:     "mcp__conflict-srv__do_thing",
		caller:   nil,
		toolName: "do_thing",
		desc:     "Duplicate",
		schema:   json.RawMessage(`{}`),
	}
	err := registry.Register(conflictingTool)
	if err == nil {
		t.Fatal("expected error when registering duplicate tool")
	}
	if !strings.Contains(err.Error(), "mcp__conflict-srv__do_thing") {
		t.Errorf("error should contain tool name, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error should mention 'already registered', got: %s", err.Error())
	}
}
