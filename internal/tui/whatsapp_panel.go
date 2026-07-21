package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

type whatsappPanelState struct {
	selected    int
	message     string
	editState   imAdapterEditState
	createMode  bool
	createInput string
}

type whatsappBindingEntry struct {
	Adapter          string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Disabled         bool
	Muted            bool
}

type whatsappBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openWhatsAppPanel() tea.Cmd {
	m.whatsappPanel = &whatsappPanelState{}
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return imPanelRefreshMsg{}
	})
}

func (m *Model) closeWhatsAppPanel() {
	m.whatsappPanel = nil
}
func defaultWATargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}

func clampWASelection(selected, total int) int {
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

func maxWA(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func (m Model) renderWhatsAppPanel() string {
	panel := m.whatsappPanel
	if panel == nil {
		return ""
	}

	entries := m.waBindingEntries()
	currentBindings := currentWABindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}

	wsPath := m.currentWorkspacePath()
	if wsPath == "" {
		wsPath = "(none)"
	}

	body := []string{
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.whatsapp.title")),
		" " + m.t("panel.whatsapp.directory", wsPath),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.whatsapp.adapters")),
		" " + m.t("panel.whatsapp.summary", len(entries), boundCount, maxWA(len(entries)-boundCount, 0)),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.whatsapp.current_binding")),
	}

	if len(currentBindings) == 0 {
		body = append(body, " "+m.t("panel.whatsapp.none"))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				" "+m.t("panel.whatsapp.adapter", current.Adapter),
				" "+m.t("panel.whatsapp.target", util.FirstNonEmpty(current.TargetID, m.t("panel.whatsapp.default"))),
				" "+m.t("panel.whatsapp.channel", util.FirstNonEmpty(current.ChannelID, m.t("panel.whatsapp.none"))),
			)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.whatsapp.adapter_list")))
	if len(entries) == 0 {
		body = append(body, " "+m.t("panel.whatsapp.no_adapters"))
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.whatsapp.add_hint")))
	} else {
		selected := clampWASelection(panel.selected, len(entries))
		labels := m.waBindingLabels(entries)
		body = append(body, m.renderProviderList(labels, selected, true))

		entry := entries[selected]
		status := m.t("panel.whatsapp.status.available")
		if entry.Disabled {
			status = m.t("panel.whatsapp.status.disabled")
		} else if entry.OccupiedBy != "" {
			status = m.t("panel.whatsapp.status.bound")
		}

		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.whatsapp.details")),
			" "+m.t("panel.whatsapp.adapter", entry.Adapter),
			" "+m.t("panel.whatsapp.status", status),
			" "+m.t("panel.whatsapp.transport", m.waAdapterStatus(entry.AdapterState)),
			" "+m.t("panel.whatsapp.bound_to", util.FirstNonEmpty(entry.OccupiedBy, m.t("panel.whatsapp.none"))),
			" "+m.t("panel.whatsapp.target", util.FirstNonEmpty(entry.TargetID, defaultWATargetID(m.currentWorkspacePath()))),
			" "+m.t("panel.whatsapp.channel", util.FirstNonEmpty(entry.WorkspaceChannel, m.t("panel.whatsapp.waiting_for_pair"))),
		)

		// Show QR / contact status
		if entry.AdapterState != nil {
			if entry.AdapterState.QRCode != "" {
				body = append(body, "",
					lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.whatsapp.pairing_qr")),
				)
			} else if entry.AdapterState.ContactURI != "" {
				body = append(body, "", " "+m.t("panel.whatsapp.contact", entry.AdapterState.ContactURI))
			}
		}

		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, " "+m.t("panel.whatsapp.last_error", strings.TrimSpace(entry.AdapterState.LastError)))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.whatsapp.occupied_by", entry.OccupiedBy)))
		}
	}

	// Actions hint
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.whatsapp.actions")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.whatsapp.adapter_name", panel.createInput)+"█",
			"",
			renderPasteShortcutHint(m.currentLanguage()),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("panel.whatsapp.enter_confirm")),
		)
	} else if len(entries) == 0 {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("panel.whatsapp.create_new")))
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("panel.whatsapp.actions_hint")))
	}

	// Edit config section
	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+panel.message))
	}

	return m.renderContextBox("/whatsapp", strings.Join(body, "\n"), lipgloss.Color("34"))
}

func (m *Model) waAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.whatsapp.not_started")
	}
	if state.Healthy {
		return m.t("panel.whatsapp.online")
	}
	if state.Status != "" {
		return state.Status
	}
	if state.LastError != "" {
		return state.LastError
	}
	return m.t("panel.whatsapp.unknown")
}

func (m *Model) handleWhatsAppPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.whatsappPanel
	if panel == nil {
		return *m, nil
	}

	// Edit mode takes priority
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.waBindingEntries()

	// Create mode takes priority
	if panel.createMode {
		switch msg.String() {
		case "esc", "ctrl+c":
			panel.createMode = false
			panel.createInput = ""
			return *m, nil
		case "enter":
			name := strings.TrimSpace(panel.createInput)
			panel.createMode = false
			panel.createInput = ""
			if name == "" {
				panel.message = m.t("panel.whatsapp.adapter_required")
				return *m, nil
			}
			return *m, m.createWAAdapterCmd(name)
		case "backspace":
			runes := []rune(panel.createInput)
			if len(runes) > 0 {
				panel.createInput = string(runes[:len(runes)-1])
			}
			return *m, nil
		case "space", " ":
			panel.createInput += " "
			return *m, nil
		}
		if len(msg.Text) > 0 {
			panel.createInput += msg.Text
		}
		return *m, nil
	}

	switch msg.String() {
	case "up", "k":
		if len(entries) > 0 {
			panel.selected = (panel.selected - 1 + len(entries)) % len(entries)
		}
	case "down", "j", "tab":
		if len(entries) > 0 {
			panel.selected = (panel.selected + 1) % len(entries)
		}
	case "enter", "b", "B":
		if len(entries) == 0 {
			panel.message = m.t("panel.whatsapp.no_adapter")
			return *m, nil
		}
		return *m, m.bindWAEntry(entries[clampWASelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.whatsapp.no_adapter")
			return *m, nil
		}
		return *m, m.clearWAChannel(entries[clampWASelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.whatsapp.no_adapter")
			return *m, nil
		}
		return *m, m.unbindWAEntry(entries[clampWASelection(panel.selected, len(entries))].Adapter)
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.whatsapp.no_adapter")
			return *m, nil
		}
		entry := entries[clampWASelection(panel.selected, len(entries))]
		edit := m.enterIMEditSelect(entry.Adapter)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "q", "Q":
		if len(entries) == 0 {
			panel.message = m.t("panel.whatsapp.no_adapter")
			return *m, nil
		}
		entry := entries[clampWASelection(panel.selected, len(entries))]
		if entry.AdapterState == nil {
			panel.message = m.t("panel.whatsapp.adapter_not_start")
			return *m, nil
		}
		// If pairing, show the dynamically generated QR code
		if entry.AdapterState.QRCode != "" {
			m.openQROverlayDirect(
				fmt.Sprintf("%s — %s", m.t("panel.qr.title"), "WhatsApp"),
				m.t("panel.qr.scan_hint"),
				entry.AdapterState.QRCode,
				entry.Adapter,
			)
			return *m, nil
		}
		// If connected, show contact URI QR (wa.me link)
		states := []*im.AdapterState{entry.AdapterState}
		if !m.openQROverlayFromStates("WhatsApp", states) {
			panel.message = m.t("panel.whatsapp.no_qr")
		}
		return *m, nil
	case "d":
		if len(entries) == 0 {
			panel.message = m.t("panel.whatsapp.message.no_bot")
			return *m, nil
		}
		entry := entries[clampWASelection(panel.selected, len(entries))]
		return *m, m.toggleIMAdapterEnabled(entry.Adapter)
	case "esc", "ctrl+c":
		m.closeWhatsAppPanel()
	}
	return *m, nil
}

func (m *Model) bindWAEntry(entry whatsappBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureWARuntime(); err != nil {
			return whatsappBindResultMsg{err: err}
		}
		if err := m.startWAAdapterIfNeeded(entry.Adapter); err != nil {
			return whatsappBindResultMsg{err: err}
		}
		ws := m.currentWorkspacePath()
		if ws == "" {
			return whatsappBindResultMsg{err: errors.New("no workspace")}
		}
		targetID := defaultWATargetID(ws)
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Platform:  im.PlatformWhatsApp,
			Adapter:   entry.Adapter,
			TargetID:  targetID,
			Workspace: ws,
		})
		if err != nil {
			return whatsappBindResultMsg{err: err}
		}
		return whatsappBindResultMsg{message: m.t("panel.whatsapp.bound_success")}
	}
}

func (m *Model) unbindWAEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return whatsappBindResultMsg{}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return whatsappBindResultMsg{err: err}
		}
		return whatsappBindResultMsg{message: m.t("panel.whatsapp.unbound")}
	}
}

func (m *Model) clearWAChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return whatsappBindResultMsg{}
		}
		if err := m.imManager.ClearChannelByAdapter(adapterName); err != nil {
			return whatsappBindResultMsg{err: err}
		}
		return whatsappBindResultMsg{message: m.t("panel.whatsapp.channel_cleared")}
	}
}

func (m *Model) createWAAdapterCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return whatsappBindResultMsg{err: errors.New(m.t("panel.whatsapp.error.config_unavailable"))}
		}
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformWhatsApp),
			Extra:    map[string]interface{}{},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return whatsappBindResultMsg{err: err}
		}
		if err := m.saveConfig(); err != nil {
			return whatsappBindResultMsg{err: fmt.Errorf("save config: %w", err)}
		}
		if err := m.ensureWARuntime(); err != nil {
			return whatsappBindResultMsg{message: m.t("panel.whatsapp.saved_start_pending", name, err)}
		}
		if err := m.startWAAdapterIfNeeded(name); err != nil {
			return whatsappBindResultMsg{message: m.t("panel.whatsapp.saved_start_failed", name, err)}
		}
		return whatsappBindResultMsg{message: m.t("panel.whatsapp.created_scan", name)}
	}
}

func (m *Model) ensureWARuntime() error {
	return m.ensureStartedCurrentWorkspaceIMRuntime(m.t("panel.whatsapp.error.config_unavailable"), "", true)
}

func (m *Model) startWAAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("adapter name required")
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf("adapter %q not configured", name)
	}
	if !adapterCfg.Enabled {
		// Auto-enable when user explicitly tries to bind from panel.
		if err := m.config.SetIMAdapterEnabled(name, true); err != nil {
			return fmt.Errorf("enable %s: %w", name, err)
		}
		if m.imManager != nil {
			_ = m.imManager.EnableBinding(name)
		}
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformWhatsApp)) {
		return fmt.Errorf("adapter %q is not a WhatsApp adapter", name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m Model) waBindingEntries() []whatsappBindingEntry {
	if m.config == nil {
		return nil
	}
	occupied := make(map[string]string)
	adapterStates := make(map[string]im.AdapterState)
	currentWorkspace := strings.TrimSpace(m.currentWorkspacePath())
	bindingByAdapter := make(map[string]im.ChannelBinding)
	if m.imManager != nil {
		snapshot := m.imManager.Snapshot()
		for _, state := range snapshot.Adapters {
			adapterStates[state.Name] = state
		}
		for _, b := range currentWABindings(m.imManager) {
			bindingByAdapter[b.Adapter] = b
		}
		if bindings, err := m.imManager.ListBindings(); err == nil {
			for _, binding := range bindings {
				occupied[binding.Adapter] = binding.Workspace
			}
		}
	}
	keys := make([]string, 0, len(m.config.IM.Adapters))
	for name, adapter := range m.config.IM.Adapters {
		if strings.EqualFold(adapter.Platform, string(im.PlatformWhatsApp)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]whatsappBindingEntry, 0, len(keys))
	for _, name := range keys {
		targetID := defaultWATargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = util.FirstNonEmpty(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		var statePtr *im.AdapterState
		if s, ok := adapterStates[name]; ok && strings.TrimSpace(s.Name) != "" {
			statePtr = &s
		}
		entries = append(entries, whatsappBindingEntry{
			Adapter:          name,
			TargetID:         targetID,
			WorkspaceChannel: workspaceChannel,
			OccupiedBy:       occupied[name],
			AdapterState:     statePtr,
			Disabled:         !m.config.IM.Adapters[name].Enabled,
			Muted:            bindingByAdapter[name].Muted,
		})
	}
	return entries
}

func (m Model) waBindingLabels(entries []whatsappBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Disabled:
			status = "disabled"
		case entry.Muted:
			status = "muted"
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = "active"
		case entry.OccupiedBy != "":
			status = fmt.Sprintf("bound(%s)", filepath.Base(entry.OccupiedBy))
		default:
			status = "available"
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func currentWABindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range mgr.CurrentBindings() {
		if b.Platform == im.PlatformWhatsApp {
			result = append(result, b)
		}
	}
	return result
}
