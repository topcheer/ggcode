package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIsWriteToolName(t *testing.T) {
	writeTools := []string{
		"write_file", "edit_config", "delete_record", "remove_item",
		"create_user", "update_profile", "insert_row", "set_value",
		"put_object", "post_data", "patch_resource", "execute_query",
		"run_script", "exec_command", "shell_exec", "move_file",
		"rename_dir", "upload_blob", "install_package", "deploy_app",
	}
	for _, name := range writeTools {
		if !isWriteToolName(name) {
			t.Errorf("isWriteToolName(%q) = false, want true", name)
		}
	}

	readTools := []string{
		"read_file", "get_config", "list_items", "search_docs",
		"fetch_data", "query_db", "stat_file", "show_status",
	}
	for _, name := range readTools {
		if isWriteToolName(name) {
			t.Errorf("isWriteToolName(%q) = true, want false", name)
		}
	}
}

type mockCaller struct {
	result *CallToolResult
	err    error
}

func (m *mockCaller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	return m.result, m.err
}

func TestReadOnlyAdapter_BlocksWriteTools(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "write_file", Description: "Write a file"},
		{Name: "read_file", Description: "Read a file"},
	}
	adapter := NewReadOnlyAdapter("testserver", &mockCaller{
		result: &CallToolResult{Content: []ToolContent{{Type: "text", Text: "ok"}}},
	}, tools)

	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatal(err)
	}

	// write_file should be blocked
	wt, ok := registry.Get("mcp__testserver__write_file")
	if !ok {
		t.Fatal("write_file tool not registered")
	}
	result, err := wt.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("write tool should return error in read-only mode")
	}

	// read_file should work normally
	rt, ok := registry.Get("mcp__testserver__read_file")
	if !ok {
		t.Fatal("read_file tool not registered")
	}
	result2, err := rt.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2.IsError {
		t.Error("read tool should not be blocked in read-only mode")
	}
	if result2.Content != "ok" {
		t.Errorf("expected 'ok', got %q", result2.Content)
	}
}

func TestReadOnlyAdapter_Description(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "read_data", Description: "Read some data"},
	}
	adapter := NewReadOnlyAdapter("srv", &mockCaller{}, tools)

	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatal(err)
	}

	rt, _ := registry.Get("mcp__srv__read_data")
	if !adapter.IsReadOnly() {
		t.Error("adapter should report read-only")
	}
	desc := rt.Description()
	if desc != "Read some data (read-only)" {
		t.Errorf("expected description suffix '(read-only)', got %q", desc)
	}
}

func TestNormalAdapter_NotReadOnly(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "write_file", Description: "Write a file"},
	}
	adapter := NewAdapter("srv", &mockCaller{
		result: &CallToolResult{Content: []ToolContent{{Type: "text", Text: "done"}}},
	}, tools)

	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatal(err)
	}

	if adapter.IsReadOnly() {
		t.Error("normal adapter should not be read-only")
	}

	wt, _ := registry.Get("mcp__srv__write_file")
	result, err := wt.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("write tool should work in normal mode")
	}
	if result.Content != "done" {
		t.Errorf("expected 'done', got %q", result.Content)
	}
}
