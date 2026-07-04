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
	// Capture images attached with this submission and clear them from the
	// pending list so they don't leak into the next submission.
	imgs := m.pendingImages
	m.pendingImages = nil
	count := m.pending.enqueueWithImages(text, imgs)
	debug.Log("tui", "queuePendingSubmission: count=%d text=%s imgs=%d", count, util.Truncate(text, 100), len(imgs))
	if count == 0 {
		return
	}
	// Render the user's input in the conversation view so it looks like a
	// normal submission, rather than showing a "[queued N pending]" hint.
	// Include image placeholders so the user sees what was attached.
	displayText := text
	for _, img := range imgs {
		displayText = strings.TrimSpace(img.placeholder + " " + displayText)
	}
	m.chatWriteUser(nextChatID(), displayText)
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

func (m *Model) consumePendingSubmissionDetailed() (string, bool, *tunnel.MessageData, []imageAttachedMsg) {
	return m.pending.consumeDetailed()
}

func (m *Model) submitPendingSubmissionCmd() tea.Cmd {
	text, hidden, override, imgs := m.consumePendingSubmissionDetailed()
	if text == "" && len(imgs) == 0 {
		return nil
	}
	if override != nil {
		m.setNextTunnelUserMessageOverride(*override)
	}
	// Restore images so startAgentWithExpand picks them up.
	m.pendingImages = imgs
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
	if m.cmdPaneMgr != nil {
		m.cmdPaneMgr.Close()
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
	text, hidden, _, _ := m.consumePendingSubmissionDetailed()
	if text == "" {
		return ""
	}
	debug.Log("tui", "drainPendingInterrupt: runID=%d text=%s", runID, util.Truncate(text, 100))
	// Do NOT call appendUserMessage here. The agent's injectPendingInterruptions()
	// adds the (wrapped) interrupt text to its contextManager, and
	// persistFullSessionMessages() will sync+persist it. Calling appendUserMessage
	// here would create a SECOND JSONL record for the same user input —
	// causing duplicate user bubbles on session reload.
	_ = hidden
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
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.ensureQueueLocked()
}

func (q *pendingQueue) ensureQueueLocked() *agentruntime.PendingQueue[*tunnel.MessageData] {
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

// syncItemsFromQueue rebuilds q.items from the queue snapshot while
// preserving Images on existing items by text-matching. Without this
// preservation, every call would silently destroy images on previously
// enqueued pending submissions.
func (q *pendingQueue) syncItemsFromQueueLocked(queue *agentruntime.PendingQueue[*tunnel.MessageData]) {
	snapshot := queue.Snapshot()
	if len(snapshot) == 0 {
		q.items = nil
		q.q = queue
		return
	}
	// Collect images from existing items to preserve through the rebuild.
	// Use text as a match key; each match is consumed (first-wins) to handle
	// duplicate text submissions.
	type imgSlot struct {
		text   string
		images []imageAttachedMsg
		used   bool
	}
	var imgSlots []imgSlot
	for _, item := range q.items {
		if len(item.Images) > 0 {
			imgSlots = append(imgSlots, imgSlot{text: item.Text, images: item.Images})
		}
	}

	items := make([]pendingSubmission, 0, len(snapshot))
	for _, item := range snapshot {
		ps := pendingSubmission{
			Text:                  item.Text,
			Hidden:                item.Hidden,
			TunnelMessageOverride: cloneTunnelMessageData(item.Meta),
		}
		for i := range imgSlots {
			if !imgSlots[i].used && imgSlots[i].text == item.Text {
				ps.Images = imgSlots[i].images
				imgSlots[i].used = true
				break
			}
		}
		items = append(items, ps)
	}
	q.items = items
	q.q = queue
}

func (q *pendingQueue) enqueue(text string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	queue := q.ensureQueueLocked()
	count := queue.Enqueue(text, false, nil)
	q.syncItemsFromQueueLocked(queue)
	return count
}

// enqueueWithImages stores images alongside the pending text in q.items.
// The agentruntime.PendingQueue doesn't support images, so we store them
// directly and recover them during consume.
func (q *pendingQueue) enqueueWithImages(text string, imgs []imageAttachedMsg) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	queue := q.ensureQueueLocked()
	count := queue.Enqueue(text, false, nil)
	q.syncItemsFromQueueLocked(queue)
	// After sync, the last item in q.items corresponds to our submission.
	// Store images there.
	if len(q.items) > 0 {
		q.items[len(q.items)-1].Images = imgs
	}
	return count
}

func (q *pendingQueue) enqueueHidden(text string, override *tunnel.MessageData) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	queue := q.ensureQueueLocked()
	count := queue.Enqueue(text, true, cloneTunnelMessageData(override))
	q.syncItemsFromQueueLocked(queue)
	return count
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
	q.q = agentruntime.NewPendingQueue[*tunnel.MessageData]()
}

func (q *pendingQueue) snapshot() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
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
	text, _, _, _ := q.consumeDetailed()
	return text
}

func (q *pendingQueue) consumeVisiblePrefix() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	queue := q.ensureQueueLocked()
	items := queue.ConsumePrefix(func(item agentruntime.PendingMessage[*tunnel.MessageData]) bool {
		return !item.Hidden && item.Meta == nil
	})
	q.syncItemsFromQueueLocked(queue)
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.Text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (q *pendingQueue) consumeDetailed() (string, bool, *tunnel.MessageData, []imageAttachedMsg) {
	q.mu.Lock()
	defer q.mu.Unlock()
	queue := q.ensureQueueLocked()
	item, ok := queue.Consume()
	if !ok {
		q.syncItemsFromQueueLocked(queue)
		return "", false, nil, nil
	}
	if item.Hidden || item.Meta != nil {
		q.syncItemsFromQueueLocked(queue)
		return strings.TrimSpace(item.Text), true, cloneTunnelMessageData(item.Meta), nil
	}
	items := queue.ConsumePrefix(func(item agentruntime.PendingMessage[*tunnel.MessageData]) bool {
		return !item.Hidden && item.Meta == nil
	})
	// Collect images from ALL consumed items (not just the first).
	// q.items still holds the pre-consume state; consumedCount tells us
	// how many leading entries were consumed.
	consumedCount := 1 + len(items)
	var allImgs []imageAttachedMsg
	for i := 0; i < consumedCount && i < len(q.items); i++ {
		allImgs = append(allImgs, q.items[i].Images...)
	}
	q.syncItemsFromQueueLocked(queue)
	parts := []string{item.Text}
	for _, pending := range items {
		parts = append(parts, pending.Text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), false, nil, allImgs
}

// stripImagePlaceholder removes a leading image placeholder from a value.
// Kept for backward compatibility with test callers.
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

// pendingImageCount returns the number of attached images.
func (m *Model) pendingImageCount() int {
	return len(m.pendingImages)
}

// clearPendingImages removes all attached images.
func (m *Model) clearPendingImages() {
	m.pendingImages = nil
}

// popPendingImage removes and returns the last attached image, if any.
func (m *Model) popPendingImage() (*imageAttachedMsg, bool) {
	n := len(m.pendingImages)
	if n == 0 {
		return nil, false
	}
	last := m.pendingImages[n-1]
	m.pendingImages = m.pendingImages[:n-1]
	return &last, true
}
