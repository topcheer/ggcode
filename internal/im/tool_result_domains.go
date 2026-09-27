package im

// r168: formatSpecialIMToolResult domain split.
//
// The former single ~60-case switch in formatSpecialIMToolResult (cyclomatic
// complexity 89) is split into per-domain formatters. Each returns
// (text, handled); handled=false means "no case in this domain matched the
// tool name" and the dispatcher moves on to the next domain, finally falling
// through formatResultSystem — the shared default tail (error results, hidden
// LSP tools, MCP-style heuristic, and the (false, "") "use default
// formatting" exit). Case bodies were transcribed 1:1 from the pristine
// switch; behavior is byte-identical and pinned by TestFormatToolResultGolden
// (testdata/im_format_golden.txt).

import (
	"fmt"
	"strings"
)

// formatSpecialIMToolResult returns (handled, formatted) for special tool types.
// handled=true means this function has dealt with the tool (either producing output
// or intentionally suppressing it); handled=false means "use default formatting".
func formatSpecialIMToolResult(tr *ToolResultInfo) (bool, string) {
	if out, ok := formatResultShell(tr); ok {
		return true, out
	}
	if out, ok := formatResultFiles(tr); ok {
		return true, out
	}
	if out, ok := formatResultWeb(tr); ok {
		return true, out
	}
	if out, ok := formatResultGit(tr); ok {
		return true, out
	}
	if out, ok := formatResultPlanTask(tr); ok {
		return true, out
	}
	if out, ok := formatResultCron(tr); ok {
		return true, out
	}
	if out, ok := formatResultTeam(tr); ok {
		return true, out
	}
	if out, ok := formatResultAgent(tr); ok {
		return true, out
	}
	return formatResultSystem(tr)
}

// formatResultShell covers shell and background-command results — always the
// dedicated formatters (hidden tools return "" but still handled).
func formatResultShell(tr *ToolResultInfo) (string, bool) {
	switch tr.ToolName {
	case "run_command", "bash", "powershell":
		// Command tools always use the dedicated formatter (handles both success and error)
		return formatIMCommandResult(tr), true
	case "start_command":
		return formatIMStartCommandResult(tr), true
	case "stop_command":
		return "", true // hidden
	case "read_command_output":
		return "", true // hidden — result consumed internally
	case "wait_command":
		return "", true // hidden
	case "write_command_input":
		return "", true // hidden
	case "list_commands":
		return "", true // hidden
	}
	return "", false
}

// formatResultFiles covers file, directory and search tool results.
func formatResultFiles(tr *ToolResultInfo) (string, bool) {
	switch tr.ToolName {
	case "read_file":
		return formatIMReadFileResult(tr), true
	case "list_directory":
		return formatIMListDirResult(tr), true
	case "glob":
		return formatIMGlobResult(tr), true
	case "edit_file":
		return formatIMEditResult(tr), true
	case "write_file":
		return formatIMWriteResult(tr), true
	case "search_files", "grep":
		return formatIMSearchResult(tr), true
	case "multi_file_read":
		// Show a brief summary instead of hiding completely
		if tr.IsError {
			return formatIMErrorResult(tr), true
		}
		pretty := prettifyToolName(tr.ToolName)
		return fmt.Sprintf("📖 %s ✓", pretty), true
	case "multi_file_edit":
		return formatIMMultiEditResult(tr), true
	case "multi_file_write":
		return formatIMMultiWriteResult(tr), true
	case "notebook_edit":
		return formatIMNotebookEditResult(tr), true
	}
	return "", false
}

// formatResultWeb covers web tool results; browser/warp are hidden as verbose
// or IM-irrelevant.
func formatResultWeb(tr *ToolResultInfo) (string, bool) {
	switch tr.ToolName {
	case "web_fetch":
		return formatIMWebFetchResult(tr), true
	case "web_search":
		return formatIMWebSearchResult(tr), true
	case "browser":
		return "", true // hidden — result is typically very large HTML/image
	case "warp":
		// Hidden — terminal management results are not useful in IM
		return "", true
	}
	return "", false
}

// formatResultGit covers git operations — show stage/commit, hide others.
func formatResultGit(tr *ToolResultInfo) (string, bool) {
	switch tr.ToolName {
	case "git_diff", "git_status", "git_log":
		return formatIMGitResult(tr), true
	case "git_add":
		return "📦 " + imLabel(toolLang(tr.Lang), "git_staged"), true
	case "git_commit":
		return "💾 " + imLabel(toolLang(tr.Lang), "git_committed"), true
	case "git_show":
		return formatIMGitShowResult(tr), true
	case "git_blame":
		return "", true // hidden — secondary git tool
	case "git_branch_list":
		return "🌿 " + imLabel(toolLang(tr.Lang), "git_branch_list"), true
	case "git_remote":
		return "", true // hidden — secondary git tool
	case "git_stash_list":
		return "📦 " + imLabel(toolLang(tr.Lang), "git_stash_list"), true
	case "git_stash":
		return "📦 " + imLabel(toolLang(tr.Lang), "git_stash"), true
	}
	return "", false
}

// formatResultPlanTask covers plan-mode, worktree, mode-switch, todo and
// internal task-tracking results.
func formatResultPlanTask(tr *ToolResultInfo) (string, bool) {
	lang := toolLang(tr.Lang)
	switch tr.ToolName {
	case "todo_write":
		return formatIMTodoResult(tr), true
	case "enter_plan_mode":
		return "", true // hidden — shows system message instead
	case "exit_plan_mode":
		plan := extractArgValue(tr.Args, "plan")
		if plan == "" {
			// #1565 case C: tr.Detail is the human-readable display string
			// (describeTool text or "" in daemon mode), never JSON -
			// extractArgValue returned "" for it unconditionally, so the
			// fallback was dead code and the plan notification was
			// swallowed whenever Args lacked the field. Use Detail
			// directly, exactly like task_create/task_update above.
			plan = tr.Detail
		}
		if plan != "" {
			return plan, true
		}
		return "", true
	case "enter_worktree":
		return formatIMWorktreeResult("🌿", tr), true
	case "exit_worktree":
		return formatIMWorktreeResult("🌿", tr), true
	case "list_worktree":
		return formatIMWorktreeResult("📋", tr), true
	case "task_create":
		subject := extractArgValue(tr.Args, "subject")
		if subject == "" {
			subject = tr.Detail
		}
		return fmt.Sprintf("📋 %s: %s", imLabel(lang, "task_create"), subject), true
	case "task_get", "task_list", "task_output":
		return "", true // hidden — internal LLM task tracking
	case "task_update":
		// Show completion notification when task is marked completed
		status := extractArgValue(tr.Args, "status")
		if status == "completed" {
			subject := extractArgValue(tr.Args, "subject")
			if subject == "" {
				subject = tr.Detail
			}
			return fmt.Sprintf("✅ %s: %s", imLabel(lang, "task_create"), subject), true
		}
		return "", true // hidden — internal status updates
	case "task_stop":
		// Show stop notification
		return "⏹ " + imLabel(lang, "task_stopped"), true
	case "switch_mode":
		return "🔄 " + imLabel(lang, "mode_switched"), true
	}
	return "", false
}

// formatResultCron covers sleep and cron tool results.
func formatResultCron(tr *ToolResultInfo) (string, bool) {
	lang := toolLang(tr.Lang)
	switch tr.ToolName {
	case "sleep":
		return formatIMSleepResult(tr), true
	case "cron_create":
		return formatIMCronCreateResult(tr), true
	case "cron_delete":
		return "⏰ Cron job deleted", true
	case "cron_list":
		return "", true // hidden
	// Newer cron tools
	case "cron_update":
		return "⏰ " + imLabel(lang, "cron_update_done"), true
	case "cron_pause":
		return "⏸ " + imLabel(lang, "cron_paused"), true
	case "cron_resume":
		return "▶ " + imLabel(lang, "cron_resumed"), true
	case "cron_get":
		return "", true // hidden — detail consumed by LLM
	}
	return "", false
}

// formatResultTeam covers team/swarm/a2a results: hidden, header-only and
// body-markdown variants.
func formatResultTeam(tr *ToolResultInfo) (string, bool) {
	lang := toolLang(tr.Lang)
	switch tr.ToolName {
	// Team/swarm/a2a tools — hidden
	case "teammate_list", "swarm_task_list", "swarm_task_claim",
		"a2a_discover", "a2a_list_tasks", "a2a_cancel_task", "a2a_get_task":
		return "", true
	// Team/swarm/a2a tools — header only
	case "team_create":
		name := extractArgValue(tr.Args, "name")
		if name == "" {
			name = tr.Detail
		}
		return fmt.Sprintf("👥 %s %s", imLabel(lang, "team_created"), name), true
	case "team_delete":
		return "👥 " + imLabel(lang, "team_deleted"), true
	case "teammate_spawn":
		name := extractArgValue(tr.Args, "name")
		if name == "" {
			name = tr.Detail
		}
		return fmt.Sprintf("🤖 %s %s", imLabel(lang, "teammate_created"), name), true
	case "teammate_shutdown":
		return "🤖 " + imLabel(lang, "teammate_shutdown_done"), true
	case "send_message":
		to := extractArgValue(tr.Args, "to")
		if to == "" {
			to = tr.Detail
		}
		return fmt.Sprintf("📨 %s → %s", imLabel(lang, "message_sent"), to), true
	case "swarm_task_create":
		subject := extractArgValue(tr.Args, "subject")
		if subject == "" {
			subject = tr.Detail
		}
		return fmt.Sprintf("📋 %s: %s", imLabel(lang, "task_created"), subject), true
	case "swarm_task_complete":
		return "✅ " + imLabel(lang, "task_completed"), true
	case "a2a_remote":
		target := extractArgValue(tr.Args, "target")
		if target == "" {
			target = tr.Detail
		}
		return fmt.Sprintf("🔗 %s → %s", imLabel(lang, "a2a_remote"), target), true
	case "a2a_send_task":
		target := extractArgValue(tr.Args, "target")
		if target == "" {
			target = tr.Detail
		}
		return fmt.Sprintf("🔗 %s → %s", imLabel(lang, "task_sent"), target), true
	// Team/swarm/a2a tools — body markdown
	case "teammate_results":
		return formatIMTeammateResultsResult(tr), true
	}
	return "", false
}

// formatResultAgent covers sub-agent delegation, skill, memory, ask_user and
// internal MCP inspection results.
func formatResultAgent(tr *ToolResultInfo) (string, bool) {
	switch tr.ToolName {
	case "spawn_agent":
		return formatIMSpawnAgentResult(tr), true
	case "wait_agent":
		return "", true // hidden — result consumed by LLM
	case "list_agents":
		return "", true // hidden
	case "list_mcp_capabilities", "get_mcp_prompt", "read_mcp_resource":
		return "", true // hidden — internal MCP inspection tools
	case "skill":
		return formatIMSkillResult(tr), true
	case "save_memory":
		return formatIMSaveMemoryResult(tr), true
	case "delete_memory":
		return formatIMDeleteMemoryResult(tr), true
	case "ask_user":
		return formatIMAskUserResult(tr), true
	case "delegate":
		agent := extractArgValue(tr.Args, "agent")
		if agent == "" {
			agent = tr.Detail
		}
		return fmt.Sprintf("🤝 %s: %s", imLabel(toolLang(tr.Lang), "delegated_to"), agent), true
	case "lanchat":
		// Hidden on success — LAN chat coordination is internal
		if tr.IsError {
			return formatIMErrorResult(tr), true
		}
		return "", true
	}
	return "", false
}

// formatResultSystem is the shared default tail for every domain miss.
// LSP tools are hidden (result is structured data consumed by LLM) and must
// be checked BEFORE the MCP heuristic: every lsp_* tool name contains "_" and
// would otherwise be captured as an MCP tool, flooding IM with one
// "🔧 Lsp X(...)" message per call (#971).
func formatResultSystem(tr *ToolResultInfo) (bool, string) {
	if tr.IsError {
		return true, formatIMErrorResult(tr)
	}
	if strings.HasPrefix(tr.ToolName, "lsp_") {
		return true, ""
	}
	// Check for MCP-style tool names (contain underscores or dots)
	if strings.Contains(tr.ToolName, "_") || strings.Contains(tr.ToolName, ".") {
		return true, formatIMMCPToolResult(tr)
	}
	return false, ""
}
