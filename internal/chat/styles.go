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

// ToolIcon returns the icon for a given tool status.
func (s Styles) ToolIcon(status ToolStatus) string {
	switch status {
	case StatusPending, StatusRunning:
		return "○"
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
	toolName := s.ToolName.Render(name)
	paramStr := strings.Join(params, " ")

	prefix := fmt.Sprintf("%s %s ", icon, toolName)
	prefixW := lipgloss.Width(prefix)

	// Cap params at max render width
	if lipgloss.Width(prefix+paramStr) > toolHeaderMaxRenderWidth {
		avail := toolHeaderMaxRenderWidth - prefixW - 1 // 1 for "…"
		if avail < 10 {
			avail = 10
		}
		// Truncate by visual width, no word-break — just cut at rune boundary
		runes := []rune(paramStr)
		w := 0
		cut := len(runes)
		for i, r := range runes {
			rw := runewidth.RuneWidth(r)
			if w+rw > avail {
				cut = i
				break
			}
			w += rw
		}
		paramStr = string(runes[:cut]) + "…"
	}

	fullLine := prefix + paramStr
	if lipgloss.Width(fullLine) <= width {
		return fullLine
	}

	// Word-wrap params onto continuation lines aligned with prefix
	indent := strings.Repeat(" ", prefixW)
	var lines []string
	remaining := paramStr
	first := true
	for remaining != "" {
		var linePrefix string
		var avail int
		if first {
			linePrefix = prefix
			avail = width - prefixW
			first = false
		} else {
			linePrefix = indent
			avail = width - prefixW
		}
		if avail < 10 {
			avail = 10
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

// truncateHeadByWidth truncates a string from the head so that its visual
// width does not exceed maxW, keeping the tail (useful for file paths where
// the filename is at the end).
func truncateHeadByWidth(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[1:]
		if lipgloss.Width(string(runes)) <= maxW {
			// Skip leading partial ANSI escape
			result := string(runes)
			if idx := strings.Index(result, "\x1b["); idx > 0 {
				// Check if there's a broken escape at the start
				if end := strings.Index(result[idx:], "m"); end != -1 {
					return result
				}
				return result[:idx] + result[idx:]
			}
			return result
		}
	}
	return ""
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
	if len(wrapped) > maxLines {
		truncated = true
		hidden := len(wrapped) - maxLines
		wrapped = wrapped[len(wrapped)-maxLines:]
		wrapped = append([]string{fmt.Sprintf("  … %d more lines", hidden)}, wrapped...)
	}

	return strings.Join(wrapped, "\n"), truncated
}
