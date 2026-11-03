package im

import (
	"fmt"
	"path/filepath"
	"strings"
)

func (a *tgAdapter) outboundText(event OutboundEvent) string {
	return defaultOutboundText(event)
}

func toolLang(lang string) ToolLanguage {
	if lang == "en" {
		return ToolLangEn
	}
	return ToolLangZhCN
}

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
		return fmt.Sprintf("⚡ %s:\n%s", imLabel(lang, "run_command"), imCodeBlock(cmd))
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
		return fmt.Sprintf("✏ %s: `%s`", imLabel(lang, "edit_file"), path)
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
	case "task_get", "task_update", "task_list", "task_stop", "task_output":
		return ""
	case "enter_plan_mode":
		return "📝 Planning..."
	case "exit_plan_mode":
		return ""

	case "task_create":
		subject := extractArgValue(args, "subject")
		if subject == "" {
			subject = tc.Detail
		}
		return fmt.Sprintf("📋 %s: %s", imLabel(lang, "task_create"), subject)

	case "lsp_definition":
		return "🔍 " + imLabel(lang, "lsp_definition")
	case "lsp_references":
		return "🔍 " + imLabel(lang, "lsp_references")
	case "lsp_hover":
		return "🔍 " + imLabel(lang, "lsp_hover")
	case "lsp_diagnostics":
		return "🔍 " + imLabel(lang, "lsp_diagnostics")
	case "lsp_rename":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("✏ %s: `%s`", imLabel(lang, "lsp_rename"), path)
	case "lsp_symbols", "lsp_workspace_symbols":
		return "🔍 " + imLabel(lang, "lsp_symbols")
	case "lsp_implementation":
		return "🔍 " + imLabel(lang, "lsp_implementation")
	case "lsp_code_actions":
		return "🔧 " + imLabel(lang, "lsp_code_actions")
	case "enter_worktree":
		name := extractArgValue(args, "name")
		if name == "" {
			name = "new worktree"
		}
		return fmt.Sprintf("🌿 Enter worktree: %s", name)
	case "exit_worktree":
		action := extractArgValue(args, "action")
		return fmt.Sprintf("🌿 Exit worktree (%s)", action)
	case "list_worktree":
		return "📋 List worktrees"

	case "teammate_list", "swarm_task_list", "swarm_task_claim",
		"a2a_discover", "a2a_list_tasks", "a2a_cancel_task", "a2a_get_task":
		return ""
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

	case "multi_file_read":
		return fmt.Sprintf("📖 %s", imLabel(lang, "read_multi"))
	case "multi_file_edit":
		return fmt.Sprintf("✏ %s", imLabel(lang, "edit_multi"))
	case "multi_file_write":
		return fmt.Sprintf("📝 %s", imLabel(lang, "write_multi"))

	case "git_add":
		return fmt.Sprintf("📦 %s", imLabel(lang, "git_stage"))
	case "git_commit":
		msg := extractArgValue(args, "message")
		if msg == "" {
			msg = tc.Detail
		}
		return fmt.Sprintf("💾 %s: %s", imLabel(lang, "git_commit"), msg)
	case "git_show":
		return fmt.Sprintf("🔍 %s", imLabel(lang, "git_show"))
	case "git_blame":
		return fmt.Sprintf("🔍 %s", imLabel(lang, "git_blame"))
	case "git_branch_list":
		return fmt.Sprintf("🌿 %s", imLabel(lang, "git_branch_list"))

	case "browser":
		action := extractArgValue(args, "action")
		url := extractArgValue(args, "url")
		if url != "" {
			return fmt.Sprintf("🌐 %s: %s", imLabel(lang, "browser"), url)
		}
		if action != "" {
			return fmt.Sprintf("🌐 %s: %s", imLabel(lang, "browser"), action)
		}
		return fmt.Sprintf("🌐 %s", imLabel(lang, "browser"))

	case "switch_mode":
		mode := extractArgValue(args, "mode")
		if mode == "" {
			mode = tc.Detail
		}
		return fmt.Sprintf("🔄 %s → %s", imLabel(lang, "switch_mode"), mode)

	case "notebook_edit":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("📓 %s: `%s`", imLabel(lang, "edit_notebook"), path)

	case "delegate":
		agent := extractArgValue(args, "agent")
		if agent == "" {
			agent = tc.Detail
		}
		return fmt.Sprintf("🤝 %s: %s", imLabel(lang, "delegate"), agent)

	case "cron_update":
		return fmt.Sprintf("⏰ %s", imLabel(lang, "cron_update"))
	case "cron_pause":
		return fmt.Sprintf("⏸ %s", imLabel(lang, "cron_pause"))
	case "cron_resume":
		return fmt.Sprintf("▶ %s", imLabel(lang, "cron_resume"))
	case "cron_get":
		return fmt.Sprintf("⏰ %s", imLabel(lang, "cron_get"))

	case "spawn_agent":
		task := extractArgValue(args, "task")
		if task == "" {
			task = tc.Detail
		}
		if task != "" {
			return fmt.Sprintf("🚀 %s: %s", imLabel(lang, "spawn_agent"), truncateRunes(compactSingleLine(task), 60, "..."))
		}
		return fmt.Sprintf("🚀 %s", imLabel(lang, "spawn_agent"))
	case "wait_agent":
		return fmt.Sprintf("⏳ %s", imLabel(lang, "wait_agent"))

	case "save_memory":
		key := extractArgValue(args, "key")
		if key == "" {
			key = tc.Detail
		}
		return fmt.Sprintf("💾 %s: `%s`", imLabel(lang, "save_memory"), key)
	case "delete_memory":
		key := extractArgValue(args, "key")
		if key == "" {
			key = tc.Detail
		}
		return fmt.Sprintf("🗑️ %s: `%s`", imLabel(lang, "delete_memory"), key)
	case "config":
		setting := extractArgValue(args, "setting")
		if setting == "" {
			setting = tc.Detail
		}
		return fmt.Sprintf("⚙ %s: `%s`", imLabel(lang, "config"), setting)

	case "screenshot":
		return "📸 Screenshot"
	case "mobile_device":
		action := extractArgValue(args, "action")
		if action == "" {
			action = tc.Detail
		}
		return fmt.Sprintf("📱 %s: %s", imLabel(lang, "mobile_device"), action)

	case "im":
		action := extractArgValue(args, "action")
		if action == "" {
			action = tc.Detail
		}
		return fmt.Sprintf("💬 %s: %s", imLabel(lang, "im_manage"), action)

	case "git_diff":
		return fmt.Sprintf("🔍 %s", imLabel(lang, "git_diff"))
	case "git_status":
		return fmt.Sprintf("📊 %s", imLabel(lang, "git_status"))
	case "git_log":
		return fmt.Sprintf("📜 %s", imLabel(lang, "git_log"))
	case "git_remote":
		return fmt.Sprintf("🔗 %s", imLabel(lang, "git_remote"))
	case "git_stash", "git_stash_list":
		return fmt.Sprintf("📦 %s", imLabel(lang, "git_stash"))

	case "ask_user":
		return "❓ " + imLabel(lang, "ask_user")

	case "stop_command", "wait_command":
		return fmt.Sprintf("⏹ %s", imLabel(lang, "command_manage"))
	case "write_command_input":
		return fmt.Sprintf("⌨ %s", imLabel(lang, "command_input"))
	case "cancel_agent":
		agentID := extractArgValue(args, "agent_id")
		if agentID == "" {
			agentID = tc.Detail
		}
		return fmt.Sprintf("❌ %s: %s", imLabel(lang, "cancel_agent"), agentID)
	case "lanchat":
		action := extractArgValue(args, "action")
		recipient := firstNonEmptyStr(extractArgValue(args, "to"), extractArgValue(args, "team"))
		if recipient != "" {
			return fmt.Sprintf("💬 LAN Chat %s → %s", action, recipient)
		}
		return fmt.Sprintf("💬 LAN Chat %s", action)
	case "warp":
		warpAction := extractArgValue(args, "action")
		if warpAction != "" {
			return fmt.Sprintf("🖥 Warp: %s", warpAction)
		}
		return "🖥 Warp"
	default:

		pretty := prettifyToolName(name)
		if tc.Detail != "" {
			return fmt.Sprintf("🔧 %s: `%s`", pretty, tc.Detail)
		}
		return fmt.Sprintf("🔧 %s", pretty)
	}
}

func formatToolResultText(tr *ToolResultInfo) string {

	handled, special := formatSpecialIMToolResult(tr)
	if handled {

		if tr.CallNotified && special != "" {
			special = stripToolHeader(tr.ToolName, special)
		}
		return special
	}

	pretty := prettifyToolName(tr.ToolName)
	output := strings.TrimSpace(redactResult(tr.Result))
	if output != "" {
		if tr.CallNotified {

			return imCodeBlock(output)
		}
		return fmt.Sprintf("🔧 %s\n%s", pretty, imCodeBlock(output))
	}
	return fmt.Sprintf("🔧 %s", pretty)
}

func stripToolHeader(toolName, formatted string) string {

	lines := strings.SplitN(formatted, "\n", 2)
	if len(lines) < 2 {
		return formatted
	}
	rest := strings.TrimSpace(lines[1])
	if rest == "" {
		return formatted
	}
	return rest
}

func formatSpecialIMToolResult(tr *ToolResultInfo) (bool, string) {
	switch tr.ToolName {
	case "run_command", "bash", "powershell":

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
		return true, ""
	case "read_command_output":
		return true, ""
	case "wait_command":
		return true, ""
	case "write_command_input":
		return true, ""
	case "list_commands":
		return true, ""
	case "spawn_agent":
		return true, formatIMSpawnAgentResult(tr)
	case "wait_agent":
		return true, ""
	case "list_agents":
		return true, ""
	case "list_mcp_capabilities", "get_mcp_prompt", "read_mcp_resource":
		return true, ""
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
		return true, ""
	case "task_create":
		subject := extractArgValue(tr.Args, "subject")
		if subject == "" {
			subject = tr.Detail
		}
		return true, fmt.Sprintf("📋 %s: %s", imLabel(toolLang(tr.Lang), "task_create"), subject)
	case "task_get", "task_list", "task_output":
		return true, ""
	case "task_update":

		status := extractArgValue(tr.Args, "status")
		if status == "completed" {
			subject := extractArgValue(tr.Args, "subject")
			if subject == "" {
				subject = tr.Detail
			}
			return true, fmt.Sprintf("✅ %s: %s", imLabel(toolLang(tr.Lang), "task_create"), subject)
		}
		return true, ""
	case "task_stop":

		return true, "⏹ " + imLabel(toolLang(tr.Lang), "task_stopped")
	case "enter_plan_mode":
		return true, ""
	case "exit_plan_mode":
		plan := extractArgValue(tr.Args, "plan")
		if plan == "" {

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

	case "teammate_list", "swarm_task_list", "swarm_task_claim",
		"a2a_discover", "a2a_list_tasks", "a2a_cancel_task", "a2a_get_task":
		return true, ""

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

	case "teammate_results":
		return true, formatIMTeammateResultsResult(tr)

	case "cron_update":
		return true, "⏰ " + imLabel(toolLang(tr.Lang), "cron_update_done")
	case "cron_pause":
		return true, "⏸ " + imLabel(toolLang(tr.Lang), "cron_paused")
	case "cron_resume":
		return true, "▶ " + imLabel(toolLang(tr.Lang), "cron_resumed")
	case "cron_get":
		return true, ""

	case "git_add":
		return true, "📦 " + imLabel(toolLang(tr.Lang), "git_staged")
	case "git_commit":
		return true, "💾 " + imLabel(toolLang(tr.Lang), "git_committed")
	case "git_show":
		return true, formatIMGitShowResult(tr)
	case "git_blame":
		return true, ""
	case "git_branch_list":
		return true, "🌿 " + imLabel(toolLang(tr.Lang), "git_branch_list")
	case "git_remote":
		return true, ""
	case "git_stash_list":
		return true, "📦 " + imLabel(toolLang(tr.Lang), "git_stash_list")
	case "git_stash":
		return true, "📦 " + imLabel(toolLang(tr.Lang), "git_stash")

	case "switch_mode":
		return true, "🔄 " + imLabel(toolLang(tr.Lang), "mode_switched")

	case "multi_file_read":

		if tr.IsError {
			return true, formatIMErrorResult(tr)
		}
		pretty := prettifyToolName(tr.ToolName)
		return true, fmt.Sprintf("📖 %s ✓", pretty)
	case "multi_file_edit":
		return true, formatIMMultiEditResult(tr)
	case "multi_file_write":
		return true, formatIMMultiWriteResult(tr)

	case "browser":
		return true, ""
	case "notebook_edit":
		return true, formatIMNotebookEditResult(tr)
	case "delegate":
		agent := extractArgValue(tr.Args, "agent")
		if agent == "" {
			agent = tr.Detail
		}
		return true, fmt.Sprintf("🤝 %s: %s", imLabel(toolLang(tr.Lang), "delegated_to"), agent)
	case "lanchat":

		if tr.IsError {
			return true, formatIMErrorResult(tr)
		}
		return true, ""
	case "warp":

		return true, ""
	default:
		if tr.IsError {
			return true, formatIMErrorResult(tr)
		}

		if strings.HasPrefix(tr.ToolName, "lsp_") {
			return true, ""
		}

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
