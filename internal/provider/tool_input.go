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

		if _, ok := decoded.(map[string]any); ok {
			return decoded
		}

		return map[string]any{
			"value": decoded,
		}
	}

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
