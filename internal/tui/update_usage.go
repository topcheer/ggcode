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

const usageSidebarRefreshInterval = 60 * time.Second

func (m *Model) ensureUsageService() *usage.Service {
	if m.usageService == nil {
		m.usageService = usage.DefaultService()
	}
	return m.usageService
}

func (m *Model) handleUsageInfoUpdated(msg usageInfoUpdatedMsg) (Model, tea.Cmd) {
	if m.usagePanel != nil {

		if msg.err != nil {
			delete(m.usagePanel.infos, msg.vendor)
			m.usagePanel.errs[msg.vendor] = msg.err.Error()
		} else if msg.info != nil {
			delete(m.usagePanel.errs, msg.vendor)
			m.usagePanel.infos[msg.vendor] = msg.info
		}

		if len(m.usagePanel.infos)+len(m.usagePanel.errs) >= m.usagePanel.expected {
			m.usagePanel.fetching = false
		}
	}

	if msg.err != nil {
		m.sidebarUsage = nil
		m.usageSidebarStatus = "probe failed: " + msg.err.Error()
	} else {
		m.sidebarUsage = msg.info
		m.usageSidebarStatus = ""
	}

	return *m, tea.Tick(usageSidebarRefreshInterval, func(time.Time) tea.Msg {
		return usageSidebarRefreshMsg{}
	})
}

type usageSidebarRefreshMsg struct{}

func (m Model) handleUsageSidebarRefreshMsg() (Model, tea.Cmd) {
	reason := m.explainUsageSidebar()
	if reason != "" {
		m.usageSidebarStatus = reason
		debug.Log("usage", "auto-probe skipped: %s", reason)

		return m, tea.Tick(usageSidebarRefreshInterval, func(time.Time) tea.Msg {
			return usageSidebarRefreshMsg{}
		})
	}
	m.usageSidebarStatus = "probing..."
	debug.Log("usage", "auto-probe firing for active endpoint")
	return m, m.fetchAllUsageCmd()
}

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

func hostOf(raw string) string {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return raw
}
