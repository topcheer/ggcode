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
	case "git_diff", "git_status", "git_log":
		return toolPresentationFor(lang, "inspect", displayToolTarget(strings.ReplaceAll(toolName, "_", " ")))
	case "sleep":
		sec, _ := strconv.Atoi(argString(args, "seconds"))
		ms, _ := strconv.Atoi(argString(args, "milliseconds"))
		d := time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond
		if d <= 0 {
			d = 0
		}
		return toolPresentation{
			DisplayName: "Sleep",
			Detail:      d.String(),
			Activity:    "Sleep for " + d.String(),
		}
	case "cron_create":
		cronExpr := argString(args, "cron")
		return toolPresentation{
			DisplayName: "Schedule",
			Detail:      cronExpr,
			Activity:    "Schedule " + cronExpr,
		}
	case "cron_delete":
		return toolPresentationFor(lang, "delete", "cron job")
	case "cron_list":
		return toolPresentationFor(lang, "inspect", "cron jobs")
	case "enter_worktree":
		name := argString(args, "name")
		return toolPresentation{
			DisplayName: "Enter Worktree",
			Detail:      name,
			Activity:    "Create worktree",
		}
	case "exit_worktree":
		action := argString(args, "action")
		return toolPresentation{
			DisplayName: "Exit Worktree",
			Detail:      action,
			Activity:    "Exit worktree (" + action + ")",
		}
	case "save_memory":
		key := argString(args, "key")
		content := argString(args, "content")
		detail := key
		if content != "" && len(content) > 40 {
			content = content[:37] + "…"
		}
		if content != "" {
			detail = key + ": " + content
		}
		return toolPresentation{
			DisplayName: "Memory",
			Detail:      detail,
			Activity:    "Saving memory " + key,
		}
	case "config":
		setting := argString(args, "setting")
		value := argString(args, "value")
		detail := setting
		if value != "" {
			detail = setting + " = " + value
		}
		return toolPresentation{
			DisplayName: "Config",
			Detail:      detail,
			Activity:    "Updating config " + setting,
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
			DisplayName: "Message",
			Detail:      detail,
			Activity:    "Sending message to " + to,
		}
	case "enter_plan_mode":
		return toolPresentation{
			DisplayName: "Plan Mode",
			Detail:      "",
			Activity:    "Entering plan mode",
		}
	case "exit_plan_mode":
		return toolPresentation{
			DisplayName: "Plan Mode",
			Detail:      "",
			Activity:    "Exiting plan mode",
		}
	case "task_create", "task_update", "task_get", "task_list", "task_stop", "task_output":
		return toolPresentationFor(lang, "task", displayToolTarget(firstNonEmpty(
			argString(args, "subject"),
			argString(args, "description"),
			argString(args, "taskId"),
		)))
	case "list_agents", "wait_agent":
		agentID := argString(args, "agent_id")
		return toolPresentation{
			DisplayName: "Agent",
			Detail:      shortenJobID(agentID),
			Activity:    "Managing agent " + shortenJobID(agentID),
		}
	case "team_create", "team_delete":
		return toolPresentation{
			DisplayName: "Team",
			Detail:      argString(args, "name"),
			Activity:    "Managing team",
		}
	case "teammate_spawn", "teammate_list", "teammate_shutdown", "teammate_results":
		return toolPresentation{
			DisplayName: "Swarm",
			Detail:      displayToolTarget(firstNonEmpty(argString(args, "name"), argString(args, "teammate_id"))),
			Activity:    "Managing swarm teammate",
		}
	case "swarm_task_create", "swarm_task_claim", "swarm_task_complete", "swarm_task_list":
		return toolPresentation{
			DisplayName: "Swarm Task",
			Detail:      displayToolTarget(firstNonEmpty(argString(args, "subject"), argString(args, "task_id"))),
			Activity:    "Managing swarm task",
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
			return "加载技能"
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
			return "Load skill"
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

func displayToolTarget(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "./")
	value = compactSingleLine(value)
	return truncateString(value, 80)
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

	cwd, err := os.Getwd()
	if err == nil {
		if filepath.IsAbs(value) {
			normCWD := normalizeDisplayPath(cwd)
			normValue := normalizeDisplayPath(value)
			if rel, relErr := filepath.Rel(normCWD, normValue); relErr == nil && !strings.HasPrefix(rel, "..") && rel != "." {
				return displayToolTarget(filepath.ToSlash(rel))
			}
		} else if !strings.HasPrefix(value, "..") {
			clean := filepath.Clean(value)
			if clean != "." {
				return displayToolTarget(filepath.ToSlash(clean))
			}
		}
	}

	base := filepath.Base(value)
	if base == "." || base == string(filepath.Separator) {
		base = value
	}
	return displayToolTarget(base)
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
