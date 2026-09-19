package tui

// #2422 knife B (runtime/provider): extracted verbatim from Model.Update's
// inline case bodies - zero behavior change.
//
// The armRestart/restartFallback/remoteRestart cases live in
// update_restart.go (#2423 slice 1, merged to main first): those
// pointer-receiver versions won the race, so this file keeps only the
// cases unique to #2422.

import (
	tea "charm.land/bubbletea/v2"
)

func (m Model) handleProviderChangedMsg(msg providerChangedMsg) (tea.Model, tea.Cmd) {
	// Config tool changed provider — refresh model state from config.
	if m.config != nil {
		if resolved, err := m.config.ResolveActiveEndpoint(); err == nil && resolved != nil {
			// VendorID/EndpointID, not the display-name fields (see the
			// 2026-09-19 note in commands_slash_admin.go).
			m.setActiveRuntimeSelection(resolved.VendorID, resolved.EndpointID, resolved.Model)
		}
		m.syncSessionSelection()
	}
	return m, nil
}

// removed: handleArmRestartMsg / handleRestartFallbackMsg / handleRemoteRestartMsg
// live in update_restart.go (#2423 slice 1, merged to main first).
