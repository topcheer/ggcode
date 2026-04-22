package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ConfigAccess provides read/write access to runtime configuration.
type ConfigAccess interface {
	Get(key string) (string, bool)
	Set(key, value string) error
	List() map[string]string
}

// ConfigTool reads or writes a configuration setting.
// When value is provided, it writes; otherwise it reads.
type ConfigTool struct {
	Access ConfigAccess
}

func (t ConfigTool) Name() string { return "config" }
func (t ConfigTool) Description() string {
	return "Read or write a runtime configuration setting. " +
		"If value is provided, sets the setting; otherwise returns the current value. " +
		"Use list=true to see all settings."
}
func (t ConfigTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"setting": {"type": "string", "description": "Configuration key to read or write"},
			"value": {"type": "string", "description": "Value to set (omit to read)"},
			"list": {"type": "boolean", "description": "List all configuration settings (ignores other params)"}
		}
	}`)
}
func (t ConfigTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Setting string `json:"setting"`
		Value   string `json:"value"`
		List    bool   `json:"list"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	// List mode
	if args.List {
		all := t.Access.List()
		if len(all) == 0 {
			return Result{Content: "No configuration settings.\n"}, nil
		}
		keys := make([]string, 0, len(all))
		for k := range all {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&sb, "- %s: %s\n", k, all[k])
		}
		return Result{Content: sb.String()}, nil
	}

	if strings.TrimSpace(args.Setting) == "" {
		return Result{IsError: true, Content: "setting is required (or use list=true)"}, nil
	}

	// Read mode
	if args.Value == "" {
		// Check if it was explicitly provided as empty vs not provided
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(input, &raw); err == nil && raw != nil {
			if _, hasValue := raw["value"]; !hasValue {
				// No value key → read mode
				val, ok := t.Access.Get(args.Setting)
				if !ok {
					return Result{IsError: true, Content: fmt.Sprintf("setting %q not found", args.Setting)}, nil
				}
				return Result{Content: fmt.Sprintf("%s = %s\n", args.Setting, val)}, nil
			}
		}
	}

	// Write mode (value provided, even if empty string)
	if err := t.Access.Set(args.Setting, args.Value); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Set %s = %s\n", args.Setting, args.Value)}, nil
}
