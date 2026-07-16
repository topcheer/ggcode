package tui

import (
	"bytes"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/chat"
)

// streamRenderInterval is the minimum time between UI updates during streaming.
// Without throttling, every tiny chunk invalidates the markdown render cache
// and triggers a full re-render on the next View() frame — causing visible
// lag on long outputs. 50ms (~20fps) is smooth enough for reading while
// cutting render work by 5-10x compared to per-chunk updates.
const streamRenderInterval = 50 * time.Millisecond

func (m *Model) appendStreamChunk(chunk string) {
	if chunk == "" {
		return
	}
	chunk = relativizeResult(chunk)
	if localized, ok := m.localizedStreamStatus(chunk); ok {
		m.appendStreamStatusLine(localized)
		return
	}
	m.chatFinishAllRunningTools()
	// Collapse reasoning block when the first body text arrives.
	// This implements the design: reasoning starts on first reasoning chunk,
	// ends on first text or tool event.
	if m.reasoningActive {
		m.chatFinishReasoning()
	}
	if m.streamBuffer == nil {
		m.streamBuffer = &bytes.Buffer{}
	}
	if !m.streamPrefixWritten {
		m.streamPrefixWritten = true
		m.nextAssistantID()
		m.chatEnsureAssistant()
	}
	if m.streamBuffer != nil {
		m.streamBuffer.WriteString(chunk)
	}
	// Throttle UI updates: only flush to AssistantItem when enough time has
	// elapsed since the last render. This prevents per-chunk markdown
	// re-rendering which causes lag on long outputs.
	now := time.Now()
	m.streamPendingFlush = true
	if now.Sub(m.streamLastRender) >= streamRenderInterval {
		m.flushStreamToUI()
	}
	m.chatListScrollToBottom()
}

// flushStreamToUI pushes the buffered stream text to the AssistantItem and
// marks the render as done. Called by appendStreamChunk (throttled) and
// chatFinishAssistant (final flush).
func (m *Model) flushStreamToUI() {
	if m.streamBuffer == nil {
		return
	}
	m.chatUpdateAssistantText(m.currentAssistantID(), m.streamBuffer.String())
	m.streamLastRender = time.Now()
	m.streamPendingFlush = false
}

// flushPendingStream forces a UI flush if there's unflushed buffered text.
// Called on spinner tick to ensure pending content eventually appears even
// if no new chunks arrive.
func (m *Model) flushPendingStream() {
	if m.streamPendingFlush && m.streamBuffer != nil {
		m.flushStreamToUI()
	}
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
	m.chatFinishAllRunningTools()
	if m.streamBuffer == nil {
		m.streamBuffer = &bytes.Buffer{}
	}
	if m.streamBuffer.Len() > 0 {
		m.renderStreamBuffer(true)
		m.streamBuffer.Reset()
	}
	m.harnessRunLiveTail = ""
	m.streamPrefixWritten = false
	m.reasoningActive = false
	m.chatFinishAssistant(m.currentAssistantID())
	m.chatWriteSystem(nextChatID(), strings.TrimSuffix(text, "\n"))
	m.chatListScrollToBottom()
}

func (m *Model) appendReasoningChunk(chunk string) {
	if chunk == "" {
		return
	}
	m.chatFinishAllRunningTools()
	if !m.streamPrefixWritten {
		m.streamPrefixWritten = true
		m.nextAssistantID()
		m.chatEnsureAssistant()
	}
	m.reasoningActive = true
	// Append reasoning to the current assistant item
	aid := m.currentAssistantID()
	if m.chatList != nil {
		if item := m.chatList.FindByID(aid); item != nil {
			if a, ok := item.(*chat.AssistantItem); ok {
				// msg.Text is already the accumulated full text from batchReasoningBuf
				a.SetReasoning(chunk)
			}
		}
	}
	m.chatListScrollToBottom()
}

// chatFinishReasoning collapses the reasoning block in the current assistant item
// and marks reasoning as inactive. It is called when the first text chunk or tool
// event arrives (the natural end of reasoning in an LLM turn), or at turn end via
// agentTurnDoneMsg (which is a no-op if reasoning was already collapsed).
func (m *Model) chatFinishReasoning() {
	if !m.reasoningActive {
		return
	}
	m.reasoningActive = false
	aid := m.currentAssistantID()
	if m.chatList != nil {
		if item := m.chatList.FindByID(aid); item != nil {
			if a, ok := item.(*chat.AssistantItem); ok {
				a.SetReasoningFinished()
			}
		}
	}
}

func (m *Model) renderStreamBuffer(renderMarkdown bool) {
	if m.streamBuffer == nil || m.streamBuffer.Len() == 0 {
		return
	}
	m.streamBuffer.Reset()
	m.harnessRunLiveTail = ""
}
