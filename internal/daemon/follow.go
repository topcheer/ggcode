package daemon

import (
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
	ansiClearLine = "\033[2K\r"
	// In raw terminal mode, \n only moves cursor down without returning to
	// column 0. We must use \r\n for proper line breaks.
	nl = "\r\n"
)

// TerminalFollowDisplay renders agent activity to the terminal using ANSI codes.
type TerminalFollowDisplay struct {
	out         *os.File
	formatTool  ToolFormatter
	mu          sync.Mutex
	roundBuf    strings.Builder
	hasToolLine bool
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

// OnToolStatus displays a tool status line.
func (d *TerminalFollowDisplay) OnToolStatus(toolName, rawArgs string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var status string
	if d.formatTool != nil {
		status = d.formatTool(toolName, rawArgs)
	}
	if status == "" {
		status = toolName
	}

	// Clear previous tool line and write new one
	if d.hasToolLine {
		fmt.Fprintf(d.out, "%s", ansiClearLine)
	}
	fmt.Fprintf(d.out, "%s  ⏳ %s%s"+nl, ansiDimYellow, status, ansiReset)
	d.hasToolLine = false // newline committed the line
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
