package tool

import (
	"encoding/json"
	"os"
	"testing"
)

func TestKittyTool_BasicInterface(t *testing.T) {
	tool := NewKittyTool("/tmp")
	if tool.Name() != "kitty" {
		t.Fatalf("Name() = %q, want %q", tool.Name(), "kitty")
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

func TestKittyTool_Clone(t *testing.T) {
	original := NewKittyTool("/workspace")
	cloned := original.Clone()

	kittyClone, ok := cloned.(*KittyTool)
	if !ok {
		t.Fatalf("Clone() returned %T, want *KittyTool", cloned)
	}
	if kittyClone.WorkingDir != original.WorkingDir {
		t.Fatalf("Clone() WorkingDir = %q, want %q", kittyClone.WorkingDir, original.WorkingDir)
	}
}

func TestKittyTool_CloneNil(t *testing.T) {
	var k *KittyTool
	cloned := k.Clone()
	if cloned == nil {
		t.Fatal("Clone() of nil should return non-nil")
	}
	if _, ok := cloned.(*KittyTool); !ok {
		t.Fatalf("Clone() of nil returned %T, want *KittyTool", cloned)
	}
}

func TestKittyTool_StatusNotDetected(t *testing.T) {
	orig := os.Getenv("TERM_PROGRAM")
	t.Setenv("TERM_PROGRAM", "xterm")
	defer os.Setenv("TERM_PROGRAM", orig)

	tool := NewKittyTool("/tmp")
	input, _ := json.Marshal(map[string]string{
		"action":      "status",
		"description": "Check kitty status",
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

func TestKittyTool_NotDetectedReturnsError(t *testing.T) {
	orig := os.Getenv("TERM_PROGRAM")
	t.Setenv("TERM_PROGRAM", "xterm")
	t.Setenv("KITTY_WINDOW_ID", "")
	defer os.Setenv("TERM_PROGRAM", orig)

	tool := NewKittyTool("/tmp")

	// All actions except status should fail when not in Kitty.
	for _, action := range []string{"list", "split", "focus", "close", "input", "get_text"} {
		input, _ := json.Marshal(map[string]string{
			"action":      action,
			"description": "Test action",
		})
		result, _ := tool.Execute(t.Context(), input)
		if !result.IsError {
			t.Fatalf("action %q should return error when not in Kitty", action)
		}
	}
}

func TestKittyTool_InvalidAction(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")

	tool := NewKittyTool("/tmp")
	input, _ := json.Marshal(map[string]string{
		"action":      "bogus_action",
		"description": "Test invalid action",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("invalid action should return error")
	}
}

func TestKittyTool_MissingAction(t *testing.T) {
	tool := NewKittyTool("/tmp")
	input, _ := json.Marshal(map[string]string{
		"description": "Missing action test",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("missing action should return error")
	}
}

func TestKittyTool_SelectTabValidation(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")
	tool := NewKittyTool("/tmp")

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

func TestKittyTool_InputValidation(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")
	tool := NewKittyTool("/tmp")

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

func TestKittyTool_SendKeyValidation(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")
	tool := NewKittyTool("/tmp")

	input, _ := json.Marshal(map[string]string{
		"action":      "send_key",
		"description": "Missing key",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("missing key should return error")
	}
}

func TestKittyTool_SplitInvalidDirection(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")
	tool := NewKittyTool("/tmp")

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

func TestKittyTool_ResizeInvalidAxis(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")
	tool := NewKittyTool("/tmp")

	input, _ := json.Marshal(map[string]string{
		"action":      "resize",
		"axis":        "diagonal",
		"description": "Invalid axis",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("invalid axis should return error")
	}
}

func TestKittyTool_SetTabTitleValidation(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")
	tool := NewKittyTool("/tmp")

	input, _ := json.Marshal(map[string]string{
		"action":      "set_tab_title",
		"text":        "",
		"description": "Empty title",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("empty tab title should return error")
	}
}

func TestKittyTool_ActionValidation(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "kitty")
	tool := NewKittyTool("/tmp")

	input, _ := json.Marshal(map[string]string{
		"action":      "action",
		"text":        "",
		"description": "Empty action string",
	})
	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("empty action string should return error")
	}
}

func TestKittyTool_Registration(t *testing.T) {
	// When not in Kitty, the tool should not be registered.
	t.Setenv("TERM_PROGRAM", "xterm")
	t.Setenv("KITTY_WINDOW_ID", "")
	if shouldRegisterKittyTool() {
		t.Fatal("should not register kitty tool when not in kitty")
	}

	// When in Kitty (TERM_PROGRAM=kitty), the tool should be registered.
	t.Setenv("TERM_PROGRAM", "kitty")
	if !shouldRegisterKittyTool() {
		t.Fatal("should register kitty tool when TERM_PROGRAM == kitty")
	}

	// When TERM_PROGRAM is lost but KITTY_WINDOW_ID is set (e.g. inside tmux in kitty).
	t.Setenv("TERM_PROGRAM", "xterm")
	t.Setenv("KITTY_WINDOW_ID", "1")
	if !shouldRegisterKittyTool() {
		t.Fatal("should register kitty tool when KITTY_WINDOW_ID is set (tmux fallback)")
	}
}

func TestKittyTool_RegisterWithBuiltinTools(t *testing.T) {
	// Verify that RegisterBuiltinTools registers kitty when in Kitty.
	t.Setenv("TERM_PROGRAM", "kitty")

	registry := NewRegistry()
	if err := RegisterBuiltinTools(registry, nil, "/tmp"); err != nil {
		t.Fatalf("RegisterBuiltinTools failed: %v", err)
	}
	if _, ok := registry.Get("kitty"); !ok {
		t.Fatal("kitty tool should be registered when TERM_PROGRAM == kitty")
	}
}

func TestKittyTool_NotRegisteredOutsideKitty(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "xterm")
	t.Setenv("KITTY_WINDOW_ID", "")

	registry := NewRegistry()
	if err := RegisterBuiltinTools(registry, nil, "/tmp"); err != nil {
		t.Fatalf("RegisterBuiltinTools failed: %v", err)
	}
	if _, ok := registry.Get("kitty"); ok {
		t.Fatal("kitty tool should not be registered when not in kitty")
	}
}

func TestKittyKeyEscapeSeq(t *testing.T) {
	tests := []struct {
		key       string
		expectSeq string
		expectSp  bool
	}{
		{"enter", "\r", true},
		{"return", "\r", true},
		{"tab", "\t", true},
		{"escape", "\x1b", true},
		{"esc", "\x1b", true},
		{"backspace", "\x7f", true},
		{"up", "\x1b[A", true},
		{"down", "\x1b[B", true},
		{"right", "\x1b[C", true},
		{"left", "\x1b[D", true},
		{"home", "\x1b[H", true},
		{"end", "\x1b[F", true},
		{"pageup", "\x1b[5~", true},
		{"pagedown", "\x1b[6~", true},
		{"delete", "\x1b[3~", true},
		{"space", " ", true},
		{"a", "", false},
		{"x", "", false},
	}
	for _, tc := range tests {
		seq, isSp := kittyKeyEscapeSeq(tc.key)
		if seq != tc.expectSeq || isSp != tc.expectSp {
			t.Errorf("kittyKeyEscapeSeq(%q) = (%q, %v), want (%q, %v)", tc.key, seq, isSp, tc.expectSeq, tc.expectSp)
		}
	}
}

func TestMatchID(t *testing.T) {
	// Zero window ID should return empty string (target current window).
	if got := matchID(0); got != "" {
		t.Errorf("matchID(0) = %q, want empty", got)
	}

	// Positive window ID should return "id:N".
	if got := matchID(42); got != "id:42" {
		t.Errorf("matchID(42) = %q, want %q", got, "id:42")
	}
}
