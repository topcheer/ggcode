package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

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

func TestAdapterToolConflict_TwoServersSameToolName(t *testing.T) {
	registry := tool.NewRegistry()

	adapterA := NewAdapter("server-a", nil, []ToolDefinition{
		{Name: "search", Description: "Search from server A", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapterA.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools A: %v", err)
	}

	adapterB := NewAdapter("server-b", nil, []ToolDefinition{
		{Name: "search", Description: "Search from server B", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapterB.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools B: %v", err)
	}

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

func TestAdapterToolConflict_SamePrefixedNameSkipped(t *testing.T) {
	registry := tool.NewRegistry()

	adapter1 := NewAdapter("myserver", nil, []ToolDefinition{
		{Name: "search", Description: "First search tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapter1.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools 1: %v", err)
	}

	adapter2 := NewAdapter("myserver", nil, []ToolDefinition{
		{Name: "search", Description: "Second search tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapter2.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools 2: %v", err)
	}

	registered, ok := registry.Get("mcp__myserver__search")
	if !ok {
		t.Fatal("tool mcp__myserver__search not found in registry")
	}
	if registered.Description() != "First search tool" {
		t.Errorf("expected first tool preserved, got description %q", registered.Description())
	}
}

func TestAdapterToolConflict_MultipleToolsPartialConflict(t *testing.T) {
	registry := tool.NewRegistry()

	adapterA := NewAdapter("srv", nil, []ToolDefinition{
		{Name: "read", Description: "Read A", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "write", Description: "Write A", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapterA.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools A: %v", err)
	}

	adapterB := NewAdapter("srv", nil, []ToolDefinition{
		{Name: "read", Description: "Read B", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "delete", Description: "Delete B", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if err := adapterB.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools B: %v", err)
	}

	readTool, ok := registry.Get("mcp__srv__read")
	if !ok {
		t.Fatal("mcp__srv__read not found")
	}
	if readTool.Description() != "Read A" {
		t.Errorf("read tool description = %q, want 'Read A'", readTool.Description())
	}

	writeTool, ok := registry.Get("mcp__srv__write")
	if !ok {
		t.Fatal("mcp__srv__write not found")
	}
	if writeTool.Description() != "Write A" {
		t.Errorf("write tool description = %q, want 'Write A'", writeTool.Description())
	}

	deleteTool, ok := registry.Get("mcp__srv__delete")
	if !ok {
		t.Fatal("mcp__srv__delete not found")
	}
	if deleteTool.Description() != "Delete B" {
		t.Errorf("delete tool description = %q, want 'Delete B'", deleteTool.Description())
	}
}

func TestAdapterToolConflict_ErrorMessage(t *testing.T) {
	registry := tool.NewRegistry()

	adapter1 := NewAdapter("conflict-srv", nil, []ToolDefinition{
		{Name: "do_thing", Description: "Original", InputSchema: json.RawMessage(`{}`)},
	})
	if err := adapter1.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools 1: %v", err)
	}

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

func TestAdapterSandboxSafeFlag(t *testing.T) {
	defs := []ToolDefinition{
		{Name: "query", Description: "Query data", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	registry := tool.NewRegistry()

	ro := NewReadOnlyAdapter("ro-srv", nil, defs)
	if err := ro.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools (read_only): %v", err)
	}
	rw := NewAdapter("rw-srv", nil, defs)
	if err := rw.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools (write): %v", err)
	}

	for name, want := range map[string]bool{
		"mcp__ro-srv__query": true,
		"mcp__rw-srv__query": false,
	} {
		registered, ok := registry.Get(name)
		if !ok {
			t.Fatalf("tool %q not found in registry", name)
		}
		ss, implements := registered.(tool.SandboxSafe)
		if !implements {
			t.Fatalf("tool %q does not implement tool.SandboxSafe", name)
		}
		if got := ss.SandboxSafe(); got != want {
			t.Errorf("%q SandboxSafe() = %v, want %v", name, got, want)
		}
	}
}
