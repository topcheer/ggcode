package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
)

func (m *Model) handleResize(width, height int) {
	if width == m.width && height == m.height {
		m.lastResizeAt = time.Now()
		return
	}
	m.width = width
	m.height = height

	m.streamViewState.setSize(width, height)
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
	prewarmMarkdownRenderers(m.panelContentWidth())

	m.syncConversationViewport()

	m.syncStatsPanelViewport(false)

}

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

func (m *Model) relayoutAfterSidebarChange() {
	inputWidth := m.mainColumnWidth() - 6
	if inputWidth < 20 {
		inputWidth = m.mainColumnWidth()
	}
	m.input.SetWidth(inputWidth)
	m.viewport.SetSize(m.mainColumnWidth(), m.calcViewportHeight())

	m.syncQuestionnaireInputWidth()
	m.syncConversationViewport()
	m.syncStatsPanelViewport(false)
}

func (m *Model) calcViewportHeight() int {
	h := m.height - 5
	if h < 3 {
		h = 3
	}
	return h
}

func composerCursorEnd(ta *textarea.Model) {
	ta.View()
	ta.MoveToEnd()
}

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
		pos += len(lines[i]) + 1
	}
	if line >= len(lines) {
		return len(val)
	}
	runes := []rune(lines[line])
	if col < 0 {
		col = 0
	}
	if col > len(runes) {
		col = len(runes)
	}
	pos += len(string(runes[:col]))
	return pos
}

func (m *Model) syncConversationViewport() {
	if m.chatList != nil {
		m.chatList.SetSize(m.conversationInnerWidth(), conversationInnerHeight(m.conversationPanelHeight()))
	}
}
