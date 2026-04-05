package tui

import (
	"time"

	"github.com/charmbracelet/glamour"
)

// handleResize updates viewport and input dimensions on window size changes.
func (m *Model) handleResize(width, height int) {
	m.width = width
	m.height = height
	m.lastResizeAt = time.Now()
	viewportHeight := height - 5
	if viewportHeight < 3 {
		viewportHeight = 3
	}
	m.viewport.SetSize(m.mainColumnWidth(), viewportHeight)
	inputWidth := m.mainColumnWidth() - 6
	if inputWidth < 20 {
		inputWidth = m.mainColumnWidth()
	}
	m.input.Width = inputWidth
	m.syncConversationViewport()
}

func (m *Model) syncConversationViewport() {
	panelHeight := m.conversationPanelHeight()
	m.viewport.SetSize(m.conversationInnerWidth(), conversationInnerHeight(panelHeight))
	m.viewport.SetContent(m.renderOutput())
}

// rebuildMarkdownRenderer creates a new glamour renderer matching the current width.
func (m *Model) rebuildMarkdownRenderer() {
	if wrap := m.mainColumnWidth() - 4; wrap > 20 {
		if r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(wrap)); err == nil {
			m.mdRenderer = r
		}
	}
}
