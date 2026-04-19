package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/im"
)

type imPanelState struct {
	selected int
	message  string
}

type imChannelEntry struct {
	Adapter   string
	Platform  im.Platform
	ChannelID string
	Healthy   bool
	Status    string
	LastError string
	Disabled  bool
	Bound     bool // has a ChannelID
}

type imPanelResultMsg struct {
	message string
	err     error
}

func (m *Model) openIMPanel() {
	m.imPanel = &imPanelState{}
}

func (m *Model) closeIMPanel() {
	m.imPanel = nil
}

func (m Model) renderIMPanel() string {
	panel := m.imPanel
	if panel == nil {
		return ""
	}

	entries := m.imChannelEntries()

	// Header
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.im.title")),
		fmt.Sprintf(" %s", m.t("panel.im.directory")),
		fmt.Sprintf("  %s", firstNonEmptyIM(m.currentWorkspacePath(), m.t("panel.im.none"))),
		"",
	}

	// Runtime status
	body = append(body,
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.im.runtime")),
		fmt.Sprintf("  %s", m.imRuntimeStatus()),
		"",
	)

	// Channels summary
	enabledCount := 0
	disabledCount := 0
	for _, e := range entries {
		if e.Disabled {
			disabledCount++
		} else {
			enabledCount++
		}
	}
	body = append(body,
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.im.channels")),
		fmt.Sprintf("  %s", m.t("panel.im.total", len(entries))),
		fmt.Sprintf("  %s", m.t("panel.im.enabled_count", enabledCount)),
		fmt.Sprintf("  %s", m.t("panel.im.disabled_count", disabledCount)),
		"",
	)

	// Channel list
	body = append(body, lipgloss.NewStyle().Bold(true).Render(m.t("panel.im.channel_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf("  %s", m.t("panel.im.no_channels")))
	} else {
		selected := clampIMSelection(panel.selected, len(entries))
		labels := m.imChannelLabels(entries)
		body = append(body, m.renderProviderList(labels, selected, true))

		entry := entries[selected]
		body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.im.details")))

		// Status
		statusLabel := m.t("panel.im.status.enabled")
		if entry.Disabled {
			statusLabel = m.t("panel.im.status.disabled")
		}
		healthLabel := m.t("panel.im.health.healthy")
		if !entry.Healthy {
			healthLabel = m.t("panel.im.health.unhealthy")
		}
		if !entry.Bound {
			healthLabel = m.t("panel.im.health.not_bound")
		}

		body = append(body,
			fmt.Sprintf("  %s", m.t("panel.im.adapter", entry.Adapter)),
			fmt.Sprintf("  %s", m.t("panel.im.platform", platformDisplayName(entry.Platform))),
			fmt.Sprintf("  %s", m.t("panel.im.state", statusLabel)),
			fmt.Sprintf("  %s", m.t("panel.im.connection", healthLabel)),
		)
		if entry.Bound {
			body = append(body,
				fmt.Sprintf("  %s", m.t("panel.im.channel_id", firstNonEmptyIM(entry.ChannelID, m.t("panel.im.none")))),
			)
		}
		if entry.LastError != "" {
			body = append(body, fmt.Sprintf("  %s", m.t("panel.im.last_error", strings.TrimSpace(entry.LastError))))
		}
	}

	// Actions hint
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.im.actions")))
	if len(entries) > 0 {
		selected := clampIMSelection(panel.selected, len(entries))
		entry := entries[selected]
		if entry.Disabled {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  "+m.t("panel.im.hints.disabled")))
		} else {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  "+m.t("panel.im.hints.enabled")))
		}
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  "+m.t("panel.im.hints.empty")))
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}

	return m.renderContextBox("/im", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) handleIMPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.imPanel
	if panel == nil {
		return *m, nil
	}
	entries := m.imChannelEntries()

	switch msg.String() {
	case "up", "k":
		if len(entries) > 0 {
			panel.selected = (panel.selected - 1 + len(entries)) % len(entries)
		}
		panel.message = ""
	case "down", "j", "tab":
		if len(entries) > 0 {
			panel.selected = (panel.selected + 1) % len(entries)
		}
		panel.message = ""
	case "d", "D":
		if len(entries) == 0 {
			panel.message = m.t("panel.im.message.no_channel")
			return *m, nil
		}
		return *m, m.disableIMChannel(entries[clampIMSelection(panel.selected, len(entries))])
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.im.message.no_channel")
			return *m, nil
		}
		return *m, m.enableIMChannel(entries[clampIMSelection(panel.selected, len(entries))])
	case "esc":
		m.closeIMPanel()
	}
	return *m, nil
}

func (m *Model) disableIMChannel(entry imChannelEntry) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return imPanelResultMsg{err: fmt.Errorf("%s", m.t("panel.im.message.no_runtime"))}
		}
		if err := m.imManager.DisableBinding(entry.Adapter); err != nil {
			return imPanelResultMsg{err: err}
		}
		return imPanelResultMsg{message: m.t("panel.im.message.disabled", entry.Adapter)}
	}
}

func (m *Model) enableIMChannel(entry imChannelEntry) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return imPanelResultMsg{err: fmt.Errorf("%s", m.t("panel.im.message.no_runtime"))}
		}
		if err := m.imManager.EnableBinding(entry.Adapter); err != nil {
			return imPanelResultMsg{err: err}
		}
		return imPanelResultMsg{message: m.t("panel.im.message.enabled", entry.Adapter)}
	}
}

func (m Model) imChannelEntries() []imChannelEntry {
	if m.imManager == nil {
		return nil
	}

	snapshot := m.imManager.Snapshot()
	adapterStates := make(map[string]im.AdapterState)
	for _, state := range snapshot.Adapters {
		adapterStates[state.Name] = state
	}

	// Collect all bindings: active + disabled
	type bindingInfo struct {
		binding  im.ChannelBinding
		disabled bool
	}
	allBindings := make(map[string]bindingInfo)

	// Add active bindings for current workspace
	for _, b := range snapshot.CurrentBindings {
		allBindings[b.Adapter] = bindingInfo{binding: b, disabled: false}
	}

	// Add disabled bindings
	for _, b := range snapshot.DisabledBindings {
		if _, exists := allBindings[b.Adapter]; !exists {
			allBindings[b.Adapter] = bindingInfo{binding: b, disabled: true}
		}
	}

	// Sort by adapter name for stable ordering
	keys := make([]string, 0, len(allBindings))
	for k := range allBindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	entries := make([]imChannelEntry, 0, len(keys))
	for _, adapterName := range keys {
		info := allBindings[adapterName]
		state := adapterStates[adapterName]

		entries = append(entries, imChannelEntry{
			Adapter:   adapterName,
			Platform:  info.binding.Platform,
			ChannelID: info.binding.ChannelID,
			Healthy:   state.Healthy,
			Status:    state.Status,
			LastError: state.LastError,
			Disabled:  info.disabled,
			Bound:     strings.TrimSpace(info.binding.ChannelID) != "",
		})
	}

	return entries
}

func (m Model) imChannelLabels(entries []imChannelEntry) []string {
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		status := m.t("panel.im.label.enabled")
		if entry.Disabled {
			status = m.t("panel.im.label.disabled")
		}
		if !entry.Bound && !entry.Disabled {
			status = m.t("panel.im.label.waiting")
		}
		platName := platformDisplayName(entry.Platform)
		label := fmt.Sprintf("%s [%s] · %s", entry.Adapter, platName, status)
		labels = append(labels, label)
	}
	return labels
}

func (m Model) imRuntimeStatus() string {
	if m.imManager != nil {
		return m.t("panel.im.runtime.available")
	}
	if m.config == nil || !m.config.IM.Enabled {
		return m.t("panel.im.runtime.disabled")
	}
	return m.t("panel.im.runtime.not_started")
}

func clampIMSelection(selected, total int) int {
	if total <= 0 {
		return 0
	}
	if selected < 0 {
		return 0
	}
	if selected >= total {
		return total - 1
	}
	return selected
}

func firstNonEmptyIM(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
