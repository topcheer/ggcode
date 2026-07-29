package provider

import (
	"bytes"
	"encoding/json"
)

func normalizeToolInputValue(raw json.RawMessage) any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err == nil {
		// Anthropic tool_use input MUST be a JSON object (dict), not a list or scalar.
		// Some API proxies (e.g. ZAI/open.bigmodel.cn) will crash with
		// "'list' object has no attribute 'get'" if input is not a dict.
		if _, ok := decoded.(map[string]any); ok {
			return decoded
		}
		// Wrap non-object values into an object.
		return map[string]any{
			"value": decoded,
		}
	}
	// Attempt JSON repair before falling back to raw wrapper.
	// Stream truncation and weak models frequently produce nearly-valid
	// JSON that can be salvaged instead of lost.
	if repaired, ok := RepairJSON(trimmed); ok {
		if err := json.Unmarshal(repaired, &decoded); err == nil {
			if _, ok := decoded.(map[string]any); ok {
				return decoded
			}
			return map[string]any{"value": decoded}
		}
	}
	return map[string]any{
		"_ggcode_raw_input": string(trimmed),
	}
}

func normalizeToolInputJSONString(raw json.RawMessage) string {
	value := normalizeToolInputValue(raw)
	data, err := json.Marshal(value)
	if err != nil {
		return `{}`
	}
	return string(data)
}
