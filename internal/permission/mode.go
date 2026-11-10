package permission

import (
	"strings"
)

type PermissionMode int

const (
	SupervisedMode PermissionMode = iota

	PlanMode

	AutoMode

	BypassMode

	AutopilotMode
)

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

func (m PermissionMode) IsCruiseMode() bool {
	return m == BypassMode || m == AutopilotMode
}

func ParsePermissionMode(s string) PermissionMode {
	switch strings.ToLower(s) {
	case "plan":
		return PlanMode
	case "auto":
		return SupervisedMode
	case "autopilot":
		return BypassMode
	case "bypass":
		return BypassMode
	case "supervised":
		return SupervisedMode
	default:
		return SupervisedMode
	}
}

var ValidPermissionModes = []PermissionMode{
	SupervisedMode, PlanMode, AutoMode, BypassMode, AutopilotMode,
}

func IsValidPermissionMode(s string) bool {
	switch strings.ToLower(s) {
	case "supervised", "plan", "auto", "bypass", "autopilot":
		return true
	}
	return false
}

func (m PermissionMode) Next() PermissionMode {
	switch m {
	case SupervisedMode, AutoMode:
		return BypassMode
	default:
		return SupervisedMode
	}
}

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
		"git_blame", "git_branch_list", "git_remote", "git_stash_list", "pr_delta",
		"web_fetch", "web_search",
		"task_list", "task_get", "plan_status",
		"cron_list", "cron_get", "list_commands", "read_command_output",
		"wait_command", "get_config", "runtime", "code_execution":

		return true
	}

	if strings.HasPrefix(name, "mcp__") {

		readOnlyWhitelist := map[string]bool{
			"mcp__web_reader__webReader":              true,
			"mcp__web-search-prime__web_search_prime": true,
		}
		return readOnlyWhitelist[name]
	}
	return false
}

func IsAlwaysAllowedTool(name string) bool {
	switch name {
	case "lanchat", "switch_mode", "runtime":
		return true

	case "compact_context":
		return true

	case "recall_compacted":
		return true
	}
	return false
}
