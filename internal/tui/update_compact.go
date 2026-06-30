package tui

import (
	tea "charm.land/bubbletea/v2"
)

// handleCompactResultMsg handles the corresponding message case.
func (m Model) handleCompactResultMsg(msg compactResultMsg) (Model, tea.Cmd) {
	if msg.err != "" {
		m.chatWriteSystem(nextSystemID(), msg.err)
	} else {
		m.chatWriteSystem(nextSystemID(), msg.text)
	}
	m.setLoading(false)
	m.spinner.Stop()
	m.statusActivity = ""
	m.chatListScrollToBottom()
	return m, nil

}
