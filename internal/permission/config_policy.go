package permission

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// DefaultMode is the default permission mode if not specified.
var DefaultMode = SupervisedMode

// ToolRule defines the permission level for a tool.
type ToolRule struct {
	Decision Decision `yaml:"decision"`
}

// ConfigPolicy implements PermissionPolicy based on configuration rules.
type ConfigPolicy struct {
	rules           map[string]Decision
	sandbox         *PathSandbox
	readOnlySandbox *PathSandbox
	detector        *DangerousDetector
	mode            PermissionMode
	previousMode    PermissionMode // mode before entering plan mode (for exit_plan_mode decision)
	mu              sync.RWMutex
}

// NewConfigPolicy creates a policy from tool rules and allowed directories.
// Default decision is Ask for any tool not explicitly listed.
func NewConfigPolicy(rules map[string]Decision, allowedDirs []string) *ConfigPolicy {
	return NewConfigPolicyWithMode(rules, allowedDirs, DefaultMode)
}

// NewConfigPolicyWithMode creates a policy with an explicit permission mode.
func NewConfigPolicyWithMode(rules map[string]Decision, allowedDirs []string, mode PermissionMode) *ConfigPolicy {
	return NewConfigPolicyWithModeAndReadOnlyDirs(rules, allowedDirs, nil, mode)
}

// NewConfigPolicyWithModeAndReadOnlyDirs creates a policy with optional
// read-only file access outside the main writable sandbox.
func NewConfigPolicyWithModeAndReadOnlyDirs(rules map[string]Decision, allowedDirs, readOnlyDirs []string, mode PermissionMode) *ConfigPolicy {
	if rules == nil {
		rules = make(map[string]Decision)
	}
	return &ConfigPolicy{
		rules:           rules,
		sandbox:         NewPathSandbox(allowedDirs),
		readOnlySandbox: newOptionalPathSandbox(readOnlyDirs),
		detector:        NewDangerousDetector(),
		mode:            mode,
	}
}

// Check returns the permission decision for a tool call.
func (p *ConfigPolicy) Check(toolName string, input json.RawMessage) (Decision, error) {
	debug.Log("permission", "Check: tool=%s input=%s", toolName, truncateStr(string(input), 200))
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Mode-specific handling
	switch p.mode {
	case BypassMode, AutopilotMode:
		// Bypass mode: allow everything except extremely dangerous operations
		if isCommandTool(toolName) {
			cmd, _ := extractCommand(input)
			if cmd != "" && p.detector.IsExtremelyDangerous(cmd) {
				return Ask, nil
			}
		}
		// Check sandbox for file tools (still protect workspace boundary).
		// In bypass/autopilot, any *write* outside the writable sandbox is
		// downgraded to Ask, regardless of whether the path is on the small
		// "sensitive" allow-list. Without this, ~/.aws/credentials,
		// ~/.docker/config.json, /etc/**, and arbitrary user files outside
		// the workspace get silently overwritten on a prompt-injected tool
		// call. See locks.md S5.
		if isWriteFileTool(toolName) {
			path, _ := extractFilePath(input)
			if path != "" && !p.sandbox.Allowed(path) {
				return Ask, nil
			}
		} else if isFileTool(toolName) {
			path, _ := extractFilePath(input)
			if path != "" && !p.sandbox.Allowed(path) && isSensitivePath(path) {
				return Ask, nil
			}
		}
		return Allow, nil
	case PlanMode:
		// Plan mode: mode control tools + read-only tools allowed, everything else denied
		// enter_plan_mode is always allowed (entering read-only mode is safe).
		// exit_plan_mode requires user confirmation in supervised mode — the plan
		// determines what code changes the agent will make next.
		if toolName == "enter_plan_mode" {
			return Allow, nil
		}
		if toolName == "exit_plan_mode" {
			// The plan determines what code changes the agent will make next.
			// This is a user-level decision (execute this plan or not), not a
			// tool-permission decision. Only autopilot auto-approves; all other
			// modes require explicit user confirmation.
			var args struct {
				Mode string `json:"mode"`
			}
			_ = json.Unmarshal(input, &args)
			if args.Mode == "autopilot" {
				return Allow, nil
			}
			// If restoring from a previous autopilot mode, also auto-allow.
			if p.previousMode == AutopilotMode {
				return Allow, nil
			}
			return Ask, nil
		}
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
		if isCommandTool(toolName) {
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
		if isCommandTool(toolName) {
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
	if p.sandbox.Allowed(path) {
		return true
	}
	return p.readOnlySandbox != nil && p.readOnlySandbox.Allowed(path)
}

// AllowedPathForTool returns true if the path is allowed for the specific tool.
func (p *ConfigPolicy) AllowedPathForTool(toolName, path string) bool {
	if p.sandbox.Allowed(path) {
		return true
	}
	return isReadOnlyFileTool(toolName) && p.readOnlySandbox != nil && p.readOnlySandbox.Allowed(path)
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
	// When entering plan mode, remember the current mode so exit_plan_mode
	// can decide whether to ask for user confirmation (skip ask if restoring
	// to autopilot).
	if mode == PlanMode && p.mode != PlanMode {
		p.previousMode = p.mode
	}
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

// isSensitivePath returns true for paths that are system-critical or user-config.
func isSensitivePath(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	sensitiveFiles := []string{
		".bashrc", ".bash_profile", ".zshrc", ".zprofile", ".profile",
		".ssh/config", ".ssh/authorized_keys", ".ssh/id_rsa", ".ssh/id_ed25519",
		".gitconfig", ".gnupg",
	}
	for _, f := range sensitiveFiles {
		if strings.HasSuffix(path, f) || path == f {
			return true
		}
	}
	// Writing directly to $HOME root (e.g., ~/.somefile where somefile is not a known app)
	if strings.HasPrefix(path, home+"/") && !strings.Contains(strings.TrimPrefix(path, home+"/"), "/") {
		// Single file directly in home dir - could be sensitive
		base := strings.TrimPrefix(path, home+"/")
		if strings.HasPrefix(base, ".") && len(base) > 1 {
			return true
		}
	}
	return false
}

func isFileTool(name string) bool {
	switch name {
	case "read_file", "write_file", "edit_file", "list_directory", "search_files", "glob":
		return true
	}
	return false
}

func isReadOnlyFileTool(name string) bool {
	switch name {
	case "read_file", "list_directory", "search_files", "glob":
		return true
	}
	return false
}

// isWriteFileTool returns true for file tools that mutate disk (used for
// extra sandbox enforcement in bypass/autopilot modes).
func isWriteFileTool(name string) bool {
	switch name {
	case "write_file", "edit_file":
		return true
	}
	return false
}

func isModeControlTool(name string) bool {
	return name == "enter_plan_mode" || name == "exit_plan_mode"
}

func isCommandTool(name string) bool {
	switch name {
	case "run_command", "start_command", "write_command_input":
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
	if v, ok := m["input"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			return s, true
		}
	}
	return "", false
}

func truncateStr(s string, maxLen int) string {
	return util.Truncate(s, maxLen)
}

func newOptionalPathSandbox(allowedDirs []string) *PathSandbox {
	if len(allowedDirs) == 0 {
		return nil
	}
	return NewPathSandbox(allowedDirs)
}
