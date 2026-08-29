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
	case "read_file", "multi_file_read", "list_directory", "search_files", "glob", "grep", "code_search":
		return true
	case "lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols",
		"lsp_diagnostics", "lsp_workspace_symbols", "lsp_code_actions",
		"lsp_implementation", "lsp_prepare_call_hierarchy",
		"lsp_incoming_calls", "lsp_outgoing_calls", "lsp_document_highlights":
		return true
	case "sleep", "git_status", "git_diff", "git_log", "git_show",
		"git_blame", "git_branch_list", "git_remote", "git_stash_list",
		"web_fetch", "web_search",
		"task_list", "task_get", "plan_status",
		"cron_list", "cron_get", "list_commands", "read_command_output",
		"wait_command", "get_config", "runtime", "code_execution":
		// code_execution (PTC) is read-only: it only calls tools from its
		// readOnlyToolNames whitelist, which are themselves read-only.
		return true
	}
	// #596-P2: MCP tools are NOT automatically read-only in plan mode.
	// Default to Ask (non-read-only) unless on an explicit whitelist.
	// This prevents write operations (drop_table, apply_patch, send_message)
	// from bypassing plan mode restrictions.
	if strings.HasPrefix(name, "mcp__") {
		// Whitelist of known read-only MCP tools. These have been verified
		// to be safe in plan mode or only perform read operations.
		readOnlyWhitelist := map[string]bool{
			"mcp__web_reader__webReader":              true,
			"mcp__web-search-prime__web_search_prime": true,
			// Add more as they are verified to be read-only
		}
		return readOnlyWhitelist[name]
	}
	return false
}

// IsAlwaysAllowedTool returns true if the tool is safe to run without approval
// in ALL permission modes, including plan mode. These are tools that have no
// side effects on the local filesystem or system state — they communicate with
// external services (LAN Chat) or are purely informational.
// #1283: "im" was removed from this list — im send_file reads and uploads
// ARBITRARY local files to IM channels (prompt injection in plan mode could
// exfiltrate screenshots/credential images with zero approval, and a user's
// explicit tools.im deny rule was bypassed because this check ran first).
// Benign im actions (status/mute/unmute/enable/disable) are still
// fast-pathed, action-aware, in Check.
func IsAlwaysAllowedTool(name string) bool {
	switch name {
	case "lanchat", "switch_mode", "runtime":
		return true
	}
	return false
}
