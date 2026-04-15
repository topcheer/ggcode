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

// formatToolResultText formats a tool result event into markdown text for IM delivery.
func formatToolResultText(tr *ToolResultInfo) string {
	result := strings.TrimSpace(tr.Result)
	if result == "" {
		return ""
	}
	if len(result) > 2000 {
		result = result[:1997] + "..."
	}
	switch tr.ToolName {
	case "bash", "run_command", "start_command", "powershell":
		if tr.IsError {
			return fmt.Sprintf("❌ 命令失败:\n```\n%s\n```", result)
		}
		return fmt.Sprintf("✅ 命令结果:\n```\n%s\n```", result)
	case "read_file":
		if tr.IsError {
			return fmt.Sprintf("❌ 读取失败: %s", result)
		}
		// Don't send full file contents, just a summary
		return ""
	case "edit_file", "write_file":
		if tr.IsError {
			return fmt.Sprintf("❌ 写入失败: %s", result)
		}
		return "✅ 完成"
	case "glob", "grep", "search_files":
		if tr.IsError {
			return fmt.Sprintf("❌ 搜索失败: %s", result)
		}
		if len(result) > 1000 {
			result = result[:997] + "..."
		}
		return fmt.Sprintf("```\n%s\n```", result)
	default:
		if tr.IsError {
			return fmt.Sprintf("❌ 失败: %s", result)
		}
		if len(result) > 500 {
			result = result[:497] + "..."
		}
		return fmt.Sprintf("```\n%s\n```", result)
	}
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
