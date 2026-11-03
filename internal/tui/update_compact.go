package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m Model) handleCompactResultMsg(msg compactResultMsg) (Model, tea.Cmd) {
	if msg.err != "" {
		m.chatWriteSystem(nextSystemID(), msg.err)
	} else {
		m.chatWriteSystem(nextSystemID(), msg.text)
	}
	m.setLoading(false)
	m.spinner.Stop()
	m.statusActivity = ""
	m.chatListFollowOutput()

	if !m.loading && m.pendingSubmissionCount() > 0 {
		return m, m.submitPendingSubmissionCmd()
	}
	return m, nil

}
