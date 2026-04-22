package permission

import (
	"strings"
)

// PermissionMode controls how the agent handles tool permissions.
type PermissionMode int

const (
	// SupervisedMode respects explicit per-tool rules and asks for anything unspecified.
	SupervisedMode PermissionMode = iota
	// PlanMode allows a strict read-only subset and denies writes/commands automatically.
	PlanMode
	// AutoMode allows safe operations and denies dangerous ones automatically.
	AutoMode
	// BypassMode allows almost everything automatically and only asks on critical cases.
	BypassMode
	// AutopilotMode uses bypass permissions and keeps going when the model asks the user to decide.
	AutopilotMode
)

func (m PermissionMode) String() string {
	switch m {
	case SupervisedMode:
		return "supervised"
	case PlanMode:
		return "plan"
	case AutoMode:
		return "auto"
	case BypassMode:
		return "bypass"
	case AutopilotMode:
		return "autopilot"
	default:
		return "supervised"
	}
}

// ParsePermissionMode parses a string to PermissionMode (case-insensitive).
func ParsePermissionMode(s string) PermissionMode {
	switch strings.ToLower(s) {
	case "plan":
		return PlanMode
	case "auto":
		return AutoMode
	case "bypass":
		return BypassMode
	case "autopilot":
		return AutopilotMode
	case "supervised":
		return SupervisedMode
	default:
		return SupervisedMode
	}
}

// ValidPermissionModes is the set of mode names accepted by ParsePermissionMode.
var ValidPermissionModes = []PermissionMode{
	SupervisedMode, PlanMode, AutoMode, BypassMode, AutopilotMode,
}

// IsValidPermissionMode returns true if s is a recognized mode name.
func IsValidPermissionMode(s string) bool {
	lower := strings.ToLower(s)
	for _, m := range ValidPermissionModes {
		if lower == strings.ToLower(m.String()) {
			return true
		}
	}
	return false
}

// Next returns the next mode in the cycle: supervised → plan → auto → bypass → autopilot → supervised.
func (m PermissionMode) Next() PermissionMode {
	switch m {
	case SupervisedMode:
		return PlanMode
	case PlanMode:
		return AutoMode
	case AutoMode:
		return BypassMode
	case BypassMode:
		return AutopilotMode
	default:
		return SupervisedMode
	}
}

// IsReadOnlyTool returns true if the tool is safe for Plan mode (read-only).
func IsReadOnlyTool(name string) bool {
	switch name {
	case "read_file", "list_directory", "search_files", "glob", "grep":
		return true
	case "lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols",
		"lsp_diagnostics", "lsp_workspace_symbols", "lsp_code_actions",
		"lsp_implementation", "lsp_prepare_call_hierarchy",
		"lsp_incoming_calls", "lsp_outgoing_calls":
		return true
	case "sleep", "git_status", "git_diff", "git_log",
		"web_fetch", "web_search",
		"task_list", "task_get", "plan_status",
		"cron_list", "list_commands", "read_command_output",
		"wait_command", "get_config":
		return true
	}
	return false
}
