package tool

import "encoding/json"

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

const ArgRationaleField = "arg_rationale"

const argRationaleDesc = "One-line justification for this call's heavy parameters (why this target, why this scope, why now). ATTACH PROACTIVELY whenever the parameters are non-trivial: chained/piped or >200-char shell commands, multi-file edits, bulk rewrites - the argument-complexity gate blocks most such calls without it, and each block wastes a full round-trip. After a first rejection it is required."

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
