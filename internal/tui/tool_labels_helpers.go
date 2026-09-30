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

var toolLabelsZh = map[string]string{ // short zh tool labels (#917)
	"read":                  "读",
	"edit":                  "编辑",
	"create":                "创建",
	"write":                 "写",
	"search":                "搜索",
	"find":                  "查找",
	"list":                  "列出",
	"run":                   "执行",
	"run_in_background":     "后台运行",
	"fetch":                 "抓取",
	"todo":                  "更新待办",
	"task":                  "执行任务",
	"skill":                 "使用技能",
	"save_memory":           "保存记忆",
	"delete":                "删除",
	"delete_memory":         "删除记忆",
	"sleep":                 "等待",
	"cron_create":           "创建定时",
	"cron_update":           "更新定时",
	"cron_pause":            "暂停定时",
	"cron_resume":           "恢复定时",
	"cron_get":              "查看定时",
	"config":                "配置",
	"enter_worktree":        "进入工作树",
	"exit_worktree":         "退出工作树",
	"send_message":          "发送消息",
	"enter_plan":            "制定计划",
	"exit_plan":             "计划",
	"team_create":           "创建团队",
	"team_delete":           "删除团队",
	"teammate_spawn":        "添加成员",
	"teammate_list":         "成员列表",
	"teammate_shutdown":     "停止成员",
	"teammate_results":      "成员结果",
	"swarm_task_create":     "创建任务",
	"swarm_task_claim":      "领取任务",
	"swarm_task_complete":   "完成任务",
	"swarm_task_list":       "任务列表",
	"list_mcp_capabilities": "MCP 服务器",
	"get_mcp_prompt":        "获取 MCP 提示",
	"read_mcp_resource":     "读取 MCP 资源",
	"ask":                   "提问",
	"inspect":               "检查",
	"input":                 "输入",
	"output":                "读取输出",
	"wait":                  "等待",
	"stop":                  "停止",
	"list_jobs":             "后台任务",
	"diff":                  "差异",
	"log":                   "日志",
	"show":                  "查看",
	"blame":                 "溯源",
	"branches":              "分支",
	"remote":                "远程",
	"stash":                 "暂存",
	"stage":                 "暂存文件",
	"commit":                "提交",
	"spawn_agent":           "启动子代理",
	"list_agents":           "获取子代理列表",
	"wait_agent":            "检查子代理进度",
	"a2a_remote":            "远程调用",
	"a2a_discover":          "发现代理",
	"a2a_send_task":         "发送任务",
	"a2a_get_task":          "获取任务",
	"a2a_list_tasks":        "任务列表",
	"a2a_cancel_task":       "取消任务",
	"use_namedagent":        "运行命名代理",
	"create_namedagent":     "创建命名代理",
	"delete_namedagent":     "删除命名代理",
	"list_namedagent":       "命名代理列表",
}

var toolLabelsEn = map[string]string{ // short en tool labels (#917)
	"read":                  "Read",
	"edit":                  "Edit",
	"create":                "Create",
	"write":                 "Write",
	"search":                "Search",
	"find":                  "Find",
	"list":                  "List",
	"run":                   "Run",
	"run_in_background":     "Run in background",
	"fetch":                 "Fetch",
	"todo":                  "Update todos",
	"task":                  "Run task",
	"skill":                 "Using Skill",
	"save_memory":           "Save Memory",
	"delete":                "Delete",
	"delete_memory":         "Delete Memory",
	"sleep":                 "Sleep",
	"cron_create":           "Create Cron",
	"cron_update":           "Update Cron",
	"cron_pause":            "Pause Cron",
	"cron_resume":           "Resume Cron",
	"cron_get":              "Inspect Cron",
	"config":                "Config",
	"enter_worktree":        "Enter Worktree",
	"exit_worktree":         "Exit Worktree",
	"send_message":          "Message",
	"enter_plan":            "Planning",
	"exit_plan":             "Plan",
	"team_create":           "Create Team",
	"team_delete":           "Delete Team",
	"teammate_spawn":        "Add Teammate",
	"teammate_list":         "List Teammates",
	"teammate_shutdown":     "Shutdown Teammate",
	"teammate_results":      "Teammate Results",
	"swarm_task_create":     "Create Task",
	"swarm_task_claim":      "Claim Task",
	"swarm_task_complete":   "Complete Task",
	"swarm_task_list":       "Task List",
	"list_mcp_capabilities": "MCP Servers",
	"get_mcp_prompt":        "Get MCP Prompt",
	"read_mcp_resource":     "Read MCP Resource",
	"ask":                   "Ask",
	"inspect":               "Inspect",
	"input":                 "Input",
	"output":                "Read Output",
	"wait":                  "Wait",
	"stop":                  "Stop",
	"list_jobs":             "List Jobs",
	"diff":                  "Diff",
	"log":                   "Log",
	"show":                  "Show",
	"blame":                 "Blame",
	"branches":              "Branches",
	"remote":                "Remote",
	"stash":                 "Stash",
	"stage":                 "Stage",
	"commit":                "Commit",
	"spawn_agent":           "Starting subagent",
	"list_agents":           "List Agents",
	"wait_agent":            "Checking subagent progress",
	"a2a_remote":            "Remote Call",
	"a2a_discover":          "Discover",
	"a2a_send_task":         "Send Task",
	"a2a_get_task":          "Get Task",
	"a2a_list_tasks":        "List Tasks",
	"a2a_cancel_task":       "Cancel Task",
	"use_namedagent":        "Run Agent",
	"create_namedagent":     "Create Agent",
	"delete_namedagent":     "Delete Agent",
	"list_namedagent":       "List Agents",
}

var toolActivityZh = map[string]string{ // zh activity text, no target
	"read":                  "读取文件",
	"edit":                  "编辑文件",
	"create":                "创建文件",
	"write":                 "写入文件",
	"search":                "搜索中...",
	"find":                  "查找文件",
	"list":                  "列出目录",
	"run":                   "执行命令",
	"run_in_background":     "后台运行命令",
	"fetch":                 "抓取网页",
	"todo":                  "更新待办",
	"task":                  "执行任务",
	"diff":                  "查看差异",
	"log":                   "查看提交历史",
	"show":                  "查看对象",
	"blame":                 "追溯行历史",
	"branches":              "列出分支",
	"remote":                "查看远程仓库",
	"stash":                 "管理贮藏",
	"stage":                 "暂存变更",
	"commit":                "提交变更",
	"skill":                 "加载技能",
	"save_memory":           "保存记忆中...",
	"delete":                "删除中...",
	"delete_memory":         "删除记忆中...",
	"sleep":                 "等待中...",
	"cron_create":           "创建定时任务...",
	"cron_update":           "更新定时任务...",
	"cron_pause":            "暂停定时任务...",
	"cron_resume":           "恢复定时任务...",
	"cron_get":              "查看定时任务...",
	"config":                "更新配置...",
	"enter_worktree":        "创建工作树...",
	"exit_worktree":         "退出工作树...",
	"send_message":          "发送消息...",
	"enter_plan":            "制定计划中...",
	"exit_plan":             "完成计划...",
	"team_create":           "创建团队中...",
	"team_delete":           "删除团队中...",
	"teammate_spawn":        "添加成员中...",
	"teammate_list":         "查看成员列表...",
	"teammate_shutdown":     "停止成员中...",
	"teammate_results":      "获取成员结果...",
	"swarm_task_create":     "创建任务中...",
	"swarm_task_claim":      "领取任务中...",
	"swarm_task_complete":   "完成任务中...",
	"swarm_task_list":       "查看任务列表...",
	"list_mcp_capabilities": "查看 MCP 服务器...",
	"get_mcp_prompt":        "获取 MCP 提示...",
	"read_mcp_resource":     "读取 MCP 资源...",
	"ask":                   "等待用户输入",
	"inspect":               "检查中...",
	"input":                 "发送输入",
	"output":                "读取输出",
	"wait":                  "等待命令",
	"stop":                  "停止命令",
	"list_jobs":             "列出后台任务",
	"a2a_remote":            "正在远程调用...",
	"a2a_discover":          "正在发现...",
	"a2a_send_task":         "正在发送任务...",
	"a2a_get_task":          "正在获取任务...",
	"a2a_list_tasks":        "正在列出任务...",
	"a2a_cancel_task":       "正在取消任务...",
	"use_namedagent":        "正在运行命名代理...",
	"create_namedagent":     "正在创建命名代理...",
	"delete_namedagent":     "正在删除命名代理...",
	"list_namedagent":       "正在列出命名代理...",
}

var toolActivityEn = map[string]string{ // en activity text, no target
	"read":                  "Reading file",
	"edit":                  "Editing file",
	"create":                "Creating file",
	"write":                 "Writing file",
	"search":                "Searching...",
	"find":                  "Finding files",
	"list":                  "Listing directory",
	"run":                   "Running command",
	"run_in_background":     "Running command in background",
	"fetch":                 "Fetching page",
	"todo":                  "Updating todos",
	"task":                  "Running task",
	"diff":                  "Diffing",
	"log":                   "Reading commit history",
	"show":                  "Showing object",
	"blame":                 "Blaming",
	"branches":              "Listing branches",
	"remote":                "Inspecting remotes",
	"stash":                 "Managing stash",
	"stage":                 "Staging changes",
	"commit":                "Committing",
	"skill":                 "Loading skill",
	"save_memory":           "Saving memory...",
	"delete":                "Deleting...",
	"delete_memory":         "Deleting memory...",
	"sleep":                 "Sleeping...",
	"cron_create":           "Scheduling...",
	"cron_update":           "Updating cron job...",
	"cron_pause":            "Pausing cron job...",
	"cron_resume":           "Resuming cron job...",
	"cron_get":              "Inspecting cron job...",
	"config":                "Updating config...",
	"enter_worktree":        "Creating worktree...",
	"exit_worktree":         "Exiting worktree...",
	"send_message":          "Sending message...",
	"enter_plan":            "Planning...",
	"exit_plan":             "Completing plan...",
	"team_create":           "Creating team...",
	"team_delete":           "Deleting team...",
	"teammate_spawn":        "Adding teammate...",
	"teammate_list":         "Listing teammates...",
	"teammate_shutdown":     "Shutting down teammate...",
	"teammate_results":      "Fetching results...",
	"swarm_task_create":     "Creating task...",
	"swarm_task_claim":      "Claiming task...",
	"swarm_task_complete":   "Completing task...",
	"swarm_task_list":       "Listing tasks...",
	"list_mcp_capabilities": "Listing MCP servers...",
	"get_mcp_prompt":        "Fetching MCP prompt...",
	"read_mcp_resource":     "Reading MCP resource...",
	"ask":                   "Waiting for user input",
	"inspect":               "Inspecting...",
	"input":                 "Sending input",
	"output":                "Reading output",
	"wait":                  "Waiting for command",
	"stop":                  "Stopping command",
	"list_jobs":             "Listing background jobs",
	"a2a_remote":            "Calling remote...",
	"a2a_discover":          "Discovering...",
	"a2a_send_task":         "Sending task...",
	"a2a_get_task":          "Getting task...",
	"a2a_list_tasks":        "Listing tasks...",
	"a2a_cancel_task":       "Canceling task...",
	"use_namedagent":        "Running named agent...",
	"create_namedagent":     "Creating named agent...",
	"delete_namedagent":     "Deleting named agent...",
	"list_namedagent":       "Listing named agents...",
}

var toolActivityVerbZh = map[string]string{ // zh activity verb + " " + target
	"read":              "读取",
	"edit":              "编辑",
	"create":            "创建",
	"write":             "写入",
	"search":            "搜索",
	"find":              "查找",
	"list":              "列出",
	"run":               "执行",
	"run_in_background": "后台运行",
	"fetch":             "抓取",
	"task":              "执行任务",
	"skill":             "加载技能",
	"ask":               "提问",
	"inspect":           "检查",
}

var toolActivityVerbEn = map[string]string{ // en activity verb + " " + target
	"read":              "Reading",
	"edit":              "Editing",
	"create":            "Creating",
	"write":             "Writing",
	"search":            "Searching",
	"find":              "Finding",
	"list":              "Listing",
	"run":               "Running",
	"run_in_background": "Running in background",
	"fetch":             "Fetching",
	"task":              "Running task",
	"skill":             "Loading skill",
	"ask":               "Asking",
	"inspect":           "Inspecting",
}

// localizedToolLabel returns the localized short label for a tool action key,
// falling back to the generic tool name for unknown actions (#917).
func localizedToolLabel(lang Language, action string) string {
	if lang == LangZhCN {
		if s, ok := toolLabelsZh[action]; ok {
			return s
		}
	} else if s, ok := toolLabelsEn[action]; ok {
		return s
	}
	return localizedGenericToolName(lang, action)
}

// localizedToolActivity returns the running-indicator text for a tool call.
// Actions with a target render as "<verb> <target>"; the rest use fixed text.
// #1766: git sub-command actions must resolve here so they never fall back to
// generic wording; unknown actions with an empty target must not end in a
// dangling space either.
func localizedToolActivity(lang Language, action, target string) string {
	texts, verbs := toolActivityEn, toolActivityVerbEn
	if lang == LangZhCN {
		texts, verbs = toolActivityZh, toolActivityVerbZh
	}
	if target == "" {
		if s, ok := texts[action]; ok {
			return s
		}
		// Equivalent to the old fall-through to localizedGenericActivity with an
		// empty target, minus the trailing space it produced.
		if lang == LangZhCN {
			return "运行"
		}
		return "Running"
	}
	if verb, ok := verbs[action]; ok {
		return verb + " " + target
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
