package im

// r167: formatToolCallText domain split.
//
// The former single 86-case switch in formatToolCallText (cyclomatic
// complexity 116) is split into per-domain formatters. Each returns
// (text, handled); handled=false means "no case in this domain matched the
// tool name" and the dispatcher moves on to the next domain, finally falling
// back to prettifyToolName. Case bodies were transcribed 1:1 from the
// pristine switch; behavior is byte-identical and pinned by
// TestFormatToolCallGolden (testdata/im_format_golden.txt).

import (
	"fmt"
	"path/filepath"
)

// formatToolCallText formats a tool call event into markdown text for IM delivery.
func formatToolCallText(tc *ToolCallInfo) string {
	lang := toolLang(tc.Lang)
	if out, ok := formatCallShell(lang, tc); ok {
		return out
	}
	if out, ok := formatCallFiles(lang, tc); ok {
		return out
	}
	if out, ok := formatCallWeb(lang, tc); ok {
		return out
	}
	if out, ok := formatCallPlanTask(lang, tc); ok {
		return out
	}
	if out, ok := formatCallCron(lang, tc); ok {
		return out
	}
	if out, ok := formatCallLSP(lang, tc); ok {
		return out
	}
	if out, ok := formatCallWorktree(lang, tc); ok {
		return out
	}
	if out, ok := formatCallTeam(lang, tc); ok {
		return out
	}
	if out, ok := formatCallGit(lang, tc); ok {
		return out
	}
	if out, ok := formatCallMemoryConfig(lang, tc); ok {
		return out
	}
	if out, ok := formatCallSystem(lang, tc); ok {
		return out
	}
	// Prettify tool names (especially MCP tools like mcp__cf__search → Mcp Cf Search)
	pretty := prettifyToolName(tc.ToolName)
	if tc.Detail != "" {
		return fmt.Sprintf("🔧 %s: `%s`", pretty, tc.Detail)
	}
	return fmt.Sprintf("🔧 %s", pretty)
}

// formatCallShell covers command execution tools.
func formatCallShell(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	switch tc.ToolName {
	case "bash", "run_command", "start_command", "powershell":
		cmd := extractCommand(tc.Args)
		if cmd == "" {
			cmd = tc.Detail
		}
		return fmt.Sprintf("⚡ %s:\n%s", imLabel(lang, "run_command"), imCodeBlock(cmd)), true
	}
	return "", false
}

// formatCallFiles covers file read/write/edit/search/listing tools.
func formatCallFiles(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
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
			return target + " " + rangeHint, true
		}
		return target, true
	case "edit_file":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("✏ %s: `%s`", imLabel(lang, "edit_file"), path), true
	case "write_file":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("📝 %s: `%s`", imLabel(lang, "write_file"), path), true
	case "glob":
		pattern := extractArgValue(args, "pattern")
		if pattern == "" {
			pattern = tc.Detail
		}
		return fmt.Sprintf("🔍 %s: `%s`", imLabel(lang, "find_files"), pattern), true
	case "grep", "search_files":
		pattern := firstNonEmptyStr(extractArgValue(args, "pattern"), extractArgValue(args, "query"))
		if pattern == "" {
			pattern = tc.Detail
		}
		return fmt.Sprintf("🔍 %s: `%s`", imLabel(lang, "search"), pattern), true
	case "list_directory":
		path := firstNonEmptyStr(extractArgValue(args, "path"), extractArgValue(args, "directory"))
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("📂 %s: `%s`", imLabel(lang, "list_directory"), path), true
	case "multi_file_read":
		return fmt.Sprintf("📖 %s", imLabel(lang, "read_multi")), true
	case "multi_file_edit":
		return fmt.Sprintf("✏ %s", imLabel(lang, "edit_multi")), true
	case "multi_file_write":
		return fmt.Sprintf("📝 %s", imLabel(lang, "write_multi")), true
	case "notebook_edit":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("📓 %s: `%s`", imLabel(lang, "edit_notebook"), path), true
	}
	return "", false
}

// formatCallWeb covers web fetch/search and browser automation.
func formatCallWeb(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
	case "web_fetch":
		url := extractArgValue(args, "url")
		if url == "" {
			url = tc.Detail
		}
		return fmt.Sprintf("🌐 %s: %s", imLabel(lang, "fetch"), url), true
	case "web_search":
		q := extractArgValue(args, "query")
		if q == "" {
			q = tc.Detail
		}
		return fmt.Sprintf("🔍 %s: %s", imLabel(lang, "search"), q), true
	case "browser":
		action := extractArgValue(args, "action")
		url := extractArgValue(args, "url")
		if url != "" {
			return fmt.Sprintf("🌐 %s: %s", imLabel(lang, "browser"), url), true
		}
		if action != "" {
			return fmt.Sprintf("🌐 %s: %s", imLabel(lang, "browser"), action), true
		}
		return fmt.Sprintf("🌐 %s", imLabel(lang, "browser")), true
	}
	return "", false
}

// formatCallPlanTask covers todo/plan/skill/sleep and task-tracking tools.
// Internal task trackers stay hidden; creation/completion are shown.
func formatCallPlanTask(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
	case "todo_write":
		return fmt.Sprintf("📋 %s", imLabel(lang, "update_todos")), true
	case "skill":
		return fmt.Sprintf("🔧 %s: `%s`", imLabel(lang, "load_skill"), tc.Detail), true
	case "sleep":
		duration := formatSleepDuration(args)
		if duration == "" {
			duration = tc.Detail
		}
		return fmt.Sprintf("⏳ Sleep for %s", duration), true
	case "task_get", "task_update", "task_list", "task_stop", "task_output":
		return "", true // hidden
	case "enter_plan_mode":
		return "📝 Planning...", true
	case "exit_plan_mode":
		return "", true // plan content sent as separate text via result
	case "task_create":
		subject := extractArgValue(args, "subject")
		if subject == "" {
			subject = tc.Detail
		}
		return fmt.Sprintf("📋 %s: %s", imLabel(lang, "task_create"), subject), true
	}
	return "", false
}

// formatCallCron covers scheduled-job tools.
func formatCallCron(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
	case "cron_create":
		cronExpr := extractArgValue(args, "cron")
		if cronExpr == "" {
			cronExpr = tc.Detail
		}
		return fmt.Sprintf("⏰ Schedule: `%s`", cronExpr), true
	case "cron_delete":
		return "⏰ Delete cron job", true
	case "cron_list":
		return "⏰ List cron jobs", true
	case "cron_update":
		return fmt.Sprintf("⏰ %s", imLabel(lang, "cron_update")), true
	case "cron_pause":
		return fmt.Sprintf("⏸ %s", imLabel(lang, "cron_pause")), true
	case "cron_resume":
		return fmt.Sprintf("▶ %s", imLabel(lang, "cron_resume")), true
	case "cron_get":
		return fmt.Sprintf("⏰ %s", imLabel(lang, "cron_get")), true
	}
	return "", false
}

// formatCallLSP covers language-server tools with concise operation labels.
func formatCallLSP(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
	case "lsp_definition":
		return "🔍 " + imLabel(lang, "lsp_definition"), true
	case "lsp_references":
		return "🔍 " + imLabel(lang, "lsp_references"), true
	case "lsp_hover":
		return "🔍 " + imLabel(lang, "lsp_hover"), true
	case "lsp_diagnostics":
		return "🔍 " + imLabel(lang, "lsp_diagnostics"), true
	case "lsp_rename":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("✏ %s: `%s`", imLabel(lang, "lsp_rename"), path), true
	case "lsp_symbols", "lsp_workspace_symbols":
		return "🔍 " + imLabel(lang, "lsp_symbols"), true
	case "lsp_implementation":
		return "🔍 " + imLabel(lang, "lsp_implementation"), true
	case "lsp_code_actions":
		return "🔧 " + imLabel(lang, "lsp_code_actions"), true
	}
	return "", false
}

// formatCallWorktree covers git worktree lifecycle tools.
func formatCallWorktree(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
	case "enter_worktree":
		name := extractArgValue(args, "name")
		if name == "" {
			name = "new worktree"
		}
		return fmt.Sprintf("🌿 Enter worktree: %s", name), true
	case "exit_worktree":
		action := extractArgValue(args, "action")
		return fmt.Sprintf("🌿 Exit worktree (%s)", action), true
	case "list_worktree":
		return "📋 List worktrees", true
	}
	return "", false
}

// formatCallTeam covers team/swarm/a2a/delegation and agent lifecycle tools.
func formatCallTeam(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
	case "teammate_list", "swarm_task_list", "swarm_task_claim",
		"a2a_discover", "a2a_list_tasks", "a2a_cancel_task", "a2a_get_task":
		return "", true // hidden
	case "team_create":
		name := extractArgValue(args, "name")
		if name == "" {
			name = tc.Detail
		}
		return fmt.Sprintf("👥 %s: %s", imLabel(lang, "team_create"), name), true
	case "team_delete":
		return "👥 " + imLabel(lang, "team_delete"), true
	case "teammate_spawn":
		name := extractArgValue(args, "name")
		if name == "" {
			name = tc.Detail
		}
		return fmt.Sprintf("🤖 %s: %s", imLabel(lang, "teammate_spawn"), name), true
	case "teammate_shutdown":
		return "🤖 " + imLabel(lang, "teammate_shutdown"), true
	case "send_message":
		to := extractArgValue(args, "to")
		if to == "" {
			to = tc.Detail
		}
		return fmt.Sprintf("📨 %s → %s", imLabel(lang, "send_message"), to), true
	case "teammate_results":
		return "📋 " + imLabel(lang, "teammate_results"), true
	case "swarm_task_create":
		subject := extractArgValue(args, "subject")
		if subject == "" {
			subject = tc.Detail
		}
		return fmt.Sprintf("📋 %s: %s", imLabel(lang, "swarm_task_create"), subject), true
	case "swarm_task_complete":
		return "✅ " + imLabel(lang, "swarm_task_complete"), true
	case "a2a_remote":
		target := extractArgValue(args, "target")
		if target == "" {
			target = tc.Detail
		}
		return fmt.Sprintf("🔗 %s → %s", imLabel(lang, "a2a_remote"), target), true
	case "a2a_send_task":
		target := extractArgValue(args, "target")
		if target == "" {
			target = tc.Detail
		}
		return fmt.Sprintf("🔗 %s → %s", imLabel(lang, "a2a_send_task"), target), true
	case "delegate":
		agent := extractArgValue(args, "agent")
		if agent == "" {
			agent = tc.Detail
		}
		return fmt.Sprintf("🤝 %s: %s", imLabel(lang, "delegate"), agent), true
	case "spawn_agent":
		task := extractArgValue(args, "task")
		if task == "" {
			task = tc.Detail
		}
		if task != "" {
			return fmt.Sprintf("🚀 %s: %s", imLabel(lang, "spawn_agent"), truncateRunes(compactSingleLine(task), 60, "...")), true
		}
		return fmt.Sprintf("🚀 %s", imLabel(lang, "spawn_agent")), true
	case "wait_agent":
		return fmt.Sprintf("⏳ %s", imLabel(lang, "wait_agent")), true
	case "cancel_agent":
		agentID := extractArgValue(args, "agent_id")
		if agentID == "" {
			agentID = tc.Detail
		}
		return fmt.Sprintf("❌ %s: %s", imLabel(lang, "cancel_agent"), agentID), true
	}
	return "", false
}

// formatCallGit covers git operation tools.
func formatCallGit(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
	case "git_add":
		return fmt.Sprintf("📦 %s", imLabel(lang, "git_stage")), true
	case "git_commit":
		msg := extractArgValue(args, "message")
		if msg == "" {
			msg = tc.Detail
		}
		return fmt.Sprintf("💾 %s: %s", imLabel(lang, "git_commit"), msg), true
	case "git_show":
		return fmt.Sprintf("🔍 %s", imLabel(lang, "git_show")), true
	case "git_blame":
		return fmt.Sprintf("🔍 %s", imLabel(lang, "git_blame")), true
	case "git_branch_list":
		return fmt.Sprintf("🌿 %s", imLabel(lang, "git_branch_list")), true
	case "git_diff":
		return fmt.Sprintf("🔍 %s", imLabel(lang, "git_diff")), true
	case "git_status":
		return fmt.Sprintf("📊 %s", imLabel(lang, "git_status")), true
	case "git_log":
		return fmt.Sprintf("📜 %s", imLabel(lang, "git_log")), true
	case "git_remote":
		return fmt.Sprintf("🔗 %s", imLabel(lang, "git_remote")), true
	case "git_stash", "git_stash_list":
		return fmt.Sprintf("📦 %s", imLabel(lang, "git_stash")), true
	}
	return "", false
}

// formatCallMemoryConfig covers memory and config management tools.
func formatCallMemoryConfig(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
	case "save_memory":
		key := extractArgValue(args, "key")
		if key == "" {
			key = tc.Detail
		}
		return fmt.Sprintf("💾 %s: `%s`", imLabel(lang, "save_memory"), key), true
	case "delete_memory":
		key := extractArgValue(args, "key")
		if key == "" {
			key = tc.Detail
		}
		return fmt.Sprintf("🗑️ %s: `%s`", imLabel(lang, "delete_memory"), key), true
	case "config":
		setting := extractArgValue(args, "setting")
		if setting == "" {
			setting = tc.Detail
		}
		return fmt.Sprintf("⚙ %s: `%s`", imLabel(lang, "config"), setting), true
	}
	return "", false
}

// formatCallSystem covers mode switching, visual/mobile, IM management,
// user interaction, and background-command management tools.
func formatCallSystem(lang ToolLanguage, tc *ToolCallInfo) (string, bool) {
	args := tc.Args
	switch tc.ToolName {
	case "switch_mode":
		mode := extractArgValue(args, "mode")
		if mode == "" {
			mode = tc.Detail
		}
		return fmt.Sprintf("🔄 %s → %s", imLabel(lang, "switch_mode"), mode), true
	case "screenshot":
		return "📸 Screenshot", true
	case "mobile_device":
		action := extractArgValue(args, "action")
		if action == "" {
			action = tc.Detail
		}
		return fmt.Sprintf("📱 %s: %s", imLabel(lang, "mobile_device"), action), true
	case "im":
		action := extractArgValue(args, "action")
		if action == "" {
			action = tc.Detail
		}
		return fmt.Sprintf("💬 %s: %s", imLabel(lang, "im_manage"), action), true
	case "ask_user":
		return "❓ " + imLabel(lang, "ask_user"), true
	case "stop_command", "wait_command":
		return fmt.Sprintf("⏹ %s", imLabel(lang, "command_manage")), true
	case "write_command_input":
		return fmt.Sprintf("⌨ %s", imLabel(lang, "command_input")), true
	case "lanchat":
		action := extractArgValue(args, "action")
		recipient := firstNonEmptyStr(extractArgValue(args, "to"), extractArgValue(args, "team"))
		if recipient != "" {
			return fmt.Sprintf("💬 LAN Chat %s → %s", action, recipient), true
		}
		return fmt.Sprintf("💬 LAN Chat %s", action), true
	case "warp":
		warpAction := extractArgValue(args, "action")
		if warpAction != "" {
			return fmt.Sprintf("🖥 Warp: %s", warpAction), true
		}
		return "🖥 Warp", true
	}
	return "", false
}
