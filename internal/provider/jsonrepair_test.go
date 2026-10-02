package provider

import (
	"encoding/json"
	"testing"
)

func TestRepairJSON_AlreadyValid(t *testing.T) {
	input := json.RawMessage(`{"name":"read_file","arguments":{"path":"/tmp/test.go"}}`)
	result, repaired := RepairJSON(input)
	if repaired {
		t.Errorf("expected repaired=false for valid JSON")
	}
	// Result should be the trimmed input.
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("result should be valid JSON: %v", err)
	}
}

func TestRepairJSON_Empty(t *testing.T) {
	result, repaired := RepairJSON([]byte(""))
	if repaired {
		t.Errorf("expected repaired=false for empty input")
	}
	result, repaired = RepairJSON([]byte("   "))
	if repaired {
		t.Errorf("expected repaired=false for whitespace-only input")
	}
	_ = result
}

func TestRepairJSON_TruncatedMissingClosingBrace(t *testing.T) {
	// Stream truncated mid-object: missing closing }
	input := `{"name":"edit_file","arguments":{"file_path":"/tmp/test.go","old_text":"foo"`
	result, repaired := RepairJSON([]byte(input))
	if !repaired {
		t.Fatalf("expected repair to succeed for truncated JSON")
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("repaired result should be valid JSON: %v\nresult: %s", err, string(result))
	}
	args, ok := m["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("expected arguments to be an object, got %T", m["arguments"])
	}
	if args["file_path"] != "/tmp/test.go" {
		t.Errorf("expected file_path=/tmp/test.go, got %v", args["file_path"])
	}
}

func TestRepairJSON_TruncatedMissingNestedClose(t *testing.T) {
	// Missing both nested } and outer }
	input := `{"name":"multi_edit_file","arguments":{"file_path":"/tmp/test.go","edits":[{"old_text":"a"`
	result, repaired := RepairJSON([]byte(input))
	if !repaired {
		t.Fatalf("expected repair to succeed for deeply truncated JSON")
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("repaired result should be valid JSON: %v\nresult: %s", err, string(result))
	}
}

func TestRepairJSON_TrailingComma(t *testing.T) {
	inputs := []string{
		`{"name":"read_file","arguments":{"path":"/tmp/test.go",}}`,
		`{"items":["a","b","c",]}`,
		`{"a":1,"b":2,}`,
	}
	for _, input := range inputs {
		result, repaired := RepairJSON([]byte(input))
		if !repaired {
			t.Errorf("expected repair for trailing comma: %s", input)
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(result, &m); err != nil {
			t.Errorf("repaired result should be valid JSON: %v (input: %s)", err, input)
		}
	}
}

func TestRepairJSON_CodeFence(t *testing.T) {
	input := "```json\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"/tmp/test.go\"}}\n```"
	result, repaired := RepairJSON([]byte(input))
	if !repaired {
		t.Fatalf("expected repair to succeed for code-fenced JSON")
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("repaired result should be valid JSON: %v\nresult: %s", err, string(result))
	}
}

func TestRepairJSON_CodeFenceNoLang(t *testing.T) {
	input := "```\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"/tmp/test.go\"}}\n```"
	result, repaired := RepairJSON([]byte(input))
	if !repaired {
		t.Fatalf("expected repair to succeed for code-fenced JSON without language")
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("repaired result should be valid JSON: %v", err)
	}
}

func TestRepairJSON_SurroundingProse(t *testing.T) {
	input := `Here is the tool call: {"name":"read_file","arguments":{"path":"/tmp/test.go"}} Hope it works!`
	result, repaired := RepairJSON([]byte(input))
	if !repaired {
		t.Fatalf("expected repair to succeed for JSON surrounded by prose")
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("repaired result should be valid JSON: %v\nresult: %s", err, string(result))
	}
	if m["name"] != "read_file" {
		t.Errorf("expected name=read_file, got %v", m["name"])
	}
}

func TestRepairJSON_SmartQuotes(t *testing.T) {
	// Smart/curly quotes instead of straight quotes
	input := "{\u201cname\u201d:\u201cread_file\u201d}"
	result, repaired := RepairJSON([]byte(input))
	if !repaired {
		t.Fatalf("expected repair to succeed for smart quotes")
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("repaired result should be valid JSON: %v\nresult: %s", err, string(result))
	}
	if m["name"] != "read_file" {
		t.Errorf("expected name=read_file, got %v", m["name"])
	}
}

func TestRepairJSON_TruncatedStringRefused(t *testing.T) {
	// #3069: stream truncated inside a string value. The old behavior
	// closed the quote and "succeeded", producing a valid-but-mutilated
	// old_text that edit_file would silently run on. Repair must now be
	// refused so the caller retries instead of executing partial args.
	input := `{"name":"edit_file","arguments":{"file_path":"/tmp/test.go","old_text":"func main() {`
	result, repaired := RepairJSON([]byte(input))
	if repaired {
		t.Fatalf("expected repair to be REFUSED for string-mid truncation, got: %s", string(result))
	}
}

func TestRepairJSON_TrailingCommaAndTruncation(t *testing.T) {
	// Both trailing comma and missing closing braces
	input := `{"name":"multi_file_edit","arguments":{"file_path":"/tmp/test.go","edits":[{"old_text":"a","new_text":"b",`
	result, repaired := RepairJSON([]byte(input))
	if !repaired {
		t.Fatalf("expected repair to succeed for trailing comma + truncation")
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("repaired result should be valid JSON: %v\nresult: %s", err, string(result))
	}
}

func TestRepairJSON_Unrepairable(t *testing.T) {
	// Completely garbled input — should return false
	inputs := []string{
		"this is just text with no json at all",
		"<<<>>>",
	}
	for _, input := range inputs {
		_, repaired := RepairJSON([]byte(input))
		if repaired {
			t.Errorf("expected repair to fail for: %s", input)
		}
	}
}

func TestRepairJSON_RealWorldTruncatedArgsRefused(t *testing.T) {
	// #3069 real-world example: vLLM streaming truncation mid new_text.
	// The old repair closed the string and returned a valid object whose
	// new_text was silently cut in half — edit_file would apply a partial
	// edit. Refusal forces the provider loop to regenerate the call.
	input := `{"file_path":"/Volumes/new/ggai/ggcode/internal/agent/agent.go","old_text":"func (a *Agent) runIteration() {","new_text":"func (a *Agent) runIteration(ctx context.Context) {`
	result, repaired := RepairJSON([]byte(input))
	if repaired {
		t.Fatalf("expected repair to be REFUSED for mid-value truncation, got: %s", string(result))
	}
}

func TestRepairJSON_ArrayWithTruncation(t *testing.T) {
	// Truncated JSON array inside an object
	input := `{"edits":[{"old_text":"a","new_text":"b"},{"old_text":"c"`
	result, repaired := RepairJSON([]byte(input))
	if !repaired {
		t.Fatalf("expected repair to succeed for truncated array")
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("repaired result should be valid JSON: %v\nresult: %s", err, string(result))
	}
}

func TestRepairJSON_PreservesEscapedQuotes(t *testing.T) {
	// JSON with escaped quotes inside string values should not be corrupted
	input := `{"name":"edit_file","arguments":{"old_text":"he said \"hello\""}}`
	result, repaired := RepairJSON([]byte(input))
	if repaired {
		t.Fatalf("expected no repair needed for valid JSON with escaped quotes")
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("result should be valid JSON: %v", err)
	}
}

func TestStripCodeFences(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"```json\n{}\n```", "{}"},
		{"```\n{}\n```", "{}"},
		{"{}", "{}"},
		{"```json\n{\"a\":1}\n```", "{\"a\":1}"},
	}
	for _, tt := range tests {
		got := stripCodeFences(tt.input)
		if got != tt.want {
			t.Errorf("stripCodeFences(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCloseUnclosed_BalancedInput(t *testing.T) {
	// Already balanced — should be unchanged
	input := `{"a":[1,2,3]}`
	got, ok := closeUnclosed(input)
	if !ok || got != input {
		t.Errorf("closeUnclosed on balanced input should be no-op, got: %s (ok=%v)", got, ok)
	}
}

func TestCloseUnclosed_MissingOneBrace(t *testing.T) {
	input := `{"a":1`
	got, ok := closeUnclosed(input)
	if !ok || got != `{"a":1}` {
		t.Errorf("closeUnclosed({\"a\":1) = %s (ok=%v), want {\"a\":1}", got, ok)
	}
}

func TestCloseUnclosed_MissingMultiple(t *testing.T) {
	input := `{"a":[{"b":1`
	got, ok := closeUnclosed(input)
	if !ok {
		t.Fatalf("balanced-tail truncation should be closable, got: %s", got)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Errorf("closeUnclosed result should be valid JSON: %v (got: %s)", err, got)
	}
}

func TestCloseUnclosed_MidStringRefused(t *testing.T) {
	// #3069: ending inside a string is unrepairable: closing the quote
	// would fabricate a truncated-but-valid value.
	for _, input := range []string{
		`{"old_text":"func main() {`,
		`{"a":"b","key":"partial value with`,
	} {
		got, ok := closeUnclosed(input)
		if ok {
			t.Errorf("closeUnclosed(%q) should refuse mid-string input, got: %s", input, got)
		}
	}
}

func TestNormalizeQuotes(t *testing.T) {
	input := "\u201chello\u201d" // "hello"
	got := normalizeQuotes(input)
	if got != `"hello"` {
		t.Errorf("normalizeQuotes = %q, want %q", got, `"hello"`)
	}
}
