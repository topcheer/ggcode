package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClipboardTool_NameAndSchema(t *testing.T) {
	tool := ClipboardTool{}
	if tool.Name() != "clipboard" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "clipboard")
	}
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
	params := tool.Parameters()
	var schema map[string]any
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatalf("Parameters() is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
}

func TestClipboardTool_InvalidAction(t *testing.T) {
	tool := ClipboardTool{}
	input, _ := json.Marshal(map[string]string{
		"action":      "delete",
		"description": "test",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for invalid action")
	}
	if !strings.Contains(res.Content, "unknown action") {
		t.Errorf("error should mention unknown action, got: %s", res.Content)
	}
}

func TestClipboardTool_WriteMissingText(t *testing.T) {
	tool := ClipboardTool{}
	input, _ := json.Marshal(map[string]string{
		"action":      "write",
		"description": "test",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for write without text")
	}
	if !strings.Contains(res.Content, "requires non-empty") {
		t.Errorf("error should mention missing text, got: %s", res.Content)
	}
}

func TestClipboardTool_WriteTooLarge(t *testing.T) {
	tool := ClipboardTool{}
	input, _ := json.Marshal(map[string]string{
		"action":      "write",
		"text":        strings.Repeat("x", clipboardMaxChars+1),
		"description": "test",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for oversized text")
	}
	if !strings.Contains(res.Content, "exceeds maximum") {
		t.Errorf("error should mention size limit, got: %s", res.Content)
	}
}

func TestClipboardTool_InvalidJSON(t *testing.T) {
	tool := ClipboardTool{}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{bad json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("expected error for invalid JSON")
	}
}

// TestClipboardTool_ReadWriteRoundTrip exercises the real clipboard if a
// utility is available, otherwise skips gracefully.
func TestClipboardTool_ReadWriteRoundTrip(t *testing.T) {
	if !ClipboardAvailable() {
		t.Skip("no clipboard utility available on this platform")
	}
	tool := ClipboardTool{}
	ctx := context.Background()

	text := "ggcode clipboard test 12345"
	writeInput, _ := json.Marshal(map[string]string{
		"action":      "write",
		"text":        text,
		"description": "writing test text",
	})
	res, err := tool.Execute(ctx, writeInput)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("write returned error: %s", res.Content)
	}

	readInput, _ := json.Marshal(map[string]string{
		"action":      "read",
		"description": "reading test text",
	})
	res, err = tool.Execute(ctx, readInput)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("read returned error: %s", res.Content)
	}
	if !strings.Contains(res.Content, text) {
		t.Errorf("clipboard read did not contain written text %q, got: %s", text, res.Content)
	}
}

func TestClipboardReadCmd_NoPanic(t *testing.T) {
	// Should not panic on any platform.
	ctx := context.Background()
	_, _ = clipboardReadCmd(ctx)
	_, _ = clipboardWriteCmd(ctx)
}
