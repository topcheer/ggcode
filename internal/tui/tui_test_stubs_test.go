package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func (m *Model) pendingSubmissionSnapshot() []string {
	snap := m.pending.snapshot()
	if len(snap) == 0 {
		return nil
	}
	return snap
}

func (m *Model) handleTunnelClientConnectedMsg() (tea.Model, tea.Cmd) {
	return m.handleTunnelClientConnectedMsgForGeneration(m.tunnelGeneration)
}

func (m *Model) resetCurrentSessionTunnelLedger() {
	mu := m.sessionMutex()
	mu.Lock()
	defer mu.Unlock()
	if m.session == nil {
		return
	}
	m.session.TunnelEvents = nil
	m.session.TunnelEventsComplete = false
}

func (m *Model) recordTunnelEvent(ev tunnel.GatewayMessage) {
	mu := m.sessionMutex()
	mu.Lock()
	defer mu.Unlock()
	if m.session == nil {
		return
	}
	m.session.TunnelEvents = append(m.session.TunnelEvents, session.TunnelEvent{
		EventID: ev.EventID,
		Type:    ev.Type,
	})
}
