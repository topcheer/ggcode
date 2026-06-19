//go:build darwin

package tool

import (
	"encoding/json"
	"os"
	"testing"
)

func TestGhosttyTool_BasicInterface(t *testing.T) {
	tool := NewGhosttyTool("/tmp")
	if tool.Name() != "ghostty" {
		t.Fatalf("Name() = %q, want %q", tool.Name(), "ghostty")
	}
	if tool.Description() == "" {
		t.Fatal("Description() should not be empty")
	}

	params := tool.Parameters()
	var schema map[string]any
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatalf("Parameters() is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("Parameters() type = %v, want object", schema["type"])
	}
}

func TestGhosttyTool_Clone(t *testing.T) {
	original := NewGhosttyTool("/workspace")
	cloned := original.Clone()

	ghosttyClone, ok := cloned.(*GhosttyTool)
	if !ok {
		t.Fatalf("Clone() returned %T, want *GhosttyTool", cloned)
	}
	if ghosttyClone.WorkingDir != original.WorkingDir {
		t.Fatalf("Clone() WorkingDir = %q, want %q", ghosttyClone.WorkingDir, original.WorkingDir)
	}
}

func TestGhosttyTool_StatusNotDetected(t *testing.T) {
	// Ensure TERM_PROGRAM is not ghostty.
	orig := os.Getenv("TERM_PROGRAM")
	t.Setenv("TERM_PROGRAM", "xterm")
	defer os.Setenv("TERM_PROGRAM", orig)

	tool := NewGhosttyTool("/tmp")
	input, _ := json.Marshal(map[string]string{
		"action":      "status",
		"description": "Check ghostty status",
	})
	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("status should not be error when not detected: %s", result.Content)
	}
	if result.Content == "" {
		t.Fatal("status should return non-empty content")
	}
}

func TestGhosttyTool_NotDetectedReturnsError(t *testing.T) {
	orig := os.Getenv("TERM_PROGRAM")
	t.Setenv("TERM_PROGRAM", "xterm")
	defer os.Setenv("TERM_PROGRAM", orig)

	tool := NewGhosttyTool("/tmp")

	// All actions except status should fail when not in Ghostty.
	for _, action := range []string{"list", "split", "focus", "close"} {
		input, _ := json.Marshal(map[string]string{
			"action":      action,
			"description": "Test action",
		})
		result, _ := tool.Execute(t.Context(), input)
		if !result.IsError {
			t.Fatalf("action %q should return error when not in Ghostty", action)
		}
	}
}

func TestGhosttyTool_InvalidAction(t *testing.T) {
	// Set TERM_PROGRAM to ghostty so we pass the detection check,
	// but the action is invalid.
	t.Setenv("TERM_PROGRAM", "ghostty")

	tool := NewGhosttyTool("/tmp")
	input, _ := json.Marshal(map[string]string{
		"action":      "bogus_action",
		"description": "Test invalid action",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("invalid action should return error")
	}
}

func TestGhosttyTool_MissingAction(t *testing.T) {
	tool := NewGhosttyTool("/tmp")
	input, _ := json.Marshal(map[string]string{
		"description": "Missing action test",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("missing action should return error")
	}
}

func TestGhosttyTool_SelectTabValidation(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	tool := NewGhosttyTool("/tmp")

	input, _ := json.Marshal(map[string]any{
		"action":      "select_tab",
		"tab_index":   0,
		"description": "Invalid tab index",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("tab_index 0 should return error")
	}
}

func TestGhosttyTool_InputValidation(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	tool := NewGhosttyTool("/tmp")

	input, _ := json.Marshal(map[string]string{
		"action":      "input",
		"text":        "",
		"description": "Empty input",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("empty input text should return error")
	}
}

func TestGhosttyTool_SendKeyValidation(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	tool := NewGhosttyTool("/tmp")

	input, _ := json.Marshal(map[string]string{
		"action":      "send_key",
		"description": "Missing key",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("missing key should return error")
	}
}

func TestGhosttyTool_SplitInvalidDirection(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	tool := NewGhosttyTool("/tmp")

	input, _ := json.Marshal(map[string]string{
		"action":      "split",
		"direction":   "sideways",
		"description": "Invalid direction",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("invalid direction should return error")
	}
}

func TestGhosttyTool_Registration(t *testing.T) {
	// When not in Ghostty, the tool should not be registered.
	t.Setenv("TERM_PROGRAM", "xterm")
	if shouldRegisterGhosttyTool() {
		t.Fatal("should not register ghostty tool when TERM_PROGRAM != ghostty")
	}

	// When in Ghostty, the tool should be registered.
	t.Setenv("TERM_PROGRAM", "ghostty")
	if !shouldRegisterGhosttyTool() {
		t.Fatal("should register ghostty tool when TERM_PROGRAM == ghostty")
	}
}

func TestGhosttyTool_RegisterWithBuiltinTools(t *testing.T) {
	// Verify that RegisterBuiltinTools registers ghostty when in Ghostty.
	t.Setenv("TERM_PROGRAM", "ghostty")

	registry := NewRegistry()
	if err := RegisterBuiltinTools(registry, nil, "/tmp"); err != nil {
		t.Fatalf("RegisterBuiltinTools failed: %v", err)
	}
	if _, ok := registry.Get("ghostty"); !ok {
		t.Fatal("ghostty tool should be registered when TERM_PROGRAM == ghostty")
	}
}

func TestGhosttyTool_NotRegisteredOutsideGhostty(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "xterm")

	registry := NewRegistry()
	if err := RegisterBuiltinTools(registry, nil, "/tmp"); err != nil {
		t.Fatalf("RegisterBuiltinTools failed: %v", err)
	}
	if _, ok := registry.Get("ghostty"); ok {
		t.Fatal("ghostty tool should not be registered when TERM_PROGRAM != ghostty")
	}
}

func TestEscapeAS(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{`hello`, `hello`},
		{`say "hi"`, `say \"hi\"`},
		{`path\to\file`, `path\\to\\file`},
		{`mix \"quote" and \backslash`, `mix \\\"quote\" and \\backslash`},
	}
	for _, tc := range tests {
		got := escapeAS(tc.input)
		if got != tc.expect {
			t.Errorf("escapeAS(%q) = %q, want %q", tc.input, got, tc.expect)
		}
	}
}

func TestTerminalSpecifier(t *testing.T) {
	// Empty terminalID should target focused terminal.
	if got := terminalSpecifier(""); got != "focused terminal of selected tab of window 1" {
		t.Errorf("terminalSpecifier(\"\") = %q", got)
	}

	// Non-empty should target by ID.
	got := terminalSpecifier("ABC-123")
	want := `first terminal whose id is "ABC-123"`
	if got != want {
		t.Errorf("terminalSpecifier(\"ABC-123\") = %q, want %q", got, want)
	}

	// Special characters should be escaped.
	got = terminalSpecifier(`test"id`)
	want = `first terminal whose id is "test\"id"`
	if got != want {
		t.Errorf("terminalSpecifier escaped = %q, want %q", got, want)
	}
}
