package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

var renderer *glamour.TermRenderer

func init() {
	var err error
	// Use a dark, terminal-friendly style
	renderer, err = glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		// Fallback: no-op renderer
		renderer = nil
	}
}

// RenderMarkdown renders a markdown string for terminal display.
// Falls back to plain text if glamour is unavailable.
func RenderMarkdown(text string) string {
	if renderer == nil {
		return text
	}
	out, err := renderer.Render(text)
	if err != nil {
		return text
	}
	// Trim trailing whitespace added by glamour
	return strings.TrimRight(out, " \t\n")
}
