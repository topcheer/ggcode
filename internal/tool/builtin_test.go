package tool

import (
	"testing"
)

func TestBuiltinTools_Registration(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterBuiltinTools(registry, nil, ""); err != nil {
		t.Fatalf("RegisterBuiltinTools failed: %v", err)
	}

	tools := registry.List()
	t.Logf("Total tools registered: %d", len(tools))

	// Check that the new tools are registered
	requiredTools := []string{"web_fetch", "web_search", "ask_user", "todo_write", "start_command", "read_command_output", "wait_command", "stop_command", "write_command_input", "list_commands", "lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols", "lsp_workspace_symbols", "lsp_diagnostics", "lsp_code_actions", "lsp_rename"}
	for _, name := range requiredTools {
		tl, ok := registry.Get(name)
		if !ok {
			t.Errorf("Tool %q not registered", name)
		} else {
			t.Logf("✓ Tool %q registered: %s", name, tl.Description())
		}
	}

	// Verify each required tool has non-empty parameters
	for _, name := range requiredTools {
		tl, ok := registry.Get(name)
		if !ok {
			continue
		}
		params := tl.Parameters()
		if len(params) == 0 {
			t.Errorf("Tool %q has empty parameters", name)
		}
	}
}
