package tool

import (
	"encoding/json"
	"testing"
)

func TestCoerceArguments_StringToInt(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"offset": {"type": "integer"},
			"limit": {"type": "integer"}
		}
	}`)
	// Model sends strings where integers are expected.
	args := json.RawMessage(`{"offset": "50", "limit": "100"}`)
	out := CoerceArguments(schema, args)

	var result map[string]int
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("coerced output failed to unmarshal: %v (raw: %s)", err, string(out))
	}
	if result["offset"] != 50 {
		t.Errorf("offset: want 50, got %d", result["offset"])
	}
	if result["limit"] != 100 {
		t.Errorf("limit: want 100, got %d", result["limit"])
	}
}

func TestCoerceArguments_AlreadyCorrect(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"integer"}}}`)
	args := json.RawMessage(`{"x": 42}`)
	out := CoerceArguments(schema, args)
	// Should be unchanged — already well-typed.
	if string(out) != string(args) {
		t.Errorf("expected no change, got: %s", string(out))
	}
}

func TestCoerceArguments_FloatStringToInt(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`)
	args := json.RawMessage(`{"n": "42.0"}`)
	out := CoerceArguments(schema, args)

	var result map[string]int
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("failed: %v (raw: %s)", err, string(out))
	}
	if result["n"] != 42 {
		t.Errorf("want 42, got %d", result["n"])
	}
}

func TestCoerceArguments_StringToNumber(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"price":{"type":"number"}}}`)
	args := json.RawMessage(`{"price": "19.99"}`)
	out := CoerceArguments(schema, args)

	var result map[string]float64
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("failed: %v (raw: %s)", err, string(out))
	}
	if result["price"] != 19.99 {
		t.Errorf("want 19.99, got %f", result["price"])
	}
}

func TestCoerceArguments_StringToBoolean(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{`{"flag": "true"}`, true},
		{`{"flag": "false"}`, false},
		{`{"flag": "1"}`, true},
		{`{"flag": "0"}`, false},
		{`{"flag": "yes"}`, true},
		{`{"flag": "no"}`, false},
		{`{"flag": true}`, true},
		{`{"flag": false}`, false},
	}
	schema := json.RawMessage(`{"type":"object","properties":{"flag":{"type":"boolean"}}}`)

	for _, tt := range tests {
		out := CoerceArguments(schema, json.RawMessage(tt.input))
		var result map[string]bool
		if err := json.Unmarshal(out, &result); err != nil {
			t.Errorf("input %s: unmarshal failed: %v (raw: %s)", tt.input, err, string(out))
			continue
		}
		if result["flag"] != tt.want {
			t.Errorf("input %s: want %v, got %v", tt.input, tt.want, result["flag"])
		}
	}
}

func TestCoerceArguments_NonCoercibleLeftAlone(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`)
	args := json.RawMessage(`{"n": "hello"}`)
	out := CoerceArguments(schema, args)
	// "hello" can't become an integer — leave it as-is so the tool's own
	// unmarshal produces a clear error.
	if string(out) != string(args) {
		t.Errorf("expected no change for non-coercible value, got: %s", string(out))
	}
}

func TestCoerceArguments_StringFieldUntouched(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	args := json.RawMessage(`{"path": "/some/file.go"}`)
	out := CoerceArguments(schema, args)
	if string(out) != string(args) {
		t.Errorf("string field should not be coerced, got: %s", string(out))
	}
}

func TestCoerceArguments_MixedTypes(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string"},
			"offset": {"type": "integer"},
			"verbose": {"type": "boolean"},
			"ratio": {"type": "number"}
		}
	}`)
	args := json.RawMessage(`{"path": "/tmp/x", "offset": "10", "verbose": "true", "ratio": "0.5"}`)
	out := CoerceArguments(schema, args)

	var result struct {
		Path    string  `json:"path"`
		Offset  int     `json:"offset"`
		Verbose bool    `json:"verbose"`
		Ratio   float64 `json:"ratio"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("failed: %v (raw: %s)", err, string(out))
	}
	if result.Path != "/tmp/x" {
		t.Errorf("path: want /tmp/x, got %s", result.Path)
	}
	if result.Offset != 10 {
		t.Errorf("offset: want 10, got %d", result.Offset)
	}
	if !result.Verbose {
		t.Errorf("verbose: want true, got %v", result.Verbose)
	}
	if result.Ratio != 0.5 {
		t.Errorf("ratio: want 0.5, got %f", result.Ratio)
	}
}

func TestCoerceArguments_EmptyOrInvalid(t *testing.T) {
	// Empty schema → no-op
	if out := CoerceArguments(nil, json.RawMessage(`{"a":1}`)); string(out) != `{"a":1}` {
		t.Errorf("expected no-op with nil schema")
	}
	// Empty args → no-op
	if out := CoerceArguments(json.RawMessage(`{}`), nil); string(out) != `` {
		t.Errorf("expected no-op with nil args")
	}
	// Invalid JSON args → no-op (return original)
	if out := CoerceArguments(json.RawMessage(`{"properties":{"a":{"type":"integer"}}}`), json.RawMessage(`invalid`)); string(out) != `invalid` {
		t.Errorf("expected no-op with invalid args JSON")
	}
}

func TestCoerceArguments_RealReadFileSchema(t *testing.T) {
	// Use the actual ReadFile schema.
	rf := ReadFile{}
	args := json.RawMessage(`{"path": "/tmp/test.go", "offset": "5", "limit": "20"}`)
	out := CoerceArguments(rf.Parameters(), args)

	var result struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("failed: %v (raw: %s)", err, string(out))
	}
	if result.Offset != 5 {
		t.Errorf("offset: want 5, got %d", result.Offset)
	}
	if result.Limit != 20 {
		t.Errorf("limit: want 20, got %d", result.Limit)
	}
	if result.Path != "/tmp/test.go" {
		t.Errorf("path corrupted: %s", result.Path)
	}
}
