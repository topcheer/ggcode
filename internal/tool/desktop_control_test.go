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

// TestDesktopControlSchemaMinimumConstraints pins #1662 case 2 in #1665:
// negative scroll amounts silently reversed direction (sign flip), negative
// max_depth/timeout_ms were garbage-in. The schema now declares minimum on
// amount/max_depth/timeout_ms; ValidateSchemaConstraints enforces them.
func TestDesktopControlSchemaMinimumConstraints(t *testing.T) {
	tool := DesktopControlTool{}
	for _, tc := range []struct {
		name string
		args string
	}{
		{"negative scroll amount", `{"action": "scroll", "amount": -5}`},
		{"zero scroll amount", `{"action": "scroll", "amount": 0}`},
		{"negative max_depth", `{"action": "snapshot_ui", "max_depth": -1}`},
		{"negative timeout_ms", `{"action": "wait_and_click", "timeout_ms": -100, "text": "Go"}`},
	} {
		if msg := ValidateSchemaConstraints(tool.Parameters(), json.RawMessage(tc.args)); msg == "" {
			t.Errorf("%s: expected schema rejection, got none", tc.name)
		}
	}
	// Valid values still pass.
	if msg := ValidateSchemaConstraints(tool.Parameters(), json.RawMessage(`{"action": "scroll", "amount": 3}`)); msg != "" {
		t.Errorf("valid scroll rejected: %s", msg)
	}
}
