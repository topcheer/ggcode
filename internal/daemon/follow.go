package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	charm "charm.land/glamour/v2"
	"github.com/topcheer/ggcode/internal/util"
)

// FollowSink receives agent events for terminal follow-mode display.
type FollowSink interface {
	// OnUserMessage displays a user message.
	OnUserMessage(text string)
	// OnToolStatus is called when a tool starts (used only for internal tracking, not display).
	OnToolStatus(toolName, rawArgs string)
	// OnToolResult displays the result of a tool call.
	OnToolResult(toolName, rawArgs, result string, isError bool)
	// OnStreamText accumulates streaming text (not displayed until OnRoundDone).
	OnStreamText(text string)
	// OnRoundDone displays the accumulated assistant text and a separator.
	OnRoundDone()
	// OnError displays an error message.
	OnError(err error)
	// Close cleans up the display.
	Close()
}

// Lang represents the display language for follow output.
type Lang string

const (
	LangZhCN Lang = "zh-CN"
	LangEn   Lang = "en"
)

// ResolveLang returns a Lang from a raw config language string.
func ResolveLang(s string) Lang {
	if s == "zh-CN" || s == "zh" {
		return LangZhCN
	}
	return LangEn
}

// ToolFormatter is a function that formats tool activity for display.
// It takes a tool name and raw JSON args, and returns a human-readable status string.
type ToolFormatter func(toolName, rawArgs string) string

// ANSI color codes
const (
	ansiCyanBold  = "\033[1;36m"
	ansiGreenBold = "\033[1;32m"
	ansiDim       = "\033[2m"
	ansiReset     = "\033[0m"
	ansiDimYellow = "\033[2;33m"
	ansiRedBold   = "\033[1;31m"
	ansiClearLine = "\033[2K\r"
	// In raw terminal mode, \n only moves cursor down without returning to
	// column 0. We must use \r\n for proper line breaks.
	nl = "\r\n"
)

// --- i18n catalog for daemon/follow output ---

var catalogEn = map[string]string{
	"daemon.started_bg":    "ggcode daemon started in background (PID: %d)",
	"daemon.workdir":       "Working directory: %s",
	"daemon.started":       "ggcode daemon started (session: %s)",
	"daemon.keys_full":     "Press x to exit, d for background, f to toggle follow",
	"daemon.keys_minimal":  "Press x to exit",
	"daemon.shutting_down": "\nShutting down...",
	"daemon.stopped":       "ggcode daemon stopped",
	"daemon.follow_on":     "follow mode enabled",
	"daemon.follow_off":    "follow mode disabled",
	"daemon.bg_ok":         "Switched to background (PID: %d)",
	"daemon.bg_fail":       "Failed to start background: %v",
	"daemon.no_binding":    "No IM channel paired with this workspace.\nPair an IM channel via /qq, /tg etc in TUI mode first, then use daemon mode.",

	"daemon.resume.title":   "Available sessions:",
	"daemon.resume.item":    "  %d. %s  (%s)",
	"daemon.resume.prompt":  "Enter session number to resume (or press Enter to start new): ",
	"daemon.resume.invalid": "Invalid selection, starting new session.",
	"daemon.resume.empty":   "No previous sessions found. Starting new session.",

	"follow.user":         "User",
	"follow.assistant":    "Assistant",
	"follow.todos_header": "  📋 Todos:",
	"follow.more_lines":   "... (%d more lines)",
}

var catalogZhCN = map[string]string{
	"daemon.started_bg":    "ggcode daemon 已在后台启动 (PID: %d)",
	"daemon.workdir":       "工作目录: %s",
	"daemon.started":       "ggcode daemon 已启动 (session: %s)",
	"daemon.keys_full":     "按 x 退出, d 后台运行, f 切换 follow 模式",
	"daemon.keys_minimal":  "按 x 退出",
	"daemon.shutting_down": "\n正在关闭...",
	"daemon.stopped":       "ggcode daemon 已停止",
	"daemon.follow_on":     "follow 模式已开启",
	"daemon.follow_off":    "follow 模式已关闭",
	"daemon.bg_ok":         "已切换到后台 (PID: %d)",
	"daemon.bg_fail":       "后台启动失败: %v",
	"daemon.no_binding":    "当前工作目录没有配对的 IM 渠道。\n请先在 TUI 模式下通过 /qq、/tg 等命令配对 IM 渠道，然后再使用 daemon 模式。",

	"daemon.resume.title":   "可恢复的会话:",
	"daemon.resume.item":    "  %d. %s  (%s)",
	"daemon.resume.prompt":  "输入序号恢复会话 (直接回车创建新会话): ",
	"daemon.resume.invalid": "无效选择, 将创建新会话。",
	"daemon.resume.empty":   "没有历史会话, 将创建新会话。",

	"follow.user":         "用户",
	"follow.assistant":    "助手",
	"follow.todos_header": "  📋 待办事项:",
	"follow.more_lines":   "... (还有 %d 行)",
}

// Tr looks up a localized string by key. Falls back to English if key missing.
func Tr(lang Lang, key string, args ...any) string {
	var msg string
	if lang == LangZhCN {
		msg = catalogZhCN[key]
	}
	if msg == "" {
		msg = catalogEn[key]
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// TerminalFollowDisplay renders agent activity to the terminal using ANSI codes.
type TerminalFollowDisplay struct {
	out        *os.File
	formatTool ToolFormatter
	lang       Lang
	workDir    string // project working directory, for path relativization
	mu         sync.Mutex
	roundBuf   strings.Builder
}

// NewTerminalFollowDisplay creates a new follow display writing to the given file.
// The formatTool callback is used to format tool status strings; if nil, tool names
// are displayed as-is.
func NewTerminalFollowDisplay(out *os.File, lang Lang, workDir string, formatTool ToolFormatter) *TerminalFollowDisplay {
	return &TerminalFollowDisplay{
		out:        out,
		lang:       lang,
		workDir:    workDir,
		formatTool: formatTool,
	}
}

// OnUserMessage displays a user message with cyan header.
func (d *TerminalFollowDisplay) OnUserMessage(text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	text = truncateForTerminal(d.relativizePaths(text), 200)
	fmt.Fprintf(d.out, "%s[%s]%s %s"+nl, ansiCyanBold, Tr(d.lang, "follow.user"), ansiReset, text)
}

// OnToolStatus is a no-op — we only display final results via OnToolResult.
func (d *TerminalFollowDisplay) OnToolStatus(toolName, rawArgs string) {
	// intentionally not displayed
}

// OnToolResult displays the tool call result.
func (d *TerminalFollowDisplay) OnToolResult(toolName, rawArgs, result string, isError bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Relativize paths in args and result
	rawArgs = d.relativizePaths(rawArgs)
	result = d.relativizePaths(result)

	// Special formatting for certain tool types
	special := formatSpecialToolResult(d.lang, toolName, rawArgs, result, isError)
	if special != "" {
		fmt.Fprint(d.out, special+nl)
		return
	}

	// Default: show icon + tool name + abbreviated result
	icon := "  ✓"
	resultColor := ansiDim
	if isError {
		icon = "  ✗"
		resultColor = ansiRedBold
	}

	pretty := prettifyToolName(toolName)
	resultPreview := summarizeToolResult(result, 120)
	if resultPreview != "" {
		fmt.Fprintf(d.out, "%s%s %s%s"+nl, resultColor, icon, pretty, ansiReset)
		fmt.Fprintf(d.out, "%s    %s%s"+nl, ansiDim, truncateForTerminal(resultPreview, 120), ansiReset)
	} else {
		fmt.Fprintf(d.out, "%s%s %s%s"+nl, resultColor, icon, pretty, ansiReset)
	}
}

// OnStreamText accumulates text for the current round.
func (d *TerminalFollowDisplay) OnStreamText(text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.roundBuf.WriteString(text)
}

// OnRoundDone displays the accumulated assistant text rendered as markdown.
func (d *TerminalFollowDisplay) OnRoundDone() {
	d.mu.Lock()
	defer d.mu.Unlock()

	text := strings.TrimSpace(d.roundBuf.String())
	d.roundBuf.Reset()

	if text != "" {
		displayText := renderMarkdown(d.relativizePaths(text))
		fmt.Fprintf(d.out, "%s[%s]%s"+nl, ansiGreenBold, Tr(d.lang, "follow.assistant"), ansiReset)
		fmt.Fprint(d.out, displayText+nl)
	}
}

// OnError displays an error message.
func (d *TerminalFollowDisplay) OnError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprintf(d.out, "❌ %v"+nl, err)
}

// Close cleans up.
func (d *TerminalFollowDisplay) Close() {
	// no-op for now
}

// relativizePaths replaces absolute paths under workDir with relative paths.
func (d *TerminalFollowDisplay) relativizePaths(text string) string {
	return util.RelativizePaths(text, d.workDir)
}

// --- Special tool result formatting ---

// formatSpecialToolResult returns a specially formatted output for certain tool types.
// Returns empty string if no special formatting applies.
func formatSpecialToolResult(lang Lang, toolName, rawArgs, result string, isError bool) string {
	if isError {
		return ""
	}

	switch toolName {
	case "todo_write":
		return formatTodoResult(lang, rawArgs)
	case "run_command", "bash", "powershell", "start_command":
		return formatCommandResult(lang, rawArgs, result, isError)
	default:
		// Check for MCP-style tool names (contain underscores or dots)
		if strings.Contains(toolName, "_") || strings.Contains(toolName, ".") {
			return formatMCPToolResult(lang, toolName, rawArgs, result, isError)
		}
		return ""
	}
}

// formatTodoResult renders todo_write as a markdown checklist.
func formatTodoResult(lang Lang, rawArgs string) string {
	var args struct {
		Todos []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil || len(args.Todos) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(ansiDimYellow)
	sb.WriteString(Tr(lang, "follow.todos_header") + nl)
	for _, t := range args.Todos {
		icon := "○"
		if t.Status == "done" {
			icon = "●"
		} else if t.Status == "in_progress" {
			icon = "◐"
		}
		sb.WriteString(fmt.Sprintf("    %s %s%s"+nl, icon, t.Content, ansiReset+ansiDimYellow))
	}
	sb.WriteString(ansiReset)
	return sb.String()
}

// formatCommandResult renders command execution with output summary.
func formatCommandResult(lang Lang, rawArgs, result string, isError bool) string {
	var args struct {
		Command string `json:"command"`
	}
	cmdPreview := ""
	if err := json.Unmarshal([]byte(rawArgs), &args); err == nil && args.Command != "" {
		cmdPreview = compactSingleLine(args.Command)
		if len(cmdPreview) > 60 {
			cmdPreview = cmdPreview[:57] + "..."
		}
	}

	if cmdPreview == "" {
		return ""
	}

	icon := "  ✓"
	resultColor := ansiDim
	if isError {
		icon = "  ✗"
		resultColor = ansiRedBold
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s%s $ %s%s"+nl, resultColor, icon, cmdPreview, ansiReset))

	// Show output preview
	output := strings.TrimSpace(result)
	if output != "" {
		lines := strings.Split(output, "\n")
		maxLines := 5
		if len(lines) > maxLines {
			for _, line := range lines[:maxLines] {
				sb.WriteString(fmt.Sprintf("%s    %s%s"+nl, ansiDim, truncateForTerminal(line, 100), ansiReset))
			}
			sb.WriteString(fmt.Sprintf("%s    %s%s"+nl, ansiDim, Tr(lang, "follow.more_lines", len(lines)-maxLines), ansiReset))
		} else {
			for _, line := range lines {
				sb.WriteString(fmt.Sprintf("%s    %s%s"+nl, ansiDim, truncateForTerminal(line, 100), ansiReset))
			}
		}
	}

	return sb.String()
}

// formatMCPToolResult renders MCP tool calls with name, args summary, and result.
func formatMCPToolResult(lang Lang, toolName, rawArgs, result string, isError bool) string {
	_ = lang // MCP tool result formatting is language-independent for now
	icon := "  ✓"
	resultColor := ansiDim
	if isError {
		icon = "  ✗"
		resultColor = ansiRedBold
	}

	pretty := prettifyToolName(toolName)

	// Try to extract a brief arg summary
	argSummary := summarizeMCPArgs(rawArgs, 50)

	var sb strings.Builder
	if argSummary != "" {
		sb.WriteString(fmt.Sprintf("%s%s %s(%s)%s"+nl, resultColor, icon, pretty, argSummary, ansiReset))
	} else {
		sb.WriteString(fmt.Sprintf("%s%s %s%s"+nl, resultColor, icon, pretty, ansiReset))
	}

	// Result preview
	resultPreview := summarizeToolResult(result, 80)
	if resultPreview != "" {
		sb.WriteString(fmt.Sprintf("%s    %s%s"+nl, ansiDim, truncateForTerminal(resultPreview, 80), ansiReset))
	}

	return sb.String()
}

// --- Helper functions ---

// summarizeToolResult extracts a brief summary from a tool result string.
func summarizeToolResult(result string, maxLen int) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	// For very long results, take first meaningful line
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > maxLen {
				return line[:maxLen-3] + "..."
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
	// Show first string-valued arg
	for k, v := range args {
		if s, ok := v.(string); ok && s != "" && k != "context" && k != "system_prompt" {
			s = compactSingleLine(s)
			if len(s) > maxLen {
				s = s[:maxLen-3] + "..."
			}
			return s
		}
	}
	return ""
}

// prettifyToolName converts a tool name to a readable form.
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

// compactSingleLine collapses whitespace and newlines.
func compactSingleLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// renderMarkdown renders markdown text to ANSI-colored terminal output.
func renderMarkdown(text string) string {
	rendered, err := charm.Render(text, "dark")
	if err != nil {
		// Fallback: just fix line endings for raw mode
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	// glamour output already has \n; convert to \r\n for raw mode
	return strings.ReplaceAll(rendered, "\n", "\r\n")
}

// truncateForTerminal prepares text for display in raw terminal mode.
// It converts \n to \r\n for proper line breaks and truncates if too long.
func truncateForTerminal(text string, maxLen int) string {
	// In raw mode, \n only moves down — need \r\n for proper line breaks.
	text = strings.ReplaceAll(text, "\n", "\r\n")
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}
