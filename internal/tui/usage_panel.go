package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/usage"
	"github.com/topcheer/ggcode/internal/util"
)

type usagePanelState struct {
	infos    map[string]*usage.UsageInfo
	errs     map[string]string
	fetching bool

	expected int
}

type usageInfoUpdatedMsg struct {
	vendor string
	info   *usage.UsageInfo
	err    error
}

func (m *Model) probeableVendors() []string {
	baseURL, apiKey, ok := m.currentEndpointForUsage()
	if !ok {
		return nil
	}
	vendor := m.ensureUsageService().Resolve(baseURL)
	if vendor == "" || strings.TrimSpace(apiKey) == "" {
		return nil
	}
	return []string{vendor}
}

func (m *Model) currentEndpointForUsage() (baseURL, apiKey string, ok bool) {
	if m.config == nil {
		return "", "", false
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
		return "", "", false
	}

	ep, err := m.config.ResolveEndpointSelection(vendor, epID, m.activeModel)
	if err != nil || ep == nil {
		return "", "", false
	}
	return ep.BaseURL, ep.APIKey, true
}

func (m *Model) resolveVendorEndpoint(vendor string) (baseURL, apiKey string) {
	b, k, ok := m.currentEndpointForUsage()
	if !ok {
		return "", ""
	}
	_ = vendor
	return b, k
}

func (m *Model) openUsagePanel() tea.Cmd {
	m.usagePanel = &usagePanelState{
		infos:    map[string]*usage.UsageInfo{},
		errs:     map[string]string{},
		fetching: true,
	}
	return m.fetchAllUsageCmd()
}

func (m *Model) refreshUsagePanel() tea.Cmd {
	if m.usagePanel == nil {
		return nil
	}

	m.usagePanel.infos = map[string]*usage.UsageInfo{}
	m.usagePanel.errs = map[string]string{}

	m.ensureUsageService().RefreshInvalidate()
	m.usagePanel.fetching = true
	return m.fetchAllUsageCmd()
}

func (m *Model) fetchAllUsageCmd() tea.Cmd {
	vendors := m.probeableVendors()
	if len(vendors) == 0 {
		return func() tea.Msg { return nil }
	}
	svc := m.ensureUsageService()

	type vendorTarget struct {
		vendor  string
		baseURL string
		apiKey  string
	}
	targets := make([]vendorTarget, 0, len(vendors))
	for _, v := range vendors {
		baseURL, apiKey := m.resolveVendorEndpoint(v)
		if apiKey == "" {
			continue
		}
		targets = append(targets, vendorTarget{vendor: v, baseURL: baseURL, apiKey: apiKey})
	}

	if m.usagePanel != nil {
		m.usagePanel.expected = len(targets)
	}
	if len(targets) == 0 {
		return func() tea.Msg { return nil }
	}
	return func() tea.Msg {

		var msgs []usageInfoUpdatedMsg = make([]usageInfoUpdatedMsg, 0, len(targets))
		for _, t := range targets {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			info, err := svc.Get(ctx, t.vendor, t.baseURL, t.apiKey)
			cancel()
			if err != nil {
				debug.Log("usage", "probe vendor=%s failed: %v", t.vendor, err)
			}
			msgs = append(msgs, usageInfoUpdatedMsg{vendor: t.vendor, info: info, err: err})
		}

		if len(msgs) == 0 {
			return nil
		}
		for _, extra := range msgs[1:] {
			if program := m.program; program != nil {
				program.Send(extra)
			}
		}
		return msgs[0]
	}
}

func (m *Model) handleUsagePanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.usagePanel
	if panel == nil {
		return *m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.usagePanel = nil
	case "r", "R":
		return *m, m.refreshUsagePanel()
	}
	return *m, nil
}

func usagePercentColorCode(p float64) string {
	switch {
	case p >= 95:
		return "9"
	case p >= 80:
		return "11"
	default:
		return "10"
	}
}

func (m Model) renderUsagePanel() string {
	panel := m.usagePanel
	if panel == nil {
		return ""
	}
	body := []string{}
	vendors := m.probeableVendors()
	if len(vendors) == 0 {
		body = append(body, " "+m.t("usage.no_probes"))
	} else {
		active := util.FirstNonEmpty(m.activeVendor, m.startupVendor)
		for _, v := range vendors {
			info, ok := panel.infos[v]
			if !ok {
				if panel.fetching {
					body = append(body, fmt.Sprintf(" %-12s %s", v, m.t("usage.refreshing")))
				} else if errText := panel.errs[v]; errText != "" {
					body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).
						Render(fmt.Sprintf(" %-12s %s", v, util.Truncate(errText, 48))))
				} else {
					body = append(body, fmt.Sprintf(" %-12s %s", v, "-"))
				}
				continue
			}
			label := v
			if v == active {
				label = "*" + v
			}
			if info.Balance != nil {
				body = append(body, fmt.Sprintf(" %-13s %s: $%.2f", label, m.t("label.balance"), *info.Balance))
			} else {
				body = append(body, fmt.Sprintf(" %-13s %s: -", label, m.t("label.balance")))
			}
			for _, w := range info.Windows {
				pct := w.UsedPercent
				if pct < 0 {
					pct = 0
				}
				if pct > 100 {
					pct = 100
				}
				bar := renderUsageBar(pct, 20)
				reset := ""
				if !w.ResetsAt.IsZero() {
					reset = "  " + m.t("label.resets") + " " + formatDuration(time.Until(w.ResetsAt))
				}
				body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color(usagePercentColorCode(pct))).
					Render(fmt.Sprintf("       %-7s %s %3.0f%%%s", w.Label, bar, pct, reset)))
			}
		}
	}
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" r refresh • Esc close")
	body = append(body, "", hint)
	return m.renderContextBox(m.t("panel.usage"), strings.Join(body, "\n"), lipgloss.Color("14"))
}

func renderUsageBar(pct float64, width int) string {
	if width < 4 {
		width = 4
	}
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

func (m Model) renderSidebarVendorUsageSection() string {
	if m.sidebarUsage != nil {
		info := m.sidebarUsage
		width := max(12, m.sidebarWidth()-4)
		rows := []string{m.renderSidebarSectionTitle(m.t("panel.usage"))}
		if info.Balance != nil {
			rows = append(rows, m.renderSidebarDetailRowWithLabelWidth(m.t("label.balance"), fmt.Sprintf("$%.2f", *info.Balance), width, 11))
		}
		for _, w := range info.Windows {
			pct := w.UsedPercent
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			value := lipgloss.NewStyle().Foreground(lipgloss.Color(usagePercentColorCode(pct))).Render(fmt.Sprintf("%.0f%%", pct))
			rows = append(rows, m.renderSidebarDetailRowWithLabelWidth(w.Label, value, width, 11))
		}
		return strings.Join(rows, "\n")
	}
	if m.usageSidebarStatus == "" {
		return ""
	}
	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle(m.t("panel.usage"))}
	rows = append(rows, util.Truncate(m.usageSidebarStatus, width))
	return strings.Join(rows, "\n")
}
