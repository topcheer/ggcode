package agent

import "github.com/topcheer/ggcode/internal/provider"

// Tool Use Examples (Anthropic advanced-tool-use beta) plumbing: config
// `tool_examples` maps exact tool names to sample invocations that
// demonstrate usage patterns a JSON Schema cannot express (format
// conventions, ID formats, optional-parameter correlations). The agent
// annotates the outbound tool definitions with the configured examples;
// the Anthropic adapter serializes them natively as `input_examples` and
// adapters without native support render a compact description suffix.
// https://www.anthropic.com/engineering/advanced-tool-use

// SetToolExamples installs the configured tool-name-to-examples map applied
// to every outbound tool definition. Wired from config via
// agentruntime.ApplyToolExamplesConfigToAgent.
func (a *Agent) SetToolExamples(m map[string][]map[string]any) {
	a.toolExamples = m
}

// ApplyToolExamples returns defs annotated with configured examples.
// Copy-on-write: when nothing matches, the input slice is returned as-is so
// unaffected tools and requests keep byte-identical serialization (prompt
// cache prefixes stay stable). Example lists are attached by reference -
// the agent never mutates them.
func ApplyToolExamples(defs []provider.ToolDefinition, configured map[string][]map[string]any) []provider.ToolDefinition {
	if len(configured) == 0 || len(defs) == 0 {
		return defs
	}
	changed := false
	for i := range defs {
		examples, ok := configured[defs[i].Name]
		if !ok || len(examples) == 0 {
			continue
		}
		if !changed {
			out := make([]provider.ToolDefinition, len(defs))
			copy(out, defs)
			defs = out
			changed = true
		}
		defs[i].Examples = examples
	}
	return defs
}
