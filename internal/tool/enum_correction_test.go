package tool

import (
	"encoding/json"
	"testing"
)

func TestCoerceEnumValues_CaseInsensitiveMatch(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"format": {"type": "string", "enum": ["json", "yaml", "text"]}
		}
	}`)
	args := json.RawMessage(`{"format": "JSON"}`)
	result := CoerceEnumValues(schema, args)

	var m map[string]string
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if m["format"] != "json" {
		t.Errorf("expected 'json', got %q", m["format"])
	}
}

func TestCoerceEnumValues_TypoCorrection(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["read", "write", "delete"]}
		}
	}`)
	args := json.RawMessage(`{"action": "wite"}`)
	result := CoerceEnumValues(schema, args)

	var m map[string]string
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if m["action"] != "write" {
		t.Errorf("expected 'write', got %q", m["action"])
	}
}

func TestCoerceEnumValues_AlreadyValid(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"format": {"type": "string", "enum": ["json", "yaml"]}
		}
	}`)
	args := json.RawMessage(`{"format": "json"}`)
	result := CoerceEnumValues(schema, args)
	if string(result) != string(args) {
		t.Errorf("valid value should be unchanged, got: %s", string(result))
	}
}

func TestCoerceEnumValues_AmbiguousNoChange(t *testing.T) {

	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"color": {"type": "string", "enum": ["red", "read"]}
		}
	}`)
	args := json.RawMessage(`{"color": "rad"}`)
	result := CoerceEnumValues(schema, args)
	var m map[string]string
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if m["color"] != "rad" {
		t.Errorf("ambiguous value should not be auto-corrected, got %q", m["color"])
	}
}

func TestCoerceEnumValues_NoMatch(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"format": {"type": "string", "enum": ["json", "yaml"]}
		}
	}`)
	args := json.RawMessage(`{"format": "binary"}`)
	result := CoerceEnumValues(schema, args)

	if string(result) != string(args) {
		t.Errorf("no close match should leave value unchanged, got: %s", string(result))
	}
}

func TestCoerceEnumValues_MultipleFields(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"format": {"type": "string", "enum": ["json", "yaml"]},
			"mode": {"type": "string", "enum": ["strict", "loose"]}
		}
	}`)
	args := json.RawMessage(`{"format": "JSON", "mode": "STRICT"}`)
	result := CoerceEnumValues(schema, args)

	var m map[string]string
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if m["format"] != "json" {
		t.Errorf("expected format 'json', got %q", m["format"])
	}
	if m["mode"] != "strict" {
		t.Errorf("expected mode 'strict', got %q", m["mode"])
	}
}

func TestCoerceEnumValues_NoEnumFields(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string"}
		}
	}`)
	args := json.RawMessage(`{"path": "/some/path"}`)
	result := CoerceEnumValues(schema, args)
	if string(result) != string(args) {
		t.Errorf("non-enum fields should be unchanged")
	}
}

func TestCoerceEnumValues_EmptyArgs(t *testing.T) {
	schema := json.RawMessage(`{"type": "object", "properties": {}}`)
	result := CoerceEnumValues(schema, nil)
	if result != nil {
		t.Errorf("nil args should return nil")
	}
}

func TestCoerceEnumValues_IntegerEnumNotChanged(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"priority": {"type": "integer", "enum": [1, 2, 3]}
		}
	}`)
	args := json.RawMessage(`{"priority": 5}`)
	result := CoerceEnumValues(schema, args)

	if string(result) != string(args) {
		t.Errorf("integer enum should not be corrected, got: %s", string(result))
	}
}

func TestSuggestClosestEnum_TypoSuggestion(t *testing.T) {
	enum := []json.RawMessage{json.RawMessage(`"read"`), json.RawMessage(`"write"`), json.RawMessage(`"delete"`)}
	suggestion := suggestClosestEnum("reed", enum)
	if suggestion != "read" {
		t.Errorf("expected 'read', got %q", suggestion)
	}
}

func TestSuggestClosestEnum_NoCloseMatch(t *testing.T) {
	enum := []json.RawMessage{json.RawMessage(`"read"`), json.RawMessage(`"write"`)}
	suggestion := suggestClosestEnum("xyz", enum)
	if suggestion != "" {
		t.Errorf("expected empty suggestion for no close match, got %q", suggestion)
	}
}

func TestLevenshteinForEnum(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"wite", "write", 1},
		{"JSON", "json", 4},
		{"json", "json", 0},
	}
	for _, tc := range tests {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestValidateSchemaConstraints_EnumSuggestion(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["read", "write", "delete"]}
		}
	}`)
	args := json.RawMessage(`{"action": "reed"}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg == "" {
		t.Fatal("invalid enum should fail")
	}
	if !containsStr(msg, "read") {
		t.Errorf("error should mention 'read' as valid option, got: %s", msg)
	}
	if !containsStr(msg, "reed") {
		t.Errorf("error should mention the invalid value, got: %s", msg)
	}

	if !containsStr(msg, "Did you mean") {
		t.Errorf("error should include suggestion for close match, got: %s", msg)
	}
}

func TestValidateSchemaConstraints_EnumNoSuggestion(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["read", "write", "delete"]}
		}
	}`)
	args := json.RawMessage(`{"action": "explode"}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg == "" {
		t.Fatal("invalid enum should fail")
	}

	if containsStr(msg, "Did you mean") {
		t.Errorf("should not suggest for distant match, got: %s", msg)
	}
}
