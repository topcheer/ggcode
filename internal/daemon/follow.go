package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	charm "charm.land/glamour/v2"
	"github.com/topcheer/ggcode/internal/chat"
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
	// OnPairingChallenge displays an IM pairing code when a channel requests binding.
	OnPairingChallenge(platform, channelID, code string, kind string)
	// OnPairingResolved clears a previously displayed pairing challenge.
	OnPairingResolved()
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

// PlatformDisplayName returns a human-readable name for an IM platform string.
func PlatformDisplayName(platform string) string {
	switch platform {
	case "qq":
		return "QQ"
	case "feishu":
		return "Feishu"
	case "telegram":
		return "Telegram"
	case "discord":
		return "Discord"
	case "dingtalk":
		return "DingTalk"
	case "slack":
		return "Slack"
	default:
		return "IM"
	}
}

// ToolFormatter is a function that formats tool activity for display.
// It takes a tool name and raw JSON args, and returns a human-readable status string.
type ToolFormatter func(toolName, rawArgs string) string

// ToolPresenter returns a display presentation for a tool call.
// Used by daemon follow to get the same labels as TUI.
type ToolPresenter interface {
	Present(toolName, rawArgs string) (displayName, detail, activity string)
}

// ANSIColors for consistent rendering
const (
	ansiDim       = "\033[2m"
	ansiReset     = "\033[0m"
	ansiClearLine = "\033[2K\r"
	ansiBold      = "\033[1m"
	ansiFgYellow  = "\033[33m"
	ansiBgBlue    = "\033[44m"
	// In raw terminal mode, \n only moves cursor down without returning to
	// column 0. We must use \r\n for proper line breaks.
	nl = "\r\n"
)

// --- i18n catalog for daemon/follow output ---

var catalogEn = map[string]string{
	"daemon.started_bg":    "ggcode daemon started in background (PID: %d)",
	"daemon.workdir":       "Working directory: %s",
	"daemon.started":       "ggcode daemon started (session: %s)",
	"daemon.keys_full":     "Press x to exit, d for background, f toggle follow, r restart, v/q/s output, M/U mute/unmute all",
	"daemon.keys_minimal":  "Press x to exit",
	"daemon.shutting_down": "\nShutting down...",
	"daemon.stopped":       "ggcode daemon stopped",
	"daemon.follow_on":     "follow mode enabled",
	"daemon.follow_off":    "follow mode disabled",
	"daemon.output_mode":   "IM output mode: %s",
	"daemon.mute_all":      "All IM channels muted (%d)",
	"daemon.unmute_all":    "All IM channels unmuted (%d)",
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

	"follow.pairing.bind":     "🔗 %s binding requested from channel %s",
	"follow.pairing.rebind":   "🔗 %s rebind requested from channel %s",
	"follow.pairing.code":     "   Enter this code in %s to complete binding:",
	"follow.pairing.resolved": "✅ Pairing resolved",
	"follow.pairing.rejected": "❌ Pairing rejected",
}

var catalogZhCN = map[string]string{
	"daemon.started_bg":    "ggcode daemon 已在后台启动 (PID: %d)",
	"daemon.workdir":       "工作目录: %s",
	"daemon.started":       "ggcode daemon 已启动 (session: %s)",
	"daemon.keys_full":     "按 x 退出, d 后台运行, f 切换 follow, r 重启, v/q/s 输出模式, M/U 全部静音/取消静音",
	"daemon.keys_minimal":  "按 x 退出",
	"daemon.shutting_down": "\n正在关闭...",
	"daemon.stopped":       "ggcode daemon 已停止",
	"daemon.follow_on":     "follow 模式已开启",
	"daemon.follow_off":    "follow 模式已关闭",
	"daemon.output_mode":   "IM 输出模式: %s",
	"daemon.mute_all":      "已静音所有 IM 渠道 (%d)",
	"daemon.unmute_all":    "已取消静音所有 IM 渠道 (%d)",
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

	"follow.pairing.bind":     "🔗 %s 绑定请求来自渠道 %s",
	"follow.pairing.rebind":   "🔗 %s 重新绑定请求来自渠道 %s",
	"follow.pairing.code":     "   请在 %s 中输入以下绑定码完成绑定:",
	"follow.pairing.resolved": "✅ 绑定完成",
	"follow.pairing.rejected": "❌ 绑定已拒绝",
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
	out       *os.File
	presenter ToolPresenter
	lang      Lang
	workDir   string // project working directory, for path relativization
	styles    chat.Styles
	termWidth int
	mu        sync.Mutex
	roundBuf  strings.Builder
}

// NewTerminalFollowDisplay creates a new follow display writing to the given file.
func NewTerminalFollowDisplay(out *os.File, lang Lang, workDir string, presenter ToolPresenter) *TerminalFollowDisplay {
	return &TerminalFollowDisplay{
		out:       out,
		lang:      lang,
		workDir:   workDir,
		presenter: presenter,
		styles:    chat.DefaultStyles(),
		termWidth: 80,
	}
}

// OnUserMessage displays a user message.
func (d *TerminalFollowDisplay) OnUserMessage(text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	text = truncateForTerminal(d.relativizePaths(text), 200)
	fmt.Fprintf(d.out, "%s%s%s"+nl, d.styles.UserPrefix, text, ansiReset)
}

// OnToolStatus is a no-op — we only display final results via OnToolResult.
func (d *TerminalFollowDisplay) OnToolStatus(toolName, rawArgs string) {
	// intentionally not displayed
}

// OnToolResult displays the tool call result using the same label system as TUI.
func (d *TerminalFollowDisplay) OnToolResult(toolName, rawArgs, result string, isError bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Relativize paths in args and result
	rawArgs = d.relativizePaths(rawArgs)
	result = d.relativizePaths(result)

	status := chat.StatusSuccess
	if isError {
		status = chat.StatusError
	}

	// Get display name from tool label system (same as TUI)
	var displayName, detail string
	if d.presenter != nil {
		displayName, detail, _ = d.presenter.Present(toolName, rawArgs)
	}
	if displayName == "" {
		displayName = prettifyToolName(toolName)
	}

	// Render header
	header := d.styles.ToolHeader(status, displayName, d.termWidth, detail)
	fmt.Fprint(d.out, header+nl)

	// Render body based on behavior
	behavior := chat.GetToolBodyBehavior(toolName)
	switch behavior {
	case chat.BodySuppress:
		// No body at all
	case chat.BodyFormatJSON:
		formatted := chat.FormatJSONResult(result)
		if formatted != "" {
			for _, line := range strings.Split(formatted, "\n") {
				fmt.Fprintf(d.out, "%s    %s%s"+nl, ansiDim, truncateForTerminal(line, 100), ansiReset)
			}
		}
	case chat.BodyMarkdown:
		if result != "" {
			rendered := renderMarkdown(result)
			fmt.Fprint(d.out, rendered+nl)
		}
	default:
		// Special formatting for certain tools
		if special := d.formatSpecialBody(toolName, rawArgs, result, isError); special != "" {
			fmt.Fprint(d.out, special)
			return
		}
		// Default: body preview
		if result != "" {
			resultPreview := summarizeToolResult(result, 120)
			if resultPreview != "" {
				fmt.Fprintf(d.out, "%s    %s%s"+nl, ansiDim, truncateForTerminal(resultPreview, 120), ansiReset)
			}
		}
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
		// Strip glamour's leading/trailing newlines so prefix and text
		// start on the same line
		displayText = strings.TrimRight(strings.TrimLeft(displayText, " \t\n"+string(rune(13))), " \t\n"+string(rune(13)))
		fmt.Fprintf(d.out, "%s%s%s", d.styles.AssistantPrefix, displayText, nl)
	}
}

// OnError displays an error message.
func (d *TerminalFollowDisplay) OnError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprintf(d.out, "❌ %v"+nl, err)
}

// OnPairingChallenge displays an IM pairing code when a channel requests binding.
func (d *TerminalFollowDisplay) OnPairingChallenge(platform, channelID, code string, kind string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := "follow.pairing.bind"
	if kind == "rebind" {
		key = "follow.pairing.rebind"
	}
	fmt.Fprint(d.out, Tr(d.lang, key, platform, channelID)+nl)
	fmt.Fprint(d.out, Tr(d.lang, "follow.pairing.code", platform)+nl)

	// Render the 4-digit code with prominent styling
	codeDigits := strings.Join(strings.Split(code, ""), "   ")
	codeBlock := ansiBold + ansiFgYellow + ansiBgBlue + "  " + codeDigits + "  " + ansiReset
	fmt.Fprint(d.out, codeBlock+nl)
}

// OnPairingResolved clears a previously displayed pairing challenge.
func (d *TerminalFollowDisplay) OnPairingResolved() {
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprint(d.out, Tr(d.lang, "follow.pairing.resolved")+nl)
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

// formatSpecialBody returns specially formatted body output for certain tool types.
// Returns empty string if no special formatting applies.
func (d *TerminalFollowDisplay) formatSpecialBody(toolName, rawArgs, result string, isError bool) string {
	if isError {
		return ""
	}

	switch toolName {
	case "todo_write":
		return formatTodoResult(d.lang, rawArgs)
	case "run_command", "bash", "powershell", "start_command":
		return formatCommandBody(d.lang, rawArgs, result, isError)
	default:
		if strings.Contains(toolName, "_") || strings.Contains(toolName, ".") {
			return formatMCPToolBody(d.lang, toolName, rawArgs, result, isError)
		}
		return ""
	}
}

// formatTodoResult renders todo_write using chat.TodoTask styling.
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

	styles := chat.DefaultStyles()
	done, total := 0, len(args.Todos)
	for _, t := range args.Todos {
		if t.Status == "done" {
			done++
		}
	}

	// Header: ⏳ To-Do  done/total
	header := styles.ToolHeader(chat.StatusRunning, "To-Do", 80, fmt.Sprintf("%d/%d", done, total))
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString(nl)

	// Task list using chat-style icons
	for _, t := range args.Todos {
		var icon string
		switch t.Status {
		case "done":
			icon = "✓"
		case "in_progress":
			icon = "→"
		default:
			icon = "○"
		}
		sb.WriteString(fmt.Sprintf("%s    %s %s%s"+nl, ansiDim, icon, t.Content, ansiReset))
	}

	return sb.String()
}

// formatCommandBody renders command execution body (without header).
func formatCommandBody(lang Lang, rawArgs, result string, isError bool) string {
	var args struct {
		Command string `json:"command"`
	}
	_ = rawArgs
	output := strings.TrimSpace(result)
	if output == "" {
		return ""
	}

	var sb strings.Builder
	body, _ := chat.FormatBody(output, 76, 5)
	for _, line := range strings.Split(body, "\n") {
		_ = args
		sb.WriteString(fmt.Sprintf("%s    %s%s"+nl, ansiDim, truncateForTerminal(line, 100), ansiReset))
	}
	return sb.String()
}

// formatMCPToolBody renders MCP tool body (without header).
func formatMCPToolBody(lang Lang, toolName, rawArgs, result string, isError bool) string {
	_ = lang
	_ = toolName
	_ = rawArgs
	resultPreview := summarizeToolResult(result, 80)
	if resultPreview == "" {
		return ""
	}
	return fmt.Sprintf("%s    %s%s"+nl, ansiDim, truncateForTerminal(resultPreview, 80), ansiReset)
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
