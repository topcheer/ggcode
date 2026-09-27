package im

import (
	"fmt"
	"strings"
)

func (a *tgAdapter) outboundText(event OutboundEvent) string {
	return defaultOutboundText(event)
}

// toolLang returns the ToolLanguage from a struct's Lang field, defaulting to zh-CN.
func toolLang(lang string) ToolLanguage {
	if lang == "en" {
		return ToolLangEn
	}
	return ToolLangZhCN
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
		// When a tool-call start notification was already sent, strip the
		// tool name header from the result to avoid duplicate IM messages.
		if tr.CallNotified && special != "" {
			special = stripToolHeader(tr.ToolName, special)
		}
		return special
	}

	// Default: prettified tool name
	pretty := prettifyToolName(tr.ToolName)
	output := strings.TrimSpace(redactResult(tr.Result))
	if output != "" {
		if tr.CallNotified {
			// Only show the result output, no tool name header
			return imCodeBlock(output)
		}
		return fmt.Sprintf("🔧 %s\n%s", pretty, imCodeBlock(output))
	}
	return fmt.Sprintf("🔧 %s", pretty)
}

// stripToolHeader removes the tool name/icon prefix from a formatted result
// string when the tool-call start notification was already sent to IM.
// For example, "⚡ Run command\n```\noutput\n```" becomes "```\noutput\n```".
func stripToolHeader(toolName, formatted string) string {
	// If the result starts with an icon + label on the first line,
	// strip everything up to the first newline (or code block start).
	lines := strings.SplitN(formatted, "\n", 2)
	if len(lines) < 2 {
		return formatted // single-line result, keep as-is
	}
	rest := strings.TrimSpace(lines[1])
	if rest == "" {
		return formatted
	}
	return rest
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
	case "delete_memory":
		return true, formatIMDeleteMemoryResult(tr)
	case "sleep":
		return true, formatIMSleepResult(tr)
	case "cron_create":
		return true, formatIMCronCreateResult(tr)
	case "cron_delete":
		return true, "⏰ Cron job deleted"
	case "cron_list":
		return true, "" // hidden
	case "task_create":
		subject := extractArgValue(tr.Args, "subject")
		if subject == "" {
			subject = tr.Detail
		}
		return true, fmt.Sprintf("📋 %s: %s", imLabel(toolLang(tr.Lang), "task_create"), subject)
	case "task_get", "task_list", "task_output":
		return true, "" // hidden — internal LLM task tracking
	case "task_update":
		// Show completion notification when task is marked completed
		status := extractArgValue(tr.Args, "status")
		if status == "completed" {
			subject := extractArgValue(tr.Args, "subject")
			if subject == "" {
				subject = tr.Detail
			}
			return true, fmt.Sprintf("✅ %s: %s", imLabel(toolLang(tr.Lang), "task_create"), subject)
		}
		return true, "" // hidden — internal status updates
	case "task_stop":
		// Show stop notification
		return true, "⏹ " + imLabel(toolLang(tr.Lang), "task_stopped")
	case "enter_plan_mode":
		return true, "" // hidden — shows system message instead
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
			return true, plan
		}
		return true, ""
	case "enter_worktree":
		return true, formatIMWorktreeResult("🌿", tr)
	case "exit_worktree":
		return true, formatIMWorktreeResult("🌿", tr)
	case "list_worktree":
		return true, formatIMWorktreeResult("📋", tr)
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
	// Newer cron tools
	case "cron_update":
		return true, "⏰ " + imLabel(toolLang(tr.Lang), "cron_update_done")
	case "cron_pause":
		return true, "⏸ " + imLabel(toolLang(tr.Lang), "cron_paused")
	case "cron_resume":
		return true, "▶ " + imLabel(toolLang(tr.Lang), "cron_resumed")
	case "cron_get":
		return true, "" // hidden — detail consumed by LLM
	// Git operations — show stage/commit, hide others
	case "git_add":
		return true, "📦 " + imLabel(toolLang(tr.Lang), "git_staged")
	case "git_commit":
		return true, "💾 " + imLabel(toolLang(tr.Lang), "git_committed")
	case "git_show":
		return true, formatIMGitShowResult(tr)
	case "git_blame":
		return true, "" // hidden — secondary git tool
	case "git_branch_list":
		return true, "🌿 " + imLabel(toolLang(tr.Lang), "git_branch_list")
	case "git_remote":
		return true, "" // hidden — secondary git tool
	case "git_stash_list":
		return true, "📦 " + imLabel(toolLang(tr.Lang), "git_stash_list")
	case "git_stash":
		return true, "📦 " + imLabel(toolLang(tr.Lang), "git_stash")
	// Mode switching
	case "switch_mode":
		return true, "🔄 " + imLabel(toolLang(tr.Lang), "mode_switched")
	// Multi-file operations
	case "multi_file_read":
		// Show a brief summary instead of hiding completely
		if tr.IsError {
			return true, formatIMErrorResult(tr)
		}
		pretty := prettifyToolName(tr.ToolName)
		return true, fmt.Sprintf("📖 %s ✓", pretty)
	case "multi_file_edit":
		return true, formatIMMultiEditResult(tr)
	case "multi_file_write":
		return true, formatIMMultiWriteResult(tr)
	// Browser — hidden (verbose), notebook — show like edit_file
	case "browser":
		return true, "" // hidden — result is typically very large HTML/image
	case "notebook_edit":
		return true, formatIMNotebookEditResult(tr)
	case "delegate":
		agent := extractArgValue(tr.Args, "agent")
		if agent == "" {
			agent = tr.Detail
		}
		return true, fmt.Sprintf("🤝 %s: %s", imLabel(toolLang(tr.Lang), "delegated_to"), agent)
	case "lanchat":
		// Hidden on success — LAN chat coordination is internal
		if tr.IsError {
			return true, formatIMErrorResult(tr)
		}
		return true, ""
	case "warp":
		// Hidden — terminal management results are not useful in IM
		return true, ""
	default:
		if tr.IsError {
			return true, formatIMErrorResult(tr)
		}
		// LSP tools → hidden (result is structured data consumed by LLM).
		// Must be checked BEFORE the MCP heuristic below: every lsp_* tool
		// name contains "_" and would otherwise be captured as an MCP tool,
		// flooding IM with one "🔧 Lsp X(...)" message per call (#971).
		if strings.HasPrefix(tr.ToolName, "lsp_") {
			return true, ""
		}
		// Check for MCP-style tool names (contain underscores or dots)
		if strings.Contains(tr.ToolName, "_") || strings.Contains(tr.ToolName, ".") {
			return true, formatIMMCPToolResult(tr)
		}
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
		return fmt.Sprintf("[approval] %s\n%s", event.Approval.ToolName, redactResult(event.Approval.Input))
	case OutboundEventApprovalResult:
		if event.Result == nil {
			return ""
		}
		return fmt.Sprintf("[approval result] %s", event.Result.Decision)
	default:
		return ""
	}
}
