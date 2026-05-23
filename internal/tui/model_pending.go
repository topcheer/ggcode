package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func (m *Model) resetExitConfirm() {
	m.exitConfirmPending = false
}

func (m *Model) promptExitConfirm() {
	m.input.SetValue("")
	m.exitConfirmPending = true
	m.chatWriteSystem(nextSystemID(), m.t("exit.confirm"))
	m.chatListScrollToBottom()
}

func (m *Model) queuePendingSubmission(text string) {
	count := m.pending.enqueue(text)
	debug.Log("tui", "queuePendingSubmission: count=%d text=%s", count, util.Truncate(text, 100))
	if count == 0 {
		return
	}
	// Render the user's input in the conversation view so it looks like a
	// normal submission, rather than showing a "[queued N pending]" hint.
	m.chatWriteUser(nextChatID(), text)
	m.chatListScrollToBottom()
}

// queuePendingSubmissionHidden enqueues text for LLM submission without
// rendering it as a user message in the chat panel (e.g., cron triggers).
func (m *Model) queuePendingSubmissionHidden(text string) {
	m.queuePendingSubmissionHiddenWithOverride(text, nil)
}

func (m *Model) queuePendingSubmissionHiddenWithOverride(text string, override *tunnel.MessageData) {
	count := m.pending.enqueueHidden(text, override)
	debug.Log("tui", "queuePendingSubmissionHidden: count=%d text=%s", count, util.Truncate(text, 100))
}

func (m *Model) pendingSubmissionCount() int {
	return m.pending.count()
}

func (m *Model) clearPendingSubmissions() {
	m.pending.clear()
}

func (m *Model) pendingSubmissionSnapshot() []string {
	return m.pending.snapshot()
}

func (m *Model) cancelActiveRun() {
	if m.runCanceled {
		return
	}
	m.runCanceled = true
	cancelledTools := m.chatCancelAllRunningTools()
	for _, tool := range cancelledTools {
		if strings.TrimSpace(tool.ID) == "" || strings.TrimSpace(tool.ToolName) == "" {
			continue
		}
		m.pushTunnelToolResult(tool.ID, tool.ToolName, "Cancelled", true)
	}
	m.pushTunnelCancel()
	if m.cancelFunc != nil {
		m.cancelFunc()
	}

	// Cancel all running sub-agents and swarm teammates
	if m.subAgentMgr != nil {
		m.subAgentMgr.CancelAll()
	}
	if m.swarmMgr != nil {
		m.swarmMgr.CancelAll()
	}

	m.spinner.Stop()
	if m.harnessRunProject != nil {
		m.statusActivity = m.t("status.cancelling")
	} else {
		m.loading = false
		m.cancelFunc = nil
		m.statusActivity = ""
	}
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	if m.pendingSubmissionCount() > 0 {
		m.restorePendingInput()
	}
	debug.Log("tui", "cancelling active loop")
	m.chatWriteSystem(nextSystemID(), m.t("interrupted"))
	m.chatListScrollToBottom()
}

func (m *Model) consumePendingSubmission() string {
	return m.pending.consume()
}

func (m *Model) consumePendingSubmissionDetailed() (string, bool, *tunnel.MessageData) {
	return m.pending.consumeDetailed()
}

func (m *Model) submitPendingSubmissionCmd() tea.Cmd {
	text, hidden, override := m.consumePendingSubmissionDetailed()
	if text == "" {
		return nil
	}
	if override != nil {
		m.setNextTunnelUserMessageOverride(*override)
	}
	if hidden {
		return m.submitHiddenText(text)
	}
	return m.submitText(text, false)
}

// shutdownAll cancels all running sub-agents and swarm teammates.
// Called on exit (double ctrl+c, ctrl+d) to avoid orphaned background work.
func (m *Model) shutdownAll() {
	if m.subAgentMgr != nil {
		m.subAgentMgr.CancelAll()
	}
	if m.swarmMgr != nil {
		m.swarmMgr.CancelAll()
	}
}

func (m *Model) restorePendingInput() {
	pending := m.pending.consume()
	pending = strings.TrimSpace(pending)
	draft := strings.TrimSpace(m.input.Value())
	switch {
	case pending == "":
		return
	case draft == "":
		m.input.SetValue(pending)
	case draft == pending:
		m.input.SetValue(draft)
	default:
		m.input.SetValue(pending + "\n\n" + draft)
	}
	composerCursorEnd(&m.input)
}

func (m *Model) drainPendingInterrupt(runID int) string {
	text := m.consumePendingSubmission()
	if text == "" {
		return ""
	}
	debug.Log("tui", "drainPendingInterrupt: runID=%d text=%s", runID, util.Truncate(text, 100))
	m.appendUserMessage(text)
	// Don't send agentInterruptMsg — the user already saw their input rendered
	// in the conversation when it was queued. No extra "[delivered]" hint needed.
	return text
}

func (m *Model) sessionMutex() *sync.Mutex {
	if m.sessionMu == nil {
		m.sessionMu = &sync.Mutex{}
	}
	return m.sessionMu
}

// --- pendingQueue methods (pointer-reachable, safe across Model copies) ---

func cloneTunnelMessageData(data *tunnel.MessageData) *tunnel.MessageData {
	if data == nil {
		return nil
	}
	cp := *data
	return &cp
}

func (q *pendingQueue) enqueue(text string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, pendingSubmission{Text: text})
	return len(q.items)
}

func (q *pendingQueue) enqueueHidden(text string, override *tunnel.MessageData) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, pendingSubmission{
		Text:           text,
		Hidden:         true,
		TunnelOverride: cloneTunnelMessageData(override),
	})
	return len(q.items)
}

func (q *pendingQueue) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *pendingQueue) clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
}

func (q *pendingQueue) snapshot() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	out := make([]string, len(q.items))
	for i, item := range q.items {
		out[i] = item.Text
	}
	return out
}

func (q *pendingQueue) consume() string {
	text, _, _ := q.consumeDetailed()
	return text
}

func (q *pendingQueue) consumeDetailed() (string, bool, *tunnel.MessageData) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return "", false, nil
	}
	parts := make([]string, 0, len(q.items))
	hidden := true
	var override *tunnel.MessageData
	for _, item := range q.items {
		parts = append(parts, item.Text)
		if !item.Hidden {
			hidden = false
		}
		if item.TunnelOverride != nil {
			override = cloneTunnelMessageData(item.TunnelOverride)
		}
	}
	if !hidden {
		override = nil
	}
	joined := strings.TrimSpace(strings.Join(parts, "\n\n"))
	q.items = nil
	return joined, hidden, override
}

func stripImagePlaceholder(value, placeholder string) string {
	trimmed := strings.TrimSpace(value)
	placeholder = strings.TrimSpace(placeholder)
	if trimmed == "" || placeholder == "" {
		return trimmed
	}
	if trimmed == placeholder {
		return ""
	}
	if strings.HasPrefix(trimmed, placeholder) {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, placeholder))
	}
	return trimmed
}

func (m *Model) stripPendingImagePlaceholder(value string) string {
	if m.pendingImage == nil {
		return strings.TrimSpace(value)
	}
	return stripImagePlaceholder(value, m.pendingImage.placeholder)
}

func (m *Model) setComposerImagePlaceholder(msg imageAttachedMsg) {
	draft := m.input.Value()
	if m.pendingImage != nil {
		draft = stripImagePlaceholder(draft, m.pendingImage.placeholder)
	}
	draft = strings.TrimSpace(draft)
	if draft == "" {
		m.input.SetValue(msg.placeholder + " ")
	} else {
		m.input.SetValue(msg.placeholder + " " + draft)
	}
	composerCursorEnd(&m.input)
}
