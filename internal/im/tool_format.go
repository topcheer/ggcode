package im

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
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
	default:
		if tc.Detail != "" {
			return fmt.Sprintf("🔧 %s: `%s`", name, tc.Detail)
		}
		return fmt.Sprintf("🔧 %s", name)
	}
}

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

	// Default: icon + prettified tool name
	icon := "✓"
	if tr.IsError {
		icon = "✗"
	}
	pretty := prettifyToolName(tr.ToolName)
	output := strings.TrimSpace(tr.Result)
	if output != "" {
		return fmt.Sprintf("%s 🔧 %s\n```\n%s\n```", icon, pretty, output)
	}
	return fmt.Sprintf("%s 🔧 %s", icon, pretty)
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
	case "web_fetch", "web_search":
		return true, formatIMWebResult(tr)
	case "git_diff", "git_status", "git_log":
		return true, formatIMGitResult(tr)
	case "ask_user":
		return true, formatIMAskUserResult(tr)
	case "start_command":
		return true, formatIMStartCommandResult(tr)
	case "stop_command":
		return true, formatIMStopCommandResult(tr)
	case "read_command_output":
		return true, formatIMReadCmdOutputResult(tr)
	case "wait_command":
		return true, formatIMWaitCommandResult(tr)
	case "write_command_input":
		return true, formatIMWriteCmdInputResult(tr)
	case "list_commands":
		return true, formatIMListCommandsResult(tr)
	case "spawn_agent":
		return true, formatIMSpawnAgentResult(tr)
	case "wait_agent":
		return true, formatIMWaitAgentResult(tr)
	case "list_agents":
		return true, formatIMListAgentsResult(tr)
	case "list_mcp_capabilities":
		return true, formatIMMCPCapabilitiesResult(tr)
	case "get_mcp_prompt":
		return true, formatIMMCPPromptResult(tr)
	case "read_mcp_resource":
		return true, formatIMMCPResourceResult(tr)
	case "skill":
		return true, formatIMSkillResult(tr)
	case "save_memory":
		return true, formatIMSaveMemoryResult(tr)
	default:
		if tr.IsError {
			return true, formatIMErrorResult(tr)
		}
		// Check for MCP-style tool names (contain underscores or dots)
		if strings.Contains(tr.ToolName, "_") || strings.Contains(tr.ToolName, ".") {
			return true, formatIMMCPToolResult(tr)
		}
		return false, ""
	}
}

// formatIMAskUserResult renders ask_user result.
func formatIMAskUserResult(tr *ToolResultInfo) string {
	icon := "✓"
	if tr.IsError {
		icon = "✗"
	}
	return fmt.Sprintf("%s 💬 %s", icon, imLabel(toolLang(tr.Lang), "reply_received"))
}

// --- Background command tools ---

// formatIMStartCommandResult renders start_command result.
func formatIMStartCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ ⚡ %s\n```\n%s\n```", imLabel(lang, "bg_command"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ ⚡ %s", imLabel(lang, "bg_command_started"))
	}
	return fmt.Sprintf("✓ ⚡ %s\n```\n%s\n```", imLabel(lang, "bg_command"), output)
}

// formatIMStopCommandResult renders stop_command result.
func formatIMStopCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🛑 %s\n```\n%s\n```", imLabel(lang, "stop_command"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 🛑 %s", imLabel(lang, "command_stopped"))
	}
	return fmt.Sprintf("✓ 🛑\n```\n%s\n```", output)
}

// formatIMReadCmdOutputResult renders read_command_output result.
func formatIMReadCmdOutputResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 📄 %s\n```\n%s\n```", imLabel(lang, "read_output"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 📄 (%s)", imLabel(lang, "no_new_output"))
	}
	return fmt.Sprintf("✓ 📄\n```\n%s\n```", output)
}

// formatIMWaitCommandResult renders wait_command result.
func formatIMWaitCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ ⏳ %s\n```\n%s\n```", imLabel(lang, "wait_command"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ ⏳ %s", imLabel(lang, "command_done"))
	}
	return fmt.Sprintf("✓ ⏳\n```\n%s\n```", output)
}

// formatIMWriteCmdInputResult renders write_command_input result.
func formatIMWriteCmdInputResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ ⌨️ %s\n```\n%s\n```", imLabel(lang, "send_input"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ ⌨️ %s", imLabel(lang, "input_sent"))
	}
	return fmt.Sprintf("✓ ⌨️\n```\n%s\n```", output)
}

// formatIMListCommandsResult renders list_commands result.
func formatIMListCommandsResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 📋 %s\n```\n%s\n```", imLabel(lang, "active_commands"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 📋 %s", imLabel(lang, "no_active_commands"))
	}
	return fmt.Sprintf("✓ 📋\n```\n%s\n```", output)
}

// --- Agent tools ---

// formatIMSpawnAgentResult renders spawn_agent result.
func formatIMSpawnAgentResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🤖 %s\n```\n%s\n```", imLabel(lang, "sub_task"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 🤖 %s", imLabel(lang, "sub_task_started"))
	}
	return fmt.Sprintf("✓ 🤖\n```\n%s\n```", output)
}

// formatIMWaitAgentResult renders wait_agent result.
func formatIMWaitAgentResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🤖 %s\n```\n%s\n```", imLabel(lang, "sub_task"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 🤖 %s", imLabel(lang, "sub_task_done"))
	}
	return fmt.Sprintf("✓ 🤖\n```\n%s\n```", output)
}

// formatIMListAgentsResult renders list_agents result.
func formatIMListAgentsResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🤖 %s\n```\n%s\n```", imLabel(lang, "sub_task_list"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 🤖 %s", imLabel(lang, "no_active_agents"))
	}
	return fmt.Sprintf("✓ 🤖\n```\n%s\n```", output)
}

// --- MCP internal tools ---

// formatIMMCPCapabilitiesResult renders list_mcp_capabilities result.
func formatIMMCPCapabilitiesResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🔗 %s\n```\n%s\n```", imLabel(lang, "mcp_service"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 🔗 %s", imLabel(lang, "mcp_service_list"))
	}
	return fmt.Sprintf("✓ 🔗\n```\n%s\n```", output)
}

// formatIMMCPPromptResult renders get_mcp_prompt result.
func formatIMMCPPromptResult(tr *ToolResultInfo) string {
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🔗 MCP Prompt\n```\n%s\n```", output)
	}
	if output == "" {
		return "✓ 🔗 MCP Prompt"
	}
	return fmt.Sprintf("✓ 🔗\n```\n%s\n```", output)
}

// formatIMMCPResourceResult renders read_mcp_resource result.
func formatIMMCPResourceResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🔗 %s\n```\n%s\n```", imLabel(lang, "resource_read"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 🔗 %s", imLabel(lang, "resource_content"))
	}
	return fmt.Sprintf("✓ 🔗\n```\n%s\n```", output)
}

// --- Productivity tools ---

// formatIMSkillResult renders skill result.
func formatIMSkillResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🔧 %s\n```\n%s\n```", imLabel(lang, "skill_load"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 🔧 %s", imLabel(lang, "skill_loaded"))
	}
	return fmt.Sprintf("✓ 🔧\n```\n%s\n```", output)
}

// formatIMSaveMemoryResult renders save_memory result.
func formatIMSaveMemoryResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 💾 %s\n```\n%s\n```", imLabel(lang, "memory_save"), output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 💾 %s", imLabel(lang, "memory_saved"))
	}
	return fmt.Sprintf("✓ 💾\n```\n%s\n```", output)
}

// formatIMErrorResult formats error results for any tool.
func formatIMErrorResult(tr *ToolResultInfo) string {
	pretty := prettifyToolName(tr.ToolName)
	output := strings.TrimSpace(tr.Result)
	if output != "" {
		return fmt.Sprintf("✗ 🔧 %s\n```\n%s\n```", pretty, output)
	}
	return fmt.Sprintf("✗ 🔧 %s", pretty)
}

// formatIMCommandResult renders command execution with full output.
// Command is shown in a bash code block; result in a plain code block.
func formatIMCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	cmd := extractCommand(tr.Args)
	if cmd == "" {
		cmd = tr.Detail
	}

	icon := "✓"
	if tr.IsError {
		icon = "✗"
	}

	output := strings.TrimSpace(tr.Result)
	if output == "" {
		if cmd == "" {
			return icon
		}
		return fmt.Sprintf("%s\n```bash\n%s\n```\n```\n(%s)\n```", icon, cmd, imLabel(lang, "no_output"))
	}

	if cmd == "" {
		return fmt.Sprintf("%s\n```\n%s\n```", icon, output)
	}
	return fmt.Sprintf("%s\n```bash\n%s\n```\n```\n%s\n```", icon, cmd, output)
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
		return fmt.Sprintf("✓ 📋 %s", imLabel(lang, "update_todos"))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✓ 📋 %s:\n", imLabel(lang, "todos")))
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
			return fmt.Sprintf("✗ %s %s\n```\n%s\n```", icon, baseName, output)
		}
		return fmt.Sprintf("✗ %s Read\n```\n%s\n```", icon, output)
	}

	if output == "" {
		if path == "" {
			return fmt.Sprintf("✓ %s Read", icon)
		}
		return fmt.Sprintf("✓ %s %s", icon, baseName)
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
			return fmt.Sprintf("✓ %s %s (%s)", icon, baseName, summary)
		}
		return fmt.Sprintf("✓ %s %s", icon, baseName)
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
			return fmt.Sprintf("✓ %s %s (%d %s, %s %d)", icon, baseName, truncTotal, imLabel(lang, "files"), imLabel(lang, "showing_first"), truncShown)
		}
		if fileCount > 0 {
			return fmt.Sprintf("✓ %s %s (%d %s)", icon, baseName, fileCount, imLabel(lang, "files"))
		}
		return fmt.Sprintf("✓ %s %s", icon, baseName)
	}

	// Plain text or unknown: show file name + range hint if applicable
	rangeHint := imFormatReadRange(lang, tr.Args)
	if path == "" {
		if rangeHint != "" {
			return fmt.Sprintf("✓ %s Read %s", icon, rangeHint)
		}
		return fmt.Sprintf("✓ %s Read", icon)
	}
	if rangeHint != "" {
		return fmt.Sprintf("✓ %s %s %s", icon, baseName, rangeHint)
	}
	return fmt.Sprintf("✓ %s %s", icon, baseName)
}

// formatIMListDirResult renders list_directory result with full output in code block.
func formatIMListDirResult(tr *ToolResultInfo) string {
	path := firstNonEmptyStr(extractArgValue(tr.Args, "path"), extractArgValue(tr.Args, "directory"))
	if path == "" {
		path = tr.Detail
	}
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		if path != "" {
			return fmt.Sprintf("✗ 📂 %s\n```\n%s\n```", path, output)
		}
		return fmt.Sprintf("✗ 📂 List\n```\n%s\n```", output)
	}
	if output == "" {
		if path != "" {
			return fmt.Sprintf("✓ 📂 %s", path)
		}
		return "✓ 📂 List"
	}
	if path != "" {
		return fmt.Sprintf("✓ 📂 %s\n```\n%s\n```", path, output)
	}
	return fmt.Sprintf("✓ 📂\n```\n%s\n```", output)
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
			return fmt.Sprintf("✗ 🔍 `%s`\n```\n%s\n```", pattern, output)
		}
		return fmt.Sprintf("✗ 🔍 Glob\n```\n%s\n```", output)
	}
	if output == "" {
		if pattern != "" {
			return fmt.Sprintf("✓ 🔍 `%s`: %s", pattern, imLabel(lang, "no_matches"))
		}
		return "✓ 🔍 Glob"
	}
	if pattern != "" {
		return fmt.Sprintf("✓ 🔍 `%s`\n```\n%s\n```", pattern, output)
	}
	return fmt.Sprintf("✓ 🔍\n```\n%s\n```", output)
}

// formatIMEditResult renders edit_file result — show emoji icon + path.
func formatIMEditResult(tr *ToolResultInfo) string {
	path := extractFilePathFromArgs(tr.Args)
	if path == "" {
		path = tr.Detail
	}
	if tr.IsError {
		if path != "" {
			return fmt.Sprintf("✗ ✏️ %s\n```\n%s\n```", path, strings.TrimSpace(tr.Result))
		}
		return fmt.Sprintf("✗ ✏️ Edit\n```\n%s\n```", strings.TrimSpace(tr.Result))
	}
	if path == "" {
		return "✓ ✏️ Edit"
	}
	return fmt.Sprintf("✓ ✏️ %s", path)
}

// formatIMWriteResult renders write_file result.
func formatIMWriteResult(tr *ToolResultInfo) string {
	path := extractFilePathFromArgs(tr.Args)
	if path == "" {
		path = tr.Detail
	}
	if tr.IsError {
		if path != "" {
			return fmt.Sprintf("✗ 📝 %s\n```\n%s\n```", path, strings.TrimSpace(tr.Result))
		}
		return fmt.Sprintf("✗ 📝 Write\n```\n%s\n```", strings.TrimSpace(tr.Result))
	}
	if path == "" {
		return "✓ 📝 Write"
	}
	return fmt.Sprintf("✓ 📝 %s", path)
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
			return fmt.Sprintf("✗ 🔍 `%s`\n```\n%s\n```", pattern, output)
		}
		return fmt.Sprintf("✗ 🔍 Search\n```\n%s\n```", output)
	}
	if output == "" {
		if pattern != "" {
			return fmt.Sprintf("✓ 🔍 `%s`: 0 %s", pattern, imLabel(lang, "results"))
		}
		return "✓ 🔍 Search"
	}
	if pattern != "" {
		return fmt.Sprintf("✓ 🔍 `%s`\n```\n%s\n```", pattern, output)
	}
	return fmt.Sprintf("✓ 🔍\n```\n%s\n```", output)
}

// formatIMWebResult renders web fetch/search result with full output in code block.
func formatIMWebResult(tr *ToolResultInfo) string {
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🌐\n```\n%s\n```", output)
	}
	if output == "" {
		return "✓ 🌐 Web"
	}
	return fmt.Sprintf("✓ 🌐\n```\n%s\n```", output)
}

// formatIMGitResult renders git tool results with full output in code block.
func formatIMGitResult(tr *ToolResultInfo) string {
	pretty := prettifyToolName(tr.ToolName)
	output := strings.TrimSpace(tr.Result)
	if tr.IsError {
		return fmt.Sprintf("✗ 🔧 %s\n```\n%s\n```", pretty, output)
	}
	if output == "" {
		return fmt.Sprintf("✓ 🔧 %s", pretty)
	}
	return fmt.Sprintf("✓ 🔧 %s\n```\n%s\n```", pretty, output)
}

// formatIMMCPToolResult renders MCP tool results with full output in code block.
func formatIMMCPToolResult(tr *ToolResultInfo) string {
	pretty := prettifyToolName(tr.ToolName)
	argSummary := summarizeMCPArgs(tr.Args, 50)
	output := strings.TrimSpace(tr.Result)

	icon := "✓"
	if tr.IsError {
		icon = "✗"
	}

	header := icon + " 🔧 " + pretty
	if argSummary != "" {
		header = fmt.Sprintf("%s 🔧 %s(%s)", icon, pretty, argSummary)
	}

	if output == "" {
		return header
	}
	return fmt.Sprintf("%s\n```\n%s\n```", header, output)
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

// imFormatReadRange formats a read range hint from args (e.g. "[100-200]").
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
