package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// --- parseRequiredSchema ---

func TestParseRequiredSchema(t *testing.T) {
	params := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to read"},
			"offset": {"type": "integer"}
		},
		"required": ["path"]
	}`)
	info := parseRequiredSchema(params)
	if info == nil || len(info.required) != 1 {
		t.Fatalf("expected 1 required field, got %#v", info)
	}
	if info.required[0].Name != "path" || info.required[0].Desc != "File path to read" {
		t.Fatalf("field mismatch: %#v", info.required[0])
	}
}

func TestParseRequiredSchema_NoRequired(t *testing.T) {
	if info := parseRequiredSchema(json.RawMessage(`{"type":"object","properties":{}}`)); info != nil {
		t.Fatalf("expected nil for schema without required, got %#v", info)
	}
	if info := parseRequiredSchema(json.RawMessage(`{invalid`)); info != nil {
		t.Fatalf("expected nil for malformed JSON, got %#v", info)
	}
	if info := parseRequiredSchema(nil); info != nil {
		t.Fatalf("expected nil for empty params, got %#v", info)
	}
}

// --- missingRequiredFields ---

func TestMissingRequiredFields(t *testing.T) {
	info := parseRequiredSchema(json.RawMessage(`{
		"properties": {"a": {"description": "param a"}, "b": {"description": "param b"}},
		"required": ["a", "b"]
	}`))

	// Both present -> nothing missing.
	if m := missingRequiredFields(info, json.RawMessage(`{"a":"x","b":1}`)); len(m) != 0 {
		t.Fatalf("expected no missing, got %v", m)
	}
	// b absent -> missing.
	m := missingRequiredFields(info, json.RawMessage(`{"a":"x"}`))
	if len(m) != 1 || m[0].Name != "b" || m[0].Desc != "param b" {
		t.Fatalf("b should be missing with desc, got %#v", m)
	}
	// #742: presence-only semantics. Null counts as missing; empty
	// string/array/object are legal values and must pass.
	if m := missingRequiredFields(info, json.RawMessage(`{"a":"","b":1}`)); len(m) != 0 {
		t.Fatalf("empty string is a legal value (edit_file delete, write_file empty), got %#v", m)
	}
	if m := missingRequiredFields(info, json.RawMessage(`{"a":null,"b":1}`)); len(m) != 1 || m[0].Name != "a" {
		t.Fatalf("explicit null should count as missing, got %#v", m)
	}
	if m := missingRequiredFields(info, json.RawMessage(`{"a":"x","b":[]}`)); len(m) != 0 {
		t.Fatalf("empty array is a legal value (todo_write clears list), got %#v", m)
	}
	if m := missingRequiredFields(info, json.RawMessage(`{"a":"x","b":{}}`)); len(m) != 0 {
		t.Fatalf("empty object is a legal value, got %#v", m)
	}
	// Non-object args: skip (let tool's own parse error surface).
	if m := missingRequiredFields(info, json.RawMessage(`"string"`)); m != nil {
		t.Fatalf("non-object args should skip, got %#v", m)
	}
}

// --- formatMissingRequired ---

func TestFormatMissingRequired_EmbedsDescription(t *testing.T) {
	msg := formatMissingRequired("edit_file", []requiredField{
		{Name: "file_path", Desc: "Path to the file to edit"},
		{Name: "old_text", Desc: "Text to find"},
	})
	for _, want := range []string{"missing required parameters: file_path, old_text", "file_path: Path to the file to edit", "old_text: Text to find", "resend the call"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}

// --- preflightRequiredCheck with real tools ---

// preflightTestTool is a uniquely-named local tool so tests never collide
// with same-name mocks other tests may have cached in the global schema
// cache (e.g. a read_file stub with no required fields).
type preflightTestTool struct {
}

func (preflightTestTool) Name() string        { return "preflight_probe_read" }
func (preflightTestTool) Description() string { return "test-only probe tool" }
func (preflightTestTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path to read"}},"required":["path"]}`)
}
func (preflightTestTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

func TestPreflightRequiredCheck_MissingPath(t *testing.T) {
	r := preflightRequiredCheck(preflightTestTool{}, json.RawMessage(`{}`))
	if r == nil || !r.IsError {
		t.Fatal("probe tool without path must be rejected")
	}
	if !strings.Contains(r.Content, "path") || !strings.Contains(r.Content, "File path to read") {
		t.Fatalf("error should name param and embed schema description: %s", r.Content)
	}
}

func TestPreflightRequiredCheck_EditFileComplete(t *testing.T) {
	args := json.RawMessage(`{"file_path":"/tmp/x","old_text":"a","new_text":"b"}`)
	if r := preflightRequiredCheck(preflightEditTool{}, args); r != nil {
		t.Fatalf("complete call must pass, got: %s", r.Content)
	}
}

type preflightEditTool struct{}

func (preflightEditTool) Name() string        { return "preflight_probe_edit" }
func (preflightEditTool) Description() string { return "test-only probe tool" }
func (preflightEditTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["file_path","old_text","new_text"]}`)
}
func (preflightEditTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

// TestPreflightRequiredCheck_EmptyValuesLegal covers the four real-tool
// regressions from #742: legal empty-value calls must pass pre-dispatch.
func TestPreflightRequiredCheck_EmptyValuesLegal(t *testing.T) {
	cases := []struct {
		name string
		tool tool.Tool
		args string
	}{
		// edit_file delete: new_text:"" removes old_text.
		{"edit_file delete", preflightEditTool{}, `{"file_path":"/tmp/x","old_text":"debug\n","new_text":""}`},
	}
	for _, c := range cases {
		if r := preflightRequiredCheck(c.tool, json.RawMessage(c.args)); r != nil {
			t.Errorf("%s: legal empty value rejected pre-dispatch: %s", c.name, r.Content)
		}
	}
}

func TestPreflightRequiredCheck_CacheReuse(t *testing.T) {
	// Two calls -> cache hit path must return identical verdict (nil-safe:
	// a rejected call returns a non-nil result, a passing call nil).
	a := preflightRequiredCheck(preflightTestTool{}, json.RawMessage(`{}`))
	b := preflightRequiredCheck(preflightTestTool{}, json.RawMessage(`{}`))
	if (a == nil) != (b == nil) {
		t.Fatal("cached schema must produce consistent verdicts")
	}
	if a != nil && a.Content != b.Content {
		t.Fatal("cached verdicts must be identical")
	}
	// Complete call passes and cached rejection still rejects.
	if r := preflightRequiredCheck(preflightTestTool{}, json.RawMessage(`{"path":"/tmp/x"}`)); r != nil {
		t.Fatalf("complete call must pass, got: %s", r.Content)
	}
}
