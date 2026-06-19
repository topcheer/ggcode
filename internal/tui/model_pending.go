package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func (m *Model) resetExitConfirm() {
	m.exitConfirmPending = false
}

func (m *Model) promptCancelConfirm() {
	m.cancelConfirmPending = true
	m.chatWriteSystem(nextSystemID(), m.t("cancel.confirm"))
}

func (m *Model) resetCancelConfirm() {
	m.cancelConfirmPending = false
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
	m.resetCancelConfirm()
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
	m.statusActivity = m.t("status.cancelling")
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
	if m.extPaneMgr != nil {
		m.extPaneMgr.CloseAll()
	}
}

func (m *Model) restorePendingInput() {
	pending := m.pending.consumeVisiblePrefix()
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
	text, hidden, _ := m.consumePendingSubmissionDetailed()
	if text == "" {
		return ""
	}
	debug.Log("tui", "drainPendingInterrupt: runID=%d text=%s", runID, util.Truncate(text, 100))
	if !hidden {
		m.appendUserMessage(text)
	}
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

func (q *pendingQueue) ensureQueue() *agentruntime.PendingQueue[*tunnel.MessageData] {
	if q.q != nil {
		return q.q
	}
	queue := agentruntime.NewPendingQueue[*tunnel.MessageData]()
	for _, item := range q.items {
		queue.Enqueue(item.Text, item.Hidden, cloneTunnelMessageData(item.TunnelMessageOverride))
	}
	q.q = queue
	return queue
}

func (q *pendingQueue) syncItemsFromQueue(queue *agentruntime.PendingQueue[*tunnel.MessageData]) {
	snapshot := queue.Snapshot()
	if len(snapshot) == 0 {
		q.items = nil
		q.q = queue
		return
	}
	items := make([]pendingSubmission, 0, len(snapshot))
	for _, item := range snapshot {
		items = append(items, pendingSubmission{
			Text:                  item.Text,
			Hidden:                item.Hidden,
			TunnelMessageOverride: cloneTunnelMessageData(item.Meta),
		})
	}
	q.items = items
	q.q = queue
}

func (q *pendingQueue) enqueue(text string) int {
	queue := q.ensureQueue()
	count := queue.Enqueue(text, false, nil)
	q.syncItemsFromQueue(queue)
	return count
}

func (q *pendingQueue) enqueueHidden(text string, override *tunnel.MessageData) int {
	queue := q.ensureQueue()
	count := queue.Enqueue(text, true, cloneTunnelMessageData(override))
	q.syncItemsFromQueue(queue)
	return count
}

func (q *pendingQueue) count() int {
	return len(q.items)
}

func (q *pendingQueue) clear() {
	q.items = nil
	q.q = agentruntime.NewPendingQueue[*tunnel.MessageData]()
}

func (q *pendingQueue) snapshot() []string {
	if len(q.items) == 0 {
		return nil
	}
	out := make([]string, 0, len(q.items))
	for _, item := range q.items {
		out = append(out, item.Text)
	}
	return out
}

func (q *pendingQueue) consume() string {
	text, _, _ := q.consumeDetailed()
	return text
}

func (q *pendingQueue) consumeVisiblePrefix() string {
	queue := q.ensureQueue()
	items := queue.ConsumePrefix(func(item agentruntime.PendingMessage[*tunnel.MessageData]) bool {
		return !item.Hidden && item.Meta == nil
	})
	q.syncItemsFromQueue(queue)
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.Text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (q *pendingQueue) consumeDetailed() (string, bool, *tunnel.MessageData) {
	queue := q.ensureQueue()
	item, ok := queue.Consume()
	if !ok {
		q.syncItemsFromQueue(queue)
		return "", false, nil
	}
	if item.Hidden || item.Meta != nil {
		q.syncItemsFromQueue(queue)
		return strings.TrimSpace(item.Text), true, cloneTunnelMessageData(item.Meta)
	}
	items := queue.ConsumePrefix(func(item agentruntime.PendingMessage[*tunnel.MessageData]) bool {
		return !item.Hidden && item.Meta == nil
	})
	q.syncItemsFromQueue(queue)
	parts := []string{item.Text}
	for _, pending := range items {
		parts = append(parts, pending.Text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), false, nil
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
