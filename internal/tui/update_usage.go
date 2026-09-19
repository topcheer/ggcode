package tui

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/usage"
	"github.com/topcheer/ggcode/internal/util"
)

// usageSidebarRefreshInterval bounds the auto-refresh cadence. Probes are
// de-duplicated by the Service cache (success TTL / negative TTL / 429
// Retry-After), so a short cadence costs nothing upstream - and coding-plan
// windows (zai 5h rolling, recovered minute-by-minute) make 60s the first
// interval that actually tracks the meter.
const usageSidebarRefreshInterval = 60 * time.Second

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
	// No attribution layer at all: probes fire ONLY for the session's
	// current endpoint (probeableVendors yields that single target), so any
	// result that arrives IS the current endpoint's data. Render it.
	if msg.err != nil {
		m.sidebarUsage = nil // keep stale data off the sidebar on error
		m.usageSidebarStatus = "probe failed: " + msg.err.Error()
	} else {
		m.sidebarUsage = msg.info
		m.usageSidebarStatus = ""
	}
	// Auto-refresh chain: re-arm the next sidebar probe. The Service cache
	// de-duplicates upstream HTTP (success TTL / negative TTL / 429
	// Retry-After), so the 60s cadence never hammers - cached rounds return
	// instantly and still refresh the sidebar timestamp.
	return *m, tea.Tick(usageSidebarRefreshInterval, func(time.Time) tea.Msg {
		return usageSidebarRefreshMsg{}
	})
}

// usageSidebarRefreshMsg re-probes the ACTIVE endpoint's usage for the
// sidebar + panel without any user action. Root cause this fixes (user
// report 2026-09-18, "trigger logic rewritten a dozen times, never right"):
// fetchAllUsageCmd previously had exactly TWO callers - openUsagePanel and
// the panel's r key. Unless the user manually opened the usage panel, no
// probe ever fired, no usageInfoUpdatedMsg ever arrived, and the sidebar
// stayed empty forever regardless of attribution correctness.
type usageSidebarRefreshMsg struct{}

// handleUsageSidebarRefreshMsg fires the probe immediately and re-arms the
// chain. Every no-probe branch writes a one-line human-readable reason into
// usageSidebarStatus (rendered in the sidebar) - the 2026-09-18 incident
// showed every failure mode rendered the same silent nothing and the chain
// was undiagnosable from the UI.
func (m Model) handleUsageSidebarRefreshMsg() (Model, tea.Cmd) {
	reason := m.explainUsageSidebar()
	if reason != "" {
		m.usageSidebarStatus = reason
		debug.Log("usage", "auto-probe skipped: %s", reason)
		// Keep the chain alive: a skipped round (mid-switch, key removed,
		// ...) must not kill the 60s loop - once the reason is gone the
		// probe resumes on its own. Returning nil here used to end polling
		// until a manual panel open (2026-09-19 switch bug: after a vendor
		// switch the sidebar went permanently dark).
		return m, tea.Tick(usageSidebarRefreshInterval, func(time.Time) tea.Msg {
			return usageSidebarRefreshMsg{}
		})
	}
	m.usageSidebarStatus = "probing..."
	debug.Log("usage", "auto-probe firing for active endpoint")
	return m, m.fetchAllUsageCmd()
}

// explainUsageSidebar re-derives the exact reason no usage probe can run for
// THIS session right now. Empty string = probeable, go ahead.
// explainUsageSidebar re-derives why no usage probe can run for THIS
// session right now. Empty string = probeable, go ahead. Resolution errors
// are passed through verbatim - they are already human-readable
// ("vendor \"x\" is not configured" ...).
func (m *Model) explainUsageSidebar() string {
	if m.config == nil {
		return "no config loaded"
	}
	vendor := util.FirstNonEmpty(m.activeVendor, m.startupVendor)
	if vendor == "" {
		vendor = m.config.Vendor
	}
	epID := m.activeEndpoint
	if epID == "" && vendor == m.config.Vendor {
		epID = m.config.Endpoint
	}
	if vendor == "" || epID == "" {
		return "no active endpoint"
	}
	ep, err := m.config.ResolveEndpointSelection(vendor, epID, m.activeModel)
	if err != nil {
		return err.Error()
	}
	if strings.TrimSpace(ep.APIKey) == "" {
		return fmt.Sprintf("endpoint %s/%s has no api key", vendor, epID)
	}
	if m.ensureUsageService().Resolve(ep.BaseURL) == "" {
		return fmt.Sprintf("no usage probe for %s", hostOf(ep.BaseURL))
	}
	return ""
}

// hostOf extracts scheme://host from a base URL for status lines.
func hostOf(raw string) string {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return raw
}
