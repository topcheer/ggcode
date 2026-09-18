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
		// #2365-①b: infos/errs are mutually exclusive per vendor - a
		// flipped result (ok→err or err→ok) must evict its opposite entry,
		// or the completion count double-counts and the render prefers a
		// stale info over a fresh error.
		if msg.err != nil {
			delete(m.usagePanel.infos, msg.vendor)
			m.usagePanel.errs[msg.vendor] = msg.err.Error()
		} else if msg.info != nil {
			delete(m.usagePanel.errs, msg.vendor)
			m.usagePanel.infos[msg.vendor] = msg.info
		}
		// #2371: denominator is the pinned snapshot size, not a live
		// probeableVendors() recount.
		if len(m.usagePanel.infos)+len(m.usagePanel.errs) >= m.usagePanel.expected {
			m.usagePanel.fetching = false
		}
	}
	// Sidebar attribution must compare in the RESOLVED probe-id domain,
	// not the config vendor-name domain: msg.vendor comes from
	// svc.Resolve(baseURL) ("openrouter", "zai", "kimi"...), while
	// activeVendor is whatever the user NAMED the vendor in ggcode.yaml
	// ("ai-gateway", ...). Name-mismatched configs never matched and the
	// sidebar silently never updated (2026-09-18 user report).
	active := ""
	if baseURL, _, ok := m.currentEndpointForUsage(); ok {
		active = m.ensureUsageService().Resolve(baseURL)
	}
	if active == "" {
		active = m.activeVendor
		if active == "" {
			active = m.startupVendor
		}
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
