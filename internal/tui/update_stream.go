package tui

import (
	tea "charm.land/bubbletea/v2"
)

// handleStreamMsg handles the corresponding message case.
func (m Model) handleStreamMsg(msg streamMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if m.runCanceled {
		return m, nil
	}
	m.appendStreamChunk(string(msg))
	return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

}

// handleAgentStreamMsg handles the corresponding message case.
func (m Model) handleAgentStreamMsg(msg agentStreamMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
		return m, nil
	}
	m.appendStreamChunk(msg.Text)
	return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
}

// handleAgentReasoningMsg handles accumulated reasoning/thinking chunks.
func (m Model) handleAgentReasoningMsg(msg agentReasoningMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
		return m, nil
	}
	m.appendReasoningChunk(msg.Text)
	return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
}
