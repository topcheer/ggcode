package tui

import (
	tea "charm.land/bubbletea/v2"
)

// handleStatusMsg records local-run status fields and keeps the loading
// spinner alive (#2422 slice 1: extracted from Model.Update - body moved
// verbatim, zero behavior change).
func (m *Model) handleStatusMsg(msg statusMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if m.runCanceled || !m.loading {
		return *m, nil
	}
	m.statusActivity = msg.Activity
	m.statusToolName = msg.ToolName
	m.statusToolArg = msg.ToolArg
	if msg.ToolCount > 0 {
		m.statusToolCount = msg.ToolCount
	}
	m.pushTunnelCurrentActivity()
	return *m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
}

// handleAgentStatusMsg is the run-scoped twin of handleStatusMsg: same
// recording, guarded by the active agent run ID (#2422 slice 1: extracted
// from Model.Update - body moved verbatim, zero behavior change).
func (m *Model) handleAgentStatusMsg(msg agentStatusMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
		return *m, nil
	}
	m.statusActivity = msg.Activity
	m.statusToolName = msg.ToolName
	m.statusToolArg = msg.ToolArg
	if msg.ToolCount > 0 {
		m.statusToolCount = msg.ToolCount
	}
	m.pushTunnelCurrentActivity()
	return *m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
}
