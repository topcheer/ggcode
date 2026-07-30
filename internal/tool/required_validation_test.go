package tool

import (
	"encoding/json"
	"testing"
)

func TestValidateRequiredParams_AllPresent(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"path": {"type": "string"}, "pattern": {"type": "string"}},
		"required": ["path", "pattern"]
	}`)
	args := json.RawMessage(`{"path": "/tmp/test.go", "pattern": "TODO"}`)
	msg := ValidateRequiredParams(schema, args)
	if msg != "" {
		t.Errorf("expected no missing params, got: %s", msg)
	}
}

func TestValidateRequiredParams_OneMissing(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"path": {"type": "string"}, "pattern": {"type": "string"}},
		"required": ["path", "pattern"]
	}`)
	args := json.RawMessage(`{"path": "/tmp/test.go"}`)
	msg := ValidateRequiredParams(schema, args)
	if msg == "" {
		t.Fatal("expected missing param error, got empty")
	}
	if want := "missing required parameter: pattern"; msg != want {
		t.Errorf("expected %q, got %q", want, msg)
	}
}

func TestValidateRequiredParams_MultipleMissing(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"path": {"type": "string"}, "pattern": {"type": "string"}, "description": {"type": "string"}},
		"required": ["path", "pattern", "description"]
	}`)
	args := json.RawMessage(`{"path": "/tmp"}`)
	msg := ValidateRequiredParams(schema, args)
	if msg == "" {
		t.Fatal("expected missing params error, got empty")
	}
	// pattern and description should both be listed
	for _, s := range []string{"pattern", "description"} {
		if !containsStr(msg, s) {
			t.Errorf("expected %q in message, got: %s", s, msg)
		}
	}
}

func TestValidateRequiredParams_EmptyString(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"path": {"type": "string"}},
		"required": ["path"]
	}`)
	args := json.RawMessage(`{"path": ""}`)
	msg := ValidateRequiredParams(schema, args)
	if msg == "" {
		t.Fatal("expected missing param error for empty string")
	}
}

func TestValidateRequiredParams_WhitespaceString(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"path": {"type": "string"}},
		"required": ["path"]
	}`)
	// A whitespace-only string value is technically present but likely useless.
	// However, we treat it as present since the JSON value is not empty/null.
	// Tools that care should use CheckRequired which trims whitespace.
	args := json.RawMessage(`{"path": "  "}`)
	msg := ValidateRequiredParams(schema, args)
	if msg != "" {
		t.Errorf("whitespace string should be treated as present (not empty), got: %s", msg)
	}
}

func TestValidateRequiredParams_NullValue(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"path": {"type": "string"}},
		"required": ["path"]
	}`)
	args := json.RawMessage(`{"path": null}`)
	msg := ValidateRequiredParams(schema, args)
	if msg == "" {
		t.Fatal("expected missing param error for null value")
	}
}

func TestValidateRequiredParams_NumberZero(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"offset": {"type": "integer"}},
		"required": ["offset"]
	}`)
	// 0 is a valid value — should NOT be treated as missing
	args := json.RawMessage(`{"offset": 0}`)
	msg := ValidateRequiredParams(schema, args)
	if msg != "" {
		t.Errorf("0 should not be treated as missing, got: %s", msg)
	}
}

func TestValidateRequiredParams_BooleanFalse(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"headless": {"type": "boolean"}},
		"required": ["headless"]
	}`)
	// false is a valid value — should NOT be treated as missing
	args := json.RawMessage(`{"headless": false}`)
	msg := ValidateRequiredParams(schema, args)
	if msg != "" {
		t.Errorf("false should not be treated as missing, got: %s", msg)
	}
}

func TestValidateRequiredParams_EmptyArray(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"files": {"type": "array"}},
		"required": ["files"]
	}`)
	args := json.RawMessage(`{"files": []}`)
	msg := ValidateRequiredParams(schema, args)
	if msg == "" {
		t.Fatal("expected missing param error for empty array")
	}
}

func TestValidateRequiredParams_NonEmptyArray(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"files": {"type": "array"}},
		"required": ["files"]
	}`)
	args := json.RawMessage(`{"files": ["a.go"]}`)
	msg := ValidateRequiredParams(schema, args)
	if msg != "" {
		t.Errorf("non-empty array should not be missing, got: %s", msg)
	}
}

func TestValidateRequiredParams_NoRequiredFields(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"path": {"type": "string"}}
	}`)
	args := json.RawMessage(`{}`)
	msg := ValidateRequiredParams(schema, args)
	if msg != "" {
		t.Errorf("no required fields = no error, got: %s", msg)
	}
}

func TestValidateRequiredParams_NoSchema(t *testing.T) {
	args := json.RawMessage(`{"path": "/tmp"}`)
	msg := ValidateRequiredParams(nil, args)
	if msg != "" {
		t.Errorf("nil schema = no error, got: %s", msg)
	}
}

func TestValidateRequiredParams_UnparseableSchema(t *testing.T) {
	schema := json.RawMessage(`{invalid json}`)
	args := json.RawMessage(`{"path": "/tmp"}`)
	msg := ValidateRequiredParams(schema, args)
	if msg != "" {
		t.Errorf("unparseable schema should silently pass, got: %s", msg)
	}
}

func TestValidateRequiredParams_UnparseableArgs(t *testing.T) {
	schema := json.RawMessage(`{"required": ["path"]}`)
	args := json.RawMessage(`{invalid}`)
	msg := ValidateRequiredParams(schema, args)
	if msg != "" {
		t.Errorf("unparseable args should silently pass (let tool handle), got: %s", msg)
	}
}

func TestIsEmptyValue(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{`null`, true},
		{`""`, true},
		{`[]`, true},
		{`{}`, true},
		{`""`, true},
		{`"hello"`, false},
		{`42`, false},
		{`0`, false},
		{`false`, false},
		{`true`, false},
		{`[1,2]`, false},
		{`{"a":1}`, false},
	}
	for _, tt := range tests {
		got := isEmptyValue(json.RawMessage(tt.input))
		if got != tt.want {
			t.Errorf("isEmptyValue(%s) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
