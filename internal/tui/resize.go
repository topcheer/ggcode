package tui

import "github.com/charmbracelet/glamour"

// handleResize updates viewport and input dimensions on window size changes.
func (m *Model) handleResize(width, height int) {
	m.width = width
	m.height = height
	viewportHeight := height - 5
	if viewportHeight < 3 {
		viewportHeight = 3
	}
	m.viewport.SetSize(width, viewportHeight)
	m.input.Width = width
	m.viewport.SetContent(m.renderOutput())
}

// rebuildMarkdownRenderer creates a new glamour renderer matching the current width.
func (m *Model) rebuildMarkdownRenderer() {
	if wrap := m.width - 4; wrap > 20 {
		if r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(wrap)); err == nil {
			m.mdRenderer = r
		}
	}
}
