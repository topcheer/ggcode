package tui

import tea "charm.land/bubbletea/v2"

func (m *Model) handleQQCommand() tea.Cmd {
	m.openQQPanel()
	return nil
}
