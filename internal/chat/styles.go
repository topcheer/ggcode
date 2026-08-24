package chat

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

// ToolStatus represents the current state of a tool call.
type ToolStatus int

const (
	StatusPending ToolStatus = iota
	StatusRunning
	StatusSuccess
	StatusError
	StatusCanceled
)

// String returns a human-readable status name.
func (s ToolStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusSuccess:
		return "success"
	case StatusError:
		return "error"
	case StatusCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

// Styles holds all rendering styles for the chat package.
type Styles struct {
	// User message
	UserPrefix string
	UserIcon   string
	UserStyle  lipgloss.Style

	// Assistant message
	AssistantPrefix string
	AssistantIcon   string
	AssistantStyle  lipgloss.Style

	// Tool name rendering
	ToolName lipgloss.Style

	// Tool body
	ToolBody lipgloss.Style
	BashBody lipgloss.Style // command output with subtle background

	// System message
	SystemPrefix string
	SystemStyle  lipgloss.Style

	// Reasoning / thinking
	ReasoningPrefix string
	ReasoningStyle  lipgloss.Style

	// Error
	ErrorStyle lipgloss.Style

	// Muted
	MutedStyle lipgloss.Style

	// Spacing
	ItemGap int // lines between items
}

// DefaultStyles returns the default style set.
func DefaultStyles() Styles {
	return Styles{
		UserPrefix:      "❯ ",
		UserIcon:        "❯",
		UserStyle:       lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		AssistantPrefix: "● ",
		AssistantIcon:   "●",
		AssistantStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("81")),
		ToolName:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		ToolBody:        lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		BashBody:        lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("235")),
		SystemPrefix:    "○ ",
		SystemStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		ReasoningPrefix: lipgloss.NewStyle().Foreground(lipgloss.Color("183")).Render("✦ "),
		ReasoningStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("147")),
		ErrorStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		MutedStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		ItemGap:         1,
	}
}

// toolAnimGlyphs are the rotating quarter-circle frames for running tools.
// They keep the circle aesthetic of the static pending icon (○) while
// clearly showing the tool is actively executing.
var toolAnimGlyphs = []string{"◐", "◓", "◑", "◒"}

// toolAnimFrame is the current animation frame index, set by the TUI on each
// spinner tick (every ~150ms). Read by ToolIcon when rendering running tools.
var toolAnimFrame int

// SetToolAnimFrame updates the global animation frame for running tool icons.
// Called by the TUI before rendering the conversation panel.
func SetToolAnimFrame(frame int) {
	toolAnimFrame = frame
}

// ToolIcon returns the icon for a given tool status.
func (s Styles) ToolIcon(status ToolStatus) string {
	switch status {
	case StatusPending:
		return "○"
	case StatusRunning:
		return toolAnimGlyphs[toolAnimFrame%len(toolAnimGlyphs)]
	case StatusSuccess:
		return "●"
	case StatusError:
		return "●"
	case StatusCanceled:
		return "⊘"
	default:
		return "?"
	}
}

// ToolIconStyle returns a styled icon for the given tool status.
func (s Styles) ToolIconStyle(status ToolStatus) string {
	icon := s.ToolIcon(status)
	switch status {
	case StatusSuccess:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render(icon) // green
	case StatusError:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(icon) // red
	case StatusPending, StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(icon) // orange
	case StatusCanceled:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(icon) // gray
	default:
		return icon
	}
}

// ToolHeader builds the standard tool header line: "✓ ToolName  params".
// If the header exceeds width, params are word-wrapped onto continuation lines
// indented to align with the first param.
const toolHeaderMaxRenderWidth = 120

func (s Styles) ToolHeader(status ToolStatus, name string, width int, params ...string) string {
	icon := s.ToolIconStyle(status)
	paramStr := strings.Join(params, " ")

	prefix, prefixW := s.toolHeaderPrefix(icon, name, width)
	paramStr = capHeaderParams(paramStr, prefixW)

	fullLine := prefix + paramStr
	if lipgloss.Width(fullLine) <= width {
		return fullLine
	}
	return wrapHeaderParams(prefix, paramStr, prefixW, width)
}

// toolNameTruncSuffix is appended to a tool name truncated for narrow headers.
const toolNameTruncSuffix = "..."

// truncateRunesByWidth returns the byte length of the longest prefix of s
// whose accumulated rune width stays within maxW.
func truncateRunesByWidth(s string, maxW int) int {
	w := 0
	for i, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxW {
			return i
		}
		w += rw
	}
	return len(s)
}

// toolHeaderPrefix builds the "icon name " prefix, honoring the width
// invariant even in narrow terminals: a long tool name (run_command = 15
// cols + icon) can exceed width outright, and every continuation line
// indents by prefixW - so the whole header block would be wider than
// width, desynchronizing Height()'s visual-line count from Render()'s
// physical lines. The name truncates at rune boundaries with the "..."
// suffix budgeted INSIDE the remaining width (the suffix was initially
// forgotten and re-broke the invariant at width 12); at degenerate widths
// only the icon survives.
func (s Styles) toolHeaderPrefix(icon, name string, width int) (prefix string, prefixW int) {
	prefix = fmt.Sprintf("%s %s ", icon, s.ToolName.Render(name))
	prefixW = lipgloss.Width(prefix)
	if prefixW <= width || width <= 0 {
		return prefix, prefixW
	}
	// Budget the NAME so the final prefix leaves 1 column for params:
	// icon + space + name + space. Derive from the measured icon width,
	// not assumptions about the styled string; 3 fixed cols (2 spaces +
	// 1 params col).
	avail := width - lipgloss.Width(icon) - 3
	if avail <= len(toolNameTruncSuffix) {
		// Degenerate: no room for a truncated name; icon only (trimmed of
		// trailing space; empty when even that overflows).
		prefix = strings.TrimSuffix(icon, " ")
		if lipgloss.Width(prefix) > width {
			prefix = ""
		}
		return prefix, lipgloss.Width(prefix)
	}
	avail -= len(toolNameTruncSuffix)
	cut := truncateRunesByWidth(name, avail)
	short := name[:cut]
	if cut < len(name) {
		short += toolNameTruncSuffix
	}
	prefix = fmt.Sprintf("%s %s ", icon, s.ToolName.Render(short))
	return prefix, lipgloss.Width(prefix)
}

// capHeaderParams shortens params for the wide-terminal one-line display
// cap (toolHeaderMaxRenderWidth). Note: this cap may exceed a NARROW
// terminal's width - the invariant for narrow terminals is enforced by
// wrapHeaderParams below.
func capHeaderParams(paramStr string, prefixW int) string {
	if lipgloss.Width(paramStr)+prefixW <= toolHeaderMaxRenderWidth {
		return paramStr
	}
	avail := toolHeaderMaxRenderWidth - prefixW - 1 // 1 for "…"
	if avail < 10 {
		avail = 10
	}
	// Truncate by visual width, no word-break — just cut at rune boundary
	cut := truncateRunesByWidth(paramStr, avail)
	return paramStr[:cut] + "…"
}

// wrapHeaderParams word-wraps params onto continuation lines aligned with
// the header prefix. Width is a hard invariant, not a preference:
// measureHeightWidth counts ceil(visualWidth/width) while List.Render
// emits physical lines, so a line wider than width desynchronizes the two
// and the viewport drifts (content floats mid-screen, blank space below).
// The old `if avail < 10 { avail = 10 }` clamp INFLATED past width in
// narrow terminals; emit at most one rune in the degenerate case.
func wrapHeaderParams(prefix, paramStr string, prefixW, width int) string {
	indent := strings.Repeat(" ", prefixW)
	var lines []string
	remaining := paramStr
	first := true
	for remaining != "" {
		linePrefix := indent
		if first {
			linePrefix = prefix
			first = false
		}
		avail := width - prefixW
		if avail < 1 {
			avail = 1
		}
		fit, rest := splitAtWidth(remaining, avail)
		lines = append(lines, linePrefix+fit)
		remaining = rest
	}
	return strings.Join(lines, "\n")
}

// splitAtWidth splits s at the maximum visual width that fits in maxW.
// Returns (fit, rest). Tries to break at a space boundary.
func splitAtWidth(s string, maxW int) (string, string) {
	if lipgloss.Width(s) <= maxW {
		return s, ""
	}
	// Walk runes, tracking visual width
	runes := []rune(s)
	totalW := 0
	breakIdx := len(runes)
	spaceIdx := -1
	for i, r := range runes {
		rw := runewidth.RuneWidth(r)
		if totalW+rw > maxW && i > 0 {
			breakIdx = i
			break
		}
		totalW += rw
		if r == ' ' {
			spaceIdx = i
		}
	}
	// Prefer breaking at last space before break point
	if spaceIdx > 0 && spaceIdx < breakIdx {
		return string(runes[:spaceIdx]), string(runes[spaceIdx+1:])
	}
	return string(runes[:breakIdx]), string(runes[breakIdx:])
}

// truncateTailByWidth truncates a string from the tail so that its visual
// width (measured by lipgloss.Width) does not exceed maxW. The result is
// safe for multi-byte runes and strips any partial ANSI sequences.
func truncateTailByWidth(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	// Remove trailing runes until width fits
	runes := []rune(s)
	// Strip ANSI-aware: work on rune level; lipgloss.Width handles ANSI internally
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if lipgloss.Width(string(runes)) <= maxW {
			return strings.TrimRight(string(runes), "\x1b")
		}
	}
	return ""
}

// tabStop is the column multiple that TAB characters expand to. Terminals
// advance to the next 8-col stop by default, but the absolute stop size
// does not matter for correctness - only that measurement and terminal
// agree, which expansion to ANY fixed stop guarantees. 4 keeps tab-aligned
// columns (git status, ls -l) reasonably narrow after wrapping.
const tabStop = 4

// expandTabs replaces every '\t' with spaces up to the next tabStop
// column, tracked per line (a tab's width depends on the current column).
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var sb strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			n := tabStop - col%tabStop
			for i := 0; i < n; i++ {
				sb.WriteByte(' ')
			}
			col += n
		case '\n':
			sb.WriteRune(r)
			col = 0
		default:
			sb.WriteRune(r)
			col++
		}
	}
	return sb.String()
}

// ToolBodyMaxLines is the maximum number of body lines shown before truncation.
const ToolBodyMaxLines = 10

// FormatBody renders tool body content with optional truncation.
// For long output, shows the last maxLines lines (users care about the end).
// Returns the formatted body and whether it was truncated.
func FormatBody(content string, width int, maxLines int) (string, bool) {
	if content == "" {
		return "", false
	}

	// Normalize carriage returns BEFORE any line processing. Windows tool
	// output is CRLF and progress-bar tools (npm/gradle/docker) rewrite the
	// line with a bare CR. Both left a stray '\r' inside an emitted line:
	// lipgloss.Width counts it as zero-width so the height math looked
	// right, but the terminal moves the cursor to column 0 mid-line,
	// mangling borders (most visible on Windows) and desynchronizing the
	// physical-line split from the visual-line count - the scroll offsets
	// then drift, showing content floating mid-screen with blank space
	// below. Treating CR as a line break feeds both cases through the
	// normal wrap/cap machinery.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	// Expand TABs to spaces: width libraries count '\t' as ONE column but
	// terminals advance the cursor to the next 8-column tab stop, so
	// "a\tb\tc" measures 3 cols and renders 17 - a per-line drift of up to
	// 7x(tab count) columns that breaks the width invariant (borders, and
	// Height() vs physical-line desync) on common bash output (git status,
	// ls, compiler alignment all emit tabs). Expanding to a fixed 4-col
	// stop makes measurement and terminal agree exactly.
	content = expandTabs(content)

	// Split into lines, then wrap each line that exceeds visual width.
	var wrapped []string
	for _, line := range strings.Split(content, "\n") {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		if lipgloss.Width(line) <= width {
			wrapped = append(wrapped, line)
			continue
		}
		// Visual-width-aware wrapping
		runes := []rune(line)
		for len(runes) > 0 {
			cut := 0
			for cut < len(runes) && lipgloss.Width(string(runes[:cut+1])) <= width {
				cut++
			}
			if cut == 0 {
				cut = 1
			}
			// Prefer breaking at a space
			chunk := string(runes[:cut])
			if spaceIdx := strings.LastIndex(chunk, " "); spaceIdx > 0 {
				runeIdx := utf8.RuneCountInString(chunk[:spaceIdx])
				wrapped = append(wrapped, string(runes[:runeIdx]))
				runes = []rune(strings.TrimLeft(string(runes[runeIdx:]), " "))
			} else {
				wrapped = append(wrapped, chunk)
				runes = runes[cut:]
			}
		}
	}

	truncated := false
	if maxLines > 0 && len(wrapped) > maxLines {
		truncated = true
		hidden := len(wrapped) - maxLines
		wrapped = wrapped[len(wrapped)-maxLines:]
		wrapped = append([]string{fmt.Sprintf("  … %d more lines", hidden)}, wrapped...)
	}

	return strings.Join(wrapped, "\n"), truncated
}
