package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ToolLanguage represents the language for tool status formatting.
type ToolLanguage string

const (
	ToolLangZhCN ToolLanguage = "zh-CN"
	ToolLangEn   ToolLanguage = "en"
)

// ToolPresentation holds the formatted display information for a tool call.
type ToolPresentation struct {
	DisplayName string // e.g. "读", "编辑", "执行"
	Detail      string // e.g. file path, command preview
	Activity    string // e.g. "读取 chart.html", "执行 npm test"
}

// DescribeTool produces a human-readable presentation of a tool call,
// mirroring the TUI's describeTool pipeline (tool_labels.go).
func DescribeTool(lang ToolLanguage, toolName, rawArgs string) ToolPresentation {
	args := parseToolArgs(rawArgs)
	fileTarget := displayToolFileTarget(extractFilePath(toolName, rawArgs))

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
		return toolPresentationFor(lang, "search", displayToolTarget(firstNonEmptyStr(
			argString(args, "pattern"),
			argString(args, "query"),
			argString(args, "path"),
		)))
	case "list_directory":
		return toolPresentationFor(lang, "list", displayToolFileTarget(firstNonEmptyStr(
			argString(args, "path"),
			argString(args, "directory"),
		)))
	case "run_command", "bash", "powershell", "start_command":
		return toolPresentationFor(lang, "run", displayToolTarget(firstNonEmptyStr(
			argString(args, "command"),
			argString(args, "cmd"),
		)))
	case "write_command_input":
		return toolPresentationFor(lang, "run", displayToolTarget(firstNonEmptyStr(
			argString(args, "job_id"),
			"background command",
		)))
	case "read_command_output", "wait_command", "stop_command", "list_commands":
		return toolPresentationFor(lang, "run", displayToolTarget(firstNonEmptyStr(
			argString(args, "job_id"),
			"background command",
		)))
	case "web_fetch":
		return toolPresentationFor(lang, "fetch", displayToolTarget(argString(args, "url")))
	case "web_search":
		return toolPresentationFor(lang, "search", displayToolTarget(argString(args, "query")))
	case "todo_write":
		return toolPresentationFor(lang, "todo", "")
	case "task", "agent":
		return toolPresentationFor(lang, "task", displayToolTarget(firstNonEmptyStr(
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
	default:
		pretty := prettifyToolName(toolName)
		return ToolPresentation{
			DisplayName: pretty,
			Detail: displayToolTarget(firstNonEmptyStr(
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

// FormatToolInline formats a tool name and detail as an inline status string,
// e.g. "读 chart.html" or just "编辑" if detail is trivial.
func FormatToolInline(name, detail string) string {
	if isTrivialToolDetail(detail) {
		return name
	}
	return name + " " + detail
}

// FormatIMStatus produces the final status string for IM delivery,
// mirroring the TUI's formatIMStatus pipeline:
//
//	describeTool → formatToolInline → localizeIMProgress
func FormatIMStatus(lang ToolLanguage, activity, toolName, toolArg string) string {
	activity = strings.TrimSpace(activity)
	toolSummary := strings.TrimSpace(FormatToolInline(toolName, toolArg))

	thinking := localizedThinking(lang)
	writing := localizedWriting(lang)

	if toolSummary == "" && (activity == thinking || activity == writing) {
		return ""
	}

	switch {
	case toolSummary != "" && (activity == "" || activity == thinking || activity == writing):
		return LocalizeIMProgress(lang, toolSummary)
	case activity != "":
		return LocalizeIMProgress(lang, activity)
	case toolSummary != "":
		return LocalizeIMProgress(lang, toolSummary)
	default:
		return ""
	}
}

// LocalizeIMProgress applies language-specific localization to an IM progress string,
// mirroring the TUI's localizeIMProgress function.
func LocalizeIMProgress(lang ToolLanguage, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	switch lang {
	case ToolLangZhCN:
		switch text {
		case "思考中...", "思考中…":
			return "我先想一下..."
		case "输出中...", "输出中…":
			return "我整理一下结果..."
		}
		base := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(text, "..."), "…"))
		if base == "" {
			return ""
		}
		if strings.HasPrefix(base, "我") || strings.HasPrefix(base, "正在") {
			return text
		}
		return "正在" + base + "..."
	default:
		switch text {
		case "Thinking...", "Thinking…":
			return "Let me think..."
		case "Writing...", "Writing…":
			return "I'm drafting the answer..."
		}
		base := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(text, "..."), "…"))
		if base == "" {
			return ""
		}
		if strings.HasPrefix(base, "I'm ") || strings.HasPrefix(base, "I am ") || strings.HasPrefix(base, "Let me ") {
			return text
		}
		return "Working on " + base + "..."
	}
}

// --- internal helpers ---

func toolPresentationFor(lang ToolLanguage, action, target string) ToolPresentation {
	return ToolPresentation{
		DisplayName: localizedToolLabel(lang, action),
		Detail:      target,
		Activity:    localizedToolActivity(lang, action, target),
	}
}

func localizedThinking(lang ToolLanguage) string {
	if lang == ToolLangZhCN {
		return "思考中..."
	}
	return "Thinking..."
}

func localizedWriting(lang ToolLanguage) string {
	if lang == ToolLangZhCN {
		return "输出中..."
	}
	return "Writing..."
}

func localizedToolLabel(lang ToolLanguage, action string) string {
	switch lang {
	case ToolLangZhCN:
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
		}
	}
	return localizedGenericToolName(lang, action)
}

func localizedToolActivity(lang ToolLanguage, action, target string) string {
	if target == "" {
		switch lang {
		case ToolLangZhCN:
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
			}
		}
	}

	switch lang {
	case ToolLangZhCN:
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

func localizedGenericActivity(lang ToolLanguage, label string) string {
	if lang == ToolLangZhCN {
		return "运行 " + label
	}
	return "Running " + label
}

func localizedGenericToolName(lang ToolLanguage, name string) string {
	if lang == ToolLangZhCN {
		return strings.ReplaceAll(name, "_", " ")
	}
	return prettifyToolName(name)
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

func parseToolArgs(raw string) map[string]any {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
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
		return compactSingleLine(vv)
	case float64:
		return compactSingleLine(strconv.FormatFloat(vv, 'f', -1, 64))
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return compactSingleLine(string(b))
	}
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func displayToolTarget(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "./")
	value = compactSingleLine(value)
	return truncateStr(value, 80)
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

func isTrivialToolDetail(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "{}", "[]", "null":
		return true
	default:
		return false
	}
}

func compactSingleLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

func extractFilePath(toolName, rawArgs string) string {
	args := parseToolArgs(rawArgs)
	if args == nil {
		return ""
	}
	switch toolName {
	case "read_file", "edit_file", "write_file":
		return firstNonEmptyStr(argString(args, "file_path"), argString(args, "path"))
	case "glob":
		return argString(args, "pattern")
	case "grep", "search_files":
		return firstNonEmptyStr(argString(args, "path"), argString(args, "directory"))
	case "list_directory":
		return firstNonEmptyStr(argString(args, "path"), argString(args, "directory"))
	default:
		return firstNonEmptyStr(argString(args, "path"), argString(args, "file_path"))
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
	title := strings.TrimSpace(firstNonEmptyStr(
		argAnyString(first, "title"),
		argAnyString(first, "prompt"),
	))
	if title == "" {
		return ""
	}
	if len(questions) == 1 {
		return title
	}
	return fmt.Sprintf("%s +%d", title, len(questions)-1)
}

func argAnyString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
