package tui

import (
	"time"
)

// handleResize updates viewport and input dimensions on window size changes.
func (m *Model) handleResize(width, height int) {
	if width == m.width && height == m.height {
		m.lastResizeAt = time.Now()
		return
	}
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
	m.input.SetWidth(inputWidth)
	m.syncQuestionnaireInputWidth()
	panelHeight := m.conversationPanelHeight()
	m.viewport.SetSize(m.conversationInnerWidth(), conversationInnerHeight(panelHeight))
	prewarmMarkdownRenderers(m.previewContentWidth(), m.fileBrowserPreviewWidth())
	m.syncPreviewViewport(false)
	m.syncFileBrowser(false)
}

func (m *Model) syncConversationViewport() {
	panelHeight := m.conversationPanelHeight()
	m.viewport.SetSize(m.conversationInnerWidth(), conversationInnerHeight(panelHeight))
	m.viewport.SetContent(m.renderOutput())
}
