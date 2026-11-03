package tui

import (
	"bytes"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m *Model) handleWebchatUserMsg(msg webchatUserMsg) (Model, tea.Cmd) {
	text := msg.Text

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

		return *m, nil
	}

	if m.knight != nil {
		m.knight.NotifyActivity()
	}

	if m.cancelFunc == nil && !m.loading && !m.projectMemoryLoading &&
		m.knightRunning == 0 {

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

		m.agent.InhibitNextRefusalLedgerWrite()
		cmd := m.startAgent(text)
		return *m, tea.Batch(m.startLoadingSpinner(m.statusActivity), cmd)
	}

	m.queuePendingSubmissionRemote(text)
	return *m, nil
}
