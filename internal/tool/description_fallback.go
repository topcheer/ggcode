package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

func InjectDescriptionFallback(schema, args json.RawMessage, toolName string) json.RawMessage {
	if len(schema) == 0 || len(args) == 0 {
		return args
	}

	var props struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &props); err != nil {
		return args
	}

	required := false
	for _, r := range props.Required {
		if r == "description" {
			required = true
			break
		}
	}
	if !required {
		return args
	}

	descProp, hasDescProp := props.Properties["description"]
	if !hasDescProp || !strings.Contains(descProp.Description, "activity label") {
		return args
	}

	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {
		return args
	}

	if argMap == nil {
		argMap = map[string]json.RawMessage{}
	}
	if v, ok := argMap["description"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil && strings.TrimSpace(s) != "" {
			return args
		}
	}

	fallback, err := json.Marshal(deriveActivityLabel(toolName, argMap))
	if err != nil {
		return args
	}
	argMap["description"] = fallback
	out, err := json.Marshal(argMap)
	if err != nil {
		return args
	}
	return out
}

func deriveActivityLabel(toolName string, args map[string]json.RawMessage) string {

	preferred := []string{"path", "file_path", "pattern", "command", "query", "action", "url", "name", "subject", "taskId"}
	for _, key := range preferred {
		v, ok := args[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil || strings.TrimSpace(s) == "" {
			continue
		}
		s = strings.TrimSpace(s)

		s = strings.Join(strings.Fields(s), " ")

		if utf8.RuneCountInString(s) > 60 {
			s = string([]rune(s)[:57]) + "..."
		}
		return fmt.Sprintf("%s %s", toolName, s)
	}
	return toolName
}
