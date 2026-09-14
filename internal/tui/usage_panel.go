package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/usage"
	"github.com/topcheer/ggcode/internal/util"
)

// #2150 batch 2: TUI surface for the usage/balance probe layer (#2352).
// One shared Service instance backs both the /usage panel (full table for
// every probe-able vendor) and the sidebar section (current vendor only).
// The Service's own 3-minute cache makes refresh cheap; the UI never
// renders a vendor it has no data for.

type usagePanelState struct {
	infos    map[string]*usage.UsageInfo
	errs     map[string]string
	fetching bool
}

type usageInfoUpdatedMsg struct {
	vendor string
	info   *usage.UsageInfo
	err    error
}

// probeableVendors lists vendors that have both a registered probe and a
// configured API key. Sorted for deterministic panel rows.
func (m *Model) probeableVendors() []string {
	svc := m.ensureUsageService()
	if m.config == nil {
		return nil
	}
	var out []string
	for vendor, vc := range m.config.Vendors {
		if !svc.Has(vendor) {
			continue
		}
		hasKey := false
		for _, ep := range vc.Endpoints {
			if strings.TrimSpace(ep.APIKey) != "" {
				hasKey = true
				break
			}
		}
		if hasKey {
			out = append(out, vendor)
		}
	}
	sort.Strings(out)
	return out
}

// resolveVendorEndpoint returns the baseURL+apiKey the probe should use:
// the active endpoint when it belongs to the vendor, else the first
// endpoint carrying a key.
func (m *Model) resolveVendorEndpoint(vendor string) (baseURL, apiKey string) {
	if m.config == nil {
		return "", ""
	}
	vc, ok := m.config.Vendors[vendor]
	if !ok {
		return "", ""
	}
	if m.config.Vendor == vendor {
		if ep, ok := vc.Endpoints[m.config.Endpoint]; ok && strings.TrimSpace(ep.APIKey) != "" {
			return ep.BaseURL, ep.APIKey
		}
	}
	for _, ep := range vc.Endpoints {
		if strings.TrimSpace(ep.APIKey) != "" {
			return ep.BaseURL, ep.APIKey
		}
	}
	return "", ""
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
	// #2365-①: clear both maps before refetching. The old code kept them,
	// so (a) a vendor whose round-2 result flipped to an error kept showing
	// its round-1 balance forever (render prefers infos, the fresh error
	// was permanently masked - stale balance presented as current), and
	// (b) a vendor present in BOTH maps inflated the fetch-completion
	// count, flipping fetching=false before the tail vendors answered.
	m.usagePanel.infos = map[string]*usage.UsageInfo{}
	m.usagePanel.errs = map[string]string{}
	// Bypass the cache: "r" is an explicit user request. Re-registering is
	// idempotent; a fresh Service per refresh is the simplest correct
	// invalidation for a rare, user-triggered action.
	m.usageService = nil
	m.usagePanel.fetching = true
	return m.fetchAllUsageCmd()
}

func (m *Model) fetchAllUsageCmd() tea.Cmd {
	vendors := m.probeableVendors()
	if len(vendors) == 0 {
		return func() tea.Msg { return nil }
	}
	svc := m.ensureUsageService()
	return func() tea.Msg {
		// Sequential per vendor is fine: 2-3 vendors max, each bounded by
		// the service's 10s HTTP timeout, and the panel renders
		// incrementally as messages arrive.
		var msgs []usageInfoUpdatedMsg
		for _, v := range vendors {
			baseURL, apiKey := m.resolveVendorEndpoint(v)
			if apiKey == "" {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			info, err := svc.Get(ctx, v, baseURL, apiKey)
			cancel()
			if err != nil {
				debug.Log("usage", "probe vendor=%s failed: %v", v, err)
			}
			msgs = append(msgs, usageInfoUpdatedMsg{vendor: v, info: info, err: err})
		}
		// bubbletea delivers one msg per Cmd; fan out via a batch.
		cmds := make([]tea.Cmd, 0, len(msgs))
		for _, msg := range msgs {
			cmds = append(cmds, func() tea.Msg { return msg })
		}
		return tea.Batch(cmds...)
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

// usagePercentColor applies the acceptance anchors: >=95 red, >=80 yellow,
// else green.
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

// renderUsageBar draws a fixed-width ASCII progress bar.
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

// renderSidebarVendorUsageSection renders the current vendor's balance /
// quota window in the sidebar. Anchor: render NOTHING until data arrived
// (no placeholder, no spinner) - a section without data is noise.
func (m Model) renderSidebarVendorUsageSection() string {
	if m.sidebarUsage == nil {
		return ""
	}
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
