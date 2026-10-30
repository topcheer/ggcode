package tool

import "encoding/json"

// argRationaleWriteToolNames mirrors internal/agent.argComplexityWriteTools
// (kept in sync manually; cross-referenced from tool_arg_complexity.go).
// These are the write-class tools whose LLM-visible schema gains the
// optional arg_rationale property so schema-strict models can actually send
// the justification the argument-complexity gate asks for (defect A fix).
var argRationaleWriteToolNames = map[string]bool{
	"run_command":      true,
	"edit_file":        true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"write_file":       true,
	"multi_file_write": true,
	"batch_replace":    true,
	"file_ops":         true,
	"notebook_edit":    true,
	"git_checkout":     true,
	"git_reset":        true,
	"git_revert":       true,
	"git_add":          true,
	"git_commit":       true,
}

// ArgRationaleField is the optional justification parameter accepted by all
// write-class tools. The agent-side argument-complexity gate consumes it
// pre-dispatch and strips it before the tool executes; tools never see it.
const ArgRationaleField = "arg_rationale"

const argRationaleDesc = "Optional one-line justification for this call's heavy parameters (why this target, why this scope, why now). Required only when the argument-complexity gate has rejected this call once; attaching it skips that reject round and is recorded in debug logs."

// injectArgRationaleSchema adds the optional arg_rationale property to a
// write tool's parameters schema. Non-write tools and schemas that already
// declare the field pass through unchanged; a malformed schema passes
// through untouched (the gate tolerates its absence at the call level).
func injectArgRationaleSchema(name string, params json.RawMessage) json.RawMessage {
	if !argRationaleWriteToolNames[name] || len(params) == 0 {
		return params
	}
	var schema map[string]any
	if err := json.Unmarshal(params, &schema); err != nil {
		return params
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		props = make(map[string]any)
	}
	if _, exists := props[ArgRationaleField]; exists {
		return params
	}
	props[ArgRationaleField] = map[string]any{
		"type":        "string",
		"description": argRationaleDesc,
	}
	schema["properties"] = props
	b, err := json.Marshal(schema)
	if err != nil {
		return params
	}
	return b
}
