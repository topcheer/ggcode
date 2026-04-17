package im

import (
	"encoding/json"
	"fmt"
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

// formatToolCallText formats a tool call event into markdown text for IM delivery.
func formatToolCallText(tc *ToolCallInfo) string {
	name := tc.ToolName
	args := tc.Args
	switch name {
	case "bash", "run_command", "start_command", "powershell":
		cmd := extractCommand(args)
		if cmd == "" {
			cmd = tc.Detail
		}
		return fmt.Sprintf("⚡ 执行命令:\n```\n%s\n```", cmd)
	case "read_file":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("📖 读取文件: `%s`", path)
	case "edit_file":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("✏️ 编辑文件: `%s`", path)
	case "write_file":
		path := extractFilePathFromArgs(args)
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("📝 写入文件: `%s`", path)
	case "glob":
		pattern := extractArgValue(args, "pattern")
		if pattern == "" {
			pattern = tc.Detail
		}
		return fmt.Sprintf("🔍 查找文件: `%s`", pattern)
	case "grep", "search_files":
		pattern := firstNonEmptyStr(extractArgValue(args, "pattern"), extractArgValue(args, "query"))
		if pattern == "" {
			pattern = tc.Detail
		}
		return fmt.Sprintf("🔍 搜索: `%s`", pattern)
	case "list_directory":
		path := firstNonEmptyStr(extractArgValue(args, "path"), extractArgValue(args, "directory"))
		if path == "" {
			path = tc.Detail
		}
		return fmt.Sprintf("📂 列出目录: `%s`", path)
	case "web_fetch":
		url := extractArgValue(args, "url")
		if url == "" {
			url = tc.Detail
		}
		return fmt.Sprintf("🌐 抓取: %s", url)
	case "web_search":
		q := extractArgValue(args, "query")
		if q == "" {
			q = tc.Detail
		}
		return fmt.Sprintf("🔍 搜索: %s", q)
	case "todo_write":
		return "📋 更新待办列表"
	case "skill":
		return fmt.Sprintf("🔧 加载技能: `%s`", tc.Detail)
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

	// Default: icon + prettified tool name
	icon := "✓"
	if tr.IsError {
		icon = "✗"
	}
	pretty := prettifyToolName(tr.ToolName)

	resultPreview := summarizeIMResult(tr.Result, 100)
	if resultPreview != "" {
		return fmt.Sprintf("  %s %s\n    %s", icon, pretty, resultPreview)
	}
	return fmt.Sprintf("  %s %s", icon, pretty)
}

// formatSpecialIMToolResult returns (handled, formatted) for special tool types.
// handled=true means this function has dealt with the tool (either producing output
// or intentionally suppressing it); handled=false means "use default formatting".
func formatSpecialIMToolResult(tr *ToolResultInfo) (bool, string) {
	if tr.IsError {
		return true, formatIMErrorResult(tr)
	}

	switch tr.ToolName {
	case "run_command", "bash", "powershell", "start_command":
		return true, formatIMCommandResult(tr)
	case "todo_write":
		return true, formatIMTodoResult(tr)
	case "read_file", "list_directory", "glob":
		// Silent on success — reading/listing files doesn't need IM notification
		return true, ""
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
	default:
		// Check for MCP-style tool names (contain underscores or dots)
		if strings.Contains(tr.ToolName, "_") || strings.Contains(tr.ToolName, ".") {
			return true, formatIMMCPToolResult(tr)
		}
		return false, ""
	}
}

// formatIMErrorResult formats error results for any tool.
func formatIMErrorResult(tr *ToolResultInfo) string {
	pretty := prettifyToolName(tr.ToolName)
	result := summarizeIMResult(tr.Result, 120)
	if result != "" {
		return fmt.Sprintf("  ✗ %s\n    %s", pretty, result)
	}
	return fmt.Sprintf("  ✗ %s", pretty)
}

// formatIMCommandResult renders command execution with output summary.
func formatIMCommandResult(tr *ToolResultInfo) string {
	cmd := extractCommand(tr.Args)
	if cmd == "" {
		cmd = tr.Detail
	}
	cmdPreview := compactSingleLine(cmd)
	if len(cmdPreview) > 60 {
		cmdPreview = cmdPreview[:57] + "..."
	}

	icon := "  ✓"
	if tr.IsError {
		icon = "  ✗"
	}

	output := strings.TrimSpace(tr.Result)
	if output == "" {
		return fmt.Sprintf("%s $ %s", icon, cmdPreview)
	}

	lines := strings.Split(output, "\n")
	maxLines := 3
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s $ %s\n", icon, cmdPreview))

	showLines := lines
	truncated := 0
	if len(lines) > maxLines {
		showLines = lines[:maxLines]
		truncated = len(lines) - maxLines
	}
	for _, line := range showLines {
		trimmed := compactSingleLine(line)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 100 {
			trimmed = trimmed[:97] + "..."
		}
		sb.WriteString(fmt.Sprintf("    %s", trimmed))
		if sb.Len() > 400 {
			break
		}
		sb.WriteString("\n")
	}
	if truncated > 0 {
		sb.WriteString(fmt.Sprintf("    ...(%d more lines)", truncated))
	} else {
		// Remove trailing newline for clean output
		s := sb.String()
		if strings.HasSuffix(s, "\n") {
			sb.Reset()
			sb.WriteString(strings.TrimRight(s, "\n"))
		}
	}
	return sb.String()
}

// formatIMTodoResult renders todo_write as a visual checklist.
func formatIMTodoResult(tr *ToolResultInfo) string {
	var args struct {
		Todos []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(tr.Args), &args); err != nil || len(args.Todos) == 0 {
		return "  📋 更新待办"
	}

	var sb strings.Builder
	sb.WriteString("  📋 待办:\n")
	for _, t := range args.Todos {
		icon := "○"
		if t.Status == "done" {
			icon = "●"
		} else if t.Status == "in_progress" {
			icon = "◐"
		}
		sb.WriteString(fmt.Sprintf("    %s %s\n", icon, t.Content))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatIMEditResult renders edit_file result.
func formatIMEditResult(tr *ToolResultInfo) string {
	path := extractFilePathFromArgs(tr.Args)
	if path == "" {
		path = tr.Detail
	}
	if path == "" {
		return "  ✓ Edit"
	}
	return fmt.Sprintf("  ✏️ %s", path)
}

// formatIMWriteResult renders write_file result.
func formatIMWriteResult(tr *ToolResultInfo) string {
	path := extractFilePathFromArgs(tr.Args)
	if path == "" {
		path = tr.Detail
	}
	if path == "" {
		return "  ✓ Write"
	}
	return fmt.Sprintf("  📝 %s", path)
}

// formatIMSearchResult renders search/grep result with match count only.
func formatIMSearchResult(tr *ToolResultInfo) string {
	pattern := firstNonEmptyStr(extractArgValue(tr.Args, "pattern"), extractArgValue(tr.Args, "query"))
	if pattern == "" {
		pattern = tr.Detail
	}
	result := strings.TrimSpace(tr.Result)
	if result == "" {
		if pattern != "" {
			return fmt.Sprintf("  🔍 `%s`: 无结果", pattern)
		}
		return "  🔍 Search"
	}
	// Count matching lines/files
	lines := strings.Split(result, "\n")
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	if pattern != "" {
		return fmt.Sprintf("  🔍 `%s`: %d 条结果", pattern, count)
	}
	return fmt.Sprintf("  🔍 %d 条结果", count)
}

// formatIMWebResult renders web fetch/search result.
func formatIMWebResult(tr *ToolResultInfo) string {
	result := strings.TrimSpace(tr.Result)
	if result == "" {
		return "  🌐 Web"
	}
	// Show first meaningful line, truncated
	summary := summarizeIMResult(result, 100)
	if summary != "" {
		return fmt.Sprintf("  🌐 %s", summary)
	}
	return "  🌐 Web"
}

// formatIMGitResult renders git tool results.
func formatIMGitResult(tr *ToolResultInfo) string {
	pretty := prettifyToolName(tr.ToolName)
	result := strings.TrimSpace(tr.Result)
	if result == "" {
		return fmt.Sprintf("  ✓ %s", pretty)
	}
	// Show brief summary
	summary := summarizeIMResult(result, 100)
	if summary != "" {
		return fmt.Sprintf("  ✓ %s\n    %s", pretty, summary)
	}
	return fmt.Sprintf("  ✓ %s", pretty)
}

// formatIMMCPToolResult renders MCP tool results with brief summary.
func formatIMMCPToolResult(tr *ToolResultInfo) string {
	pretty := prettifyToolName(tr.ToolName)
	argSummary := summarizeMCPArgs(tr.Args, 50)

	icon := "  ✓"
	if tr.IsError {
		icon = "  ✗"
	}

	if argSummary != "" {
		result := fmt.Sprintf("%s %s(%s)", icon, pretty, argSummary)
		summary := summarizeIMResult(tr.Result, 80)
		if summary != "" {
			return result + "\n    " + summary
		}
		return result
	}

	summary := summarizeIMResult(tr.Result, 80)
	if summary != "" {
		return fmt.Sprintf("%s %s\n    %s", icon, pretty, summary)
	}
	return fmt.Sprintf("%s %s", icon, pretty)
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
