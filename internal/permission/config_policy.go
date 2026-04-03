package permission

import (
	"encoding/json"
	"sync"
)

// ToolRule defines the permission level for a tool.
type ToolRule struct {
	Decision Decision `yaml:"decision"`
}

// ConfigPolicy implements PermissionPolicy based on configuration rules.
type ConfigPolicy struct {
	rules    map[string]Decision
	sandbox  *PathSandbox
	detector *DangerousDetector
	mu       sync.RWMutex
}

// NewConfigPolicy creates a policy from tool rules and allowed directories.
// Default decision is Ask for any tool not explicitly listed.
func NewConfigPolicy(rules map[string]Decision, allowedDirs []string) *ConfigPolicy {
	if rules == nil {
		rules = make(map[string]Decision)
	}
	return &ConfigPolicy{
		rules:    rules,
		sandbox:  NewPathSandbox(allowedDirs),
		detector: NewDangerousDetector(),
	}
}

// Check returns the permission decision for a tool call.
func (p *ConfigPolicy) Check(toolName string, input json.RawMessage) (Decision, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Check runtime overrides first
	if d, ok := p.rules[toolName]; ok {
		// For file tools, also check sandbox
		if isFileTool(toolName) {
			path, _ := extractFilePath(input)
			if path != "" && !p.sandbox.Allowed(path) {
				return Deny, nil
			}
		}
		// For run_command, check dangerous
		if toolName == "run_command" {
			cmd, _ := extractCommand(input)
			if cmd != "" && p.detector.IsDangerous(cmd) {
				return Deny, nil
			}
		}
		return d, nil
	}

	// Default: ask for all tools
	return Ask, nil
}

// IsDangerous returns true if the command is inherently dangerous.
func (p *ConfigPolicy) IsDangerous(command string) bool {
	return p.detector.IsDangerous(command)
}

// AllowedPath returns true if the path is within the sandbox.
func (p *ConfigPolicy) AllowedPath(path string) bool {
	return p.sandbox.Allowed(path)
}

// SetOverride allows runtime modification of per-tool policy.
func (p *ConfigPolicy) SetOverride(toolName string, decision Decision) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules[toolName] = decision
}

// GetDecision returns the current decision for a tool (for TUI display).
func (p *ConfigPolicy) GetDecision(toolName string) Decision {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if d, ok := p.rules[toolName]; ok {
		return d
	}
	return Ask
}

func isFileTool(name string) bool {
	switch name {
	case "read_file", "write_file", "edit_file", "list_directory":
		return true
	}
	return false
}

func extractFilePath(input json.RawMessage) (string, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return "", false
	}
	for _, key := range []string{"file_path", "path", "directory"} {
		if v, ok := m[key]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil {
				return s, true
			}
		}
	}
	return "", false
}

func extractCommand(input json.RawMessage) (string, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return "", false
	}
	if v, ok := m["command"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			return s, true
		}
	}
	return "", false
}
