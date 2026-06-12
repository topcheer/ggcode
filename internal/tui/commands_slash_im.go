package tui

import tea "charm.land/bubbletea/v2"

// IM-related slash command handlers.
// The core IM slash commands (/listim, /muteim, /muteall, /muteself, /restart)
// are handled by the IM gateway runtime in internal/im/ and the daemon bridge.
// This file collects TUI-side IM panel helpers.

func (m *Model) handleQQCommand() tea.Cmd {
	m.openQQPanel()
	return nil
}
