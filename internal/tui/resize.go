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
	m.syncQuestionnaireInputWidth()
	prewarmMarkdownRenderers(m.panelContentWidth())
	// Update conversation panel dimensions on resize so chatList re-renders
	// correctly at the new terminal size.
	m.syncConversationViewport()
	// chatList items cache by width — will re-render automatically on next Render()
	m.syncStatsPanelViewport(false)

}

// composerHeight returns the textarea height based on the number of lines
// in the input value. Min 1, max 10. Used only in tests.
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

// relayoutAfterSidebarChange re-computes input and viewport widths when the
// sidebar is toggled without a window resize event.
func (m *Model) relayoutAfterSidebarChange() {
	inputWidth := m.mainColumnWidth() - 6
	if inputWidth < 20 {
		inputWidth = m.mainColumnWidth()
	}
	m.input.SetWidth(inputWidth)
	m.viewport.SetSize(m.mainColumnWidth(), m.calcViewportHeight())
	// #1390: handleResize also runs these three syncs - chatList caches
	// items BY WIDTH, so skipping them left the conversation panel
	// rendering at the pre-toggle width (misaligned wrap/truncation) and
	// the stats/questionnaire panels stale until the next WindowSizeMsg.
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

// composerCursorEnd moves the cursor to the very end of the textarea value
// and scrolls the viewport to follow it.
//
// Why View() before MoveToEnd: SetValue's internal repositionView runs while
// the viewport still holds the previous content, so its ScrollDown clamps
// against the old line count and the tail of long text stays clipped (CJK
// text hits this fast: soft-wrap only fits ~7 full-width runes per line at
// narrow widths, so 10 wrapped lines arrive at ~70 chars). Rendering once
// first syncs the viewport content; MoveToEnd then scrolls so the cursor -
// and the text being typed - is visible. Without this, restored drafts
// (pending input, history recall) appear truncated in the composer while the
// full value is silently sent on submit.
func composerCursorEnd(ta *textarea.Model) {
	ta.View()
	ta.MoveToEnd()
}

// inputCursor returns the absolute byte offset of the cursor in the textarea value.
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

// syncConversationViewport updates chatList dimensions to match the
// current conversation panel size. Called after content changes that
// may affect layout.
func (m *Model) syncConversationViewport() {
	if m.chatList != nil {
		m.chatList.SetSize(m.conversationInnerWidth(), conversationInnerHeight(m.conversationPanelHeight()))
	}
}
