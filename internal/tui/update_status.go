package tui

import (
	tea "charm.land/bubbletea/v2"
)

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
