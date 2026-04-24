package tui

import (
	"strings"

	"github.com/muesli/reflow/wordwrap"
)

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
