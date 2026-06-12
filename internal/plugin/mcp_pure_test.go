package plugin

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestFirstNonEmpty_Plugin(t *testing.T) {
	got := firstNonEmpty("", "  ", "hello")
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	if firstNonEmpty("", "  ") != "" {
		t.Error("expected empty")
	}
}

func TestNormalizeMCPError(t *testing.T) {
	if normalizeMCPError(nil) != "" {
		t.Error("expected empty for nil")
	}
	got := normalizeMCPError(errors.New("test error"))
	if got != "test error" {
		t.Errorf("expected 'test error', got %q", got)
	}
}

func TestDecorateContextError(t *testing.T) {
	got := decorateContextError("timed out", nil, "")
	if got != "timed out" {
		t.Errorf("expected 'timed out', got %q", got)
	}
	got = decorateContextError("timed out", errors.New("context deadline exceeded"), "context deadline exceeded")
	if got != "timed out" {
		t.Errorf("expected 'timed out', got %q", got)
	}
}

func TestExtractPromptText(t *testing.T) {
	// Single text
	got := extractPromptText(json.RawMessage(`{"type":"text","text":"hello"}`))
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	// List
	got = extractPromptText(json.RawMessage(`[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]`))
	if got != "line1\nline2" {
		t.Errorf("expected 'line1\\nline2', got %q", got)
	}
	// Invalid
	got = extractPromptText(json.RawMessage(`invalid`))
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	// Empty
	got = extractPromptText(nil)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestCompactJSON(t *testing.T) {
	got := compactJSON(json.RawMessage(`{"key": "value"}`))
	if got != `{"key":"value"}` {
		t.Errorf("expected compacted, got %q", got)
	}
	if compactJSON(nil) != "" {
		t.Error("expected empty for nil")
	}
	if compactJSON(json.RawMessage(`invalid`)) != "invalid" {
		t.Error("expected passthrough for invalid json")
	}
}

func TestIsOptionalCapabilityUnavailable(t *testing.T) {
	if isOptionalCapabilityUnavailable(nil) {
		t.Error("expected false for nil")
	}
	if isOptionalCapabilityUnavailable(errors.New("normal error")) {
		t.Error("expected false for normal error")
	}
}

func TestMCPOAuthRequiredError(t *testing.T) {
	err := &MCPOAuthRequiredError{ServerName: "test"}
	if err.Error() == "" {
		t.Error("expected non-empty error")
	}
}

func TestListPromptNames_Nil(t *testing.T) {
	got := listPromptNames(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestListResourceNames_Nil(t *testing.T) {
	got := listResourceNames(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}
