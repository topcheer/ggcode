package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/util"
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

// toolPresentationBuilder renders the presentation for one DescribeTool
// case. Builders are pure dispatchers over the precomputed (lang, toolName,
// args, fileTarget) tuple; none re-parse rawArgs or touch global state.
type toolPresentationBuilder func(lang ToolLanguage, toolName string, args map[string]any, fileTarget string) ToolPresentation

// toolPresentationBuilders carries each former DescribeTool switch case body
// one-to-one (r193 flatten, table-driven like the r169/r170 label/activity
// tables). The pristine cases were disjoint string matches with immediate
// returns, so case order carried no semantics and map lookup + default is
// equivalent. run_command/bash/powershell/start_command shared a
// byte-identical body, as did write_command_input and the four background
// job readers.
var toolPresentationBuilders = map[string]toolPresentationBuilder{
	"read_file":           readFilePresentation,
	"edit_file":           editFilePresentation,
	"write_file":          writeFilePresentation,
	"glob":                globPresentation,
	"grep":                searchToolPresentation,
	"search_files":        searchToolPresentation,
	"list_directory":      listDirectoryPresentation,
	"run_command":         commandToolPresentation,
	"bash":                commandToolPresentation,
	"powershell":          commandToolPresentation,
	"start_command":       commandToolPresentation,
	"write_command_input": backgroundJobPresentation,
	"read_command_output": backgroundJobPresentation,
	"wait_command":        backgroundJobPresentation,
	"stop_command":        backgroundJobPresentation,
	"list_commands":       backgroundJobPresentation,
	"web_fetch":           webFetchPresentation,
	"web_search":          webSearchPresentation,
	"todo_write":          todoWritePresentation,
	"task":                agentTaskPresentation,
	"agent":               agentTaskPresentation,
	"skill":               skillPresentation,
	"ask_user":            askUserPresentation,
	"git_diff":            gitInspectPresentation,
	"git_status":          gitInspectPresentation,
	"git_log":             gitInspectPresentation,
}

// DescribeTool produces a human-readable presentation of a tool call,
// mirroring the TUI's describeTool pipeline (tool_labels.go).
//
// The former 17-case switch (CC 22) is now a flat orchestrator: the
// pre-switch (args, fileTarget) computation is unchanged and unconditional,
// then toolPresentationBuilders dispatches to the per-case body. The
// default branch stays inline.
func DescribeTool(lang ToolLanguage, toolName, rawArgs string) ToolPresentation {
	args := parseToolArgs(rawArgs)
	fileTarget := displayToolFileTarget(extractFilePath(toolName, rawArgs))
	if build, ok := toolPresentationBuilders[toolName]; ok {
		return build(lang, toolName, args, fileTarget)
	}
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

func readFilePresentation(lang ToolLanguage, _ string, _ map[string]any, fileTarget string) ToolPresentation {
	return toolPresentationFor(lang, "read", fileTarget)
}

func editFilePresentation(lang ToolLanguage, _ string, args map[string]any, fileTarget string) ToolPresentation {
	if strings.TrimSpace(argString(args, "old_text")) == "" && fileTarget != "" {
		return toolPresentationFor(lang, "create", fileTarget)
	}
	return toolPresentationFor(lang, "edit", fileTarget)
}

func writeFilePresentation(lang ToolLanguage, _ string, _ map[string]any, fileTarget string) ToolPresentation {
	return toolPresentationFor(lang, "write", fileTarget)
}

func globPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "find", displayToolTarget(argString(args, "pattern")))
}

func searchToolPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "search", displayToolTarget(firstNonEmptyStr(
		argString(args, "pattern"),
		argString(args, "query"),
		argString(args, "path"),
	)))
}

func listDirectoryPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "list", displayToolFileTarget(firstNonEmptyStr(
		argString(args, "path"),
		argString(args, "directory"),
	)))
}

// commandToolPresentation covers run_command/bash/powershell and
// start_command, whose former switch bodies were byte-identical.
func commandToolPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	target := displayToolTarget(firstNonEmptyStr(
		argString(args, "command"),
		argString(args, "cmd"),
	))
	if desc := argString(args, "description"); desc != "" {
		return ToolPresentation{
			DisplayName: desc,
			Detail:      target,
			Activity:    localizedCommandActivity(lang, desc),
		}
	}
	return toolPresentationFor(lang, "run", target)
}

// backgroundJobPresentation covers write_command_input and the four
// background job readers, whose former switch bodies were byte-identical.
func backgroundJobPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "run", displayToolTarget(firstNonEmptyStr(
		argString(args, "job_id"),
		"background command",
	)))
}

func webFetchPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "fetch", displayToolTarget(argString(args, "url")))
}

func webSearchPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "search", displayToolTarget(argString(args, "query")))
}

func todoWritePresentation(lang ToolLanguage, _ string, _ map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "todo", "")
}

func agentTaskPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "task", displayToolTarget(firstNonEmptyStr(
		argString(args, "description"),
		argString(args, "prompt"),
		argString(args, "agent_type"),
	)))
}

func skillPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "skill", displayToolTarget(argString(args, "skill")))
}

func askUserPresentation(lang ToolLanguage, _ string, args map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "ask", displayToolTarget(askUserToolTarget(args)))
}

func gitInspectPresentation(lang ToolLanguage, toolName string, _ map[string]any, _ string) ToolPresentation {
	return toolPresentationFor(lang, "inspect", displayToolTarget(strings.ReplaceAll(toolName, "_", " ")))
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
	if v, ok := toolActivityTable(toolLabelZh, toolLabelEn, lang)[action]; ok {
		return v
	}
	return localizedGenericToolName(lang, action)
}

// localizedToolActivity resolves the progress activity line for a tool
// action. Tables live in tool_activity_tables.go (extracted r169 from the
// former inline switches, mirroring the r166 imLabel split). The pristine
// with-target switch has no "todo" entry, so todo+target falls through to
// localizedGenericActivity; unknown actions always do. Output is pinned
// byte-for-byte by TestLocalizedToolActivityGolden.
func localizedToolActivity(lang ToolLanguage, action, target string) string {
	if target == "" {
		if v, ok := toolActivityTable(toolActivityNoTargetZh, toolActivityNoTargetEn, lang)[action]; ok {
			return v
		}
		return localizedGenericActivity(lang, "")
	}
	if v, ok := toolActivityTable(toolActivityWithTargetZh, toolActivityWithTargetEn, lang)[action]; ok {
		return v + target
	}
	return localizedGenericActivity(lang, target)
}

// toolActivityTable picks the zh table for zh-CN and the en table for every
// other language, mirroring the pre-refactor switch's case/default split.
func toolActivityTable(zh, en map[string]string, lang ToolLanguage) map[string]string {
	if lang == ToolLangZhCN {
		return zh
	}
	return en
}

func localizedCommandActivity(lang ToolLanguage, desc string) string {
	if lang == ToolLangZhCN {
		return "正在" + desc
	}
	return desc
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
		parts[i] = strings.ToUpper(string([]rune(part)[:1])) + string([]rune(part)[1:])
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
	// Try to make absolute paths relative to cwd
	if filepath.IsAbs(value) {
		cwd, _ := os.Getwd()
		normCWD := normalizeDisplayPath(cwd)
		normValue := normalizeDisplayPath(value)
		if rel, relErr := filepath.Rel(normCWD, normValue); relErr == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	cwd, _ := os.Getwd()
	return util.FormatToolDetail(filepath.ToSlash(value), cwd)
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
