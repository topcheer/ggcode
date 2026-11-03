package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue4054UnknownAlphaKeyName(t *testing.T) {
	names := []string{"backspace", "Delete", "forwardDelete", "whatever"}
	for _, k := range names {
		if !unknownAlphaKeyName(strings.ToLower(k)) {
			t.Errorf("unknownAlphaKeyName(%q) = false, want true (looks like a key name)", k)
		}
	}

	literals := []string{"a", "1", "123", "a1", "f13", "ctrl+3", "§", "", "sp ace", "f-13"}
	for _, k := range literals {
		if unknownAlphaKeyName(k) {
			t.Errorf("unknownAlphaKeyName(%q) = true, want false (eligible for literal typing)", k)
		}
	}
}

func TestIssue4055SchemaDocumentsDeleteSemantics(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(DesktopControlTool{}.Parameters(), &schema); err != nil {
		t.Fatalf("unmarshal desktop_control parameters: %v", err)
	}
	textDesc := schema.Properties["text"].Description
	if textDesc == "" {
		t.Fatal("text param description missing from schema")
	}
	for _, want := range []string{"'delete' is backspace on macOS", "forward-delete"} {
		if !strings.Contains(textDesc, want) {
			t.Errorf("desktop_control text param description must document %q, got: %s", want, textDesc)
		}
	}
}
