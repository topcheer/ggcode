package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/util"
)

type toolPresentation struct {
	DisplayName string
	Detail      string
	Activity    string
}

type commandPreview struct {
	Title                  string
	CommandLines           []string
	CommandHiddenLineCount int
}

const maxPreviewLines = 5

func describeTool(lang Language, toolName, rawArgs string) toolPresentation {
	args := parseToolArgs(rawArgs)
	fileTarget := displayToolFileTarget(hooks.ExtractFilePath(toolName, rawArgs))

	switch toolName {
	case "read_file":
		return toolPresentationFor(lang, "read", fileTarget)
	case "edit_file":
		if strings.TrimSpace(argString(args, "old_text")) == "" && fileTarget != "" {
			return toolPresentationFor(lang, "create", fileTarget)
		}
		return toolPresentationFor(lang, "edit", fileTarget)
	case "write_file":
		return toolPresentationFor(lang, "write", fileTarget)
	case "glob":
		return toolPresentationFor(lang, "find", displayToolTarget(argString(args, "pattern")))
	case "grep", "search_files":
		return toolPresentationFor(lang, "search", displayToolTarget(firstNonEmpty(
			argString(args, "pattern"),
			argString(args, "query"),
			argString(args, "path"),
		)))
	case "list_directory":
		return toolPresentationFor(lang, "list", displayToolFileTarget(firstNonEmpty(
			argString(args, "path"),
			argString(args, "directory"),
		)))
	case "run_command", "bash", "powershell", "start_command":
		if present, ok := commandToolPresentation(lang, rawCommandArg(args)); ok {
			return present
		}
		return toolPresentationFor(lang, "run", displayToolTarget(firstNonEmpty(
			argString(args, "command"),
			argString(args, "cmd"),
		)))
	case "write_command_input":
		// The input text being sent to the process is the most important detail
		inputText := argString(args, "input")
		jobID := argString(args, "job_id")
		if inputText != "" {
			// Truncate long input for display
			if len(inputText) > 60 {
				inputText = inputText[:57] + "…"
			}
			detail := fmt.Sprintf("→ %s", inputText)
			if jobID != "" {
				shortID := shortenJobID(jobID)
				detail = fmt.Sprintf("[%s] → %s", shortID, inputText)
			}
			return toolPresentationFor(lang, "input", detail)
		}
		return toolPresentationFor(lang, "input", displayToolTarget(firstNonEmpty(
			jobID,
			"background command",
		)))
	case "read_command_output":
		jobID := argString(args, "job_id")
		return toolPresentationFor(lang, "output", displayToolTarget(shortenJobID(jobID)))
	case "wait_command":
		jobID := argString(args, "job_id")
		waitSec := argString(args, "wait_seconds")
		detail := shortenJobID(jobID)
		if waitSec != "" {
			detail = fmt.Sprintf("%s (%ss)", detail, waitSec)
		}
		return toolPresentationFor(lang, "wait", displayToolTarget(detail))
	case "stop_command":
		jobID := argString(args, "job_id")
		return toolPresentationFor(lang, "stop", displayToolTarget(shortenJobID(jobID)))
	case "list_commands":
		return toolPresentationFor(lang, "list_jobs", "")
	case "web_fetch":
		return toolPresentationFor(lang, "fetch", displayToolTarget(argString(args, "url")))
	case "web_search":
		return toolPresentationFor(lang, "search", displayToolTarget(argString(args, "query")))
	case "todo_write":
		return toolPresentationFor(lang, "todo", "")
	case "task", "agent":
		return toolPresentationFor(lang, "task", displayToolTarget(firstNonEmpty(
			argString(args, "description"),
			argString(args, "prompt"),
			argString(args, "agent_type"),
		)))
	case "skill":
		return toolPresentationFor(lang, "skill", displayToolTarget(argString(args, "skill")))
	case "ask_user":
		return toolPresentationFor(lang, "ask", displayToolTarget(askUserToolTarget(args)))
	case "git_status":
		return toolPresentationFor(lang, "inspect", displayToolFileTarget(argString(args, "path")))
	case "git_diff":
		detail := ""
		if argString(args, "cached") == "true" {
			detail = "--cached"
		}
		if f := argString(args, "file"); f != "" {
			if detail != "" {
				detail += " "
			}
			detail += displayToolFileTarget(f)
		}
		return toolPresentationFor(lang, "diff", detail)
	case "git_log":
		return toolPresentationFor(lang, "log", "")
	case "git_show":
		return toolPresentationFor(lang, "show", displayToolTarget(argString(args, "revision")))
	case "git_blame":
		return toolPresentationFor(lang, "blame", displayToolFileTarget(argString(args, "file")))
	case "git_branch_list":
		detail := ""
		if argString(args, "remote") == "true" {
			detail = "--remote"
		}
		return toolPresentationFor(lang, "branches", detail)
	case "git_remote":
		return toolPresentationFor(lang, "remote", "")
	case "git_stash_list":
		return toolPresentationFor(lang, "stash", "list")
	case "git_add":
		files := parseStringSlice(args, "files")
		return toolPresentationFor(lang, "stage", displayToolFileTarget(strings.Join(files, ", ")))
	case "git_commit":
		return toolPresentationFor(lang, "commit", compactSingleLine(argString(args, "message")))
	case "git_stash":
		action := argString(args, "action")
		if action == "" {
			action = "push"
		}
		return toolPresentationFor(lang, "stash", action)
	case "sleep":
		sec, _ := strconv.Atoi(argString(args, "seconds"))
		ms, _ := strconv.Atoi(argString(args, "milliseconds"))
		d := time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond
		if d <= 0 {
			d = 0
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "sleep"),
			Detail:      d.String(),
			Activity:    localizedToolActivity(lang, "sleep", d.String()),
		}
	case "cron_create":
		cronExpr := argString(args, "cron")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_create"),
			Detail:      cronExpr,
			Activity:    localizedToolActivity(lang, "cron_create", cronExpr),
		}
	case "cron_delete":
		return toolPresentationFor(lang, "delete", "cron job")
	case "cron_list":
		return toolPresentationFor(lang, "inspect", "cron jobs")
	case "enter_worktree":
		name := argString(args, "name")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "enter_worktree"),
			Detail:      name,
			Activity:    localizedToolActivity(lang, "enter_worktree", name),
		}
	case "exit_worktree":
		action := argString(args, "action")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "exit_worktree"),
			Detail:      action,
			Activity:    localizedToolActivity(lang, "exit_worktree", action),
		}
	case "save_memory":
		key := argString(args, "key")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "save_memory"),
			Detail:      key,
			Activity:    localizedToolActivity(lang, "save_memory", key),
		}
	case "config":
		setting := argString(args, "setting")
		value := argString(args, "value")
		detail := setting
		if value != "" {
			detail = setting + " = " + value
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "config"),
			Detail:      detail,
			Activity:    localizedToolActivity(lang, "config", detail),
		}
	case "send_message":
		to := argString(args, "to")
		msg := argString(args, "message")
		detail := to
		if msg != "" {
			if len(msg) > 40 {
				msg = msg[:37] + "…"
			}
			detail = to + ": " + msg
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "send_message"),
			Detail:      detail,
			Activity:    localizedToolActivity(lang, "send_message", to),
		}
	case "enter_plan_mode":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "enter_plan"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "enter_plan", ""),
		}
	case "exit_plan_mode":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "exit_plan"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "exit_plan", ""),
		}
	case "task_create", "task_update", "task_get", "task_list", "task_stop", "task_output":
		return toolPresentationFor(lang, "task", displayToolTarget(firstNonEmpty(
			argString(args, "subject"),
			argString(args, "description"),
			argString(args, "taskId"),
		)))
	case "spawn_agent":
		task := argString(args, "task")
		return toolPresentation{
			DisplayName: toolLabelFor(lang, "spawn_agent"),
			Detail:      compactSingleLine(task),
			Activity:    toolLabelFor(lang, "spawn_agent"),
		}
	case "list_agents":
		return toolPresentation{
			DisplayName: toolLabelFor(lang, "list_agents"),
			Detail:      "",
			Activity:    toolLabelFor(lang, "list_agents"),
		}
	case "wait_agent":
		agentID := argString(args, "agent_id")
		return toolPresentation{
			DisplayName: toolLabelFor(lang, "wait_agent"),
			Detail:      shortenJobID(agentID),
			Activity:    toolLabelFor(lang, "wait_agent"),
		}
	case "team_create", "team_delete":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "team"),
			Detail:      argString(args, "name"),
			Activity:    localizedToolActivity(lang, "team", ""),
		}
	case "teammate_spawn", "teammate_list", "teammate_shutdown", "teammate_results":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "teammate"),
			Detail:      displayToolTarget(firstNonEmpty(argString(args, "name"), argString(args, "teammate_id"))),
			Activity:    localizedToolActivity(lang, "teammate", ""),
		}
	case "swarm_task_create", "swarm_task_claim", "swarm_task_complete", "swarm_task_list":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "swarm_task"),
			Detail:      displayToolTarget(firstNonEmpty(argString(args, "subject"), argString(args, "task_id"))),
			Activity:    localizedToolActivity(lang, "swarm_task", ""),
		}
	default:
		// LSP tools share a common pattern: show file:line
		if strings.HasPrefix(toolName, "lsp_") {
			return lspToolPresentation(lang, toolName, args, fileTarget)
		}
		pretty := prettifyToolName(toolName)
		return toolPresentation{
			DisplayName: pretty,
			Detail: displayToolTarget(firstNonEmpty(
				fileTarget,
				displayToolFileTarget(argString(args, "path")),
				displayToolFileTarget(argString(args, "file_path")),
				argString(args, "pattern"),
				argString(args, "query"),
				argString(args, "url"),
				argString(args, "description"),
			)),
			Activity: localizedGenericActivity(lang, pretty),
		}
	}
}

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
	title := strings.TrimSpace(firstNonEmpty(
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

func commandToolPresentation(lang Language, rawCommand string) (toolPresentation, bool) {
	rawCommand = relativizeResult(rawCommand)
	preview := buildCommandPreview(rawCommand)
	if preview.Title == "" {
		return toolPresentation{}, false
	}
	title := displayToolTarget(preview.Title)
	// Build detail from command lines (showing script preview alongside the title)
	var detailParts []string
	for _, line := range preview.CommandLines {
		part := compactSingleLine(strings.TrimRight(line, " \t"))
		if len(part) > 50 {
			part = part[:47] + "…"
		}
		detailParts = append(detailParts, part)
	}
	// Show at most 2 command lines in detail to avoid header bloat
	maxDetailLines := 2
	if len(detailParts) > maxDetailLines {
		hidden := len(detailParts) - maxDetailLines + preview.CommandHiddenLineCount
		detailParts = detailParts[:maxDetailLines]
		detailParts = append(detailParts, fmt.Sprintf("+%d more", hidden))
	} else if preview.CommandHiddenLineCount > 0 {
		detailParts = append(detailParts, fmt.Sprintf("+%d more", preview.CommandHiddenLineCount))
	}
	detail := strings.Join(detailParts, "; ")
	return toolPresentation{
		DisplayName: title,
		Detail:      detail,
		Activity:    localizedCommandActivity(lang, title),
	}, true
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
			return "创建定时任务"
		case "config":
			return "配置"
		case "enter_worktree":
			return "进入工作树"
		case "exit_worktree":
			return "退出工作树"
		case "send_message":
			return "发送消息"
		case "enter_plan":
			return "计划模式"
		case "exit_plan":
			return "退出计划"
		case "team":
			return "团队"
		case "teammate":
			return "队友"
		case "swarm_task":
			return "协作任务"
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
			return "启动子代理"
		case "list_agents":
			return "获取子代理列表"
		case "wait_agent":
			return "等待子代理结果"
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
			return "Schedule"
		case "config":
			return "Config"
		case "enter_worktree":
			return "Enter Worktree"
		case "exit_worktree":
			return "Exit Worktree"
		case "send_message":
			return "Message"
		case "enter_plan":
			return "Plan Mode"
		case "exit_plan":
			return "Exit Plan"
		case "team":
			return "Team"
		case "teammate":
			return "Teammate"
		case "swarm_task":
			return "Swarm Task"
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
			return "Spawn Agent"
		case "list_agents":
			return "List Agents"
		case "wait_agent":
			return "Wait Agent"
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
			case "config":
				return "更新配置..."
			case "enter_worktree":
				return "创建工作树..."
			case "exit_worktree":
				return "退出工作树..."
			case "send_message":
				return "发送消息..."
			case "enter_plan":
				return "进入计划模式..."
			case "exit_plan":
				return "退出计划模式..."
			case "team":
				return "管理团队..."
			case "teammate":
				return "管理队友..."
			case "swarm_task":
				return "管理协作任务..."
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
			case "config":
				return "Updating config..."
			case "enter_worktree":
				return "Creating worktree..."
			case "exit_worktree":
				return "Exiting worktree..."
			case "send_message":
				return "Sending message..."
			case "enter_plan":
				return "Entering plan mode..."
			case "exit_plan":
				return "Exiting plan mode..."
			case "team":
				return "Managing team..."
			case "teammate":
				return "Managing teammate..."
			case "swarm_task":
				return "Managing swarm task..."
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
	return firstNonEmpty(
		rawArgString(args, "command"),
		rawArgString(args, "cmd"),
	)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
	value = strings.TrimPrefix(value, "./")
	value = compactSingleLine(value)
	return shortenPath(value)
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
	return shortenPath(value)
}

func buildCommandPreview(rawCommand string) commandPreview {
	lines := commandPreviewLines(rawCommand)
	if len(lines) == 0 {
		return commandPreview{}
	}

	titleIndex, title := leadingCommentTitle(lines)
	previewLines := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == titleIndex {
			continue
		}
		previewLines = append(previewLines, compactSingleLine(strings.TrimRight(line, " \t")))
	}

	return commandPreview{
		Title:                  title,
		CommandLines:           previewLines,
		CommandHiddenLineCount: hiddenPreviewLineCount(len(previewLines)),
	}
}

func hiddenPreviewLineCount(total int) int {
	if total <= maxPreviewLines {
		return 0
	}
	return total - maxPreviewLines
}

func commandPreviewLines(rawCommand string) []string {
	rawCommand = strings.ReplaceAll(rawCommand, "\r\n", "\n")
	rawCommand = strings.TrimSpace(rawCommand)
	if rawCommand == "" {
		return nil
	}

	lines := strings.Split(rawCommand, "\n")
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func leadingCommentTitle(lines []string) (int, string) {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			return -1, ""
		}
		title := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		if title != "" {
			return i, title
		}
	}
	return -1, ""
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

func prettifyToolName(name string) string {
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	parts := strings.Fields(name)
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
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
