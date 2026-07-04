package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestScreenshotToolName(t *testing.T) {
	tool := ScreenshotTool{}
	if tool.Name() != "screenshot" {
		t.Fatalf("expected name 'screenshot', got %q", tool.Name())
	}
}

func TestScreenshotToolDescription(t *testing.T) {
	tool := ScreenshotTool{}
	desc := tool.Description()
	if desc == "" {
		t.Fatal("description should not be empty")
	}
	// Must mention key capabilities.
	for _, want := range []string{"screenshot", "window", "display"} {
		if !containsStr(desc, want) {
			t.Errorf("description missing %q", want)
		}
	}
}

// Use existing containsStr from run_command_test.go

func TestScreenshotToolParameters(t *testing.T) {
	tool := ScreenshotTool{}
	params := tool.Parameters()
	if len(params) == 0 {
		t.Fatal("parameters should not be empty")
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatalf("parameters is not valid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("missing properties")
	}
	for _, required := range []string{"action", "window", "display", "region", "cursor", "delay_ms", "format", "quality", "output_path", "max_width"} {
		if _, ok := props[required]; !ok {
			t.Errorf("missing parameter %q", required)
		}
	}
}

func TestScreenshotToolInvalidAction(t *testing.T) {
	tool := ScreenshotTool{}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"frobnicate"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid action")
	}
}

func TestScreenshotToolInvalidParams(t *testing.T) {
	tool := ScreenshotTool{}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{invalid json`))
	if !result.IsError {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestScreenshotToolNilSafeExecute(t *testing.T) {
	tool := ScreenshotTool{}
	// Empty input should default to action=capture and either succeed (real screen)
	// or return an error result (headless CI). Either way, no panic.
	_, _ = tool.Execute(context.Background(), json.RawMessage(`{}`))
}
