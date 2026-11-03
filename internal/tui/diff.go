package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	diffAddStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	diffRemoveStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	diffHeaderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	diffContextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

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
		} else if hasDiff && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			sb.WriteString(diffAddStyle.Render(line))
			sb.WriteString("\n")
		} else if hasDiff && strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			sb.WriteString(diffRemoveStyle.Render(line))
			sb.WriteString("\n")
		} else if hasDiff {
			sb.WriteString(diffContextStyle.Render(line))
			sb.WriteString("\n")
		}
	}

	if !hasDiff {
		return ""
	}

	result := sb.String()
	border := diffHeaderStyle.Render(strings.Repeat("─", 40))
	return fmt.Sprintf("%s\n%s%s", border, result, border)
}
