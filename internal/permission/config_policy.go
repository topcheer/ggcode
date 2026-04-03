package permission

import (
	"encoding/json"
	"sync"
)

// DefaultMode is the default permission mode if not specified.
var DefaultMode = SupervisedMode

// ToolRule defines the permission level for a tool.
type ToolRule struct {
	Decision Decision `yaml:"decision"`
}

// ConfigPolicy implements PermissionPolicy based on configuration rules.
type ConfigPolicy struct {
	rules    map[string]Decision
	sandbox  *PathSandbox
	detector *DangerousDetector
	mode     PermissionMode
	mu       sync.RWMutex
}

// NewConfigPolicy creates a policy from tool rules and allowed directories.
// Default decision is Ask for any tool not explicitly listed.
func NewConfigPolicy(rules map[string]Decision, allowedDirs []string) *ConfigPolicy {
	return NewConfigPolicyWithMode(rules, allowedDirs, DefaultMode)
}

// NewConfigPolicyWithMode creates a policy with an explicit permission mode.
func NewConfigPolicyWithMode(rules map[string]Decision, allowedDirs []string, mode PermissionMode) *ConfigPolicy {
	if rules == nil {
		rules = make(map[string]Decision)
	}
	return &ConfigPolicy{
		rules:    rules,
		sandbox:  NewPathSandbox(allowedDirs),
		detector: NewDangerousDetector(),
		mode:     mode,
	}
}

// Check returns the permission decision for a tool call.
func (p *ConfigPolicy) Check(toolName string, input json.RawMessage) (Decision, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Mode-specific handling
	switch p.mode {
	case PlanMode:
		// Plan mode: read-only tools allowed, everything else denied
		if IsReadOnlyTool(toolName) {
			// Still check sandbox for read tools
			if isFileTool(toolName) {
				path, _ := extractFilePath(input)
				if path != "" && !p.sandbox.Allowed(path) {
					return Deny, nil
				}
			}
			return Allow, nil
		}
		return Deny, nil
	case AutoMode:
		// Auto mode: allow safe ops, deny dangerous ones, no prompts
		if toolName == "run_command" {
			cmd, _ := extractCommand(input)
			if cmd != "" && p.detector.IsDangerous(cmd) {
				return Deny, nil
			}
		}
		// Check sandbox for file tools
		if isFileTool(toolName) {
			path, _ := extractFilePath(input)
			if path != "" && !p.sandbox.Allowed(path) {
				return Deny, nil
			}
		}
		return Allow, nil
	}

	// Supervised mode (default): check overrides, then ask
	if d, ok := p.rules[toolName]; ok {
		if isFileTool(toolName) {
			path, _ := extractFilePath(input)
			if path != "" && !p.sandbox.Allowed(path) {
				return Deny, nil
			}
		}
		if toolName == "run_command" {
			cmd, _ := extractCommand(input)
			if cmd != "" && p.detector.IsDangerous(cmd) {
				return Deny, nil
			}
		}
		return d, nil
	}

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

// Mode returns the current permission mode.
func (p *ConfigPolicy) Mode() PermissionMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

// SetMode changes the permission mode at runtime.
func (p *ConfigPolicy) SetMode(mode PermissionMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
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
