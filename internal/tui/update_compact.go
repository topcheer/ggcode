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
	m.chatListFollowOutput()
	// Drain pending submissions (same contract as handleAgentDoneMsg).
	// /compact sets loading=true; a user message submitted during the
	// compaction was queued (and rendered as a sent bubble) but no agent run
	// happened, so no agentDoneMsg will ever arrive. Without this drain the
	// message sat in the queue indefinitely — displayed as sent, never
	// processed.
	if !m.loading && m.pendingSubmissionCount() > 0 {
		return m, m.submitPendingSubmissionCmd()
	}
	return m, nil

}
