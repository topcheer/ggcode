package permission

import (
	"strings"
)

// PermissionMode controls how the agent handles tool permissions.
//
// Mode-system refactor: there are now exactly TWO user-facing modes:
//
//   - supervised: most tool calls flow without prompting; only extremely
//     dangerous calls (system-level destruction, credential exfiltration,
//     out-of-sandbox writes) are gated. This absorbs the old "auto" mode,
//     which is now a parse-time alias.
//   - bypass: nothing is gated except system-level destructive commands
//     (the old "autopilot" permission surface). The autopilot cruise
//     control (goal declaration + strategist oversight) is enabled while
//     in bypass mode.
//
// plan is an internal mode: users cannot switch into it directly. It is
// entered/exited only by the enter_plan_mode/exit_plan_mode tools. Inside
// plan mode everything is allowed EXCEPT explicit file-writing tools
// (run_command and other shell access is permitted).
//
// The legacy enum values AutoMode and AutopilotMode are retained for
// wire/config compatibility (hundreds of call sites, stored configs) but
// normalize: String() reports the canonical name and every policy branch
// treats them as their canonical counterpart.
type PermissionMode int

const (
	// SupervisedMode gates only extremely dangerous calls; everything
	// else flows without prompting (absorbs the old AutoMode).
	SupervisedMode PermissionMode = iota
	// PlanMode is tool-entered read-planning mode: all tools allowed except
	// explicit file writers. Not user-switchable.
	PlanMode
	// AutoMode is a deprecated alias for SupervisedMode (config compat).
	AutoMode
	// BypassMode gates nothing except system-level destructive commands;
	// cruise control (strategist/goal) runs while in this mode.
	BypassMode
	// AutopilotMode is a deprecated alias for BypassMode (config compat).
	AutopilotMode
)

// String returns the canonical user-facing mode name. Deprecated aliases
// normalize to their canonical counterpart so stored configs and UI state
// converge on the two-mode system on next save.
func (m PermissionMode) String() string {
	switch m {
	case SupervisedMode, AutoMode:
		return "supervised"
	case PlanMode:
		return "plan"
	case BypassMode, AutopilotMode:
		return "bypass"
	default:
		return "supervised"
	}
}

// IsCruiseMode reports whether the autopilot cruise control (goal
// declaration, strategist oversight) is active. Cruise now rides on the
// bypass permission surface: the old standalone AutopilotMode is an alias.
func (m PermissionMode) IsCruiseMode() bool {
	return m == BypassMode || m == AutopilotMode
}

// ParsePermissionMode parses a string to PermissionMode (case-insensitive).
// Legacy names "auto" and "autopilot" are accepted and normalized.
func ParsePermissionMode(s string) PermissionMode {
	switch strings.ToLower(s) {
	case "plan":
		return PlanMode
	case "auto": // legacy alias
		return SupervisedMode
	case "autopilot": // legacy alias
		return BypassMode
	case "bypass":
		return BypassMode
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
// Legacy names "auto"/"autopilot" remain valid (config compat) even
// though they normalize to supervised/bypass.
func IsValidPermissionMode(s string) bool {
	switch strings.ToLower(s) {
	case "supervised", "plan", "auto", "bypass", "autopilot":
		return true
	}
	return false
}

// Next returns the next USER-switchable mode in the two-mode cycle:
// supervised → bypass → supervised. Plan is tool-internal and never part
// of the cycle; legacy alias values normalize into the cycle.
func (m PermissionMode) Next() PermissionMode {
	switch m {
	case SupervisedMode, AutoMode:
		return BypassMode
	default: // BypassMode, AutopilotMode, PlanMode (tool-internal)
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
	// compact_context (CAT): session-scoped context reclaim with no
	// filesystem or external side effects; see internal/tool/compact_context.go.
	case "compact_context":
		return true
	// recall_compacted (re-retrieval): read-only search of the session's
	// compacted-content archive; no side effects; see
	// internal/tool/recall_compacted.go.
	case "recall_compacted":
		return true
	}
	return false
}
