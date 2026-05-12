package im

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// imLabel returns a localized label string for IM tool display.
func imLabel(lang ToolLanguage, key string) string {
	switch lang {
	case ToolLangEn:
		switch key {
		case "run_command":
			return "Run command"
		case "read":
			return "Reading"
		case "read_file":
			return "Reading file"
		case "edit_file":
			return "Edit file"
		case "write_file":
			return "Write file"
		case "find_files":
			return "Find files"
		case "search":
			return "Search"
		case "list_directory":
			return "List directory"
		case "fetch":
			return "Fetch"
		case "update_todos":
			return "Update todos"
		case "load_skill":
			return "Load skill"
		case "pages":
			return "pages"
		case "lines_extracted":
			return "lines extracted"
		case "files":
			return "files"
		case "showing_first":
			return "showing first"
		case "lines":
			return "lines"
		case "from_line":
			return "from line"
		case "first_lines":
			return "lines"
		case "no_output":
			return "no output"
		case "no_matches":
			return "no matches"
		case "no_active_commands":
			return "no active commands"
		case "no_active_agents":
			return "no active agents"
		case "bg_command_started":
			return "Background command started"
		case "bg_command":
			return "Background command"
		case "command_stopped":
			return "Command stopped"
		case "stop_command":
			return "Stop command"
		case "read_output":
			return "Read output"
		case "no_new_output":
			return "no new output"
		case "wait_command":
			return "Wait command"
		case "command_done":
			return "Command completed"
		case "input_sent":
			return "Input sent"
		case "send_input":
			return "Send input"
		case "active_commands":
			return "Active commands"
		case "sub_task":
			return "Sub-task"
		case "sub_task_started":
			return "Sub-task started"
		case "sub_task_done":
			return "Sub-task completed"
		case "sub_task_list":
			return "Sub-task list"
		case "no_active_subtasks":
			return "no active sub-tasks"
		case "mcp_service":
			return "MCP service"
		case "mcp_service_list":
			return "MCP service list"
		case "mcp_prompt":
			return "MCP Prompt"
		case "resource_read":
			return "Resource read"
		case "resource_content":
			return "Resource content"
		case "skill_loaded":
			return "Skill loaded"
		case "skill_load":
			return "Skill load"
		case "memory_saved":
			return "Memory saved"
		case "memory_save":
			return "Memory save"
		case "reply_received":
			return "Reply received"
		case "todos":
			return "Todos"
		case "results":
			return "results"
		}
	default: // zh-CN
		switch key {
		case "run_command":
			return "执行命令"
		case "read":
			return "读取"
		case "read_file":
			return "读取文件"
		case "edit_file":
			return "编辑文件"
		case "write_file":
			return "写入文件"
		case "find_files":
			return "查找文件"
		case "search":
			return "搜索"
		case "list_directory":
			return "列出目录"
		case "fetch":
			return "抓取"
		case "update_todos":
			return "更新待办列表"
		case "load_skill":
			return "加载技能"
		case "pages":
			return "页"
		case "lines_extracted":
			return "行"
		case "files":
			return "个文件"
		case "showing_first":
			return "展示前"
		case "lines":
			return "行"
		case "from_line":
			return "从行"
		case "first_lines":
			return "前 %d 行"
		case "no_output":
			return "无输出"
		case "no_matches":
			return "无匹配"
		case "no_active_commands":
			return "无活动命令"
		case "no_active_agents":
			return "无活动子任务"
		case "bg_command_started":
			return "后台命令已启动"
		case "bg_command":
			return "后台命令"
		case "command_stopped":
			return "命令已停止"
		case "stop_command":
			return "停止命令"
		case "read_output":
			return "读取输出"
		case "no_new_output":
			return "无新输出"
		case "wait_command":
			return "等待命令"
		case "command_done":
			return "命令完成"
		case "input_sent":
			return "输入已发送"
		case "send_input":
			return "输入发送"
		case "active_commands":
			return "活动命令"
		case "sub_task":
			return "子任务"
		case "sub_task_started":
			return "子任务已启动"
		case "sub_task_done":
			return "子任务完成"
		case "sub_task_list":
			return "子任务列表"
		case "no_active_subtasks":
			return "无活动子任务"
		case "mcp_service":
			return "MCP 服务"
		case "mcp_service_list":
			return "MCP 服务列表"
		case "mcp_prompt":
			return "MCP Prompt"
		case "resource_read":
			return "资源读取"
		case "resource_content":
			return "资源内容"
		case "skill_loaded":
			return "技能已加载"
		case "skill_load":
			return "技能加载"
		case "memory_saved":
			return "记忆已保存"
		case "memory_save":
			return "记忆保存"
		case "reply_received":
			return "收到回复"
		case "todos":
			return "待办"
		case "results":
			return "条结果"
		}
	}
	return key
}

// formatIMAskUserResult renders ask_user result.
func formatIMAskUserResult(tr *ToolResultInfo) string {
	return fmt.Sprintf("💬 %s", imLabel(toolLang(tr.Lang), "reply_received"))
}

// --- Background command tools ---

// formatIMStartCommandResult renders start_command result.
func formatIMStartCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	cmd := extractCommand(tr.Args)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		if cmd != "" {
			return fmt.Sprintf("⚡ %s: %s\n```\n%s\n```", imLabel(lang, "bg_command"), cmd, output)
		}
		return fmt.Sprintf("⚡ %s\n```\n%s\n```", imLabel(lang, "bg_command"), output)
	}
	if output == "" {
		if cmd != "" {
			return fmt.Sprintf("⚡ %s\n```\n%s\n```", imLabel(lang, "bg_command_started"), cmd)
		}
		return fmt.Sprintf("⚡ %s", imLabel(lang, "bg_command_started"))
	}
	if cmd != "" {
		return fmt.Sprintf("⚡ %s: %s\n```\n%s\n```", imLabel(lang, "bg_command"), cmd, output)
	}
	return fmt.Sprintf("⚡ %s\n```\n%s\n```", imLabel(lang, "bg_command"), output)
}

// formatIMStopCommandResult renders stop_command result.
func formatIMStopCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("🛑 %s\n```\n%s\n```", imLabel(lang, "stop_command"), output)
	}
	if output == "" {
		return fmt.Sprintf("🛑 %s", imLabel(lang, "command_stopped"))
	}
	return fmt.Sprintf("🛑\n```\n%s\n```", output)
}

// formatIMReadCmdOutputResult renders read_command_output result.
func formatIMReadCmdOutputResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("📄 %s\n```\n%s\n```", imLabel(lang, "read_output"), output)
	}
	if output == "" {
		return fmt.Sprintf("📄 (%s)", imLabel(lang, "no_new_output"))
	}
	return fmt.Sprintf("📄\n```\n%s\n```", output)
}

// formatIMWaitCommandResult renders wait_command result.
func formatIMWaitCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("⏳ %s\n```\n%s\n```", imLabel(lang, "wait_command"), output)
	}
	if output == "" {
		return fmt.Sprintf("⏳ %s", imLabel(lang, "command_done"))
	}
	return fmt.Sprintf("⏳\n```\n%s\n```", output)
}

// formatIMWriteCmdInputResult renders write_command_input result.
func formatIMWriteCmdInputResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("⌨️ %s\n```\n%s\n```", imLabel(lang, "send_input"), output)
	}
	if output == "" {
		return fmt.Sprintf("⌨️ %s", imLabel(lang, "input_sent"))
	}
	return fmt.Sprintf("⌨️\n```\n%s\n```", output)
}

// formatIMListCommandsResult renders list_commands result.
func formatIMListCommandsResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("📋 %s\n```\n%s\n```", imLabel(lang, "active_commands"), output)
	}
	if output == "" {
		return fmt.Sprintf("📋 %s", imLabel(lang, "no_active_commands"))
	}
	return fmt.Sprintf("📋\n```\n%s\n```", output)
}

// --- Agent tools ---

// formatIMSpawnAgentResult renders spawn_agent result.
func formatIMSpawnAgentResult(tr *ToolResultInfo) string {
	name := extractArgValue(tr.Args, "description")
	if name == "" {
		name = "sub-agent"
	}
	if tr.IsError {
		output := strings.TrimSpace(tr.Result)
		return fmt.Sprintf("🤖 %s\n```\n%s\n```", name, output)
	}
	return fmt.Sprintf("🤖 %s", name)
}

// formatIMWaitAgentResult renders wait_agent result.
func formatIMWaitAgentResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("🤖 %s\n```\n%s\n```", imLabel(lang, "sub_task"), output)
	}
	if output == "" {
		return fmt.Sprintf("🤖 %s", imLabel(lang, "sub_task_done"))
	}
	return fmt.Sprintf("🤖\n```\n%s\n```", output)
}

// formatIMListAgentsResult renders list_agents result.
func formatIMListAgentsResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("🤖 %s\n```\n%s\n```", imLabel(lang, "sub_task_list"), output)
	}
	if output == "" {
		return fmt.Sprintf("🤖 %s", imLabel(lang, "no_active_agents"))
	}
	return fmt.Sprintf("🤖\n```\n%s\n```", output)
}

// --- MCP internal tools ---

// formatIMMCPCapabilitiesResult renders list_mcp_capabilities result.
func formatIMMCPCapabilitiesResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("🔗 %s\n```\n%s\n```", imLabel(lang, "mcp_service"), output)
	}
	if output == "" {
		return fmt.Sprintf("🔗 %s", imLabel(lang, "mcp_service_list"))
	}
	return fmt.Sprintf("🔗\n```\n%s\n```", output)
}

// formatIMMCPPromptResult renders get_mcp_prompt result.
func formatIMMCPPromptResult(tr *ToolResultInfo) string {
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("🔗 MCP Prompt\n```\n%s\n```", output)
	}
	if output == "" {
		return "🔗 MCP Prompt"
	}
	return fmt.Sprintf("🔗\n```\n%s\n```", output)
}

// formatIMMCPResourceResult renders read_mcp_resource result.
func formatIMMCPResourceResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("🔗 %s\n```\n%s\n```", imLabel(lang, "resource_read"), output)
	}
	if output == "" {
		return fmt.Sprintf("🔗 %s", imLabel(lang, "resource_content"))
	}
	return fmt.Sprintf("🔗\n```\n%s\n```", output)
}

// --- Productivity tools ---

// formatIMSkillResult renders skill result.
func formatIMSkillResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("🔧 %s\n```\n%s\n```", imLabel(lang, "skill_load"), output)
	}
	if output == "" {
		return fmt.Sprintf("🔧 %s", imLabel(lang, "skill_loaded"))
	}
	return fmt.Sprintf("🔧\n```\n%s\n```", output)
}

// formatIMSaveMemoryResult renders save_memory result.
func formatIMSaveMemoryResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("💾 %s\n```\n%s\n```", imLabel(lang, "memory_save"), output)
	}
	if output == "" {
		return fmt.Sprintf("💾 %s", imLabel(lang, "memory_saved"))
	}
	return fmt.Sprintf("💾\n```\n%s\n```", output)
}

// formatIMSleepResult renders sleep result — suppressed on success
// because the user already saw "⏳ Sleep for Xs" at ToolCall time.
func formatIMSleepResult(tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("⏳ Sleep\n```\n%s\n```", strings.TrimSpace(tr.Result))
	}
	// Suppress successful sleep results — the ToolCall event already
	// told the user "⏳ Sleep for Xs", no need to repeat.
	return ""
}

// formatIMCronCreateResult renders cron_create result.
func formatIMCronCreateResult(tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("⏰ Cron\n```\n%s\n```", strings.TrimSpace(tr.Result))
	}
	// Result is JSON: {"ID":"cron-1","CronExpr":"*/5 * * * *",...}
	var job struct {
		ID        string `json:"ID"`
		CronExpr  string `json:"CronExpr"`
		Prompt    string `json:"Prompt"`
		Recurring bool   `json:"Recurring"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(tr.Result)), &job); err != nil {
		return "⏰ Cron job created"
	}
	return fmt.Sprintf("⏰ Cron job created: `%s` → %q", job.CronExpr, truncateStr(job.Prompt, 50))
}

// formatIMCronDeleteResult renders cron_delete result.
func formatIMCronDeleteResult(tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("⏰ Cron\n```\n%s\n```", strings.TrimSpace(tr.Result))
	}
	return "⏰ Cron job deleted"
}

// formatIMCronListResult renders cron_list result.
func formatIMCronListResult(tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("⏰ Cron\n```\n%s\n```", strings.TrimSpace(tr.Result))
	}
	output := strings.TrimSpace(tr.Result)
	if output == "" || strings.Contains(output, "No scheduled jobs") {
		return "⏰ No scheduled cron jobs"
	}
	return fmt.Sprintf("⏰\n```\n%s\n```", output)
}

// formatIMTeammateResultsResult renders teammate_results result with body markdown.
func formatIMTeammateResultsResult(tr *ToolResultInfo) string {
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("📋 收集团队结果\n```\n%s\n```", output)
	}
	if output == "" {
		return "📋 收集团队结果"
	}
	return fmt.Sprintf("📋\n```\n%s\n```", output)
}

// formatIMWorktreeResult renders enter_worktree/exit_worktree results.
func formatIMWorktreeResult(icon string, tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("%s Worktree\n```\n%s\n```", icon, strings.TrimSpace(tr.Result))
	}
	output := strings.TrimSpace(tr.Result)
	if output == "" {
		return fmt.Sprintf("%s Worktree", icon)
	}
	return fmt.Sprintf("%s %s", icon, output)
}

// formatSleepDuration parses sleep tool args and returns a human-readable duration.
// e.g. {"seconds":5,"milliseconds":500} → "5.5s"
func formatSleepDuration(args string) string {
	var a struct {
		Seconds      int `json:"seconds"`
		Milliseconds int `json:"milliseconds"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return ""
	}
	d := time.Duration(a.Seconds)*time.Second + time.Duration(a.Milliseconds)*time.Millisecond
	if d <= 0 {
		return "0s"
	}
	return d.String()
}

// formatIMErrorResult formats error results for any tool.
func formatIMErrorResult(tr *ToolResultInfo) string {
	pretty := prettifyToolName(tr.ToolName)
	output := strings.TrimSpace(tr.Result)
	if output != "" {
		return fmt.Sprintf("🔧 %s\n```\n%s\n```", pretty, output)
	}
	return fmt.Sprintf("🔧 %s", pretty)
}

// formatIMCommandResult renders command execution result as success/failure.
func formatIMCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	cmd := extractCommand(tr.Args)
	if cmd == "" {
		cmd = tr.Detail
	}
	if tr.IsError {
		output := strings.TrimSpace(tr.Result)
		if cmd != "" {
			return fmt.Sprintf("❌\n```\n%s\n```\n```\n%s\n```", cmd, output)
		}
		return fmt.Sprintf("❌ %s", imLabel(lang, "command_failed"))
	}
	if cmd != "" {
		return fmt.Sprintf("✅\n```\n%s\n```", cmd)
	}
	return fmt.Sprintf("✅ %s", imLabel(lang, "command_done"))
}

// formatIMTodoResult renders todo_write as a visual checklist.
func formatIMTodoResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	var args struct {
		Todos []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(tr.Args), &args); err != nil || len(args.Todos) == 0 {
		return fmt.Sprintf("📋 %s", imLabel(lang, "update_todos"))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 %s:\n", imLabel(lang, "todos")))
	for _, t := range args.Todos {
		icon := "○"
		if t.Status == "done" {
			icon = "●"
		} else if t.Status == "in_progress" {
			icon = "◐"
		}
		sb.WriteString(fmt.Sprintf("  %s %s\n", icon, t.Content))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatIMReadFileResult renders read_file result with format-aware summary.
func formatIMReadFileResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	path := extractFilePathFromArgs(tr.Args)
	if path == "" {
		path = tr.Detail
	}
	baseName := filepath.Base(path)
	icon := imFileExtIcon(path)
	output := strings.TrimSpace(tr.Result)

	if tr.IsError {
		if path != "" {
			return fmt.Sprintf("%s %s\n```\n%s\n```", icon, baseName, output)
		}
		return fmt.Sprintf("%s Read\n```\n%s\n```", icon, output)
	}

	if output == "" {
		if path == "" {
			return fmt.Sprintf("%s Read", icon)
		}
		return fmt.Sprintf("%s %s", icon, baseName)
	}

	firstLine := firstLineOf(output)

	// Document extraction: "[Extracted from pdf, 3 pages]"
	if strings.HasPrefix(firstLine, "[Extracted from ") {
		format, pages := parseExtractedInfo(firstLine)
		lines := countResultLines(output)
		var summary string
		if pages > 0 && lines > 0 {
			summary = imDocSummary(lang, pages, lines)
		} else if pages > 0 {
			summary = imPagesSummary(lang, pages)
		} else if lines > 0 {
			summary = imLinesSummary(lang, lines)
		}
		label := imFileTypeLabel(path)
		if label == "" {
			label = format
		}
		if summary != "" {
			return fmt.Sprintf("%s %s (%s)", icon, baseName, summary)
		}
		return fmt.Sprintf("%s %s", icon, baseName)
	}

	// Archive: "[Archive: zip format, 15 files]"
	if strings.HasPrefix(firstLine, "[Archive: ") {
		_, fileCount := parseArchiveInfo(firstLine)
		// Check second line for truncation notice
		lines := strings.SplitN(output, "\n", 3)
		var truncShown, truncTotal int
		if len(lines) >= 2 {
			secondLine := strings.TrimSpace(lines[1])
			if strings.HasPrefix(secondLine, "[Showing first ") {
				truncShown, truncTotal = parseArchiveTruncation(secondLine)
			}
		}
		if truncTotal > 0 {
			return fmt.Sprintf("%s %s (%d %s, %s %d)", icon, baseName, truncTotal, imLabel(lang, "files"), imLabel(lang, "showing_first"), truncShown)
		}
		if fileCount > 0 {
			return fmt.Sprintf("%s %s (%d %s)", icon, baseName, fileCount, imLabel(lang, "files"))
		}
		return fmt.Sprintf("%s %s", icon, baseName)
	}

	// Plain text or unknown: show file name + range hint if applicable
	rangeHint := imFormatReadRange(lang, tr.Args)
	if path == "" {
		if rangeHint != "" {
			return fmt.Sprintf("%s Read %s", icon, rangeHint)
		}
		return fmt.Sprintf("%s Read", icon)
	}
	if rangeHint != "" {
		return fmt.Sprintf("%s %s %s", icon, baseName, rangeHint)
	}
	return fmt.Sprintf("%s %s", icon, baseName)
}

// formatIMListDirResult renders list_directory result with full output in code block.
func formatIMListDirResult(tr *ToolResultInfo) string {
	path := firstNonEmptyStr(extractArgValue(tr.Args, "path"), extractArgValue(tr.Args, "directory"))
	if path == "" {
		path = tr.Detail
	}
	if tr.IsError {
		output := strings.TrimSpace(tr.Result)
		if path != "" {
			return fmt.Sprintf("📂 %s\n```\n%s\n```", path, output)
		}
		return fmt.Sprintf("📂 List\n```\n%s\n```", output)
	}
	count := countResultLines(strings.TrimSpace(tr.Result))
	if path != "" {
		return fmt.Sprintf("📂 %s (%d items)", path, count)
	}
	return fmt.Sprintf("📂 List (%d items)", count)
}

// formatIMGlobResult renders glob result with full output in code block.
func formatIMGlobResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	pattern := extractArgValue(tr.Args, "pattern")
	if pattern == "" {
		pattern = tr.Detail
	}
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		if pattern != "" {
			return fmt.Sprintf("🔍 `%s`\n```\n%s\n```", pattern, output)
		}
		return fmt.Sprintf("🔍 Glob\n```\n%s\n```", output)
	}
	matches := countResultLines(output)
	if pattern != "" {
		return fmt.Sprintf("🔍 `%s` — %d %s", pattern, matches, imLabel(lang, "matches"))
	}
	return fmt.Sprintf("🔍 %d %s", matches, imLabel(lang, "matches"))
}

// formatIMEditResult renders edit_file result — show emoji icon + path + diff stats.
func formatIMEditResult(tr *ToolResultInfo) string {
	path := extractFilePathFromArgs(tr.Args)
	if path == "" {
		path = tr.Detail
	}
	baseName := filepath.Base(path)
	icon := "✏️"
	if tr.IsError {
		if path != "" {
			return fmt.Sprintf("%s %s\n```\n%s\n```", icon, baseName, strings.TrimSpace(tr.Result))
		}
		return fmt.Sprintf("%s Edit\n```\n%s\n```", icon, strings.TrimSpace(tr.Result))
	}
	// Parse added/removed from rawArgs (same logic as TUI renderEditDiff)
	added, removed := countEditLines(tr.Args)
	if path == "" {
		return fmt.Sprintf("%s Edit (+%d -%d)", icon, added, removed)
	}
	return fmt.Sprintf("%s %s (+%d -%d)", icon, baseName, added, removed)
}

// formatIMWriteResult renders write_file result.
func formatIMWriteResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	path := extractFilePathFromArgs(tr.Args)
	if path == "" {
		path = tr.Detail
	}
	baseName := filepath.Base(path)
	icon := "📝"
	if tr.IsError {
		if path != "" {
			return fmt.Sprintf("%s %s\n```\n%s\n```", icon, baseName, strings.TrimSpace(tr.Result))
		}
		return fmt.Sprintf("%s Write\n```\n%s\n```", icon, strings.TrimSpace(tr.Result))
	}
	// Count lines from content arg in rawArgs (same logic as TUI renderFileLineCount)
	lines := countWriteLines(tr.Args)
	if path == "" {
		return fmt.Sprintf("%s Write (%d %s)", icon, lines, imLabel(lang, "lines"))
	}
	return fmt.Sprintf("%s %s (%d %s)", icon, baseName, lines, imLabel(lang, "lines"))
}

// formatIMSearchResult renders search/grep result with full output in code block.
func formatIMSearchResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	pattern := firstNonEmptyStr(extractArgValue(tr.Args, "pattern"), extractArgValue(tr.Args, "query"))
	if pattern == "" {
		pattern = tr.Detail
	}
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		if pattern != "" {
			return fmt.Sprintf("🔍 `%s`\n```\n%s\n```", pattern, output)
		}
		return fmt.Sprintf("🔍 Search\n```\n%s\n```", output)
	}
	matches := countResultLines(output)
	if pattern != "" {
		return fmt.Sprintf("🔍 `%s` — %d %s", pattern, matches, imLabel(lang, "matches"))
	}
	return fmt.Sprintf("🔍 %d %s", matches, imLabel(lang, "matches"))
}

// formatIMWebResult renders web fetch/search result with full output in code block.
func formatIMWebFetchResult(tr *ToolResultInfo) string {
	url := extractArgValue(tr.Args, "url")
	if url == "" {
		url = tr.Detail
	}
	if tr.IsError {
		return fmt.Sprintf("🌐 %s\n```\n%s\n```", truncate(url, 60), strings.TrimSpace(tr.Result))
	}
	if url != "" {
		return fmt.Sprintf("🌐 %s", truncate(url, 60))
	}
	return "🌐 Fetch"
}

func formatIMWebSearchResult(tr *ToolResultInfo) string {
	query := extractArgValue(tr.Args, "query")
	if query == "" {
		query = tr.Detail
	}
	if tr.IsError {
		return fmt.Sprintf("🔍 %s\n```\n%s\n```", truncate(query, 60), strings.TrimSpace(tr.Result))
	}
	if query != "" {
		return fmt.Sprintf("🔍 %s", truncate(query, 60))
	}
	return "🔍 Search"
}

// formatIMGitResult renders git tool results with concise summary.
func formatIMGitResult(tr *ToolResultInfo) string {
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		pretty := prettifyToolName(tr.ToolName)
		return fmt.Sprintf("🔧 %s\n```\n%s\n```", pretty, output)
	}
	switch tr.ToolName {
	case "git_status":
		return fmt.Sprintf("🔧 Git Status\n%s", formatIMGitStatusSummary(output))
	case "git_diff":
		added, deleted := countDiffLines(output)
		return fmt.Sprintf("🔧 Git Diff (+%d -%d)", added, deleted)
	case "git_log":
		return fmt.Sprintf("🔧 Git Log\n%s", formatIMGitLogSummary(output))
	default:
		pretty := prettifyToolName(tr.ToolName)
		return fmt.Sprintf("🔧 %s", pretty)
	}
}

// formatIMMCPToolResult renders MCP tool results — header only (result consumed by LLM).
func formatIMMCPToolResult(tr *ToolResultInfo) string {
	pretty := prettifyToolName(tr.ToolName)
	argSummary := summarizeMCPArgs(tr.Args, 50)

	if argSummary != "" {
		return fmt.Sprintf("🔧 %s(%s)", pretty, argSummary)
	}
	return "🔧 " + pretty
}

// summarizeIMResult extracts a brief summary from a tool result string.
func summarizeIMResult(result string, maxLen int) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		line = compactSingleLine(line)
		if line != "" {
			if len(line) > maxLen {
				return line[:maxLen-3] + "..."
			}
			return line
		}
	}
	return ""
}

// summarizeMCPArgs produces a brief summary of MCP tool arguments.
func summarizeMCPArgs(rawArgs string, maxLen int) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil || len(args) == 0 {
		return ""
	}
	for k, v := range args {
		if s, ok := v.(string); ok && s != "" && k != "context" && k != "system_prompt" {
			s = compactSingleLine(s)
			if len(s) > maxLen {
				s = s[:maxLen-3] + "..."
			}
			return s
		}
	}
	return ""
}

func extractCommand(args string) string {
	var a map[string]any
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return strings.TrimSpace(args)
	}
	return firstNonEmptyStr(
		stringFromAny(a["command"]),
		stringFromAny(a["cmd"]),
	)
}

func extractFilePathFromArgs(args string) string {
	var a map[string]any
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return ""
	}
	return firstNonEmptyStr(
		stringFromAny(a["file_path"]),
		stringFromAny(a["path"]),
	)
}

func extractArgValue(args, key string) string {
	var a map[string]any
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return ""
	}
	return stringFromAny(a[key])
}

// --- read_file format-aware display helpers ---

// imFileExt returns the file extension, supporting double extensions like .tar.gz.
func imFileExt(path string) string {
	name := strings.ToLower(filepath.Base(path))
	for _, double := range []string{".tar.gz", ".tar.bz2", ".tar.xz"} {
		if strings.HasSuffix(name, double) {
			return double
		}
	}
	return strings.ToLower(filepath.Ext(path))
}

// imFileExtIcon returns an emoji icon based on file extension.
func imFileExtIcon(path string) string {
	ext := imFileExt(path)
	switch ext {
	case ".pdf":
		return "📄"
	case ".docx", ".doc":
		return "📄"
	case ".xlsx", ".xls":
		return "📊"
	case ".pptx", ".ppt":
		return "📊"
	case ".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tar.xz":
		return "📦"
	case ".pages", ".numbers", ".key":
		return "📄"
	case ".svg":
		return "🖼️"
	case ".odt", ".ods", ".odp":
		return "📄"
	case ".epub":
		return "📖"
	case ".rtf":
		return "📄"
	default:
		return "📖"
	}
}

// imFileTypeLabel returns a short type label for the file extension.
func imFileTypeLabel(path string) string {
	ext := imFileExt(path)
	switch ext {
	case ".pdf":
		return "PDF"
	case ".docx", ".doc":
		return "Word"
	case ".xlsx", ".xls":
		return "Excel"
	case ".pptx", ".ppt":
		return "PPT"
	case ".zip":
		return "ZIP"
	case ".tar":
		return "TAR"
	case ".tar.gz", ".tgz":
		return "tar.gz"
	case ".tar.bz2":
		return "tar.bz2"
	case ".pages":
		return "Pages"
	case ".numbers":
		return "Numbers"
	case ".key":
		return "Keynote"
	case ".svg":
		return "SVG"
	case ".odt":
		return "ODT"
	case ".ods":
		return "ODS"
	case ".odp":
		return "ODP"
	case ".epub":
		return "EPUB"
	case ".rtf":
		return "RTF"
	default:
		return ""
	}
}

// imIsArchiveExt checks if the extension is an archive format.
func imIsArchiveExt(ext string) bool {
	switch ext {
	case ".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tar.xz":
		return true
	}
	return false
}

// firstLineOf returns the first non-empty line from text.
func firstLineOf(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// parseExtractedInfo parses "[Extracted from pdf, 3 pages]" header.
// Returns (format, pageCount). pageCount is 0 if not present.
func parseExtractedInfo(header string) (format string, pages int) {
	// "[Extracted from pdf, 3 pages]"
	// "[Extracted from docx]"
	s := strings.TrimPrefix(header, "[Extracted from ")
	s = strings.TrimSuffix(s, "]")
	if s == header { // prefix didn't match
		return "", 0
	}
	parts := strings.SplitN(s, ", ", 2)
	format = strings.TrimSpace(parts[0])
	if len(parts) == 2 && strings.HasSuffix(parts[1], " pages") {
		nStr := strings.TrimSuffix(parts[1], " pages")
		if n, err := strconv.Atoi(strings.TrimSpace(nStr)); err == nil {
			pages = n
		}
	}
	return
}

// parseArchiveInfo parses "[Archive: zip format, 15 files]" header.
// Returns (format, fileCount).
func parseArchiveInfo(header string) (format string, files int) {
	// "[Archive: zip format, 15 files]"
	s := strings.TrimPrefix(header, "[Archive: ")
	s = strings.TrimSuffix(s, "]")
	if s == header {
		return "", 0
	}
	// "zip format, 15 files"
	parts := strings.SplitN(s, ", ", 2)
	if len(parts) == 2 {
		format = strings.TrimSuffix(strings.TrimSpace(parts[0]), " format")
		fileStr := strings.TrimSuffix(strings.TrimSpace(parts[1]), " files")
		if n, err := strconv.Atoi(fileStr); err == nil {
			files = n
		}
	} else {
		format = strings.TrimSuffix(s, " format")
	}
	return
}

// parseArchiveTruncation parses "[Showing first 500 of 1000 files]".
// Returns (shown, total). Both 0 if not found.
func parseArchiveTruncation(line string) (shown, total int) {
	s := strings.TrimPrefix(line, "[Showing first ")
	s = strings.TrimSuffix(s, "]")
	if s == line {
		return 0, 0
	}
	// "500 of 1000 files"
	parts := strings.SplitN(s, " of ", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	n1, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	n2, _ := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(parts[1], " files")))
	return n1, n2
}

// countResultLines counts non-empty lines in the result text (skipping header lines).
func countResultLines(result string) int {
	lines := 0
	for _, line := range strings.Split(result, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "[") {
			lines++
		}
	}
	return lines
}

// countEditLines counts added/removed lines from edit_file rawArgs JSON.
// Same logic as TUI renderEditDiff.
func countEditLines(rawArgs string) (added, removed int) {
	var args struct {
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
		Edits   []struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		} `json:"edits"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return 0, 0
	}
	if len(args.Edits) > 0 {
		for _, e := range args.Edits {
			removed += len(strings.Split(e.OldText, "\n"))
			added += len(strings.Split(e.NewText, "\n"))
		}
	} else {
		removed = len(strings.Split(args.OldText, "\n"))
		added = len(strings.Split(args.NewText, "\n"))
	}
	return
}

// countWriteLines counts lines from write_file rawArgs JSON content.
// Same logic as TUI renderFileLineCount.
func countWriteLines(rawArgs string) int {
	var args struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil || args.Content == "" {
		return 1
	}
	lines := strings.Count(args.Content, "\n")
	if !strings.HasSuffix(args.Content, "\n") {
		lines++
	}
	return lines
}

// countDiffLines counts added (+) and deleted (-) lines in unified diff output.
func countDiffLines(result string) (added, deleted int) {
	for _, line := range strings.Split(result, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			deleted++
		}
	}
	return
}

// formatIMGitStatusSummary renders a concise git status summary for IM.
func formatIMGitStatusSummary(output string) string {
	modified, added, deleted, untracked := 0, 0, 0, 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 {
			continue
		}
		x := line[0]
		y := line[1]
		path := line[2:]
		if strings.HasPrefix(path, " ") {
			path = path[1:]
		}
		_ = path
		switch {
		case x == '?' && y == '?':
			untracked++
		case x == 'A' || y == 'A':
			added++
		case x == 'D' || y == 'D':
			deleted++
		case (x == 'M' || y == 'M') && x != 'D' && y != 'D':
			modified++
		}
	}
	var parts []string
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", modified))
	}
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", added))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", deleted))
	}
	if untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", untracked))
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, ", ")
}

// formatIMGitLogSummary renders up to 3 recent commits for IM.
func formatIMGitLogSummary(output string) string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= 3 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

// Returns empty string if no offset/limit specified.
func imFormatReadRange(lang ToolLanguage, rawArgs string) string {
	offset := extractArgIntValue(rawArgs, "offset")
	limit := extractArgIntValue(rawArgs, "limit")
	if offset <= 0 && limit <= 0 {
		return ""
	}
	if lang == ToolLangEn {
		if offset > 0 && limit > 0 {
			return fmt.Sprintf("[lines %d-%d]", offset, offset+limit-1)
		}
		if offset > 0 {
			return fmt.Sprintf("[from line %d]", offset)
		}
		return fmt.Sprintf("[first %d %s]", limit, imLabel(lang, "lines"))
	}
	if offset > 0 && limit > 0 {
		return fmt.Sprintf("[行 %d-%d]", offset, offset+limit-1)
	}
	if offset > 0 {
		return fmt.Sprintf("[%s %d]", imLabel(lang, "from_line"), offset)
	}
	return fmt.Sprintf("[前 %d 行]", limit)
}

// imDocSummary returns a localized document summary string (pages + lines).
func imDocSummary(lang ToolLanguage, pages, lines int) string {
	if lang == ToolLangEn {
		return fmt.Sprintf("%d %s, %d %s", pages, imLabel(lang, "pages"), lines, imLabel(lang, "lines"))
	}
	return fmt.Sprintf("%d %s, %d %s", pages, imLabel(lang, "pages"), lines, imLabel(lang, "lines_extracted"))
}

// imPagesSummary returns a localized pages-only summary.
func imPagesSummary(lang ToolLanguage, pages int) string {
	return fmt.Sprintf("%d %s", pages, imLabel(lang, "pages"))
}

// imLinesSummary returns a localized lines-only summary.
func imLinesSummary(lang ToolLanguage, lines int) string {
	if lang == ToolLangEn {
		return fmt.Sprintf("%d %s", lines, imLabel(lang, "lines_extracted"))
	}
	return fmt.Sprintf("%s %d %s", imLabel(lang, "read"), lines, imLabel(lang, "lines_extracted"))
}

// extractArgIntValue extracts an integer argument from raw JSON args.
func extractArgIntValue(rawArgs, key string) int {
	var a map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &a); err != nil {
		return 0
	}
	v, ok := a[key]
	if !ok {
		return 0
	}
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return int(f)
}
