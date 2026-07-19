package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ConfigAccess provides unified read/write access to ALL ggcode configuration.
// Implementations route dot-notation keys to the appropriate config file
// (ggcode.yaml, keys.env, harness.yaml, oauth-tokens, etc.).
type ConfigAccess interface {
	// Get reads a config key. Supports dot-notation paths.
	// Returns the value as a string (complex values are JSON-encoded).
	Get(key string) (string, error)
	// Set writes a config key. Persists to the appropriate file.
	// For provider-affecting keys, probes the target before committing.
	Set(key, value string) error
	// List returns all config settings, optionally filtered by section.
	// Section can be: "", "core", "api_key", "vendors", "mcp", "im", "a2a", "knight", "harness", "oauth", "runtime".
	List(section string) (string, error)
	// Delete removes a config key.
	// Only works for: mcp_servers.<name>, im.adapters.<name>.
	Delete(key string) error
}

// ConfigTool reads, writes, lists, or deletes configuration settings.
type ConfigTool struct {
	Access ConfigAccess
}

func (t ConfigTool) Name() string { return "config" }
func (t ConfigTool) Description() string {
	return "Read, write, list, or delete configuration settings with dot-notation keys. " +
		"Omit value to read; empty strings are treated as reads. " +
		"Provider settings (vendor/endpoint/model/api_key) are probed before committing. " +
		"Secrets stored in keys.env, not YAML. Use list=true to discover all keys."
}
func (t ConfigTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"setting": {
				"type": "string",
			"description": "Config key in dot-notation. Common keys: 'vendor', 'endpoint', 'model', 'api_key', 'language', 'default_mode', 'max_iterations', 'scope'. Provider settings are critical: read current vendor/endpoint/model first, discover available models with vendors.<name>.endpoints.<ep>.discover_models when possible, and prefer scope=instance for project-specific overrides. Scope control: read 'scope' to see current save target, set 'scope' to 'global' or 'instance' to switch. Vendor/endpoint info: 'vendors.<name>', 'vendors.<name>.endpoints.<ep>'. Model lists: 'vendors.<name>.endpoints.<ep>.models' (configured), 'vendors.<name>.endpoints.<ep>.discover_models' (live API query). MCP: 'mcp_servers', 'mcp_servers.<name>'. IM: 'im.output_mode', 'im.adapters.<name>'. A2A: 'a2a.host', 'a2a.auth.api_key'. Knight: 'knight.enabled'. Use list=true to see all."
			},
			"value": {
				"type": "string",
			"description": "Value to set. Omit this field to read the current value. Empty strings are treated as reads to avoid accidental clearing. For complex values (arrays, objects), pass a JSON string. Provider-affecting values (vendor/endpoint/model/api_key) are probed before commit; failed probes leave the current working config unchanged. For secrets (api_key, tokens), the value is stored securely in keys.env, never in the main config file, and is not echoed back."
			},
			"list": {
				"type": "boolean",
				"description": "List all settings. Optionally filter by section: 'core', 'api_key', 'vendors', 'mcp', 'im', 'a2a', 'knight', 'harness', 'oauth', 'runtime'. Empty string or omitted lists everything."
			},
			"delete": {
				"type": "boolean",
				"description": "Delete the specified key. Supported keys: 'mcp_servers.<name>' (remove MCP server), 'im.adapters.<name>' (remove IM adapter)."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Reading config', '修改配置'). You MUST always provide this field."
			}
		},
		"required": [
			"description"
		]
	}`)
}
func (t ConfigTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Setting string `json:"setting"`
		Value   string `json:"value"`
		List    bool   `json:"list"`
		Delete  bool   `json:"delete"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	// List mode
	if args.List {
		content, err := t.Access.List(args.Setting) // Setting used as section filter
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: content}, nil
	}

	if strings.TrimSpace(args.Setting) == "" {
		return Result{IsError: true, Content: "setting is required (or use list=true)"}, nil
	}

	// Delete mode
	if args.Delete {
		if err := t.Access.Delete(args.Setting); err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: fmt.Sprintf("Deleted %s\n", args.Setting)}, nil
	}

	// Read mode. Empty string values are treated as reads to avoid accidental clearing
	// when clients include optional string fields with their zero value.
	if args.Value == "" {
		val, err := t.Access.Get(args.Setting)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: fmt.Sprintf("%s = %s\n", args.Setting, val)}, nil
	}

	// Write mode
	if err := t.Access.Set(args.Setting, args.Value); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Set %s = %s\n", args.Setting, configSetDisplayValue(args.Setting, args.Value))}, nil
}

func configSetDisplayValue(setting, value string) string {
	lower := strings.ToLower(setting)
	if lower == "api_key" || strings.HasPrefix(lower, "api_key.") || strings.Contains(lower, "api_key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") {
		return "(secret stored securely)"
	}
	return value
}
