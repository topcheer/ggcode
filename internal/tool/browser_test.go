package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserToolInterface(t *testing.T) {
	b := NewBrowser()

	if b.Name() != "browser" {
		t.Errorf("Name() = %q, want %q", b.Name(), "browser")
	}

	desc := strings.ToLower(b.Description())
	if !strings.Contains(desc, "devtools protocol") || !strings.Contains(desc, "spa") {
		t.Errorf("Description should mention DevTools Protocol and SPA support, got: %s", desc)
	}

	params := b.Parameters()
	var schema map[string]interface{}
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatalf("Parameters() returned invalid JSON: %v", err)
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema missing properties")
	}

	// Verify all expected properties exist
	requiredProps := []string{"action", "url", "profile", "session", "selector", "text", "expression", "wait_for", "headless", "description"}
	for _, prop := range requiredProps {
		if _, ok := props[prop]; !ok {
			t.Errorf("schema missing property %q", prop)
		}
	}

	// Verify required includes action and description
	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatal("schema missing required array")
	}
	foundAction, foundDesc := false, false
	for _, r := range required {
		if r == "action" {
			foundAction = true
		}
		if r == "description" {
			foundDesc = true
		}
	}
	if !foundAction {
		t.Error("schema should require 'action'")
	}
	if !foundDesc {
		t.Error("schema should require 'description'")
	}
}

func TestBrowserToolUnknownAction(t *testing.T) {
	b := NewBrowser()
	input, _ := json.Marshal(map[string]string{
		"action":      "invalid_action",
		"description": "test",
	})
	result, err := b.Execute(nil, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for unknown action")
	}
	if !strings.Contains(result.Content, "unknown action") {
		t.Errorf("error should mention unknown action, got: %s", result.Content)
	}
}

func TestBrowserFormatJSResult(t *testing.T) {
	tests := []struct {
		input    interface{}
		contains string
	}{
		{nil, "undefined"},
		{"hello", "hello"},
		{float64(42), "42"},
		{float64(3.14), "3.14"},
		{true, "true"},
		{false, "false"},
		{[]interface{}{"a", "b", "c"}, "[a, b, c]"},
	}

	for _, tt := range tests {
		result := formatJSResult(tt.input)
		if !strings.Contains(result, tt.contains) {
			t.Errorf("formatJSResult(%v) = %q, expected to contain %q", tt.input, result, tt.contains)
		}
	}
}

func TestBrowserProfileManagement(t *testing.T) {
	b := NewBrowser()
	if b == nil {
		t.Fatal("NewBrowser returned nil")
	}
	if b.profiles == nil {
		t.Fatal("profiles map not initialized")
	}
}

func TestBrowserCloserInterface(t *testing.T) {
	b := NewBrowser()

	// Browser must implement Closer
	var _ Closer = b

	// Close on a fresh browser (no profiles created) should be safe
	if err := b.Close(); err != nil {
		t.Errorf("Close() on fresh browser should not error: %v", err)
	}

	// Close again (idempotent) should be safe
	if err := b.Close(); err != nil {
		t.Errorf("Close() twice should be safe: %v", err)
	}
}

func TestRegistryCloseAll(t *testing.T) {
	r := NewRegistry()
	browser := NewBrowser()
	if err := r.Register(browser); err != nil {
		t.Fatal(err)
	}

	// Register a non-Closer tool too — CloseAll should skip it
	if err := r.Register(WebFetch{}); err != nil {
		t.Fatal(err)
	}

	// CloseAll should call Close on Browser but skip WebFetch
	errs := r.CloseAll()
	if len(errs) != 0 {
		t.Errorf("CloseAll should not return errors, got: %v", errs)
	}

	// Verify browser profiles were cleared
	browser.mu.Lock()
	cleared := len(browser.profiles) == 0
	browser.mu.Unlock()
	if !cleared {
		t.Error("CloseAll should have cleared browser profiles")
	}
}

func TestBrowserSchemaHasProfileAndSession(t *testing.T) {
	b := NewBrowser()
	var schema map[string]interface{}
	if err := json.Unmarshal(b.Parameters(), &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]interface{})

	// Profile param must exist and mention cookies/sessions
	profileDesc, _ := props["profile"].(map[string]interface{})
	if profileDesc == nil {
		t.Fatal("missing 'profile' property")
	}
	descStr, _ := profileDesc["description"].(string)
	if !strings.Contains(strings.ToLower(descStr), "profile") {
		t.Errorf("profile description should mention profiles, got: %s", descStr)
	}

	// Session param must exist
	if _, ok := props["session"]; !ok {
		t.Error("missing 'session' property")
	}
}

func TestBrowserMissingAction(t *testing.T) {
	b := NewBrowser()
	input, _ := json.Marshal(map[string]string{
		"description": "test without action",
	})
	result, _ := b.Execute(nil, input)
	if !result.IsError {
		t.Error("expected error for missing action")
	}
}
