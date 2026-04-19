package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	diffAddStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	diffRemoveStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	diffHeaderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // cyan
	diffContextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
)

// FormatDiff formats a unified diff for TUI display.
func FormatDiff(diff string) string {
	if diff == "" {
		return ""
	}

	var sb strings.Builder
	lines := strings.Split(diff, "\n")
	hasDiff := false

	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			sb.WriteString(diffHeaderStyle.Render(line))
			sb.WriteString("\n")
			hasDiff = true
		} else if strings.HasPrefix(line, "+") {
			sb.WriteString(diffAddStyle.Render(line))
			sb.WriteString("\n")
			hasDiff = true
		} else if strings.HasPrefix(line, "-") {
			sb.WriteString(diffRemoveStyle.Render(line))
			sb.WriteString("\n")
			hasDiff = true
		} else if hasDiff {
			sb.WriteString(diffContextStyle.Render(line))
			sb.WriteString("\n")
		}
	}

	if !hasDiff {
		return ""
	}

	// Wrap with border
	result := sb.String()
	border := diffHeaderStyle.Render(strings.Repeat("─", 40))
	return fmt.Sprintf("%s\n%s%s", border, result, border)
}

// IsDiffContent checks if text looks like a unified diff.
func IsDiffContent(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "@@") {
			return true
		}
	}
	return false
}
