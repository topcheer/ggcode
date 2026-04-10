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
		return decoded
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
