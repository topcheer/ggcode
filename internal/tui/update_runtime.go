package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m Model) handleProviderChangedMsg(msg providerChangedMsg) (tea.Model, tea.Cmd) {

	if m.config != nil {
		if resolved, err := m.config.ResolveActiveEndpoint(); err == nil && resolved != nil {

			m.setActiveRuntimeSelection(resolved.VendorID, resolved.EndpointID, resolved.Model)
		}
		m.syncSessionSelection()
	}
	return m, nil
}
