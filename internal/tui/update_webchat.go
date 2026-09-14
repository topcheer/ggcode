package tui

import (
	"bytes"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

// handleWebchatUserMsg processes a webchat message injected from the webui
// (#2357 slice 2: extracted from Model.Update - zero behavior change, the
// body moved verbatim). Two paths: start the agent when idle (the #1882
// predicate), or queue the submission when busy.
func (m *Model) handleWebchatUserMsg(msg webchatUserMsg) (Model, tea.Cmd) {
	text := msg.Text
	// #1860 case 1: attach the image blocks to pendingImages so the
	// next submission carries them (same slot pasted images use). A
	// pure-image message is no longer dropped: the WS layer already
	// acked "delivered, N images" before this point.
	for _, blk := range msg.Images {
		if blk.Type != "image" {
			continue
		}
		att, err := buildWebchatImageAttachment(blk)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("webchat image rejected: %v", err))
			continue
		}
		m.pendingImages = append(m.pendingImages, att)
	}
	if text == "" {
		// Pure image (or all images rejected): nothing to route now.
		// Images that attached stay in pendingImages and ride the next
		// submission; starting a run with empty text would be worse.
		return *m, nil
	}
	// Notify Knight idle timer — webchat counts as user activity too.
	if m.knight != nil {
		m.knight.NotifyActivity()
	}
	// #1882: same predicate as the local/remote gates (#1762) - a
	// #1744 case 1: cancelFunc==nil is NOT idle - knight runs (and any
	// agentless loading state) never set it, so a remote webchat user
	// during a knight run was judged idle and started a CONCURRENT agent
	// (cancelFunc overwritten, event streams interleaved). loading is the
	// real idleness predicate; cancelFunc stays as a belt-and-suspenders
	// guard for the injected-but-not-yet-loading window.
	// #1890: scheduled knight tasks (runMaintenanceTask /
	// stageSkillRevision) run on their OWN agent and never touch
	// m.loading/m.cancelFunc - both gates stay open while they run.
	// knightRunning (counted by the task-event sink) closes that twin
	// path the same way adhoc loading does.
	if m.cancelFunc == nil && !m.loading && !m.projectMemoryLoading &&
		m.knightRunning == 0 {
		// Render the user bubble and persist to session.
		m.chatWriteUser(nextChatID(), text)
		m.chatListScrollToBottom()
		m.appendUserMessage(text)
		m.streamBuffer = &bytes.Buffer{}
		m.shellBuffer = nil
		m.streamPrefixWritten = false
		m.setLoading(true)
		m.loopStart = time.Now()
		m.statusActivity = m.t("status.thinking")
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		cmd := m.startAgent(text)
		return *m, tea.Batch(m.startLoadingSpinner(m.statusActivity), cmd)
	}
	// Agent is busy — queue for submission. The message will be
	// persisted by startNormalTextRun when the pending submission
	// is drained. Calling appendUserMessage here would duplicate
	// the message in the JSONL file.
	// queuePendingSubmission renders the user bubble immediately.
	m.queuePendingSubmission(text)
	return *m, nil
}
