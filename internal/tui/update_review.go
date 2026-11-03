package tui

import (
	"bytes"
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m Model) handleReviewReadyMsg(msg reviewReadyMsg) (tea.Model, tea.Cmd) {

	if m.loading {
		m.queuePendingSubmission("/review")
		return m, nil
	}

	m.chatWriteUser(nextChatID(), "/review")
	m.appendUserMessage("/review")
	m.streamBuffer = &bytes.Buffer{}
	m.streamPrefixWritten = false
	m.setLoading(true)
	m.loopStart = time.Now()
	m.statusActivity = m.t("status.thinking")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	return m, m.startAgent(msg.text)
}

func (m Model) handleCommitReadyMsg(msg commitReadyMsg) (tea.Model, tea.Cmd) {

	if m.loading {
		m.queuePendingSubmission("/commit")
		return m, nil
	}

	m.chatWriteUser(nextChatID(), "/commit")
	m.appendUserMessage("/commit")
	m.streamBuffer = &bytes.Buffer{}
	m.streamPrefixWritten = false
	m.setLoading(true)
	m.loopStart = time.Now()
	m.statusActivity = m.t("status.thinking")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	return m, m.startAgent(msg.text)
}
