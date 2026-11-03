package provider

import (
	"bytes"
	"encoding/json"

	"github.com/topcheer/ggcode/internal/debug"
)

type StrictToolsSetter interface {
	SetStrictTools(allow map[string]bool)
}

var DefaultStrictTools = []string{"read_file", "edit_file", "write_file", "run_command"}

func StrictToolsAllowlist(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

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

func injectAPF(v any) any {
	m, ok := v.(map[string]any)
	if !ok {

		if arr, isArray := v.([]any); isArray {
			for i, e := range arr {
				arr[i] = injectAPF(e)
			}
		}
		return v
	}

	if props, ok := m["properties"].(map[string]any); ok {
		for k, sub := range props {
			props[k] = injectAPF(sub)
		}
	}
	if items, ok := m["items"]; ok {
		m["items"] = injectAPF(items)
	}

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
