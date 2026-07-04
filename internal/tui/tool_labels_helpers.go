package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/util"
)

func askUserToolTarget(args map[string]any) string {
	if title := strings.TrimSpace(argString(args, "title")); title != "" {
		return title
	}
	rawQuestions, ok := args["questions"]
	if !ok {
		return ""
	}
	questions, ok := rawQuestions.([]any)
	if !ok || len(questions) == 0 {
		return ""
	}
	first, ok := questions[0].(map[string]any)
	if !ok {
		return ""
	}
	title := strings.TrimSpace(util.FirstNonEmpty(
		argAnyString(first["title"]),
		argAnyString(first["prompt"]),
	))
	if title == "" {
		return ""
	}
	if len(questions) == 1 {
		return title
	}
	return fmt.Sprintf("%s +%d", title, len(questions)-1)
}

func argAnyString(v any) string {
	s, _ := v.(string)
	return s
}

func toolPresentationFor(lang Language, action, target string) toolPresentation {
	return toolPresentation{
		DisplayName: localizedToolLabel(lang, action),
		Detail:      target,
		Activity:    localizedToolActivity(lang, action, target),
	}
}

func toolLabelFor(lang Language, action string) string {
	return localizedToolLabel(lang, action)
}

func localizedToolLabel(lang Language, action string) string {
	switch lang {
	case LangZhCN:
		switch action {
		case "read":
			return "读"
		case "edit":
			return "编辑"
		case "create":
			return "创建"
		case "write":
			return "写"
		case "search":
			return "搜索"
		case "find":
			return "查找"
		case "list":
			return "列出"
		case "run":
			return "执行"
		case "run_in_background":
			return "后台运行"
		case "fetch":
			return "抓取"
		case "todo":
			return "更新待办"
		case "task":
			return "执行任务"
		case "skill":
			return "使用技能"
		case "save_memory":
			return "保存记忆"
		case "sleep":
			return "等待"
		case "cron_create":
			return "创建定时"
		case "cron_update":
			return "更新定时"
		case "cron_pause":
			return "暂停定时"
		case "cron_resume":
			return "恢复定时"
		case "cron_get":
			return "查看定时"
		case "config":
			return "配置"
		case "enter_worktree":
			return "进入工作树"
		case "exit_worktree":
			return "退出工作树"
		case "send_message":
			return "发送消息"
		case "enter_plan":
			return "制定计划"
		case "exit_plan":
			return "计划"
		case "team_create":
			return "创建团队"
		case "team_delete":
			return "删除团队"
		case "teammate_spawn":
			return "添加成员"
		case "teammate_list":
			return "成员列表"
		case "teammate_shutdown":
			return "停止成员"
		case "teammate_results":
			return "成员结果"
		case "swarm_task_create":
			return "创建任务"
		case "swarm_task_claim":
			return "领取任务"
		case "swarm_task_complete":
			return "完成任务"
		case "swarm_task_list":
			return "任务列表"
		case "list_mcp_capabilities":
			return "MCP 服务器"
		case "get_mcp_prompt":
			return "获取 MCP 提示"
		case "read_mcp_resource":
			return "读取 MCP 资源"
		case "ask":
			return "提问"
		case "inspect":
			return "检查"
		case "input":
			return "输入"
		case "output":
			return "读取输出"
		case "wait":
			return "等待"
		case "stop":
			return "停止"
		case "list_jobs":
			return "后台任务"
		case "diff":
			return "差异"
		case "log":
			return "日志"
		case "show":
			return "查看"
		case "blame":
			return "溯源"
		case "branches":
			return "分支"
		case "remote":
			return "远程"
		case "stash":
			return "暂存"
		case "stage":
			return "暂存文件"
		case "commit":
			return "提交"
		case "spawn_agent":
			return "Starting subagent"
		case "list_agents":
			return "获取子代理列表"
		case "wait_agent":
			return "Checking subagent progress"
		case "a2a_remote":
			return "远程调用"
		case "a2a_discover":
			return "发现代理"
		case "a2a_send_task":
			return "发送任务"
		case "a2a_get_task":
			return "获取任务"
		case "a2a_list_tasks":
			return "任务列表"
		case "a2a_cancel_task":
			return "取消任务"
		}
	default:
		switch action {
		case "read":
			return "Read"
		case "edit":
			return "Edit"
		case "create":
			return "Create"
		case "write":
			return "Write"
		case "search":
			return "Search"
		case "find":
			return "Find"
		case "list":
			return "List"
		case "run":
			return "Run"
		case "run_in_background":
			return "Run in background"
		case "fetch":
			return "Fetch"
		case "todo":
			return "Update todos"
		case "task":
			return "Run task"
		case "skill":
			return "Using Skill"
		case "save_memory":
			return "Save Memory"
		case "sleep":
			return "Sleep"
		case "cron_create":
			return "Create Cron"
		case "cron_update":
			return "Update Cron"
		case "cron_pause":
			return "Pause Cron"
		case "cron_resume":
			return "Resume Cron"
		case "cron_get":
			return "Inspect Cron"
		case "config":
			return "Config"
		case "enter_worktree":
			return "Enter Worktree"
		case "exit_worktree":
			return "Exit Worktree"
		case "send_message":
			return "Message"
		case "enter_plan":
			return "Planning"
		case "exit_plan":
			return "Plan"
		case "team_create":
			return "Create Team"
		case "team_delete":
			return "Delete Team"
		case "teammate_spawn":
			return "Add Teammate"
		case "teammate_list":
			return "List Teammates"
		case "teammate_shutdown":
			return "Shutdown Teammate"
		case "teammate_results":
			return "Teammate Results"
		case "swarm_task_create":
			return "Create Task"
		case "swarm_task_claim":
			return "Claim Task"
		case "swarm_task_complete":
			return "Complete Task"
		case "swarm_task_list":
			return "Task List"
		case "list_mcp_capabilities":
			return "MCP Servers"
		case "get_mcp_prompt":
			return "Get MCP Prompt"
		case "read_mcp_resource":
			return "Read MCP Resource"
		case "ask":
			return "Ask"
		case "inspect":
			return "Inspect"
		case "input":
			return "Input"
		case "output":
			return "Read Output"
		case "wait":
			return "Wait"
		case "stop":
			return "Stop"
		case "list_jobs":
			return "List Jobs"
		case "diff":
			return "Diff"
		case "log":
			return "Log"
		case "show":
			return "Show"
		case "blame":
			return "Blame"
		case "branches":
			return "Branches"
		case "remote":
			return "Remote"
		case "stash":
			return "Stash"
		case "stage":
			return "Stage"
		case "commit":
			return "Commit"
		case "spawn_agent":
			return "Starting subagent"
		case "list_agents":
			return "List Agents"
		case "wait_agent":
			return "Checking subagent progress"
		case "a2a_remote":
			return "Remote Call"
		case "a2a_discover":
			return "Discover"
		case "a2a_send_task":
			return "Send Task"
		case "a2a_get_task":
			return "Get Task"
		case "a2a_list_tasks":
			return "List Tasks"
		case "a2a_cancel_task":
			return "Cancel Task"
		}
	}
	return localizedGenericToolName(lang, action)
}

func localizedToolActivity(lang Language, action, target string) string {
	if target == "" {
		switch lang {
		case LangZhCN:
			switch action {
			case "read":
				return "读取文件"
			case "edit":
				return "编辑文件"
			case "create":
				return "创建文件"
			case "write":
				return "写入文件"
			case "search":
				return "搜索中..."
			case "find":
				return "查找文件"
			case "list":
				return "列出目录"
			case "run":
				return "执行命令"
			case "run_in_background":
				return "后台运行命令"
			case "fetch":
				return "抓取网页"
			case "todo":
				return "更新待办"
			case "task":
				return "执行任务"
			case "skill":
				return "加载技能"
			case "save_memory":
				return "保存记忆中..."
			case "sleep":
				return "等待中..."
			case "cron_create":
				return "创建定时任务..."
			case "cron_update":
				return "更新定时任务..."
			case "cron_pause":
				return "暂停定时任务..."
			case "cron_resume":
				return "恢复定时任务..."
			case "cron_get":
				return "查看定时任务..."
			case "config":
				return "更新配置..."
			case "enter_worktree":
				return "创建工作树..."
			case "exit_worktree":
				return "退出工作树..."
			case "send_message":
				return "发送消息..."
			case "enter_plan":
				return "制定计划中..."
			case "exit_plan":
				return "完成计划..."
			case "team_create":
				return "创建团队中..."
			case "team_delete":
				return "删除团队中..."
			case "teammate_spawn":
				return "添加成员中..."
			case "teammate_list":
				return "查看成员列表..."
			case "teammate_shutdown":
				return "停止成员中..."
			case "teammate_results":
				return "获取成员结果..."
			case "swarm_task_create":
				return "创建任务中..."
			case "swarm_task_claim":
				return "领取任务中..."
			case "swarm_task_complete":
				return "完成任务中..."
			case "swarm_task_list":
				return "查看任务列表..."
			case "list_mcp_capabilities":
				return "查看 MCP 服务器..."
			case "get_mcp_prompt":
				return "获取 MCP 提示..."
			case "read_mcp_resource":
				return "读取 MCP 资源..."
			case "ask":
				return "等待用户输入"
			case "inspect":
				return "检查中..."
			case "input":
				return "发送输入"
			case "output":
				return "读取输出"
			case "wait":
				return "等待命令"
			case "stop":
				return "停止命令"
			case "list_jobs":
				return "列出后台任务"
			case "a2a_remote":
				return "正在远程调用..."
			case "a2a_discover":
				return "正在发现..."
			case "a2a_send_task":
				return "正在发送任务..."
			case "a2a_get_task":
				return "正在获取任务..."
			case "a2a_list_tasks":
				return "正在列出任务..."
			case "a2a_cancel_task":
				return "正在取消任务..."
			}
		default:
			switch action {
			case "read":
				return "Reading file"
			case "edit":
				return "Editing file"
			case "create":
				return "Creating file"
			case "write":
				return "Writing file"
			case "search":
				return "Searching..."
			case "find":
				return "Finding files"
			case "list":
				return "Listing directory"
			case "run":
				return "Running command"
			case "run_in_background":
				return "Running command in background"
			case "fetch":
				return "Fetching page"
			case "todo":
				return "Updating todos"
			case "task":
				return "Running task"
			case "skill":
				return "Loading skill"
			case "save_memory":
				return "Saving memory..."
			case "sleep":
				return "Sleeping..."
			case "cron_create":
				return "Scheduling..."
			case "cron_update":
				return "Updating cron job..."
			case "cron_pause":
				return "Pausing cron job..."
			case "cron_resume":
				return "Resuming cron job..."
			case "cron_get":
				return "Inspecting cron job..."
			case "config":
				return "Updating config..."
			case "enter_worktree":
				return "Creating worktree..."
			case "exit_worktree":
				return "Exiting worktree..."
			case "send_message":
				return "Sending message..."
			case "enter_plan":
				return "Planning..."
			case "exit_plan":
				return "Completing plan..."
			case "team_create":
				return "Creating team..."
			case "team_delete":
				return "Deleting team..."
			case "teammate_spawn":
				return "Adding teammate..."
			case "teammate_list":
				return "Listing teammates..."
			case "teammate_shutdown":
				return "Shutting down teammate..."
			case "teammate_results":
				return "Fetching results..."
			case "swarm_task_create":
				return "Creating task..."
			case "swarm_task_claim":
				return "Claiming task..."
			case "swarm_task_complete":
				return "Completing task..."
			case "swarm_task_list":
				return "Listing tasks..."
			case "list_mcp_capabilities":
				return "Listing MCP servers..."
			case "get_mcp_prompt":
				return "Fetching MCP prompt..."
			case "read_mcp_resource":
				return "Reading MCP resource..."
			case "ask":
				return "Waiting for user input"
			case "inspect":
				return "Inspecting..."
			case "input":
				return "Sending input"
			case "output":
				return "Reading output"
			case "wait":
				return "Waiting for command"
			case "stop":
				return "Stopping command"
			case "list_jobs":
				return "Listing background jobs"
			case "a2a_remote":
				return "Calling remote..."
			case "a2a_discover":
				return "Discovering..."
			case "a2a_send_task":
				return "Sending task..."
			case "a2a_get_task":
				return "Getting task..."
			case "a2a_list_tasks":
				return "Listing tasks..."
			case "a2a_cancel_task":
				return "Canceling task..."
			}
		}
	}

	switch lang {
	case LangZhCN:
		switch action {
		case "read":
			return "读取 " + target
		case "edit":
			return "编辑 " + target
		case "create":
			return "创建 " + target
		case "write":
			return "写入 " + target
		case "search":
			return "搜索 " + target
		case "find":
			return "查找 " + target
		case "list":
			return "列出 " + target
		case "run":
			return "执行 " + target
		case "run_in_background":
			return "后台运行 " + target
		case "fetch":
			return "抓取 " + target
		case "task":
			return "执行任务 " + target
		case "skill":
			return "加载技能 " + target
		case "ask":
			return "提问 " + target
		case "inspect":
			return "检查 " + target
		}
	default:
		switch action {
		case "read":
			return "Reading " + target
		case "edit":
			return "Editing " + target
		case "create":
			return "Creating " + target
		case "write":
			return "Writing " + target
		case "search":
			return "Searching " + target
		case "find":
			return "Finding " + target
		case "list":
			return "Listing " + target
		case "run":
			return "Running " + target
		case "run_in_background":
			return "Running in background " + target
		case "fetch":
			return "Fetching " + target
		case "task":
			return "Running task " + target
		case "skill":
			return "Loading skill " + target
		case "ask":
			return "Asking " + target
		case "inspect":
			return "Inspecting " + target
		}
	}
	return localizedGenericActivity(lang, target)
}

func localizedGenericActivity(lang Language, label string) string {
	if lang == LangZhCN {
		return "运行 " + label
	}
	return "Running " + label
}

func localizedCommandActivity(lang Language, title string) string {
	if lang == LangZhCN {
		return "执行 " + title
	}
	return "Running " + title
}

func localizedGenericToolName(lang Language, name string) string {
	if lang == LangZhCN {
		return strings.ReplaceAll(name, "_", " ")
	}
	return prettifyToolName(name)
}

func formatToolInline(name, detail string) string {
	if isTrivialToolDetail(detail) {
		return name
	}
	return name + " " + detail
}

func parseToolArgs(raw string) map[string]any {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}

func compactToolArgsPreview(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if isTrivialToolDetail(trimmed) {
		return ""
	}
	args := parseToolArgs(raw)
	if args == nil {
		return compactSingleLine(raw)
	}
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"file_path", "path", "directory", "file", "filename"} {
		if value, ok := args[key].(string); ok {
			args[key] = displayToolFileTarget(value)
		}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return compactSingleLine(raw)
	}
	return compactSingleLine(string(b))
}

func isTrivialToolDetail(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "{}", "[]", "null":
		return true
	default:
		return false
	}
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	switch vv := v.(type) {
	case string:
		return vv
	case float64:
		return strconv.FormatFloat(vv, 'f', -1, 64)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// parseStringSlice extracts a string array from parsed args.
func parseStringSlice(args map[string]any, key string) []string {
	if args == nil {
		return nil
	}
	v, ok := args[key]
	if !ok {
		return nil
	}
	switch vv := v.(type) {
	case []string:
		return vv
	case []any:
		var result []string
		for _, item := range vv {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func rawArgString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

func rawCommandArg(args map[string]any) string {
	return util.FirstNonEmpty(
		rawArgString(args, "command"),
		rawArgString(args, "cmd"),
	)
}

func shortenJobID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// lspToolPresentation creates a presentation for LSP tools showing file:line.
func lspToolPresentation(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	// Map LSP tool names to short action labels
	var action string
	switch toolName {
	case "lsp_hover":
		action = "hover"
	case "lsp_definition":
		action = "definition"
	case "lsp_references":
		action = "references"
	case "lsp_symbols":
		action = "symbols"
	case "lsp_workspace_symbols":
		action = "workspace symbols"
	case "lsp_diagnostics":
		action = "diagnostics"
	case "lsp_code_actions":
		action = "code actions"
	case "lsp_rename":
		action = "rename"
	case "lsp_implementation":
		action = "implementation"
	case "lsp_prepare_call_hierarchy":
		action = "call hierarchy"
	case "lsp_incoming_calls":
		action = "incoming calls"
	case "lsp_outgoing_calls":
		action = "outgoing calls"
	default:
		action = strings.TrimPrefix(toolName, "lsp_")
	}

	// Build detail: "file:line" or just "file"
	detail := fileTarget
	if detail == "" {
		detail = displayToolFileTarget(argString(args, "path"))
	}
	line := argString(args, "line")
	if line != "" && detail != "" {
		detail = detail + ":" + line
	}

	// For rename, include the new name
	if toolName == "lsp_rename" {
		newName := argString(args, "new_name")
		if newName != "" {
			detail = fmt.Sprintf("%s → %s", detail, newName)
		}
	}

	displayName := "LSP"
	switch lang {
	case LangZhCN:
		activity := "LSP " + action
		if detail != "" {
			activity = "LSP " + action + " " + detail
		}
		return toolPresentation{
			DisplayName: displayName,
			Detail:      detail,
			Activity:    activity,
		}
	default:
		activity := "LSP " + action
		if detail != "" {
			activity = "LSP " + action + " " + detail
		}
		return toolPresentation{
			DisplayName: displayName,
			Detail:      detail,
			Activity:    activity,
		}
	}
}

// shortenPath renders a path in compact form.
// For workspace-relative paths ≤ 30 chars: return as-is.
// For longer or absolute paths: "/first10chars.../basename".
func shortenPath(path string) string {
	path = filepath.ToSlash(path)
	if path == "" || path == "." || path == "/" {
		return path
	}
	base := filepath.ToSlash(filepath.Base(path))
	if base == "" || base == "." || base == "/" {
		return path
	}

	// If not absolute, try as-is first (relative paths are already short)
	if !filepath.IsAbs(path) && len(path) <= 30 {
		return path
	}

	// Try to make it relative to cwd first
	cwd, err := os.Getwd()
	if err == nil && filepath.IsAbs(path) {
		normCWD := normalizeDisplayPath(cwd)
		normPath := normalizeDisplayPath(path)
		if rel, relErr := filepath.Rel(normCWD, normPath); relErr == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			rel = filepath.ToSlash(rel)
			if len(rel) <= 30 {
				return rel
			}
		}
	}

	// Shorten: "first10chars.../basename"
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." || dir == "" {
		return path
	}
	prefix := dir
	if len(prefix) > 10 {
		prefix = prefix[:10]
	}
	return prefix + ".../" + base
}

func displayToolTarget(value string) string {
	value = strings.TrimSpace(value)
	value = compactSingleLine(value)
	cwd, _ := os.Getwd()
	return util.FormatToolDetail(value, cwd)
}

func displayToolFileTarget(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimRight(value, `/\`)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "./")
	// Try to make absolute paths relative to cwd
	if filepath.IsAbs(value) {
		cwd, _ := os.Getwd()
		normCWD := normalizeDisplayPath(cwd)
		normValue := normalizeDisplayPath(value)
		if rel, relErr := filepath.Rel(normCWD, normValue); relErr == nil && !strings.HasPrefix(rel, "..") {
			value = filepath.ToSlash(rel)
		}
	}
	cwd, _ := os.Getwd()
	return util.FormatToolDetail(value, cwd)
}

func normalizeDisplayPath(value string) string {
	value = filepath.Clean(value)
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		return resolved
	}
	dir := filepath.Dir(value)
	base := filepath.Base(value)
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolvedDir, base)
	}
	return value
}

// firstLine returns the first line of a multi-line string.
func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		return s[:idx]
	}
	return s
}

// friendlyToolName returns a short, user-friendly name for a tool,
// with special mappings for common tools and prettifyToolName as fallback.
func friendlyToolName(name string) string {
	switch name {
	case "run_command":
		return "Bash"
	case "start_command":
		return "Background Bash"
	default:
		return prettifyToolName(name)
	}
}

func prettifyToolName(name string) string {
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	parts := strings.Fields(name)
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

// relativizeResult replaces absolute paths in tool result text with relative paths.
func relativizeResult(text string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return text
	}
	return util.RelativizePaths(text, cwd)
}
