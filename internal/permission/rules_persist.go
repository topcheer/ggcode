package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/topcheer/ggcode/internal/debug"
)

type PermissionRulesFile struct {
	ToolOverrides        map[string]string `json:"tool_overrides"`
	CommandAllowPatterns []string          `json:"command_allow_patterns"`
	CommandDenyPatterns  []string          `json:"command_deny_patterns"`
}

func RulesFilePath(configDir string) string {
	return filepath.Join(configDir, "permission_rules.json")
}

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

func SaveRules(path string, data *PermissionRulesFile) error {
	if data == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

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

	var allowPats, denyPats []string
	if p.cmdRules != nil {
		allowPats = userFriendlyPatterns(p.cmdRules.AllowPatterns())
		denyPats = userFriendlyPatterns(p.cmdRules.DenyPatterns())
	}
	p.mu.RUnlock()
	data.CommandAllowPatterns = allowPats
	data.CommandDenyPatterns = denyPats

	return data
}

func userFriendlyPatterns(regexPatterns []string) []string {
	out := make([]string, 0, len(regexPatterns))
	for _, re := range regexPatterns {
		s := re

		s = trimPrefix(s, "(?i)^")
		s = trimPrefix(s, "^")

		s = replaceAll(s, ".*", "*")

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

			result += string(s[i+1])
			i++
		} else {
			result += string(s[i])
		}
	}
	return result
}

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
