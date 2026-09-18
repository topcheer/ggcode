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

// #2150 batch 2: TUI surface for the usage/balance probe layer (#2352).
// One shared Service instance backs both the /usage panel (full table for
// every probe-able vendor) and the sidebar section (current vendor only).
// The Service's own 3-minute cache makes refresh cheap; the UI never
// renders a vendor it has no data for.

type usagePanelState struct {
	infos    map[string]*usage.UsageInfo
	errs     map[string]string
	fetching bool
	// expected is the completion denominator pinned by fetchAllUsageCmd
	// to its snapshot size (#2371): numerator (infos+errs) and denominator
	// share one source, so config changes mid-fetch cannot wedge the
	// spinner (added vendor never msgs) or double-count it away.
	expected int
}

type usageInfoUpdatedMsg struct {
	vendor string // probe id (panel table key)
	info   *usage.UsageInfo
	err    error
}

// probeableVendors lists vendors that have both a registered probe and a
// configured API key. Sorted for deterministic panel rows.
// probeableVendors returns the single vendor whose balance the usage
// panel probes. Owner ruling (2026-09-15, second cut): adapter selection
// is BY THE CURRENT ENDPOINT'S URL - never by config vendor name - and
// only the endpoint the session is actually using is ever probed (other
// endpoints' usage is meaningless for this session). A custom vendor
// pointed at open.bigmodel.cn gets the zai probe; a zhipu-NAMED vendor
// pointed anywhere else gets nothing.
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

// currentEndpointForUsage resolves the endpoint THIS session is using:
// runtime activeVendor+activeEndpoint first, config Vendor+Endpoint as
// fallback (daemon/IM attached sessions resolve through config). It
// deliberately never scans sibling endpoints.
// currentEndpointForUsage resolves THE endpoint this session is chatting
// through, via the SAME runtime resolver the chat provider uses
// (ResolveEndpointSelection: vendor-key fallback + ${VAR} expansion
// applied). No parallel hand-rolled config walking - the probe must see
// exactly what the chat sees.
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
	ep, err := m.config.ResolveEndpointSelection(vendor, epID, "")
	if err != nil || ep == nil {
		return "", "", false
	}
	return ep.BaseURL, ep.APIKey, true
}

// resolveVendorEndpoint returns the baseURL+apiKey the probe should use:
// the active endpoint when it belongs to the vendor, else the first
// endpoint carrying a key.
// resolveVendorEndpoint returns the baseURL+apiKey of the endpoint THIS
// session uses for the given (URL-resolved) vendor. Owner ruling: never
// fall through to sibling endpoints - if the current endpoint is not
// usable there is nothing to probe.
func (m *Model) resolveVendorEndpoint(vendor string) (baseURL, apiKey string) {
	b, k, ok := m.currentEndpointForUsage()
	if !ok {
		return "", ""
	}
	_ = vendor // URL-resolved id; the endpoint is the single source
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
	// #2365-①: clear both maps before refetching. The old code kept them,
	// so (a) a vendor whose round-2 result flipped to an error kept showing
	// its round-1 balance forever (render prefers infos, the fresh error
	// was permanently masked - stale balance presented as current), and
	// (b) a vendor present in BOTH maps inflated the fetch-completion
	// count, flipping fetching=false before the tail vendors answered.
	m.usagePanel.infos = map[string]*usage.UsageInfo{}
	m.usagePanel.errs = map[string]string{}
	// #2366-①: refresh via RefreshInvalidate, NOT dropping the Service
	// instance. The old `m.usageService = nil` rebuilt an empty Service, so
	// singleflight AND every negative entry - including Retry-After-sized
	// 429 back-offs - were discarded: pressing r repeatedly hammered ALL
	// probeable vendors with no back-off. RefreshInvalidate keeps negative
	// pacing state and only re-probes vendors with a cached success.
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
	// #2371: snapshot {vendor, baseURL, apiKey} triples HERE, on the
	// Update goroutine, before the closure - bubbletea runs Cmds on their
	// own goroutine, and the old closure called m.resolveVendorEndpoint
	// (unlocked reads of m.config maps) racing SetConfig's bare writes
	// from /model, /provider hot-switches. The closure below touches
	// nothing on m. Vendors without a key drop out of the snapshot, so
	// the expected count matches exactly what will produce messages.
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
	// #2371-②: pin the completion denominator to the snapshot size.
	// handleUsageInfoUpdated used to recompute probeableVendors() as the
	// denominator - a vendor added mid-flight never sends a msg, so the
	// count could never reach it and the spinner stuck on "refreshing"
	// until a manual r/Esc. Numerator and denominator now share one
	// source (the snapshot); config changes during a fetch neither wedge
	// nor double-count.
	if m.usagePanel != nil {
		m.usagePanel.expected = len(targets)
	}
	if len(targets) == 0 {
		return func() tea.Msg { return nil }
	}
	return func() tea.Msg {
		// Sequential per vendor is fine: 2-3 vendors max, each bounded by
		// the service's 10s HTTP timeout, and the panel renders
		// incrementally as messages arrive.
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
// quota window in the sidebar. With no probe result yet (or ever), the
// section shows WHY in one line: the resolved endpoint URL and the exact
// reason no usage is displayed - the "silent empty sidebar" of 2026-09-18
// was undiagnosable from the UI because every failure mode rendered the
// same nothing.
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
