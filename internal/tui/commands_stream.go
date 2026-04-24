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
	m.dualWriteSystem("\n")
}

func (m *Model) ensureOutputHasBlankLine() {
	if m.output == nil || m.output.Len() == 0 {
		return
	}
	switch {
	case strings.HasSuffix(m.output.String(), "\n\n"):
		return
	case strings.HasSuffix(m.output.String(), "\n"):
		m.dualWriteSystem("\n")
	default:
		m.dualWriteSystem("\n\n")
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
		m.streamPrefixWritten = true
		m.nextAssistantID()
		m.chatEnsureAssistant()
	}
	if m.streamBuffer != nil {
		m.streamBuffer.WriteString(chunk)
	}
	m.chatUpdateAssistantText(m.currentAssistantID(), m.streamBuffer.String())
	m.chatListScrollToBottom()
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
	m.chatFinishAssistant(m.currentAssistantID())
	m.chatWriteSystem(nextChatID(), strings.TrimSuffix(text, "\n"))
	m.chatListScrollToBottom()
}

func (m *Model) renderCurrentStreamMarkdown() string {
	if m.streamBuffer == nil || m.streamBuffer.Len() == 0 {
		return ""
	}
	return trimLeadingRenderedSpacing(RenderMarkdownWidth(m.streamBuffer.String(), max(20, m.conversationInnerWidth()-2)))
}

func (m *Model) renderStreamBuffer(renderMarkdown bool) {
	if m.streamBuffer == nil || m.streamBuffer.Len() == 0 {
		return
	}
	m.streamBuffer.Reset()
	m.harnessRunLiveTail = ""
}
