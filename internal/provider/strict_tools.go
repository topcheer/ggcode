package provider

import (
	"bytes"
	"encoding/json"

	"github.com/topcheer/ggcode/internal/debug"
)

// Strict tool use (structured outputs) plumbing.
//
// Concept: OpenAI ("strict: true" on function definitions) and Anthropic
// ("strict": true on tool params + beta-GA structured outputs) compile the
// tool's JSON Schema into a grammar that constrains token generation, so
// required fields are ALWAYS present with correct types. Without it,
// `required` is only a semantic hint — session data showed read_file /
// edit_file / write_file silently omitting their required `description`
// parameter (see docs/design/tool-schema-strict-mode.md).
//
// Hard requirements enforced here (both providers):
//   - the input schema must carry "additionalProperties": false (injected
//     recursively for every object node before the request is sent);
//   - top-level properties must all be listed in "required" (an OpenAI strict
//     requirement; also keeps Anthropic's 24-optional-parameter grammar
//     complexity budget safe);
//   - Anthropic caps strict tools at 20 per request, so the feature is
//     applied to a curated allowlist (DefaultStrictTools), never to all ~191
//     registered tools.
//
// Docs:
//   https://platform.claude.com/docs/en/build-with-claude/structured-outputs
//   https://platform.openai.com/docs/guides/structured-outputs

// StrictToolsSetter is implemented by providers that support strict tool
// definitions. The allowlist maps tool names to their strict flag; tools not
// in the map are sent without `strict` (zero risk on every endpoint because
// strict mode only ever ADDS the field for allowlisted tools).
type StrictToolsSetter interface {
	SetStrictTools(allow map[string]bool)
}

// DefaultStrictTools is the default strict-tool allowlist: the built-in
// file/command tools whose REQUIRED `description` parameter (used for TUI
// activity labels) models historically omit. Keeps request-wide strict
// counts far below Anthropic's 20-tool complexity cap.
var DefaultStrictTools = []string{"read_file", "edit_file", "write_file", "run_command"}

// StrictToolsAllowlist builds the setter map from a config allowlist.
// Unknown names are harmless (never matched by any ToolDefinition).
func StrictToolsAllowlist(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// InjectAdditionalPropertiesFalse recursively sets "additionalProperties":
// false on every object-schema node (root, properties, items, tuple
// prefixes, composition branches, and $defs/definitions). A node that
// already sets it to false is left untouched; any other value (absent,
// true, non-boolean) is overridden because strict mode semantically
// forbids undeclared properties. Invalid JSON is returned unchanged.
func InjectAdditionalPropertiesFalse(raw json.RawMessage) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(injectAPF(v))
	if err != nil {
		return raw
	}
	return json.RawMessage(out)
}

// injectAPF mutates map nodes in place and returns the (possibly rewritten)
// value. Property-key iteration order follows the decoded map, which Go
// randomizes; strict compilation is order-insensitive, so no canonical
// reordering is attempted (avoids churn vs. hand-written schemas).
func injectAPF(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		// Only objects can be schemas; recurse through containers that may
		// hold schemas (arrays) so nested definitions are covered.
		if arr, isArray := v.([]any); isArray {
			for i, e := range arr {
				arr[i] = injectAPF(e)
			}
		}
		return v
	}
	// Recurse into schema-bearing sub-nodes first.
	if props, ok := m["properties"].(map[string]any); ok {
		for k, sub := range props {
			props[k] = injectAPF(sub)
		}
	}
	if items, ok := m["items"]; ok {
		m["items"] = injectAPF(items)
	}
	// additionalProperties (any prior value, including schema-form): strict
	// mode forbids undeclared properties entirely, so normalize to the
	// canonical false.
	m["additionalProperties"] = false
	for _, key := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if arr, ok := m[key].([]any); ok {
			for i, e := range arr {
				arr[i] = injectAPF(e)
			}
		}
	}
	for _, key := range []string{"$defs", "definitions"} {
		if defs, ok := m[key].(map[string]any); ok {
			for k, sub := range defs {
				defs[k] = injectAPF(sub)
			}
		}
	}
	return m
}

// PrepareStrictToolSchema validates a tool schema for strict mode and returns
// the request-ready schema. It enforces the top-level all-required rule and
// injects additionalProperties:false. The bool result is false when the tool
// is NOT strict-compatible (callers must then send it without `strict`).
func PrepareStrictToolSchema(name string, raw json.RawMessage) (json.RawMessage, bool) {
	if len(bytes.TrimSpace(raw)) == 0 || !json.Valid(raw) {
		debug.Log("provider", "strict tool %q skipped: schema is empty or invalid JSON", name)
		return raw, false
	}
	var probe struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		debug.Log("provider", "strict tool %q skipped: schema probe failed: %v", name, err)
		return raw, false
	}
	required := make(map[string]bool, len(probe.Required))
	for _, r := range probe.Required {
		required[r] = true
	}
	for prop := range probe.Properties {
		if !required[prop] {
			debug.Log("provider", "strict tool %q skipped: optional top-level field %q violates strict all-required rule", name, prop)
			return raw, false
		}
	}
	return InjectAdditionalPropertiesFalse(raw), true
}
