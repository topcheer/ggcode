package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/hooks"
)

type toolPresentation struct {
	DisplayName string
	Detail      string
	Activity    string
}

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
	case "run_command", "bash", "powershell":
		return toolPresentationFor(lang, "run", displayToolTarget(firstNonEmpty(
			argString(args, "command"),
			argString(args, "cmd"),
		)))
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
		return toolPresentationFor(lang, "ask", displayToolTarget(argString(args, "question")))
	case "git_diff", "git_status", "git_log":
		return toolPresentationFor(lang, "inspect", displayToolTarget(strings.ReplaceAll(toolName, "_", " ")))
	default:
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

func toolPresentationFor(lang Language, action, target string) toolPresentation {
	return toolPresentation{
		DisplayName: localizedToolLabel(lang, action),
		Detail:      target,
		Activity:    localizedToolActivity(lang, action, target),
	}
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

func localizedGenericToolName(lang Language, name string) string {
	if lang == LangZhCN {
		return strings.ReplaceAll(name, "_", " ")
	}
	return prettifyToolName(name)
}

func formatToolInline(name, detail string) string {
	if detail == "" {
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
	args := parseToolArgs(raw)
	if args == nil {
		return compactSingleLine(raw)
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

func firstNonEmpty(values ...string) string {
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
