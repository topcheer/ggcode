package provider

import (
	"encoding/json"
	"strings"
)

// Anthropic Tool Use Examples (advanced-tool-use beta) helpers shared by the
// provider adapters. Examples are sample invocations that demonstrate usage
// patterns a JSON Schema cannot express: format conventions ("2024-11-06"
// vs "Nov 6"), ID formats ("USR-12345"), optional-parameter correlations.
// Anthropic reported tool-call accuracy improving from 72% to 90% on complex
// parameter handling with examples. The Anthropic adapter serializes them
// natively as `input_examples`; adapters without native support render a
// compact suffix into the tool description instead.
// https://www.anthropic.com/engineering/advanced-tool-use

const (
	// toolExamplesMaxRendered caps how many examples are rendered into a
	// description suffix on providers without native input_examples support
	// (OpenAI, Gemini). Examples live in the description there, so every one
	// costs context on every turn; two cover the dominant format conventions.
	toolExamplesMaxRendered = 2
	// toolExamplesRenderBudget caps the total serialized size of the rendered
	// suffix so a misconfigured example cannot bloat every request.
	toolExamplesRenderBudget = 600
)

// defsCarryExamples reports whether any tool definition carries usage
// examples. The Anthropic adapter uses this to decide whether the
// advanced-tool-use beta header is needed on a request.
func defsCarryExamples(tools []ToolDefinition) bool {
	for i := range tools {
		if len(tools[i].Examples) > 0 {
			return true
		}
	}
	return false
}

// toolBetaHeaderValues composes the anthropic-beta header value for a
// tool-using request: the base value (e.g. interleaved-thinking) joined with
// the advanced-tool-use beta when any tool carries usage examples. When the
// server-side Tool Search Tool is active, its own request options already
// send advancedToolUseBeta and would clobber a second WithHeader on the same
// key, so the examples beta is skipped there.
func toolBetaHeaderValues(base string, tools []ToolDefinition, toolSearchActive bool) string {
	var vals []string
	if base != "" {
		vals = append(vals, base)
	}
	if defsCarryExamples(tools) && !toolSearchActive {
		vals = append(vals, advancedToolUseBeta)
	}
	return strings.Join(vals, ",")
}

// ExamplesDescriptionSuffix renders examples into a compact description
// suffix for providers without native input_examples support (OpenAI,
// Gemini). Rendering caps at toolExamplesMaxRendered examples within
// toolExamplesRenderBudget bytes; examples that would overflow the budget
// are dropped rather than truncating mid-JSON (invalid JSON examples would
// teach the model the wrong format). Returns "" when there is nothing to
// render, so unaffected tools keep byte-identical descriptions.
func ExamplesDescriptionSuffix(examples []map[string]any) string {
	if len(examples) == 0 {
		return ""
	}
	n := len(examples)
	if n > toolExamplesMaxRendered {
		n = toolExamplesMaxRendered
	}
	const header = "\n\nExample usage:"
	var b strings.Builder
	b.WriteString(header)
	used := len(header)
	for i := 0; i < n; i++ {
		raw, err := json.Marshal(examples[i])
		if err != nil {
			continue
		}
		if used+len(raw)+1 > toolExamplesRenderBudget {
			break
		}
		b.WriteByte('\n')
		b.Write(raw)
		used += len(raw) + 1
	}
	if b.Len() == len(header) {
		return ""
	}
	return b.String()
}
