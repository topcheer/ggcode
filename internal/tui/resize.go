package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"github.com/mattn/go-runewidth"
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
	m.input.SetHeight(composerWrappedHeight(m.input.Value(), m.input.Width()))
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

// composerWrappedHeight calculates the actual number of rendered lines
// accounting for word-wrapping at the given textarea width.
func composerWrappedHeight(value string, width int) int {
	if width <= 0 {
		width = 1
	}
	totalLines := 0
	for _, line := range strings.Split(value, "\n") {
		if len(line) == 0 {
			totalLines++
			continue
		}
		// Use runewidth for correct CJK character width calculation.
		lineWidth := runewidth.StringWidth(line)
		if lineWidth == 0 {
			totalLines++
			continue
		}
		wrapped := (lineWidth + width - 1) / width
		if wrapped < 1 {
			wrapped = 1
		}
		totalLines += wrapped
	}
	if totalLines < 1 {
		totalLines = 1
	}
	if totalLines > 10 {
		totalLines = 10
	}
	return totalLines
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
