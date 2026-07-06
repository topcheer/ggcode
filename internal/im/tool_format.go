package im

import (
	"fmt"
	"path/filepath"
	"strings"
)

func (a *tgAdapter) outboundText(event OutboundEvent) string {
	switch event.Kind {
	case OutboundEventText:
		return event.Text
	case OutboundEventStatus:
		return event.Status
	case OutboundEventToolCall:
		if event.ToolCall == nil {
			return ""
		}
		return formatToolCallText(event.ToolCall)
	case OutboundEventToolResult:
		if event.ToolRes == nil {
			return ""
		}
		return formatToolResultText(event.ToolRes)
	case OutboundEventApprovalRequest:
		if event.Approval == nil {
			return ""
		}
		return fmt.Sprintf("[approval] %s\n%s", event.Approval.ToolName, event.Approval.Input)
	case OutboundEventApprovalResult:
		if event.Result == nil {
			return ""
		}
		return fmt.Sprintf("[approval result] %s", event.Result.Decision)
	default:
		return ""
	}
}

// toolLang returns the ToolLanguage from a struct's Lang field, defaulting to zh-CN.
func toolLang(lang string) ToolLanguage {
	if lang == "en" {
		return ToolLangEn
	}
	return ToolLangZhCN
}

// formatToolCallText formats a tool call event into markdown text for IM delivery.
func formatToolCallText(tc *ToolCallInfo) string {
	lang := toolLang(tc.Lang)
	name := tc.ToolName
	args := tc.Args
	switch name {
	case "bash", "run_command", "start_command", "powershell":
		cmd := extractCommand(args)
		if cmd == "" {
			cmd = tc.Detail
		}
		return fmt.Sprintf("⚡ %s:\n```\n%s\n```", imLabel(lang, "run_command"), cmd)
	case "read_file":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		icon := imFileExtIcon(path)
		label := imFileTypeLabel(path)
		baseName := filepath.Base(path)
		rangeHint := imFormatReadRange(lang, args)
		var target string
		if label != "" {
			target = fmt.Sprintf("%s %s %s: `%s`", icon, imLabel(lang, "read"), label, baseName)
		} else {
			target = fmt.Sprintf("%s %s: `%s`", icon, imLabel(lang, "read_file"), path)
		}
		if rangeHint != "" {
			return target + " " + rangeHint
		}
		return target
	case "edit_file":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("✏️ %s: `%s`", imLabel(lang, "edit_file"), path)
	case "write_file":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("📝 %s: `%s`", imLabel(lang, "write_file"), path)
	case "glob":
		pattern := extractArgValue(args, "pattern")
		if pattern == "" {
			pattern = tc.Detail
		}
		return fmt.Sprintf("🔍 %s: `%s`", imLabel(lang, "find_files"), pattern)
	case "grep", "search_files":
		pattern := firstNonEmptyStr(extractArgValue(args, "pattern"), extractArgValue(args, "query"))
		if pattern == "" {
			pattern = tc.Detail
		}
		return fmt.Sprintf("🔍 %s: `%s`", imLabel(lang, "search"), pattern)
	case "list_directory":
		path := firstNonEmptyStr(extractArgValue(args, "path"), extractArgValue(args, "directory"))
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("📂 %s: `%s`", imLabel(lang, "list_directory"), path)
	case "web_fetch":
		url := extractArgValue(args, "url")
		if url == "" {
			url = tc.Detail
		}
		return fmt.Sprintf("🌐 %s: %s", imLabel(lang, "fetch"), url)
	case "web_search":
		q := extractArgValue(args, "query")
		if q == "" {
			q = tc.Detail
		}
		return fmt.Sprintf("🔍 %s: %s", imLabel(lang, "search"), q)
	case "todo_write":
		return fmt.Sprintf("📋 %s", imLabel(lang, "update_todos"))
	case "skill":
		return fmt.Sprintf("🔧 %s: `%s`", imLabel(lang, "load_skill"), tc.Detail)
	case "sleep":
		duration := formatSleepDuration(args)
		if duration == "" {
			duration = tc.Detail
		}
		return fmt.Sprintf("⏳ Sleep for %s", duration)
	case "cron_create":
		cronExpr := extractArgValue(args, "cron")
		if cronExpr == "" {
			cronExpr = tc.Detail
		}
		return fmt.Sprintf("⏰ Schedule: `%s`", cronExpr)
	case "cron_delete":
		return "⏰ Delete cron job"
	case "cron_list":
		return "⏰ List cron jobs"
	case "task_create", "task_get", "task_update", "task_list", "task_stop":
		return "" // hidden
	case "enter_plan_mode":
		return "📝 Planning..."
	case "exit_plan_mode":
		return "" // plan content sent as separate text via result
	case "enter_worktree":
		name := extractArgValue(args, "name")
		if name == "" {
			name = "new worktree"
		}
		return fmt.Sprintf("🌿 Enter worktree: %s", name)
	case "exit_worktree":
		action := extractArgValue(args, "action")
		return fmt.Sprintf("🌿 Exit worktree (%s)", action)
	// Team/swarm/a2a tools
	case "teammate_list", "swarm_task_list", "swarm_task_claim",
		"a2a_discover", "a2a_list_tasks", "a2a_cancel_task", "a2a_get_task":
		return "" // hidden
	case "team_create":
		name := extractArgValue(args, "name")
		if name == "" {
			name = tc.Detail
		}
		return fmt.Sprintf("👥 %s: %s", imLabel(lang, "team_create"), name)
	case "team_delete":
		return "👥 " + imLabel(lang, "team_delete")
	case "teammate_spawn":
		name := extractArgValue(args, "name")
		if name == "" {
			name = tc.Detail
		}
		return fmt.Sprintf("🤖 %s: %s", imLabel(lang, "teammate_spawn"), name)
	case "teammate_shutdown":
		return "🤖 " + imLabel(lang, "teammate_shutdown")
	case "send_message":
		to := extractArgValue(args, "to")
		if to == "" {
			to = tc.Detail
		}
		return fmt.Sprintf("📨 %s → %s", imLabel(lang, "send_message"), to)
	case "teammate_results":
		return "📋 " + imLabel(lang, "teammate_results")
	case "swarm_task_create":
		subject := extractArgValue(args, "subject")
		if subject == "" {
			subject = tc.Detail
		}
		return fmt.Sprintf("📋 %s: %s", imLabel(lang, "swarm_task_create"), subject)
	case "swarm_task_complete":
		return "✅ " + imLabel(lang, "swarm_task_complete")
	case "a2a_remote":
		target := extractArgValue(args, "target")
		if target == "" {
			target = tc.Detail
		}
		return fmt.Sprintf("🔗 %s → %s", imLabel(lang, "a2a_remote"), target)
	case "a2a_send_task":
		target := extractArgValue(args, "target")
		if target == "" {
			target = tc.Detail
		}
		return fmt.Sprintf("🔗 %s → %s", imLabel(lang, "a2a_send_task"), target)
	default:
		if tc.Detail != "" {
			return fmt.Sprintf("🔧 %s: `%s`", name, tc.Detail)
		}
		return fmt.Sprintf("🔧 %s", name)
	}
}

// formatToolResultText formats a tool result event into concise IM text,
// mirroring the terminal follow display style: icon + tool name + brief summary.
// Returns empty string if the tool result should be silently suppressed (e.g. read_file success).
func formatToolResultText(tr *ToolResultInfo) string {
	// Special formatting for certain tool types.
	// handled is true when formatSpecialIMToolResult has handled this tool
	// (including the "suppress" case like read_file success).
	handled, special := formatSpecialIMToolResult(tr)
	if handled {
		return special
	}

	// Default: prettified tool name
	pretty := prettifyToolName(tr.ToolName)
	output := strings.TrimSpace(tr.Result)
	if output != "" {
		return fmt.Sprintf("🔧 %s\n```\n%s\n```", pretty, output)
	}
	return fmt.Sprintf("🔧 %s", pretty)
}

// formatSpecialIMToolResult returns (handled, formatted) for special tool types.
// handled=true means this function has dealt with the tool (either producing output
// or intentionally suppressing it); handled=false means "use default formatting".
func formatSpecialIMToolResult(tr *ToolResultInfo) (bool, string) {
	switch tr.ToolName {
	case "run_command", "bash", "powershell":
		// Command tools always use the dedicated formatter (handles both success and error)
		return true, formatIMCommandResult(tr)
	case "todo_write":
		return true, formatIMTodoResult(tr)
	case "read_file":
		return true, formatIMReadFileResult(tr)
	case "list_directory":
		return true, formatIMListDirResult(tr)
	case "glob":
		return true, formatIMGlobResult(tr)
	case "edit_file":
		return true, formatIMEditResult(tr)
	case "write_file":
		return true, formatIMWriteResult(tr)
	case "search_files", "grep":
		return true, formatIMSearchResult(tr)
	case "web_fetch":
		return true, formatIMWebFetchResult(tr)
	case "web_search":
		return true, formatIMWebSearchResult(tr)
	case "git_diff", "git_status", "git_log":
		return true, formatIMGitResult(tr)
	case "ask_user":
		return true, formatIMAskUserResult(tr)
	case "start_command":
		return true, formatIMStartCommandResult(tr)
	case "stop_command":
		return true, "" // hidden
	case "read_command_output":
		return true, "" // hidden — result consumed internally
	case "wait_command":
		return true, "" // hidden
	case "write_command_input":
		return true, "" // hidden
	case "list_commands":
		return true, "" // hidden
	case "spawn_agent":
		return true, formatIMSpawnAgentResult(tr)
	case "wait_agent":
		return true, "" // hidden — result consumed by LLM
	case "list_agents":
		return true, "" // hidden
	case "list_mcp_capabilities", "get_mcp_prompt", "read_mcp_resource":
		return true, "" // hidden — internal MCP inspection tools
	case "skill":
		return true, formatIMSkillResult(tr)
	case "save_memory":
		return true, formatIMSaveMemoryResult(tr)
	case "sleep":
		return true, formatIMSleepResult(tr)
	case "cron_create":
		return true, formatIMCronCreateResult(tr)
	case "cron_delete":
		return true, "⏰ Cron job deleted"
	case "cron_list":
		return true, "" // hidden
	case "task_create", "task_get", "task_update", "task_list", "task_stop":
		return true, "" // hidden — internal LLM task tracking
	case "enter_plan_mode":
		return true, "" // hidden — shows system message instead
	case "exit_plan_mode":
		plan := extractArgValue(tr.Args, "plan")
		if plan == "" {
			plan = extractArgValue(tr.Detail, "plan")
		}
		if plan != "" {
			return true, plan
		}
		return true, ""
	case "enter_worktree":
		return true, formatIMWorktreeResult("🌿", tr)
	case "exit_worktree":
		return true, formatIMWorktreeResult("🌿", tr)
	// Team/swarm/a2a tools — hidden
	case "teammate_list", "swarm_task_list", "swarm_task_claim",
		"a2a_discover", "a2a_list_tasks", "a2a_cancel_task", "a2a_get_task":
		return true, ""
	// Team/swarm/a2a tools — header only
	case "team_create":
		name := extractArgValue(tr.Args, "name")
		if name == "" {
			name = tr.Detail
		}
		return true, fmt.Sprintf("👥 %s %s", imLabel(toolLang(tr.Lang), "team_created"), name)
	case "team_delete":
		return true, "👥 " + imLabel(toolLang(tr.Lang), "team_deleted")
	case "teammate_spawn":
		name := extractArgValue(tr.Args, "name")
		if name == "" {
			name = tr.Detail
		}
		return true, fmt.Sprintf("🤖 %s %s", imLabel(toolLang(tr.Lang), "teammate_created"), name)
	case "teammate_shutdown":
		return true, "🤖 " + imLabel(toolLang(tr.Lang), "teammate_shutdown_done")
	case "send_message":
		to := extractArgValue(tr.Args, "to")
		if to == "" {
			to = tr.Detail
		}
		return true, fmt.Sprintf("📨 %s → %s", imLabel(toolLang(tr.Lang), "message_sent"), to)
	case "swarm_task_create":
		subject := extractArgValue(tr.Args, "subject")
		if subject == "" {
			subject = tr.Detail
		}
		return true, fmt.Sprintf("📋 %s: %s", imLabel(toolLang(tr.Lang), "task_created"), subject)
	case "swarm_task_complete":
		return true, "✅ " + imLabel(toolLang(tr.Lang), "task_completed")
	case "a2a_remote":
		target := extractArgValue(tr.Args, "target")
		if target == "" {
			target = tr.Detail
		}
		return true, fmt.Sprintf("🔗 %s → %s", imLabel(toolLang(tr.Lang), "a2a_remote"), target)
	case "a2a_send_task":
		target := extractArgValue(tr.Args, "target")
		if target == "" {
			target = tr.Detail
		}
		return true, fmt.Sprintf("🔗 %s → %s", imLabel(toolLang(tr.Lang), "task_sent"), target)
	// Team/swarm/a2a tools — body markdown
	case "teammate_results":
		return true, formatIMTeammateResultsResult(tr)
	default:
		if tr.IsError {
			return true, formatIMErrorResult(tr)
		}
		// Check for MCP-style tool names (contain underscores or dots)
		if strings.Contains(tr.ToolName, "_") || strings.Contains(tr.ToolName, ".") {
			return true, formatIMMCPToolResult(tr)
		}
	case "git_show", "git_blame", "git_branch_list", "git_remote",
		"git_stash_list", "git_add", "git_commit", "git_stash":
		return true, "" // hidden — secondary git tools
	}
	// LSP tools → hidden
	if strings.HasPrefix(tr.ToolName, "lsp_") {
		return true, ""
	}
	return false, ""
}

func (a *WechatAdapter) outboundText(event OutboundEvent) string {
	return defaultOutboundText(event)
}

func (a *dingtalkAdapter) outboundText(event OutboundEvent) string {
	return defaultOutboundText(event)
}

// defaultOutboundText is the shared outboundText implementation used by adapters
// that do not need custom per-adapter formatting.
func defaultOutboundText(event OutboundEvent) string {
	switch event.Kind {
	case OutboundEventText:
		return event.Text
	case OutboundEventStatus:
		return event.Status
	case OutboundEventToolCall:
		if event.ToolCall == nil {
			return ""
		}
		return formatToolCallText(event.ToolCall)
	case OutboundEventToolResult:
		if event.ToolRes == nil {
			return ""
		}
		return formatToolResultText(event.ToolRes)
	case OutboundEventApprovalRequest:
		if event.Approval == nil {
			return ""
		}
		return fmt.Sprintf("[approval] %s\n%s", event.Approval.ToolName, event.Approval.Input)
	case OutboundEventApprovalResult:
		if event.Result == nil {
			return ""
		}
		return fmt.Sprintf("[approval result] %s", event.Result.Decision)
	default:
		return ""
	}
}
