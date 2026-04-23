package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
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
	m.input.SetHeight(composerHeight(m.input.Value()))
	m.syncQuestionnaireInputWidth()
	panelHeight := m.conversationPanelHeight()
	m.viewport.SetSize(m.conversationInnerWidth(), conversationInnerHeight(panelHeight))
	prewarmMarkdownRenderers(m.previewContentWidth(), m.fileBrowserPreviewWidth())
	m.chatEntries.InvalidateAll()
	m.syncPreviewViewport(false)
	m.syncFileBrowser(false)
}

// composerHeight returns the textarea height based on the number of lines
// in the input value. Min 1, max 10.
func composerHeight(value string) int {
	lines := strings.Count(value, "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > 10 {
		lines = 10
	}
	return lines
}

// composerCursorEnd moves the cursor to the very end of the textarea value.
func composerCursorEnd(ta *textarea.Model) {
	val := ta.Value()
	ta.SetValue(val)
}

// inputCursor returns the absolute character offset of the cursor in the textarea.
func inputCursor(ta *textarea.Model) int {
	line := ta.Line()
	col := ta.Column()
	val := ta.Value()
	if val == "" {
		return 0
	}
	lines := strings.Split(val, "\n")
	pos := 0
	for i := 0; i < line && i < len(lines); i++ {
		pos += len(lines[i]) + 1 // +1 for newline
	}
	pos += col
	return pos
}

func (m *Model) syncConversationViewport() {
	panelHeight := m.conversationPanelHeight()
	m.viewport.SetSize(m.conversationInnerWidth(), conversationInnerHeight(panelHeight))
	m.viewport.SetContent(m.renderOutput())
}
