package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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

	// Tool status icons
	ToolPending  string
	ToolRunning  string
	ToolSuccess  string
	ToolError    string
	ToolCanceled string

	// Tool name rendering
	ToolName lipgloss.Style

	// Tool body
	ToolBody lipgloss.Style

	// System message
	SystemPrefix string
	SystemStyle  lipgloss.Style

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
		ToolPending:     "⏳",
		ToolRunning:     "⏳",
		ToolSuccess:     "✓",
		ToolError:       "✗",
		ToolCanceled:    "⊘",
		ToolName:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		ToolBody:        lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		SystemPrefix:    "○ ",
		SystemStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		ErrorStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		MutedStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		ItemGap:         1,
	}
}

// ToolIcon returns the icon for a given tool status.
func (s Styles) ToolIcon(status ToolStatus) string {
	switch status {
	case StatusPending:
		return s.ToolPending
	case StatusRunning:
		return s.ToolRunning
	case StatusSuccess:
		return s.ToolSuccess
	case StatusError:
		return s.ToolError
	case StatusCanceled:
		return s.ToolCanceled
	default:
		return "?"
	}
}

// ToolIconStyle returns a styled icon for a given tool status.
func (s Styles) ToolIconStyle(status ToolStatus) string {
	icon := s.ToolIcon(status)
	switch status {
	case StatusSuccess:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(icon)
	case StatusError:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(icon)
	case StatusPending, StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(icon)
	case StatusCanceled:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(icon)
	default:
		return icon
	}
}

// ToolHeader builds the standard tool header line: "✓ ToolName  params..."
// Params are joined and truncated to fit within width.
func (s Styles) ToolHeader(status ToolStatus, name string, width int, params ...string) string {
	icon := s.ToolIconStyle(status)
	toolName := s.ToolName.Render(name)
	paramStr := strings.Join(params, " ")

	prefix := fmt.Sprintf("%s %s ", icon, toolName)
	prefixWidth := lipgloss.Width(prefix)
	remaining := width - prefixWidth - 1 // 1 for trailing space
	if remaining < 10 {
		remaining = 10
	}
	if len(paramStr) > remaining {
		paramStr = paramStr[:remaining-1] + "…"
	}

	return prefix + paramStr
}

// ToolBodyMaxLines is the maximum number of body lines shown before truncation.
const ToolBodyMaxLines = 10

// FormatBody renders tool body content with optional truncation.
// Returns the formatted body and whether it was truncated.
func FormatBody(content string, width int, maxLines int) (string, bool) {
	if content == "" {
		return "", false
	}

	lines := strings.Split(content, "\n")
	truncated := false
	if len(lines) > maxLines {
		truncated = true
		hidden := len(lines) - maxLines
		lines = lines[:maxLines]
		lines = append(lines, fmt.Sprintf("  … %d more lines", hidden))
	}

	// Pad each line
	for i, line := range lines {
		if len(line) < width {
			// No-op — keep as is
			_ = i
		}
	}

	return strings.Join(lines, "\n"), truncated
}
