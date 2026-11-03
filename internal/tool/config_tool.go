package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ConfigAccess interface {
	Get(key string) (string, error)
	Set(key, value string) error
	List(section string) (string, error)
	Delete(key string) error
}

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
			"description": "Config key in dot-notation. Common: 'vendor', 'endpoint', 'model', 'api_key', 'language', 'default_mode', 'scope'. Provider settings are critical — read current values before changing. Vendors: 'vendors.<name>', 'vendors.<name>.endpoints.<ep>.models' (configured) or '.discover_models' (discover available models via live query). Scope: set 'scope' to 'global'/'instance' to switch save target (instance = per-workspace overrides). MCP: 'mcp_servers.<name>'. IM: 'im.adapters.<name>'. A2A: 'a2a.host'. Fallback chain: 'fallback.enabled/vendor/endpoint/model' (legacy single entry, highest priority), 'fallbacks' (JSON array, replaces chain), 'fallbacks.<N>.<field>' (edit entry N), 'fallbacks.append'. Use list=true to see all."
			},
			"value": {
				"type": "string",
			"description": "Value to set. Omit this field to read the current value. Empty strings are treated as reads to avoid accidental clearing. For complex values (arrays, objects), pass a JSON string. Provider-affecting values (vendor/endpoint/model/api_key) are probed before commit; failed probes leave the current working config unchanged. For secrets (api_key, tokens), the value is stored securely in keys.env, never in the main config file, and is not echoed back."
			},
			"list": {
				"type": "boolean",
				"description": "List all settings. Optionally filter by section: 'core', 'api_key', 'vendors', 'mcp', 'im', 'a2a', 'knight' 'oauth', 'runtime'. Empty string or omitted lists everything."
			},
			"delete": {
				"type": "boolean",
				"description": "Delete the specified key. Supported keys: 'mcp_servers.<name>' (remove MCP server), 'im.adapters.<name>' (remove IM adapter)."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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

	if args.List {
		content, err := t.Access.List(args.Setting)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: content}, nil
	}

	if strings.TrimSpace(args.Setting) == "" {
		return Result{IsError: true, Content: "setting is required (or use list=true)"}, nil
	}

	if args.Delete {
		if err := t.Access.Delete(args.Setting); err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: fmt.Sprintf("Deleted %s\n", args.Setting)}, nil
	}

	if args.Value == "" {
		val, err := t.Access.Get(args.Setting)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: fmt.Sprintf("%s = %s\n", args.Setting, val)}, nil
	}

	if err := t.Access.Set(args.Setting, args.Value); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Set %s = %s\n", args.Setting, configSetDisplayValue(args.Setting, args.Value))}, nil
}

func configSetDisplayValue(setting, value string) string {

	lower := strings.ToLower(setting)
	secretKeys := []string{"api_key", "auth_token", "access_token", "refresh_token",
		"client_secret", "secret_key", "private_key", "password", "passwd", "token"}
	for _, k := range secretKeys {
		if strings.HasPrefix(lower, k+".") || lower == k || strings.HasSuffix(lower, "."+k) ||
			strings.Contains(lower, "_"+k+"_") {
			return "(secret stored securely)"
		}
	}

	if strings.Contains(lower, "api_key") || strings.Contains(lower, "auth_token") {
		return "(secret stored securely)"
	}
	return value
}
