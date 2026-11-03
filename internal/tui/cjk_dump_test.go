package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
)

func mkTA(width int) textarea.Model {
	ta := textarea.New()
	ta.Prompt = "❯ "
	ta.Focus()
	ta.SetWidth(width)
	ta.ShowLineNumbers = false
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 10
	taStyles := textarea.DefaultStyles(true)
	taStyles.Focused.Base = lipgloss.NewStyle()
	taStyles.Blurred.Base = lipgloss.NewStyle()
	ta.SetStyles(taStyles)
	return ta
}

func stripANSIForTest(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && (r == 'm' || r == 'K'):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}
