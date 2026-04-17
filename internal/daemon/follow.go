package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	charm "charm.land/glamour/v2"
)

// FollowSink receives agent events for terminal follow-mode display.
type FollowSink interface {
	// OnUserMessage displays a user message.
	OnUserMessage(text string)
	// OnToolStatus displays a tool status line.
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

// pendingToolCall tracks a tool call waiting for its result.
type pendingToolCall struct {
	toolName string
	rawArgs  string
	status   string // formatted status text
}

// TerminalFollowDisplay renders agent activity to the terminal using ANSI codes.
type TerminalFollowDisplay struct {
	out         *os.File
	formatTool  ToolFormatter
	mu          sync.Mutex
	roundBuf    strings.Builder
	hasToolLine bool

	// Pending tool call awaiting result
	pendingTool *pendingToolCall
}

// NewTerminalFollowDisplay creates a new follow display writing to the given file.
// The formatTool callback is used to format tool status strings; if nil, tool names
// are displayed as-is.
func NewTerminalFollowDisplay(out *os.File, formatTool ToolFormatter) *TerminalFollowDisplay {
	return &TerminalFollowDisplay{
		out:        out,
		formatTool: formatTool,
	}
}

// OnUserMessage displays a user message with cyan header.
func (d *TerminalFollowDisplay) OnUserMessage(text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clearToolLine()
	text = truncateForTerminal(text, 200)
	fmt.Fprintf(d.out, "%s[用户]%s %s"+nl, ansiCyanBold, ansiReset, text)
}

// OnToolStatus displays a pending tool status line.
// The display is buffered — we'll wait for OnToolResult to show the combined output.
func (d *TerminalFollowDisplay) OnToolStatus(toolName, rawArgs string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var status string
	if d.formatTool != nil {
		status = d.formatTool(toolName, rawArgs)
	}
	if status == "" {
		status = prettifyToolName(toolName)
	}

	// Store pending tool call — result will be shown via OnToolResult
	d.pendingTool = &pendingToolCall{
		toolName: toolName,
		rawArgs:  rawArgs,
		status:   status,
	}
}

// OnToolResult displays the combined tool call + result.
func (d *TerminalFollowDisplay) OnToolResult(toolName, rawArgs, result string, isError bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Match with pending tool call
	pending := d.pendingTool
	if pending != nil && pending.toolName == toolName {
		d.pendingTool = nil
		d.emitToolResult(pending.status, toolName, rawArgs, result, isError)
	} else {
		// No pending call matched — just show the result
		status := prettifyToolName(toolName)
		d.emitToolResult(status, toolName, rawArgs, result, isError)
	}
}

// emitToolResult renders a tool call + result line.
func (d *TerminalFollowDisplay) emitToolResult(status, toolName, rawArgs, result string, isError bool) {
	d.clearToolLine()

	// Special formatting for certain tool types
	special := formatSpecialToolResult(toolName, rawArgs, result, isError)
	if special != "" {
		fmt.Fprint(d.out, special+nl)
		return
	}

	// Default: show status + abbreviated result
	icon := "  ✓"
	resultColor := ansiDim
	if isError {
		icon = "  ✗"
		resultColor = ansiRedBold
	}

	resultPreview := summarizeToolResult(result, 120)
	if resultPreview != "" {
		fmt.Fprintf(d.out, "%s%s %s%s"+nl, resultColor, icon, status, ansiReset)
		fmt.Fprintf(d.out, "%s    %s%s"+nl, ansiDim, truncateForTerminal(resultPreview, 120), ansiReset)
	} else {
		fmt.Fprintf(d.out, "%s%s %s%s"+nl, resultColor, icon, status, ansiReset)
	}
}

// OnStreamText accumulates text for the current round.
func (d *TerminalFollowDisplay) OnStreamText(text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.roundBuf.WriteString(text)
}

// OnRoundDone displays the accumulated assistant text (rendered as markdown) and a separator.
func (d *TerminalFollowDisplay) OnRoundDone() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clearToolLine()

	// Flush any pending tool call that never got a result
	if d.pendingTool != nil {
		fmt.Fprintf(d.out, "%s  ⏳ %s%s"+nl, ansiDimYellow, d.pendingTool.status, ansiReset)
		d.pendingTool = nil
	}

	text := strings.TrimSpace(d.roundBuf.String())
	d.roundBuf.Reset()

	if text != "" {
		displayText := renderMarkdown(text)
		fmt.Fprintf(d.out, "%s[助手]%s"+nl, ansiGreenBold, ansiReset)
		fmt.Fprint(d.out, displayText+nl)
	}
	// Separator
	fmt.Fprintf(d.out, "%s────────────────────────────────%s"+nl, ansiDim, ansiReset)
}

// OnError displays an error message.
func (d *TerminalFollowDisplay) OnError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clearToolLine()
	fmt.Fprintf(d.out, "❌ %v"+nl, err)
}

// Close cleans up.
func (d *TerminalFollowDisplay) Close() {
	// no-op for now
}

func (d *TerminalFollowDisplay) clearToolLine() {
	if d.hasToolLine {
		fmt.Fprintf(d.out, "%s", ansiClearLine)
		d.hasToolLine = false
	}
}

// --- Special tool result formatting ---

// formatSpecialToolResult returns a specially formatted output for certain tool types.
// Returns empty string if no special formatting applies.
func formatSpecialToolResult(toolName, rawArgs, result string, isError bool) string {
	if isError {
		return ""
	}

	switch toolName {
	case "todo_write":
		return formatTodoResult(rawArgs)
	case "run_command", "bash", "powershell", "start_command":
		return formatCommandResult(rawArgs, result, isError)
	default:
		// Check for MCP-style tool names (contain underscores or dots)
		if strings.Contains(toolName, "_") || strings.Contains(toolName, ".") {
			return formatMCPToolResult(toolName, rawArgs, result, isError)
		}
		return ""
	}
}

// formatTodoResult renders todo_write as a markdown checklist.
func formatTodoResult(rawArgs string) string {
	var args struct {
		Todos []struct {
			Subject     string `json:"subject"`
			Description string `json:"description"`
			Status      string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil || len(args.Todos) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(ansiDimYellow)
	sb.WriteString("  📋 待办事项:" + nl)
	for _, t := range args.Todos {
		icon := "○"
		if t.Status == "completed" {
			icon = "●"
		} else if t.Status == "in_progress" {
			icon = "◐"
		}
		desc := ""
		if t.Description != "" {
			desc = ansiDim + " — " + truncateForTerminal(t.Description, 60) + ansiReset + ansiDim
		}
		sb.WriteString(fmt.Sprintf("    %s %s%s"+nl, icon, t.Subject, desc))
	}
	sb.WriteString(ansiReset)
	return sb.String()
}

// formatCommandResult renders command execution with output summary.
func formatCommandResult(rawArgs, result string, isError bool) string {
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
			sb.WriteString(fmt.Sprintf("%s    ... (%d more lines)%s"+nl, ansiDim, len(lines)-maxLines, ansiReset))
		} else {
			for _, line := range lines {
				sb.WriteString(fmt.Sprintf("%s    %s%s"+nl, ansiDim, truncateForTerminal(line, 100), ansiReset))
			}
		}
	}

	return sb.String()
}

// formatMCPToolResult renders MCP tool calls with name, args summary, and result.
func formatMCPToolResult(toolName, rawArgs, result string, isError bool) string {
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
