package tui

import (
	"context"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/util"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
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
	m.queuePendingSubmissionOrigin(text, false)
}

func (m *Model) queuePendingSubmissionRemote(text string) {
	m.queuePendingSubmissionOrigin(text, true)
}

func (m *Model) queuePendingSubmissionOrigin(text string, remote bool) {

	imgs := m.pendingImages
	m.pendingImages = nil
	chatID := nextChatID()
	m.lastQueuedChatID = chatID
	m.queuedChatIDs = append(m.queuedChatIDs, chatID)
	count := m.pending.enqueueWithImagesOrigin(text, imgs, remote)
	debug.Log("tui", "queuePendingSubmission: count=%d text=%s imgs=%d", count, util.Truncate(text, 100), len(imgs))
	if count == 0 {
		return
	}

	displayText := text
	for _, img := range imgs {
		displayText = strings.TrimSpace(img.placeholder + " " + displayText)
	}
	m.chatWriteUser(chatID, displayText)
	m.chatListScrollToBottom()
	// 队列事件(append):webui 端镜像排队条目(nil fn=TUI 单机,安全 no-op)
	if m.queueNotifyFn != nil {
		m.queueNotifyFn("append", displayText, count)
	}
}

func (m *Model) dequeueLastVisible() (text string, imgs []imageAttachedMsg, ok bool) {
	text, imgs, ok = m.pending.popLastVisible()
	if !ok {
		return "", nil, false
	}
	// 队列事件(dequeue):↑ 键取回也是出列,webui 端同步移除
	if m.queueNotifyFn != nil {
		m.queueNotifyFn("dequeue", text, m.pending.count())
	}

	if m.lastQueuedChatID != "" {
		m.chatList.RemoveByID(m.lastQueuedChatID)

		for i := len(m.queuedChatIDs) - 1; i >= 0; i-- {
			if m.queuedChatIDs[i] == m.lastQueuedChatID {
				m.queuedChatIDs = append(m.queuedChatIDs[:i], m.queuedChatIDs[i+1:]...)
				break
			}
		}
		m.lastQueuedChatID = ""
	}
	debug.Log("tui", "dequeueLastVisible: dequeued text=%s", util.Truncate(text, 100))
	return text, imgs, ok
}

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

func (m *Model) pendingHeadIsAgentOrigin() bool {
	return m.pending.headIsAgentOrigin()
}

func (m *Model) clearPendingSubmissions() {
	count := m.pending.count()
	m.pending.clear()
	// 队列事件(clear):cancel 等清队,webui 端全清
	if count > 0 && m.queueNotifyFn != nil {
		m.queueNotifyFn("clear", "", 0)
	}
}

// QueuedSubmissions 返回可见排队项文本快照(webui GET /api/chat/queue 用,
// hidden 内部队列不暴露)
func (m *Model) QueuedSubmissions() []string {
	return m.pending.visibleTexts()
}

func (m *Model) cancelActiveRun() {
	m.resetCancelConfirm()
	if m.runCanceled {
		debug.Log("cancel", "cancelActiveRun: already cancelled, skipping")
		return
	}
	debug.Log("cancel", "cancelActiveRun: START")

	if !m.shellOwnedLoading && m.cancelFunc == nil {
		debug.Log("cancel", "cancelActiveRun: no active cancellable run (loading is uncancellable e.g. /compact); not poisoning runCanceled")
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

	if m.shellOwnedLoading && m.shellCancelFunc != nil {
		m.shellCancelFunc()
		return
	}
	if m.cancelFunc != nil {
		m.cancelFunc()
	}

	if m.subAgentMgr != nil || m.swarmMgr != nil {
		subMgr := m.subAgentMgr
		swarmMgr := m.swarmMgr
		prog := m.program
		m.subAgentsCanceling = true
		debug.Log("cancel", "cancelActiveRun: setting subAgentsCanceling=true, starting async CancelAll")
		safego.Go("tui.cancelSubAgents", func() {
			if subMgr != nil {
				cancelled := subMgr.CancelAll()
				debug.Log("cancel", "async CancelAll: subagents cancelled=%d", cancelled)
			}
			if swarmMgr != nil {
				swarmMgr.CancelAll()
				debug.Log("cancel", "async CancelAll: swarm teammates cancelled")
			}
			debug.Log("cancel", "cancelActiveRun: async CancelAll complete, clearing subAgentsCanceling")
			if prog != nil {
				prog.Send(subAgentsCancelDoneMsg{})
			}
		})
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
	text, hidden, override, imgs, _ := m.pending.consumeDetailed()
	return text, hidden, override, imgs
}

func (m *Model) submitPendingSubmissionCmd() tea.Cmd {
	text, hidden, override, imgs, remote := m.pending.consumeDetailed()
	if text == "" && len(imgs) == 0 {
		return nil
	}
	// 队列事件(dequeue):turn 结束自动消费,webui 端同步移除(hidden 内部队列不通知)
	if !hidden && m.queueNotifyFn != nil {
		m.queueNotifyFn("dequeue", text, m.pending.count())
	}
	if remote {

		m.agent.InhibitNextRefusalLedgerWrite()
	}
	if override != nil {
		m.setNextTunnelUserMessageOverride(*override)
	}

	m.pendingImages = imgs
	if hidden {
		return m.submitHiddenText(text)
	}
	return m.submitText(text, false)
}

func (m *Model) shutdownAll() {

	if m.subAgentMgr != nil || m.swarmMgr != nil {
		subMgr := m.subAgentMgr
		swarmMgr := m.swarmMgr
		safego.Go("tui.shutdownAll", func() {
			if subMgr != nil {
				subMgr.CancelAll()
			}
			if swarmMgr != nil {
				swarmMgr.CancelAll()
			}
		})
	}
	if m.extPaneMgr != nil {
		m.extPaneMgr.CloseAll()
	}
	if m.cmdPaneMgr != nil {
		m.cmdPaneMgr.Close()
	}

	if m.jobManager != nil {
		jm := m.jobManager
		safego.Go("tui.shutdownAll.commandJobs", func() { jm.ShutdownAll(2 * time.Second) })
	}

	m.cancelKnightTasks()
}

type knightTaskHandle struct {
	cancel context.CancelFunc
}

type knightCancelSet struct {
	mu      sync.Mutex
	handles []*knightTaskHandle
}

func (m *Model) registerKnightTask() (context.Context, *knightTaskHandle) {

	ctx, cancel := context.WithCancel(context.Background())
	h := &knightTaskHandle{cancel: cancel}
	m.knightTasks.mu.Lock()
	m.knightTasks.handles = append(m.knightTasks.handles, h)
	m.knightTasks.mu.Unlock()
	return ctx, h
}

func (m *Model) releaseKnightTask(h *knightTaskHandle) {
	if m.knightTasks == nil {
		return
	}
	m.knightTasks.mu.Lock()
	for i, cur := range m.knightTasks.handles {
		if cur == h {
			m.knightTasks.handles = append(m.knightTasks.handles[:i], m.knightTasks.handles[i+1:]...)
			break
		}
	}
	m.knightTasks.mu.Unlock()
}

func (m *Model) cancelKnightTasks() {
	if m.knightTasks == nil {
		return
	}
	m.knightTasks.mu.Lock()
	handles := m.knightTasks.handles
	m.knightTasks.handles = nil
	m.knightTasks.mu.Unlock()
	for _, h := range handles {
		h.cancel()
	}
}

func (m *Model) restorePendingInput() {

	for _, id := range m.queuedChatIDs {
		if id != "" {
			m.chatList.RemoveByID(id)
		}
	}
	m.queuedChatIDs = nil
	m.lastQueuedChatID = ""
	pending, restoredImgs := m.pending.consumeVisiblePrefix()

	if hidden := m.pendingSubmissionCount(); hidden > 0 {
		debug.Log("tui", "restorePendingInput: %d hidden submission(s) remain queued for next run", hidden)
	}
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

	if len(restoredImgs) > 0 {
		m.pendingImages = append(m.pendingImages, restoredImgs...)
		debug.Log("tui", "restorePendingInput: restored %d image(s) from cancelled queue", len(restoredImgs))
	}
	composerCursorEnd(&m.input)
}

func (m *Model) drainPendingInterrupt(runID int) []provider.ContentBlock {
	text, hidden, override, imgs := m.consumePendingSubmissionDetailed()
	if text == "" && len(imgs) == 0 {
		return nil
	}
	debug.Log("tui", "drainPendingInterrupt: runID=%d text=%s", runID, util.Truncate(text, 100))

	if override != nil {
		m.setNextTunnelUserMessageOverride(*override)
	}
	if len(imgs) > 0 {

		// #4598: this runs on the AGENT goroutine (the interruption
		// handler set up in submit.go), while the Update goroutine may be
		// appending a freshly pasted image to m.pendingImages right now.
		// The old `m.pendingImages = imgs` REPLACED the slice and silently
		// dropped anything pasted mid-run. Merge instead - same shape as
		// restorePendingInput's #1744 restore above.

		m.pendingImages = append(m.pendingImages, imgs...)
	}
	_ = hidden

	return []provider.ContentBlock{{Type: "text", Text: text}}
}

func (m *Model) sessionMutex() *sync.Mutex {
	if m.sessionMu == nil {
		m.sessionMu = &sync.Mutex{}
	}
	return m.sessionMu
}

func cloneTunnelMessageData(data *tunnel.MessageData) *tunnel.MessageData {
	if data == nil {
		return nil
	}
	cp := *data
	return &cp
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

func (q *pendingQueue) syncItemsFromQueueLocked(queue *agentruntime.PendingQueue[*tunnel.MessageData]) {
	snapshot := queue.Snapshot()
	if len(snapshot) == 0 {
		q.items = nil
		q.q = queue
		return
	}

	type entrySlot struct {
		text   string
		images []imageAttachedMsg
		remote bool
		used   bool
	}
	var slots []entrySlot
	for _, item := range q.items {
		if len(item.Images) > 0 || item.RemoteOrigin {
			slots = append(slots, entrySlot{text: item.Text, images: item.Images, remote: item.RemoteOrigin})
		}
	}

	items := make([]pendingSubmission, 0, len(snapshot))
	for _, item := range snapshot {
		ps := pendingSubmission{
			Text:                  item.Text,
			Hidden:                item.Hidden,
			TunnelMessageOverride: cloneTunnelMessageData(item.Meta),
		}
		for i := range slots {
			if !slots[i].used && slots[i].text == item.Text {
				ps.Images = slots[i].images
				ps.RemoteOrigin = slots[i].remote
				slots[i].used = true
				break
			}
		}
		items = append(items, ps)
	}
	q.items = items
	q.q = queue
}

func (q *pendingQueue) enqueueWithImages(text string, imgs []imageAttachedMsg) int {
	return q.enqueueWithImagesOrigin(text, imgs, false)
}

func (q *pendingQueue) enqueueWithImagesOrigin(text string, imgs []imageAttachedMsg, remote bool) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	queue := q.ensureQueueLocked()
	count := queue.Enqueue(text, false, nil)
	q.syncItemsFromQueueLocked(queue)

	if len(q.items) > 0 {
		q.items[len(q.items)-1].Images = imgs
		q.items[len(q.items)-1].RemoteOrigin = remote
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

// visibleTexts 返回可见排队项文本(hidden 内部队列不暴露),webui 队列快照用
func (q *pendingQueue) visibleTexts() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.items))
	for _, it := range q.items {
		if !it.Hidden {
			out = append(out, it.Text)
		}
	}
	return out
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

func (q *pendingQueue) popLastVisible() (text string, imgs []imageAttachedMsg, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	queue := q.ensureQueueLocked()
	item, found := queue.PopLastVisible()
	if !found {
		q.syncItemsFromQueueLocked(queue)
		return "", nil, false
	}

	for i := len(q.items) - 1; i >= 0; i-- {
		if q.items[i].Text == item.Text && !q.items[i].Hidden {
			imgs = q.items[i].Images
			break
		}
	}
	q.syncItemsFromQueueLocked(queue)
	return strings.TrimSpace(item.Text), imgs, true
}

func (q *pendingQueue) consume() string {
	text, _, _, _, _ := q.consumeDetailed()
	return text
}

func (q *pendingQueue) headIsAgentOrigin() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return false
	}
	return q.items[0].RemoteOrigin || q.items[0].Hidden
}

func (q *pendingQueue) consumeVisiblePrefix() (string, []imageAttachedMsg) {
	q.mu.Lock()
	defer q.mu.Unlock()
	queue := q.ensureQueueLocked()
	items := queue.ConsumePrefix(func(item agentruntime.PendingMessage[*tunnel.MessageData]) bool {
		return !item.Hidden && item.Meta == nil
	})

	consumedCount := len(items)
	var allImgs []imageAttachedMsg
	for i := 0; i < consumedCount && i < len(q.items); i++ {
		allImgs = append(allImgs, q.items[i].Images...)
	}
	q.syncItemsFromQueueLocked(queue)
	if len(items) == 0 {
		return "", allImgs
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.Text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), allImgs
}

func (q *pendingQueue) consumeDetailed() (string, bool, *tunnel.MessageData, []imageAttachedMsg, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	queue := q.ensureQueueLocked()
	item, ok := queue.Consume()
	if !ok {
		q.syncItemsFromQueueLocked(queue)
		return "", false, nil, nil, false
	}
	if item.Hidden || item.Meta != nil {
		remote := q.itemsRemoteLocked(1)
		q.syncItemsFromQueueLocked(queue)
		return strings.TrimSpace(item.Text), true, cloneTunnelMessageData(item.Meta), nil, remote
	}
	headRemote := false
	if len(q.items) > 0 {
		headRemote = q.items[0].RemoteOrigin
	}

	sameOriginLimit := 0
	for i := 1; i < len(q.items); i++ {
		if q.items[i].Hidden || q.items[i].TunnelMessageOverride != nil {
			break
		}
		if q.items[i].RemoteOrigin != headRemote {
			break
		}
		sameOriginLimit++
	}
	n := 0
	items := queue.ConsumePrefix(func(item agentruntime.PendingMessage[*tunnel.MessageData]) bool {
		if n >= sameOriginLimit || item.Hidden || item.Meta != nil {
			return false
		}
		n++
		return true
	})

	consumedCount := 1 + len(items)
	remote := q.itemsRemoteLocked(consumedCount)
	var allImgs []imageAttachedMsg
	for i := 0; i < consumedCount && i < len(q.items); i++ {
		allImgs = append(allImgs, q.items[i].Images...)
	}
	q.syncItemsFromQueueLocked(queue)
	parts := []string{item.Text}
	for _, pending := range items {
		parts = append(parts, pending.Text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), false, nil, allImgs, remote
}

func (q *pendingQueue) itemsRemoteLocked(n int) bool {
	for i := 0; i < n && i < len(q.items); i++ {
		if q.items[i].RemoteOrigin {
			return true
		}
	}
	return false
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

func (m *Model) pendingImageCount() int {
	return len(m.pendingImages)
}

func (m *Model) clearPendingImages() {
	m.pendingImages = nil
}

func (m *Model) popPendingImage() (*imageAttachedMsg, bool) {
	n := len(m.pendingImages)
	if n == 0 {
		return nil, false
	}
	last := m.pendingImages[n-1]
	m.pendingImages = m.pendingImages[:n-1]
	return &last, true
}
