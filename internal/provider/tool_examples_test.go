package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func sampleExamples() []map[string]any {
	return []map[string]any{
		{"title": "Login page returns 500 error", "priority": "critical", "due_date": "2026-01-15"},
		{"title": "Add dark mode", "labels": []string{"feature"}},
		{"title": "Update docs"},
	}
}

func TestExamplesDescriptionSuffixRendersCappedExamples(t *testing.T) {
	suffix := ExamplesDescriptionSuffix(sampleExamples())
	if suffix == "" {
		t.Fatal("expected non-empty suffix")
	}
	if !strings.Contains(suffix, "Example usage:") {
		t.Fatalf("missing header: %q", suffix)
	}
	// Capped at toolExamplesMaxRendered: the third example must not appear.
	if strings.Contains(suffix, "Update docs") {
		t.Fatalf("third example should be capped, got: %q", suffix)
	}
	if !strings.Contains(suffix, "2026-01-15") || !strings.Contains(suffix, "dark mode") {
		t.Fatalf("first two examples lost: %q", suffix)
	}
}

func TestExamplesDescriptionSuffixEmptyIsByteIdentical(t *testing.T) {
	if got := ExamplesDescriptionSuffix(nil); got != "" {
		t.Fatalf("nil examples should render empty, got %q", got)
	}
	if got := ExamplesDescriptionSuffix([]map[string]any{}); got != "" {
		t.Fatalf("empty examples should render empty, got %q", got)
	}
}

func TestExamplesDescriptionSuffixBudgetDropsWholeExample(t *testing.T) {
	huge := map[string]any{"blob": strings.Repeat("x", toolExamplesRenderBudget+100)}
	suffix := ExamplesDescriptionSuffix([]map[string]any{{"ok": true}, huge})
	// The first (small) example renders; the oversized one is dropped whole
	// rather than truncating mid-JSON.
	if !strings.Contains(suffix, `{"ok":true}`) {
		t.Fatalf("small example lost: %q", suffix)
	}
	if strings.Contains(suffix, "xxx") {
		t.Fatalf("oversized example should be dropped whole: %q", suffix)
	}
	if len(suffix) > toolExamplesRenderBudget+64 {
		t.Fatalf("budget exceeded: %d bytes", len(suffix))
	}
}

func TestDefsCarryExamples(t *testing.T) {
	if defsCarryExamples([]ToolDefinition{{Name: "a"}, {Name: "b"}}) {
		t.Fatal("no examples should report false")
	}
	if !defsCarryExamples([]ToolDefinition{{Name: "a"}, {Name: "b", Examples: sampleExamples()}}) {
		t.Fatal("examples should report true")
	}
}

func TestToolBetaHeaderValuesJoinsThinkingAndExamples(t *testing.T) {
	tools := []ToolDefinition{{Name: "t", Examples: sampleExamples()}}
	got := toolBetaHeaderValues("interleaved-thinking-2025-05-14", tools, false)
	want := "interleaved-thinking-2025-05-14," + advancedToolUseBeta
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	// Without examples, base value passes through untouched.
	if got := toolBetaHeaderValues("interleaved-thinking-2025-05-14", nil, false); got != "interleaved-thinking-2025-05-14" {
		t.Fatalf("unexpected base passthrough: %q", got)
	}
	// Tool Search active: its own opts already send advanced-tool-use, so
	// the examples beta is skipped to avoid a duplicate/clobbered header.
	if got := toolBetaHeaderValues("", tools, true); got != "" {
		t.Fatalf("tool-search active should skip examples beta, got %q", got)
	}
}

func TestAnthropicToolParamSerializesInputExamples(t *testing.T) {
	// Mirror of the anthropic.go tool-loop decoration: the SDK stable
	// ToolParam serializes Examples under the wire key `input_examples`.
	tool := anthropic.ToolUnionParamOfTool(anthropic.ToolInputSchemaParam{Type: "object"}, "create_ticket")
	if tool.OfTool == nil {
		t.Fatal("OfTool variant expected")
	}
	tool.OfTool.InputExamples = sampleExamples()
	raw, err := json.Marshal(tool.OfTool)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe struct {
		InputExamples []map[string]any `json:"input_examples"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(probe.InputExamples) != 3 {
		t.Fatalf("want 3 input_examples on the wire, got %d: %s", len(probe.InputExamples), raw)
	}
}

func TestOpenAIConvertToolsAppendsExamplesSuffix(t *testing.T) {
	p := &OpenAIProvider{}
	defs := []ToolDefinition{
		{Name: "plain", Description: "Plain tool"},
		{Name: "with_examples", Description: "Complex tool", Examples: sampleExamples()},
	}
	got := p.convertTools(defs)
	if len(got) != 2 {
		t.Fatalf("want 2 tools, got %d", len(got))
	}
	if got[0].Function.Description != "Plain tool" {
		t.Fatalf("unaffected tool must keep byte-identical description: %q", got[0].Function.Description)
	}
	if !strings.HasPrefix(got[1].Function.Description, "Complex tool") {
		t.Fatalf("base description lost: %q", got[1].Function.Description)
	}
	if !strings.Contains(got[1].Function.Description, "Example usage:") {
		t.Fatalf("examples suffix missing: %q", got[1].Function.Description)
	}
}

func TestGeminiConvertToolsAppendsExamplesSuffix(t *testing.T) {
	p := &GeminiProvider{}
	defs := []ToolDefinition{{Name: "with_examples", Description: "Complex tool", Examples: sampleExamples()}}
	tools := p.convertTools(defs)
	var desc string
	found := false
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		for _, fd := range tool.FunctionDeclarations {
			if fd != nil && fd.Name == "with_examples" {
				desc, found = fd.Description, true
			}
		}
	}
	if !found {
		t.Fatal("function declaration not found")
	}
	if !strings.HasPrefix(desc, "Complex tool") || !strings.Contains(desc, "Example usage:") {
		t.Fatalf("gemini description suffix missing: %q", desc)
	}
}
