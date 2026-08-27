package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

const uiLabelSchema = `{
	"type": "object",
	"properties": {
		"pattern": {"type": "string"},
		"description": {"type": "string", "description": "REQUIRED. Brief activity label shown in the UI. You MUST always provide this field."}
	},
	"required": ["pattern", "description"]
}`

// Semantic description (e.g. create_skill): the description IS the payload.
const semanticSchema = `{
	"type": "object",
	"properties": {
		"description": {"type": "string", "description": "One-line description of what this skill does."}
	},
	"required": ["description"]
}`

func TestInjectDescriptionFallback_MissingUILabel(t *testing.T) {
	out := InjectDescriptionFallback(
		json.RawMessage(uiLabelSchema),
		json.RawMessage(`{"pattern": "TODO"}`),
		"grep",
	)
	var m map[string]string
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if m["description"] == "" {
		t.Fatal("expected fallback description injected")
	}
	if !strings.HasPrefix(m["description"], "grep ") {
		t.Errorf("fallback should start with tool name, got %q", m["description"])
	}
	if !strings.Contains(m["description"], "TODO") {
		t.Errorf("fallback should mention primary arg, got %q", m["description"])
	}
	// Injected args must now pass required validation - the rejection loop is broken.
	if msg := ValidateRequiredParams(json.RawMessage(uiLabelSchema), out); msg != "" {
		t.Errorf("after fallback, validation should pass, got: %s", msg)
	}
}

func TestInjectDescriptionFallback_EmptyStringLabel(t *testing.T) {
	out := InjectDescriptionFallback(
		json.RawMessage(uiLabelSchema),
		json.RawMessage(`{"pattern": "x", "description": ""}`),
		"grep",
	)
	var m map[string]string
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if m["description"] == "" {
		t.Fatal("empty description should also get fallback")
	}
}

func TestInjectDescriptionFallback_PresentLeftAlone(t *testing.T) {
	in := json.RawMessage(`{"pattern": "x", "description": "Searching for TODOs"}`)
	out := InjectDescriptionFallback(json.RawMessage(uiLabelSchema), in, "grep")
	if string(out) != string(in) {
		t.Errorf("provided description must not be overwritten, got %s", out)
	}
}

func TestInjectDescriptionFallback_SemanticDescriptionNotInjected(t *testing.T) {
	// create_skill-style: description is user data; missing must still reject.
	in := json.RawMessage(`{"name": "deploy"}`)
	out := InjectDescriptionFallback(json.RawMessage(semanticSchema), in, "create_skill")
	if string(out) != string(in) {
		t.Errorf("semantic description must NOT be auto-filled, got %s", out)
	}
	if msg := ValidateRequiredParams(json.RawMessage(semanticSchema), out); msg == "" {
		t.Fatal("semantic description missing should still be rejected")
	}
}

func TestInjectDescriptionFallback_NotRequiredNoop(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"description":{"type":"string","description":"Optional. Brief activity label."}},"required":["path"]}`)
	in := json.RawMessage(`{"path": "/tmp"}`)
	out := InjectDescriptionFallback(schema, in, "read_file")
	if string(out) != string(in) {
		t.Errorf("optional description must not be injected, got %s", out)
	}
}

func TestDeriveActivityLabel_TruncatesAndCollapses(t *testing.T) {
	long := strings.Repeat("word ", 40)
	args := map[string]json.RawMessage{"command": json.RawMessage(`"` + long + `"`)}
	label := deriveActivityLabel("run_command", args)
	if len(label) > 75 {
		t.Errorf("label should be truncated, got %d chars: %q", len(label), label)
	}
	if strings.Contains(label, "\n") {
		t.Error("label must be single-line")
	}
}
