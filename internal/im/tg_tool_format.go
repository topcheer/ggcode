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
// It merges the command/action and result into a single message.
func formatToolResultText(tr *ToolResultInfo) string {
	result := strings.TrimSpace(tr.Result)
	switch tr.ToolName {
	case "bash", "run_command", "start_command", "powershell":
		cmd := extractCommand(tr.Args)
		if cmd == "" {
			cmd = tr.Detail
		}
		if tr.IsError {
			if result == "" {
				return fmt.Sprintf("❌ 命令失败:\n```\n%s\n```", cmd)
			}
			if len(result) > 1500 {
				result = result[:1497] + "..."
			}
			return fmt.Sprintf("❌ 命令失败:\n```\n%s\n```\n```\n%s\n```", cmd, result)
		}
		if result == "" {
			return fmt.Sprintf("✅ 命令完成:\n```\n%s\n```", cmd)
		}
		if len(result) > 1500 {
			result = result[:1497] + "..."
		}
		return fmt.Sprintf("⚡ 执行命令:\n```\n%s\n```\n结果:\n```\n%s\n```", cmd, result)
	case "read_file":
		if tr.IsError {
			return fmt.Sprintf("❌ 读取失败: %s", result)
		}
		return ""
	case "edit_file", "write_file":
		path := extractFilePathFromArgs(tr.Args)
		if path == "" {
			path = tr.Detail
		}
		if tr.IsError {
			return fmt.Sprintf("❌ 写入失败 `%s`: %s", path, result)
		}
		return fmt.Sprintf("✅ `%s` 完成", path)
	case "glob", "grep", "search_files":
		pattern := firstNonEmptyStr(extractArgValue(tr.Args, "pattern"), extractArgValue(tr.Args, "query"))
		if pattern == "" {
			pattern = tr.Detail
		}
		if tr.IsError {
			return fmt.Sprintf("❌ 搜索 `%s` 失败: %s", pattern, result)
		}
		if result == "" {
			return fmt.Sprintf("🔍 搜索 `%s`: 无结果", pattern)
		}
		if len(result) > 1000 {
			result = result[:997] + "..."
		}
		return fmt.Sprintf("🔍 搜索 `%s`:\n```\n%s\n```", pattern, result)
	default:
		name := tr.Detail
		if name == "" {
			name = tr.ToolName
		}
		if tr.IsError {
			return fmt.Sprintf("❌ %s 失败: %s", name, result)
		}
		if result == "" {
			return fmt.Sprintf("✅ %s 完成", name)
		}
		if len(result) > 500 {
			result = result[:497] + "..."
		}
		return fmt.Sprintf("🔧 %s:\n```\n%s\n```", name, result)
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
