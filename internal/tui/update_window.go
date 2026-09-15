package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// handleWindowSizeMsg resizes layouts and re-anchors the startup gate clock
// (#2422 slice 1: extracted from Model.Update - body moved verbatim, zero
// behavior change).
func (m *Model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (Model, tea.Cmd) {
	m.handleResize(msg.Width, msg.Height)
	// Reset the startup clock when Bubble Tea sends the first WindowSizeMsg.
	// This ensures the startup input gate window is measured from the moment
	// the TUI event loop actually starts, not from model creation time (which
	// can be hundreds of milliseconds earlier due to config loading, IM setup, etc.).
	if m.startedAt.IsZero() || time.Since(m.startedAt) > startupInputGateWindow {
		m.startedAt = time.Now()
	}
	return *m, nil
}
