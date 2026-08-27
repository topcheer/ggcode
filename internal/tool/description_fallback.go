package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

// InjectDescriptionFallback inspects the tool schema for a "description"
// parameter that is (a) declared required, (b) missing or empty in args, and
// (c) a UI-only activity label rather than semantic tool data. When all three
// hold, it injects a derived fallback label so the call proceeds instead of
// being rejected by ValidateRequiredParams - saving a full LLM round-trip
// over a purely cosmetic field.
//
// Why the "activity label" marker matters: some tools (create_skill,
// cmd_snippet save, task_create, ...) take a "description" argument that IS
// the payload (the skill's own description, the snippet's description).
// Auto-filling those would silently corrupt user data, so they are exempt:
// their parameter help text does not contain the marker and the call is
// still rejected, forcing the model to supply real content.
//
// Returns args unchanged when no injection is needed or parsing fails.
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

	// (a) schema must require description
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

	// (c) must be a UI activity label, not semantic data
	descProp, hasDescProp := props.Properties["description"]
	if !hasDescProp || !strings.Contains(descProp.Description, "activity label") {
		return args
	}

	// (b) description missing or empty in args
	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {
		return args
	}
	if v, ok := argMap["description"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil && strings.TrimSpace(s) != "" {
			return args // already provided
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

// deriveActivityLabel builds a short, human-readable fallback label from the
// tool name and the most informative argument present (path, pattern,
// command, query, ...). Deterministic and cheap - no LLM involvement.
func deriveActivityLabel(toolName string, args map[string]json.RawMessage) string {
	// Preference order: the most identifying argument first.
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
		// Collapse whitespace and newlines so multi-line blobs stay single-line.
		s = strings.Join(strings.Fields(s), " ")
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return fmt.Sprintf("%s %s", toolName, s)
	}
	return toolName
}
