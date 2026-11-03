package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m *Model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (Model, tea.Cmd) {
	m.handleResize(msg.Width, msg.Height)

	if m.startedAt.IsZero() || time.Since(m.startedAt) > startupInputGateWindow {
		m.startedAt = time.Now()
	}
	return *m, nil
}
