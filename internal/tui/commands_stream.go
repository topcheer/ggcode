package tui

import (
	"bytes"
	"strings"
)

func (m *Model) ensureOutputEndsWithNewline() {
	if m.output == nil || m.output.Len() == 0 {
		return
	}
	if strings.HasSuffix(m.output.String(), "\n") {
		return
	}
	m.output.WriteString("\n")
}

func (m *Model) ensureOutputHasBlankLine() {
	if m.output == nil || m.output.Len() == 0 {
		return
	}
	switch {
	case strings.HasSuffix(m.output.String(), "\n\n"):
		return
	case strings.HasSuffix(m.output.String(), "\n"):
		m.output.WriteString("\n")
	default:
		m.output.WriteString("\n\n")
	}
}

func (m *Model) appendStreamChunk(chunk string) {
	if chunk == "" {
		return
	}
	chunk = relativizeResult(chunk)
	if localized, ok := m.localizedStreamStatus(chunk); ok {
		m.appendStreamStatusLine(localized)
		return
	}
	m.closeToolActivityGroup()
	m.flushGroupedActivitiesToOutput()
	if m.streamBuffer == nil {
		m.streamBuffer = &bytes.Buffer{}
	}
	if !m.streamPrefixWritten {
		m.ensureOutputHasBlankLine()
		m.streamStartPos = m.output.Len()
		m.output.WriteString(assistantBulletStyle.Render("● "))
		m.streamPrefixWritten = true

		// New: start a streaming assistant entry in chatEntries
		m.chatEntries.Append(ChatEntry{
			Role:      "assistant",
			Prefix:    "● ",
			Streaming: true,
		})
	}
	if m.streamBuffer != nil {
		m.streamBuffer.WriteString(chunk)
	}
	// Update the last chatEntry's raw text from streamBuffer
	if last := m.chatEntries.LastMatching("assistant"); last != nil && last.Streaming {
		last.RawText = m.streamBuffer.String()
		last.Invalidate()
	}
	m.rewriteActiveStreamOutput(true)
	m.trimOutput()
	m.syncConversationViewport()
	m.viewport.GotoBottom()
}

func (m *Model) localizedStreamStatus(chunk string) (string, bool) {
	switch strings.TrimSpace(chunk) {
	case "[compacting conversation to stay within context window]":
		return m.t("status.compacting"), true
	case "[conversation compacted]":
		return m.t("status.compacted"), true
	default:
		return "", false
	}
}

func (m *Model) appendStreamStatusLine(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.closeToolActivityGroup()
	m.flushGroupedActivitiesToOutput()
	if m.streamBuffer == nil {
		m.streamBuffer = &bytes.Buffer{}
	}
	if m.streamBuffer.Len() > 0 {
		m.renderStreamBuffer(true)
		m.streamBuffer.Reset()
	}
	m.harnessRunLiveTail = ""
	m.streamPrefixWritten = false
	m.streamStartPos = -1
	// Finalize streaming assistant entry before adding compaction line
	if last := m.chatEntries.LastMatching("assistant"); last != nil && last.Streaming {
		last.Streaming = false
	}
	switch {
	case m.output == nil || m.output.Len() == 0:
	case strings.HasSuffix(m.output.String(), "\n\n"):
		m.output.Truncate(m.output.Len() - 1)
	default:
		m.ensureOutputEndsWithNewline()
	}
	m.output.WriteString(compactionBulletStyle.Render("● "))
	m.output.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		m.output.WriteString("\n")
	}
	m.chatEntries.Append(ChatEntry{
		Role:    "compaction",
		RawText: compactionBulletStyle.Render("● ") + text,
	})
	m.trimOutput()
	m.syncConversationViewport()
	m.viewport.GotoBottom()
}

func (m *Model) renderCurrentStreamMarkdown() string {
	if m.streamBuffer == nil || m.streamBuffer.Len() == 0 {
		return ""
	}
	return trimLeadingRenderedSpacing(RenderMarkdownWidth(m.streamBuffer.String(), max(20, m.conversationInnerWidth()-2)))
}

func (m *Model) rewriteActiveStreamOutput(renderMarkdown bool) {
	if !m.streamPrefixWritten || m.streamStartPos < 0 || m.streamStartPos > m.output.Len() {
		return
	}
	m.output.Truncate(m.streamStartPos)
	m.output.WriteString(assistantBulletStyle.Render("● "))
	rendered := m.streamBuffer.String()
	if renderMarkdown {
		rendered = m.renderCurrentStreamMarkdown()
	}
	if rendered != "" {
		m.output.WriteString(rendered)
	}
	if m.harnessRunLiveTail != "" {
		m.output.WriteString(m.harnessRunLiveTail)
	}
}

func (m *Model) renderStreamBuffer(renderMarkdown bool) {
	if m.streamBuffer == nil || m.streamBuffer.Len() == 0 {
		return
	}
	m.rewriteActiveStreamOutput(renderMarkdown)
	m.streamBuffer.Reset()
	m.harnessRunLiveTail = ""
}
