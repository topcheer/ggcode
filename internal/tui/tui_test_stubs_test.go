package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// This file holds thin compatibility shims referenced by the committed tui test
// suite. The tunnel/pending APIs were refactored (methods renamed/removed
// during the TunnelHost and dead-code-removal passes), but several committed
// coverage tests still call the original names. These delegating wrappers
// preserve test coverage against the current API without touching production
// call sites.

// pendingSubmissionSnapshot returns a copy of the current pending submission
// messages, or nil when empty.
func (m *Model) pendingSubmissionSnapshot() []string {
	snap := m.pending.snapshot()
	if len(snap) == 0 {
		return nil
	}
	return snap
}

// handleTunnelClientConnectedMsg handles a mobile-tunnel client connection for
// the current tunnel generation. It delegates to the generation-scoped handler.
func (m *Model) handleTunnelClientConnectedMsg() (tea.Model, tea.Cmd) {
	return m.handleTunnelClientConnectedMsgForGeneration(m.tunnelGeneration)
}

// resetCurrentSessionTunnelLedger clears the in-memory tunnel event ledger for
// the current session so a fresh canonical replay is required.
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

// recordTunnelEvent appends a gateway event to the current session's tunnel
// event ledger (used as a broker event recorder in tests).
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
