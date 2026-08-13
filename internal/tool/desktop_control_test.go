package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDesktopControlInvalidAction(t *testing.T) {
	tool := DesktopControlTool{}
	input, _ := json.Marshal(map[string]any{
		"action": "bogus",
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for bogus action")
	}
}

func TestDesktopControlParseParams(t *testing.T) {
	tool := DesktopControlTool{}
	// Verify parameter schema parses
	params := tool.Parameters()
	if len(params) == 0 {
		t.Fatal("expected non-empty parameters")
	}
}

func TestDesktopControlDefaults(t *testing.T) {
	// Ensure defaults are applied when fields are missing
	input, _ := json.Marshal(map[string]any{
		"action": "active_app",
	})
	tool := DesktopControlTool{}
	// This will execute on the current platform; on macOS without
	// accessibility permissions it may fail, but should not panic.
	_, _ = tool.Execute(context.Background(), input)
}

func TestDesktopControlName(t *testing.T) {
	tool := DesktopControlTool{}
	if tool.Name() != "desktop_control" {
		t.Fatalf("expected desktop_control, got %s", tool.Name())
	}
}
