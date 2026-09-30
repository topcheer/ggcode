package im

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/security"
	"github.com/topcheer/ggcode/internal/tool"
)

// redactResult sanitizes terminal control sequences and masks secrets in
// tool result text for safe display in IM.
func redactResult(s string) string {
	return security.RedactForDisplay(security.SanitizeTerminalForDisplay(s))
}

// imFenceLen returns the number of backticks needed to safely fence content:
// one more than the longest backtick run inside the content (minimum 3), so
// an inner ``` can never close the outer fence early (CommonMark rule: a
// fence is only closed by a fence of at least the same length).
func imFenceLen(content string) int {
	maxRun, run := 0, 0
	for _, r := range content {
		if r == '`' {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	if maxRun < 3 {
		return 3
	}
	return maxRun + 1
}

// imCodeBlock wraps content in a markdown code fence long enough that no
// backtick run inside the content can prematurely close it (#971).
func imCodeBlock(content string) string {
	fence := strings.Repeat("`", imFenceLen(content))
	return fence + "\n" + content + "\n" + fence
}

// imLabel returns a localized label string for IM tool display.
// imLabelEn maps IM tool display keys to English labels.
var imLabelEn = map[string]string{
	"run_command":            "Run command",
	"read":                   "Reading",
	"read_file":              "Reading file",
	"edit_file":              "Edit file",
	"write_file":             "Write file",
	"find_files":             "Find files",
	"search":                 "Search",
	"list_directory":         "List directory",
	"fetch":                  "Fetch",
	"update_todos":           "Update todos",
	"load_skill":             "Load skill",
	"pages":                  "pages",
	"lines_extracted":        "lines extracted",
	"files":                  "files",
	"showing_first":          "showing first",
	"lines":                  "lines",
	"from_line":              "from line",
	"first_lines":            "lines",
	"no_output":              "no output",
	"no_matches":             "no matches",
	"matches":                "matches",
	"no_active_commands":     "no active commands",
	"no_active_agents":       "no active agents",
	"bg_command_started":     "Background command started",
	"bg_command":             "Background command",
	"command_stopped":        "Command stopped",
	"stop_command":           "Stop command",
	"read_output":            "Read output",
	"no_new_output":          "no new output",
	"wait_command":           "Wait command",
	"command_done":           "Command completed",
	"command_failed":         "Command failed",
	"input_sent":             "Input sent",
	"send_input":             "Send input",
	"active_commands":        "Active commands",
	"sub_task":               "Sub-task",
	"sub_task_started":       "Sub-task started",
	"sub_task_done":          "Sub-task completed",
	"sub_task_list":          "Sub-task list",
	"no_active_subtasks":     "no active sub-tasks",
	"mcp_service":            "MCP service",
	"mcp_service_list":       "MCP service list",
	"mcp_prompt":             "MCP Prompt",
	"resource_read":          "Resource read",
	"resource_content":       "Resource content",
	"skill_loaded":           "Skill loaded",
	"skill_load":             "Skill load",
	"memory_saved":           "Memory saved",
	"memory_save":            "Memory save",
	"memory_deleted":         "Memory deleted",
	"memory_delete":          "Memory delete",
	"delete_memory":          "Delete memory",
	"reply_received":         "Reply received",
	"todos":                  "Todos",
	"results":                "results",
	"team_create":            "Create team",
	"team_delete":            "Delete team",
	"teammate_spawn":         "Spawn teammate",
	"teammate_shutdown":      "Shutdown teammate",
	"send_message":           "Send message",
	"teammate_results":       "Collect team results",
	"swarm_task_create":      "Create task",
	"swarm_task_complete":    "Complete task",
	"a2a_remote":             "Remote call",
	"a2a_send_task":          "Send task",
	"team_created":           "Team created",
	"team_deleted":           "Team deleted",
	"teammate_created":       "Teammate created",
	"teammate_shutdown_done": "Teammate shut down",
	"message_sent":           "Message sent",
	"task_created":           "Task created",
	"task_completed":         "Task completed",
	"task_sent":              "Task sent",
	"read_multi":             "Read multiple files",
	"edit_multi":             "Edit multiple files",
	"write_multi":            "Write multiple files",
	"git_stage":              "Stage files",
	"git_commit":             "Commit",
	"git_show":               "Git show",
	"git_blame":              "Git blame",
	"git_branch_list":        "List branches",
	"git_staged":             "Files staged",
	"git_committed":          "Committed",
	"switch_mode":            "Switch mode",
	"mode_switched":          "Mode switched",
	"browser":                "Browser",
	"edit_notebook":          "Edit notebook",
	"delegate":               "Delegate",
	"delegated_to":           "Delegated to",
	"cron_update":            "Update cron job",
	"cron_pause":             "Pause cron job",
	"cron_resume":            "Resume cron job",
	"cron_get":               "Get cron job",
	"cron_update_done":       "Cron job updated",
	"cron_paused":            "Cron job paused",
	"cron_resumed":           "Cron job resumed",
	"spawn_agent":            "Spawn agent",
	"wait_agent":             "Wait for agent",
	"save_memory":            "Save memory",
	"config":                 "Config",
	"mobile_device":          "Mobile device",
	"im_manage":              "IM adapter",
	"git_diff":               "Git diff",
	"git_status":             "Git status",
	"git_log":                "Git log",
	"git_remote":             "Git remote",
	"git_stash":              "Git stash",
	"git_stash_list":         "Git stash list",
	"ask_user":               "Ask user",
	"command_manage":         "Manage command",
	"command_input":          "Send input",
	"files_edited":           "files edited",
	"files_written":          "files written",
	"files_failed":           "files failed",
	"edits":                  "edits",
	"failed":                 "failed",
	"errors":                 "errors",
	"cancel_agent":           "Cancelling agent",
	"task_create":            "Task created",
	"task_stopped":           "Task stopped",
	"lsp_definition":         "Go to definition",
	"lsp_references":         "Find references",
	"lsp_hover":              "Type info",
	"lsp_diagnostics":        "Diagnostics",
	"lsp_rename":             "Rename symbol",
	"lsp_symbols":            "Symbols",
	"lsp_implementation":     "Find implementations",
	"lsp_code_actions":       "Code actions",
}

// imLabelZh maps IM tool display keys to Simplified Chinese labels.
var imLabelZh = map[string]string{
	"run_command":            "执行命令",
	"read":                   "读取",
	"read_file":              "读取文件",
	"edit_file":              "编辑文件",
	"write_file":             "写入文件",
	"find_files":             "查找文件",
	"search":                 "搜索",
	"list_directory":         "列出目录",
	"fetch":                  "抓取",
	"update_todos":           "更新待办列表",
	"load_skill":             "加载技能",
	"pages":                  "页",
	"lines_extracted":        "行",
	"files":                  "个文件",
	"showing_first":          "展示前",
	"lines":                  "行",
	"from_line":              "从行",
	"first_lines":            "前 %d 行",
	"no_output":              "无输出",
	"no_matches":             "无匹配",
	"matches":                "处匹配",
	"no_active_commands":     "无活动命令",
	"no_active_agents":       "无活动子任务",
	"bg_command_started":     "后台命令已启动",
	"bg_command":             "后台命令",
	"command_stopped":        "命令已停止",
	"stop_command":           "停止命令",
	"read_output":            "读取输出",
	"no_new_output":          "无新输出",
	"wait_command":           "等待命令",
	"command_done":           "命令完成",
	"command_failed":         "命令失败",
	"input_sent":             "输入已发送",
	"send_input":             "输入发送",
	"active_commands":        "活动命令",
	"sub_task":               "子任务",
	"sub_task_started":       "子任务已启动",
	"sub_task_done":          "子任务完成",
	"sub_task_list":          "子任务列表",
	"no_active_subtasks":     "无活动子任务",
	"mcp_service":            "MCP 服务",
	"mcp_service_list":       "MCP 服务列表",
	"mcp_prompt":             "MCP Prompt",
	"resource_read":          "资源读取",
	"resource_content":       "资源内容",
	"skill_loaded":           "技能已加载",
	"skill_load":             "技能加载",
	"memory_saved":           "记忆已保存",
	"memory_save":            "记忆保存",
	"reply_received":         "收到回复",
	"todos":                  "待办",
	"results":                "条结果",
	"team_create":            "创建团队",
	"team_delete":            "删除团队",
	"teammate_spawn":         "创建队友",
	"teammate_shutdown":      "关闭队友",
	"send_message":           "发送消息",
	"teammate_results":       "收集团队结果",
	"swarm_task_create":      "创建任务",
	"swarm_task_complete":    "完成任务",
	"a2a_remote":             "远程调用",
	"a2a_send_task":          "发送任务",
	"team_created":           "团队已创建",
	"team_deleted":           "团队已删除",
	"teammate_created":       "队友已创建",
	"teammate_shutdown_done": "队友已关闭",
	"message_sent":           "消息已发送",
	"task_created":           "任务已创建",
	"task_completed":         "任务已完成",
	"task_sent":              "任务已发送",
	"read_multi":             "读取多文件",
	"edit_multi":             "编辑多文件",
	"write_multi":            "写入多文件",
	"git_stage":              "暂存文件",
	"git_commit":             "提交",
	"git_show":               "Git show",
	"git_blame":              "Git blame",
	"git_branch_list":        "列出分支",
	"git_staged":             "文件已暂存",
	"git_committed":          "已提交",
	"switch_mode":            "切换模式",
	"mode_switched":          "模式已切换",
	"browser":                "浏览器",
	"edit_notebook":          "编辑笔记本",
	"delegate":               "委托",
	"delegated_to":           "委托给",
	"cron_update":            "更新定时任务",
	"cron_pause":             "暂停定时任务",
	"cron_resume":            "恢复定时任务",
	"cron_get":               "查看定时任务",
	"cron_update_done":       "定时任务已更新",
	"cron_paused":            "定时任务已暂停",
	"cron_resumed":           "定时任务已恢复",
	"spawn_agent":            "创建子代理",
	"wait_agent":             "等待子代理",
	"save_memory":            "保存记忆",
	"config":                 "配置",
	"mobile_device":          "移动设备",
	"im_manage":              "IM 适配器",
	"git_diff":               "查看差异",
	"git_status":             "查看状态",
	"git_log":                "查看日志",
	"git_remote":             "查看远程",
	"git_stash":              "储藏",
	"git_stash_list":         "储藏列表",
	"ask_user":               "询问用户",
	"command_manage":         "管理命令",
	"command_input":          "发送输入",
	"files_edited":           "个文件已编辑",
	"files_written":          "个文件已写入",
	"files_failed":           "个文件失败",
	"edits":                  "处修改",
	"failed":                 "个失败",
	"errors":                 "个错误",
	"cancel_agent":           "取消子代理",
	"task_create":            "任务已创建",
	"task_stopped":           "任务已停止",
	"lsp_definition":         "跳转定义",
	"lsp_references":         "查找引用",
	"lsp_hover":              "类型信息",
	"lsp_diagnostics":        "诊断",
	"lsp_rename":             "重命名符号",
	"lsp_symbols":            "符号",
	"lsp_implementation":     "查找实现",
	"lsp_code_actions":       "代码操作",
}

// imLabel returns a localized label string for IM tool display. A key
// missing from the selected language table falls back to the key itself.
func imLabel(lang ToolLanguage, key string) string {
	if lang == ToolLangEn {
		if s, ok := imLabelEn[key]; ok {
			return s
		}
		return key
	}
	if s, ok := imLabelZh[key]; ok {
		return s
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
	return "⚡ " + tool.StartCommandResultText(redactResult(tr.Result), tr.IsError)
}

// formatIMStopCommandResult renders stop_command result.
func formatIMStopCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("🛑 %s\n%s", imLabel(lang, "stop_command"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("🛑 %s", imLabel(lang, "command_stopped"))
	}
	return fmt.Sprintf("🛑\n%s", imCodeBlock(output))
}

// formatIMReadCmdOutputResult renders read_command_output result.
func formatIMReadCmdOutputResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("📄 %s\n%s", imLabel(lang, "read_output"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("📄 (%s)", imLabel(lang, "no_new_output"))
	}
	return fmt.Sprintf("📄\n%s", imCodeBlock(output))
}

// formatIMWaitCommandResult renders wait_command result.
func formatIMWaitCommandResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("⏳ %s\n%s", imLabel(lang, "wait_command"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("⏳ %s", imLabel(lang, "command_done"))
	}
	return fmt.Sprintf("⏳\n%s", imCodeBlock(output))
}

// formatIMWriteCmdInputResult renders write_command_input result.
func formatIMWriteCmdInputResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("⌨ %s\n%s", imLabel(lang, "send_input"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("⌨ %s", imLabel(lang, "input_sent"))
	}
	return fmt.Sprintf("⌨\n%s", imCodeBlock(output))
}

// formatIMListCommandsResult renders list_commands result.
func formatIMListCommandsResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("📋 %s\n%s", imLabel(lang, "active_commands"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("📋 %s", imLabel(lang, "no_active_commands"))
	}
	return fmt.Sprintf("📋\n%s", imCodeBlock(output))
}

// --- Agent tools ---

// formatIMSpawnAgentResult renders spawn_agent result.
func formatIMSpawnAgentResult(tr *ToolResultInfo) string {
	name := extractArgValue(tr.Args, "description")
	if name == "" {
		name = "sub-agent"
	}
	if tr.IsError {
		output := strings.TrimSpace(redactResult(tr.Result))
		return fmt.Sprintf("🤖 %s\n%s", name, imCodeBlock(output))
	}
	return fmt.Sprintf("🤖 %s", name)
}

// formatIMWaitAgentResult renders wait_agent result.
func formatIMWaitAgentResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("🤖 %s\n%s", imLabel(lang, "sub_task"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("🤖 %s", imLabel(lang, "sub_task_done"))
	}
	return fmt.Sprintf("🤖\n%s", imCodeBlock(output))
}

// formatIMListAgentsResult renders list_agents result.
func formatIMListAgentsResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("🤖 %s\n%s", imLabel(lang, "sub_task_list"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("🤖 %s", imLabel(lang, "no_active_agents"))
	}
	return fmt.Sprintf("🤖\n%s", imCodeBlock(output))
}

// --- MCP internal tools ---

// formatIMMCPCapabilitiesResult renders list_mcp_capabilities result.
func formatIMMCPCapabilitiesResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("🔗 %s\n%s", imLabel(lang, "mcp_service"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("🔗 %s", imLabel(lang, "mcp_service_list"))
	}
	return fmt.Sprintf("🔗\n%s", imCodeBlock(output))
}

// formatIMMCPPromptResult renders get_mcp_prompt result.
func formatIMMCPPromptResult(tr *ToolResultInfo) string {
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("🔗 MCP Prompt\n%s", imCodeBlock(output))
	}
	if output == "" {
		return "🔗 MCP Prompt"
	}
	return fmt.Sprintf("🔗\n%s", imCodeBlock(output))
}

// formatIMMCPResourceResult renders read_mcp_resource result.
func formatIMMCPResourceResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("🔗 %s\n%s", imLabel(lang, "resource_read"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("🔗 %s", imLabel(lang, "resource_content"))
	}
	return fmt.Sprintf("🔗\n%s", imCodeBlock(output))
}

// --- Productivity tools ---

// formatIMSkillResult renders skill result.
func formatIMSkillResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("🔧 %s\n%s", imLabel(lang, "skill_load"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("🔧 %s", imLabel(lang, "skill_loaded"))
	}
	return fmt.Sprintf("🔧\n%s", imCodeBlock(output))
}

// formatIMSaveMemoryResult renders save_memory result.
func formatIMSaveMemoryResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("💾 %s\n%s", imLabel(lang, "memory_save"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("💾 %s", imLabel(lang, "memory_saved"))
	}
	return fmt.Sprintf("💾\n%s", imCodeBlock(output))
}

// formatIMDeleteMemoryResult renders delete_memory result.
func formatIMDeleteMemoryResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("🗑️ %s\n%s", imLabel(lang, "memory_delete"), imCodeBlock(output))
	}
	if output == "" {
		return fmt.Sprintf("🗑️ %s", imLabel(lang, "memory_deleted"))
	}
	return fmt.Sprintf("🗑️\n%s", imCodeBlock(output))
}

// formatIMSleepResult renders sleep result — suppressed on success
// because the user already saw "⏳ Sleep for Xs" at ToolCall time.
func formatIMSleepResult(tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("⏳ Sleep\n%s", imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
	}
	// Suppress successful sleep results — the ToolCall event already
	// told the user "⏳ Sleep for Xs", no need to repeat.
	return ""
}

// formatIMCronCreateResult renders cron_create result.
func formatIMCronCreateResult(tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("⏰ Cron\n%s", imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
	}
	// Result is JSON: {"ID":"cron-1","CronExpr":"*/5 * * * *",...}
	var job struct {
		ID        string `json:"ID"`
		CronExpr  string `json:"CronExpr"`
		Prompt    string `json:"Prompt"`
		Recurring bool   `json:"Recurring"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(redactResult(tr.Result))), &job); err != nil {
		return "⏰ Cron job created"
	}
	return fmt.Sprintf("⏰ Cron job created: `%s` → %q", job.CronExpr, truncateRunes(job.Prompt, 50, "..."))
}

// formatIMCronDeleteResult renders cron_delete result.
func formatIMCronDeleteResult(tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("⏰ Cron\n%s", imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
	}
	return "⏰ Cron job deleted"
}

// formatIMCronListResult renders cron_list result.
func formatIMCronListResult(tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("⏰ Cron\n%s", imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
	}
	output := strings.TrimSpace(redactResult(tr.Result))
	if output == "" || strings.Contains(output, "No scheduled jobs") {
		return "⏰ No scheduled cron jobs"
	}
	return fmt.Sprintf("⏰\n%s", imCodeBlock(output))
}

// formatIMTeammateResultsResult renders teammate_results result with body markdown.
func formatIMTeammateResultsResult(tr *ToolResultInfo) string {
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		return fmt.Sprintf("📋 收集团队结果\n%s", imCodeBlock(output))
	}
	if output == "" {
		return "📋 收集团队结果"
	}
	return fmt.Sprintf("📋\n%s", imCodeBlock(output))
}

// formatIMWorktreeResult renders enter_worktree/exit_worktree results.
func formatIMWorktreeResult(icon string, tr *ToolResultInfo) string {
	if tr.IsError {
		return fmt.Sprintf("%s Worktree\n%s", icon, imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
	}
	output := strings.TrimSpace(redactResult(tr.Result))
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
	output := strings.TrimSpace(redactResult(tr.Result))
	if output != "" {
		return fmt.Sprintf("🔧 %s\n%s", pretty, imCodeBlock(output))
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
		output := strings.TrimSpace(redactResult(tr.Result))
		if cmd != "" {
			return "❌\n" + imCodeBlock(cmd) + "\n" + imCodeBlock(output)
		}
		return fmt.Sprintf("❌ %s", imLabel(lang, "command_failed"))
	}
	if cmd != "" {
		return fmt.Sprintf("✅\n%s", imCodeBlock(cmd))
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
	output := strings.TrimSpace(redactResult(tr.Result))

	if tr.IsError {
		if path != "" {
			return fmt.Sprintf("%s %s\n%s", icon, baseName, imCodeBlock(output))
		}
		return fmt.Sprintf("%s Read\n%s", icon, imCodeBlock(output))
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
		output := strings.TrimSpace(redactResult(tr.Result))
		if path != "" {
			return fmt.Sprintf("📂 %s\n%s", path, imCodeBlock(output))
		}
		return fmt.Sprintf("📂 List\n%s", imCodeBlock(output))
	}
	count := countResultLines(strings.TrimSpace(redactResult(tr.Result)))
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
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		if pattern != "" {
			return fmt.Sprintf("🔍 `%s`\n%s", pattern, imCodeBlock(output))
		}
		return fmt.Sprintf("🔍 Glob\n%s", imCodeBlock(output))
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
	icon := "✏"
	if tr.IsError {
		if path != "" {
			return fmt.Sprintf("%s %s\n%s", icon, baseName, imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
		}
		return fmt.Sprintf("%s Edit\n%s", icon, imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
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
			return fmt.Sprintf("%s %s\n%s", icon, baseName, imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
		}
		return fmt.Sprintf("%s Write\n%s", icon, imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
	}
	// Count lines from content arg in rawArgs (same logic as TUI renderFileLineCount)
	lines := countWriteLines(tr.Args)
	if path == "" {
		return fmt.Sprintf("%s Write (%d %s)", icon, lines, imLabel(lang, "lines"))
	}
	return fmt.Sprintf("%s %s (%d %s)", icon, baseName, lines, imLabel(lang, "lines"))
}

// formatIMMultiEditResult renders multi_file_edit result as a concise summary.
// The result JSON has: {"summary": "...", "results": [{"path": "...", "status": "success", "applied_edit_count": N}, ...]}
func formatIMMultiEditResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	if tr.IsError {
		return formatIMErrorResult(tr)
	}
	var raw struct {
		Summary string `json:"summary"`
		Results []struct {
			Path             string `json:"path"`
			Status           string `json:"status"`
			AppliedEditCount int    `json:"applied_edit_count"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(redactResult(tr.Result))), &raw); err != nil {
		// Non-JSON result — show trimmed output
		return fmt.Sprintf("✏ %s", strings.TrimSpace(redactResult(tr.Result)))
	}
	successCount := 0
	failedCount := 0
	totalEdits := 0
	for _, r := range raw.Results {
		if r.Status == "success" {
			successCount++
			totalEdits += r.AppliedEditCount
		} else {
			failedCount++
		}
	}
	if successCount == 0 && failedCount > 0 {
		return fmt.Sprintf("✏ %d %s (%d %s)",
			failedCount, imLabel(lang, "files_failed"), failedCount, imLabel(lang, "errors"))
	}
	if failedCount > 0 {
		return fmt.Sprintf("✏ %d %s (%d %s, %d %s)",
			successCount, imLabel(lang, "files_edited"), totalEdits, imLabel(lang, "edits"),
			failedCount, imLabel(lang, "failed"))
	}
	return fmt.Sprintf("✏ %d %s (%d %s)",
		successCount, imLabel(lang, "files_edited"), totalEdits, imLabel(lang, "edits"))
}

// formatIMMultiWriteResult renders multi_file_write result as a concise summary.
func formatIMMultiWriteResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	if tr.IsError {
		return formatIMErrorResult(tr)
	}
	// multi_file_write returns "Successfully wrote N files" or similar
	output := strings.TrimSpace(redactResult(tr.Result))
	if output == "" {
		return fmt.Sprintf("📝 %s", imLabel(lang, "write_multi"))
	}
	// Try to parse JSON result
	var raw struct {
		Results []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(output), &raw); err == nil && len(raw.Results) > 0 {
		successCount := 0
		failedCount := 0
		for _, r := range raw.Results {
			if r.Status == "success" {
				successCount++
			} else {
				failedCount++
			}
		}
		if failedCount > 0 {
			return fmt.Sprintf("📝 %d %s (%d %s)",
				successCount, imLabel(lang, "files_written"), failedCount, imLabel(lang, "failed"))
		}
		return fmt.Sprintf("📝 %d %s", successCount, imLabel(lang, "files_written"))
	}
	// Non-JSON fallback
	return fmt.Sprintf("📝 %s", output)
}

// formatIMNotebookEditResult renders notebook_edit result similar to edit_file.
func formatIMNotebookEditResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	path := extractFilePathFromArgs(tr.Args)
	if path == "" {
		path = tr.Detail
	}
	baseName := filepath.Base(path)
	icon := "📓"
	if tr.IsError {
		if path != "" {
			return fmt.Sprintf("%s %s\n%s", icon, baseName, imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
		}
		return fmt.Sprintf("%s Notebook\n%s", icon, imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
	}
	if path == "" {
		return fmt.Sprintf("%s %s", icon, imLabel(lang, "edit_notebook"))
	}
	return fmt.Sprintf("%s %s", icon, baseName)
}

// formatIMGitShowResult renders git_show result as a commit summary.
func formatIMGitShowResult(tr *ToolResultInfo) string {
	if tr.IsError {
		return formatIMErrorResult(tr)
	}
	output := strings.TrimSpace(redactResult(tr.Result))
	if output == "" {
		return "🔍 git show"
	}
	// Show first few lines (commit hash, author, date, message)
	lines := strings.SplitN(output, "\n", 6)
	var summary string
	if len(lines) > 0 {
		// First line is usually "commit <hash>"
		summary = lines[0]
		if len(lines) > 4 {
			// Include up to the commit message line
			summary = strings.Join(lines[:5], "\n")
		} else {
			summary = strings.Join(lines, "\n")
		}
	}
	return fmt.Sprintf("🔍 git show\n%s", imCodeBlock(summary))
}

// formatIMSearchResult renders search/grep result with full output in code block.
func formatIMSearchResult(tr *ToolResultInfo) string {
	lang := toolLang(tr.Lang)
	pattern := firstNonEmptyStr(extractArgValue(tr.Args, "pattern"), extractArgValue(tr.Args, "query"))
	if pattern == "" {
		pattern = tr.Detail
	}
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		if pattern != "" {
			return fmt.Sprintf("🔍 `%s`\n%s", pattern, imCodeBlock(output))
		}
		return fmt.Sprintf("🔍 Search\n%s", imCodeBlock(output))
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
		return fmt.Sprintf("🌐 %s\n%s", truncateRunes(url, 60, ""), imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
	}
	if url != "" {
		return fmt.Sprintf("🌐 %s", truncateRunes(url, 60, ""))
	}
	return "🌐 Fetch"
}

func formatIMWebSearchResult(tr *ToolResultInfo) string {
	query := extractArgValue(tr.Args, "query")
	if query == "" {
		query = tr.Detail
	}
	if tr.IsError {
		return fmt.Sprintf("🔍 %s\n%s", truncateRunes(query, 60, ""), imCodeBlock(strings.TrimSpace(redactResult(tr.Result))))
	}
	if query != "" {
		return fmt.Sprintf("🔍 %s", truncateRunes(query, 60, ""))
	}
	return "🔍 Search"
}

// formatIMGitResult renders git tool results with concise summary.
func formatIMGitResult(tr *ToolResultInfo) string {
	output := strings.TrimSpace(redactResult(tr.Result))
	if tr.IsError {
		pretty := prettifyToolName(tr.ToolName)
		return fmt.Sprintf("🔧 %s\n%s", pretty, imCodeBlock(output))
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
			if len([]rune(line)) > maxLen {
				return truncateRunes(line, maxLen, "...")
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
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := args[k].(string); ok && s != "" && k != "context" && k != "system_prompt" {
			s = compactSingleLine(s)
			if len([]rune(s)) > maxLen {
				s = truncateRunes(s, maxLen, "...")
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
		return "🖼"
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
	modified, added, deleted, untracked, renamed := 0, 0, 0, 0, 0
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
		case x == 'R' || y == 'R':
			renamed++
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
	if renamed > 0 {
		parts = append(parts, fmt.Sprintf("%d renamed", renamed))
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
