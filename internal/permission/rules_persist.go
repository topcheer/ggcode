package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/topcheer/ggcode/internal/debug"
)

// PermissionRulesFile is the JSON representation of persisted permission rules.
// Stored at ~/.ggcode/permission_rules.json as a separate config file so it
// doesn't bloat or conflict with the main ggcode.yaml.
type PermissionRulesFile struct {
	ToolOverrides        map[string]string `json:"tool_overrides"`         // tool name → "allow"|"deny"
	CommandAllowPatterns []string          `json:"command_allow_patterns"` // e.g. "git diff*", "npm test*"
	CommandDenyPatterns  []string          `json:"command_deny_patterns"`
}

// RulesFilePath returns the default path for persisted permission rules.
func RulesFilePath(configDir string) string {
	return filepath.Join(configDir, "permission_rules.json")
}

// LoadRules reads persisted permission rules from a JSON file.
// Returns an empty struct if the file doesn't exist.
func LoadRules(path string) *PermissionRulesFile {
	b, err := os.ReadFile(path)
	if err != nil {
		return &PermissionRulesFile{
			ToolOverrides:        make(map[string]string),
			CommandAllowPatterns: []string{},
			CommandDenyPatterns:  []string{},
		}
	}
	var data PermissionRulesFile
	if err := json.Unmarshal(b, &data); err != nil {
		debug.Log("permission", "failed to parse rules file %s: %v", path, err)
		return &PermissionRulesFile{
			ToolOverrides:        make(map[string]string),
			CommandAllowPatterns: []string{},
			CommandDenyPatterns:  []string{},
		}
	}
	if data.ToolOverrides == nil {
		data.ToolOverrides = make(map[string]string)
	}
	if data.CommandAllowPatterns == nil {
		data.CommandAllowPatterns = []string{}
	}
	if data.CommandDenyPatterns == nil {
		data.CommandDenyPatterns = []string{}
	}
	return &data
}

// SaveRules persists permission rules to a JSON file atomically.
func SaveRules(path string, data *PermissionRulesFile) error {
	if data == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	// Sort patterns for deterministic output
	sort.Strings(data.CommandAllowPatterns)
	sort.Strings(data.CommandDenyPatterns)

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SnapshotRules builds a PermissionRulesFile from the current policy state.
// This captures tool overrides and command patterns for persistence.
func SnapshotRules(p *ConfigPolicy, configDir string) *PermissionRulesFile {
	if p == nil {
		return nil
	}
	data := &PermissionRulesFile{
		ToolOverrides:        make(map[string]string),
		CommandAllowPatterns: []string{},
		CommandDenyPatterns:  []string{},
	}

	p.mu.RLock()
	for tool, d := range p.rules {
		switch d {
		case Allow:
			data.ToolOverrides[tool] = "allow"
		case Deny:
			data.ToolOverrides[tool] = "deny"
		}
	}
	p.mu.RUnlock()

	if p.cmdRules != nil {
		// Convert regex patterns back to user-friendly glob patterns
		data.CommandAllowPatterns = userFriendlyPatterns(p.cmdRules.AllowPatterns())
		data.CommandDenyPatterns = userFriendlyPatterns(p.cmdRules.DenyPatterns())
	}

	return data
}

// userFriendlyPatterns converts regex patterns like "^git diff.*" back to
// user-friendly glob patterns like "git diff*".
func userFriendlyPatterns(regexPatterns []string) []string {
	out := make([]string, 0, len(regexPatterns))
	for _, re := range regexPatterns {
		s := re
		// Strip leading (?i)^
		s = trimPrefix(s, "(?i)^")
		s = trimPrefix(s, "^")
		// Convert .* back to *
		s = replaceAll(s, ".*", "*")
		// Unescape regex metacharacters
		s = unescapeRegex(s)
		out = append(out, s)
	}
	return out
}

func trimPrefix(s, prefix string) string {
	for len(prefix) > 0 && len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

func replaceAll(s, old, new string) string {
	result := ""
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}

func unescapeRegex(s string) string {
	result := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			// Keep the next char literally (skip the backslash)
			result += string(s[i+1])
			i++
		} else {
			result += string(s[i])
		}
	}
	return result
}

// ApplyToPolicy loads rules from file and applies them to a ConfigPolicy.
func (data *PermissionRulesFile) ApplyToPolicy(p *ConfigPolicy) {
	if data == nil || p == nil {
		return
	}
	for tool, d := range data.ToolOverrides {
		switch d {
		case "allow":
			p.SetOverride(tool, Allow)
		case "deny":
			p.SetOverride(tool, Deny)
		}
	}
	if len(data.CommandAllowPatterns) > 0 || len(data.CommandDenyPatterns) > 0 {
		rs := NewCommandRuleSetFromLists(data.CommandAllowPatterns, data.CommandDenyPatterns)
		p.SetCommandRuleSet(rs)
	}
}
