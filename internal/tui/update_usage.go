package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/usage"
)

// ensureUsageService lazily builds the shared usage Service (#2150 batch
// 2b: DefaultService is the single registration site for all P1 probes -
// the TUI's old three-probe inline list silently hid the other five).
func (m *Model) ensureUsageService() *usage.Service {
	if m.usageService == nil {
		m.usageService = usage.DefaultService()
	}
	return m.usageService
}

// handleUsageInfoUpdated processes probe results on the Update loop (#2150
// batch 2): both the panel table and the sidebar snapshot read model state
// under the same serialization. Extracted from Model.Update as the first
// slice of the #2357 complexity refactor (zero behavior change).
func (m *Model) handleUsageInfoUpdated(msg usageInfoUpdatedMsg) (Model, tea.Cmd) {
	if m.usagePanel != nil {
		if msg.err != nil {
			m.usagePanel.errs[msg.vendor] = msg.err.Error()
		} else if msg.info != nil {
			m.usagePanel.infos[msg.vendor] = msg.info
		}
		if len(m.usagePanel.infos)+len(m.usagePanel.errs) >= len(m.probeableVendors()) {
			m.usagePanel.fetching = false
		}
	}
	active := m.activeVendor
	if active == "" {
		active = m.startupVendor
	}
	if msg.vendor == active {
		if msg.err != nil {
			m.sidebarUsage = nil // keep stale data off the sidebar on error
		} else {
			m.sidebarUsage = msg.info
		}
	}
	return *m, nil
}
