package tool

import (
	"encoding/json"
	"testing"
)

// --- ValidateSchemaConstraints tests ---

func TestValidateSchemaConstraints_EnumValid(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["read", "write", "delete"]}
		},
		"required": ["action"]
	}`)
	args := json.RawMessage(`{"action": "read"}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg != "" {
		t.Errorf("valid enum should pass, got: %s", msg)
	}
}

func TestValidateSchemaConstraints_EnumInvalid(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["read", "write", "delete"]}
		},
		"required": ["action"]
	}`)
	args := json.RawMessage(`{"action": "explode"}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg == "" {
		t.Fatal("invalid enum should fail")
	}
	if !containsStr(msg, "read") || !containsStr(msg, "write") || !containsStr(msg, "delete") {
		t.Errorf("error should list allowed values, got: %s", msg)
	}
	if !containsStr(msg, "explode") {
		t.Errorf("error should mention the invalid value, got: %s", msg)
	}
}

func TestValidateSchemaConstraints_EnumNumeric(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"priority": {"type": "integer", "enum": [1, 2, 3]}
		}
	}`)
	args := json.RawMessage(`{"priority": 5}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg == "" {
		t.Fatal("invalid numeric enum should fail")
	}
}

func TestValidateSchemaConstraints_Minimum(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"offset": {"type": "integer", "minimum": 0}
		}
	}`)
	args := json.RawMessage(`{"offset": -5}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg == "" {
		t.Fatal("value below minimum should fail")
	}
	if !containsStr(msg, "0") {
		t.Errorf("error should mention minimum value, got: %s", msg)
	}
}

func TestValidateSchemaConstraints_Maximum(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"limit": {"type": "integer", "maximum": 100}
		}
	}`)
	args := json.RawMessage(`{"limit": 500}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg == "" {
		t.Fatal("value above maximum should fail")
	}
}

func TestValidateSchemaConstraints_ExclusiveMinimum(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"count": {"type": "integer", "exclusiveMinimum": 0}
		}
	}`)
	args := json.RawMessage(`{"count": 0}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg == "" {
		t.Fatal("value equal to exclusiveMinimum should fail")
	}
}

func TestValidateSchemaConstraints_MinLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "minLength": 3}
		}
	}`)
	args := json.RawMessage(`{"path": "ab"}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg == "" {
		t.Fatal("string shorter than minLength should fail")
	}
}

func TestValidateSchemaConstraints_MaxLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "maxLength": 10}
		}
	}`)
	args := json.RawMessage(`{"name": "this_is_a_very_long_string"}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg == "" {
		t.Fatal("string longer than maxLength should fail")
	}
}

func TestValidateSchemaConstraints_AllValid(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["read", "write"]},
			"offset": {"type": "integer", "minimum": 0, "maximum": 100},
			"query": {"type": "string", "minLength": 1, "maxLength": 1000}
		},
		"required": ["action"]
	}`)
	args := json.RawMessage(`{"action": "read", "offset": 50, "query": "hello"}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg != "" {
		t.Errorf("all valid constraints should pass, got: %s", msg)
	}
}

func TestValidateSchemaConstraints_NoSchema(t *testing.T) {
	msg := ValidateSchemaConstraints(nil, json.RawMessage(`{"a": 1}`))
	if msg != "" {
		t.Errorf("nil schema should return empty, got: %s", msg)
	}
}

func TestValidateSchemaConstraints_NoProperties(t *testing.T) {
	schema := json.RawMessage(`{"type": "object"}`)
	args := json.RawMessage(`{"a": 1}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg != "" {
		t.Errorf("schema without properties should return empty, got: %s", msg)
	}
}

func TestValidateSchemaConstraints_SkipsMissingParams(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["read", "write"]}
		},
		"required": ["action"]
	}`)
	// Missing "action" — ValidateSchemaConstraints should not report it
	// (that's ValidateRequiredParams's job).
	args := json.RawMessage(`{}`)
	msg := ValidateSchemaConstraints(schema, args)
	if msg != "" {
		t.Errorf("missing params should not be reported by constraint validation, got: %s", msg)
	}
}

// --- StripUnknownParams tests ---

func TestStripUnknownParams_RemovesExtras(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string"},
			"pattern": {"type": "string"}
		}
	}`)
	args := json.RawMessage(`{"path": "/tmp/test.go", "pattern": "TODO", "bogus": true, "extra": "remove me"}`)
	out := StripUnknownParams(schema, args)

	var result map[string]json.RawMessage
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if _, exists := result["bogus"]; exists {
		t.Error("bogus should be stripped")
	}
	if _, exists := result["extra"]; exists {
		t.Error("extra should be stripped")
	}
	if _, exists := result["path"]; !exists {
		t.Error("path should be preserved")
	}
	if _, exists := result["pattern"]; !exists {
		t.Error("pattern should be preserved")
	}
}

func TestStripUnknownParams_NoExtras(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string"}
		}
	}`)
	args := json.RawMessage(`{"path": "/tmp/test.go"}`)
	out := StripUnknownParams(schema, args)
	// Should return the same bytes since nothing was stripped
	if string(out) != string(args) {
		t.Errorf("no extras = no change, got: %s", string(out))
	}
}

func TestStripUnknownParams_AdditionalPropertiesTrue(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string"}
		},
		"additionalProperties": true
	}`)
	args := json.RawMessage(`{"path": "/tmp", "custom": 42}`)
	out := StripUnknownParams(schema, args)
	// additionalProperties: true means extras are allowed, should be unchanged
	var result map[string]json.RawMessage
	json.Unmarshal(out, &result)
	if _, exists := result["custom"]; !exists {
		t.Error("additionalProperties: true should preserve unknown params")
	}
}

func TestStripUnknownParams_NilSchema(t *testing.T) {
	args := json.RawMessage(`{"a": 1}`)
	out := StripUnknownParams(nil, args)
	if string(out) != string(args) {
		t.Errorf("nil schema should return input unchanged")
	}
}

func TestStripUnknownParams_EmptyArgs(t *testing.T) {
	schema := json.RawMessage(`{"properties": {"a": {"type": "string"}}}`)
	out := StripUnknownParams(schema, nil)
	if out != nil {
		t.Errorf("nil args should return nil")
	}
}
