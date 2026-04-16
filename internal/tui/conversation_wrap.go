package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/muesli/reflow/wordwrap"
)

func (m Model) renderConversationUserEntry(prefix, text string) string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return m.styles.user.Render(prefix)
	}
	available := max(1, m.conversationInnerWidth()-lipgloss.Width(prefix))
	lines := wrapConversationText(text, available)
	if len(lines) == 0 {
		lines = []string{""}
	}
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	rows := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			rows = append(rows, m.styles.user.Render(prefix)+line)
			continue
		}
		rows = append(rows, indent+line)
	}
	return strings.Join(rows, "\n")
}

func wrapConversationText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	rawLines := strings.Split(text, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		line := strings.TrimRight(raw, " ")
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		wrapped := wordwrap.String(line, width)
		for _, candidate := range strings.Split(wrapped, "\n") {
			lines = append(lines, hardWrapHarnessPanelLine(candidate, width)...)
		}
	}
	return lines
}
