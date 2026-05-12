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
		lipgloss.NewStyle().Bold(true).Render("WhatsApp"),
		fmt.Sprintf(" Directory: %s", wsPath),
		"",
		lipgloss.NewStyle().Bold(true).Render("Adapters"),
		fmt.Sprintf(" Configured: %d  Bound: %d  Available: %d", len(entries), boundCount, maxWA(len(entries)-boundCount, 0)),
		"",
		lipgloss.NewStyle().Bold(true).Render("Current Binding"),
	}

	if len(currentBindings) == 0 {
		body = append(body, " (none)")
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" Adapter:  %s", current.Adapter),
				fmt.Sprintf(" Target:   %s", util.FirstNonEmpty(current.TargetID, "(default)")),
				fmt.Sprintf(" Channel:  %s", util.FirstNonEmpty(current.ChannelID, "(none)")),
			)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render("Adapter List"))
	if len(entries) == 0 {
		body = append(body, " No WhatsApp adapters configured.")
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Add one in ggcode.yaml: im.adapters.<name>.platform = whatsapp"))
	} else {
		selected := clampWASelection(panel.selected, len(entries))
		labels := m.waBindingLabels(entries)
		body = append(body, m.renderProviderList(labels, selected, true))

		entry := entries[selected]
		status := "available"
		if entry.Disabled {
			status = "disabled"
		} else if entry.OccupiedBy != "" {
			status = "bound"
		}

		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render("Details"),
			fmt.Sprintf(" Adapter:   %s", entry.Adapter),
			fmt.Sprintf(" Status:    %s", status),
			fmt.Sprintf(" Transport: %s", m.waAdapterStatus(entry.AdapterState)),
			fmt.Sprintf(" Bound to:  %s", util.FirstNonEmpty(entry.OccupiedBy, "(none)")),
			fmt.Sprintf(" Target:    %s", util.FirstNonEmpty(entry.TargetID, defaultWATargetID(m.currentWorkspacePath()))),
			fmt.Sprintf(" Channel:   %s", util.FirstNonEmpty(entry.WorkspaceChannel, "(waiting for pairing)")),
		)

		// Show QR / contact status
		if entry.AdapterState != nil {
			if entry.AdapterState.QRCode != "" {
				body = append(body, "",
					lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" Status: pairing — press q to show QR code"),
				)
			} else if entry.AdapterState.ContactURI != "" {
				body = append(body, "", fmt.Sprintf(" Contact: %s", entry.AdapterState.ContactURI))
			}
		}

		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" Error: %s", strings.TrimSpace(entry.AdapterState.LastError)))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(fmt.Sprintf(" Occupied by: %s", entry.OccupiedBy)))
		}
	}

	// Actions hint
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render("Actions"))
	if panel.createMode {
		body = append(body,
			" Adapter name: "+panel.createInput+"█",
			"",
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" enter confirm · esc cancel"),
		)
	} else if len(entries) == 0 {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" i create new adapter · esc close"))
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" ↑↓ navigate · enter bind · u unbind · x clear · e edit · i new · q QR · esc close"))
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
		return "not started"
	}
	if state.Healthy {
		return "online"
	}
	if state.Status != "" {
		return state.Status
	}
	if state.LastError != "" {
		return state.LastError
	}
	return "unknown"
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
				panel.message = "Adapter name required"
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
			panel.message = "No adapter available"
			return *m, nil
		}
		return *m, m.bindWAEntry(entries[clampWASelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = "No adapter available"
			return *m, nil
		}
		return *m, m.clearWAChannel(entries[clampWASelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = "No adapter available"
			return *m, nil
		}
		return *m, m.unbindWAEntry(entries[clampWASelection(panel.selected, len(entries))].Adapter)
	case "e", "E":
		if len(entries) == 0 {
			panel.message = "No adapter available"
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
			panel.message = "No adapter available"
			return *m, nil
		}
		entry := entries[clampWASelection(panel.selected, len(entries))]
		if entry.AdapterState == nil {
			panel.message = "Adapter not started yet"
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
			panel.message = "No QR code or contact available yet"
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
		return whatsappBindResultMsg{message: "Bound successfully"}
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
		return whatsappBindResultMsg{message: "Unbound"}
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
		return whatsappBindResultMsg{message: "Channel cleared"}
	}
}

func (m *Model) createWAAdapterCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return whatsappBindResultMsg{err: errors.New("config unavailable")}
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
			return whatsappBindResultMsg{message: fmt.Sprintf("Adapter %q saved (start pending: %v)", name, err)}
		}
		if err := m.startWAAdapterIfNeeded(name); err != nil {
			return whatsappBindResultMsg{message: fmt.Sprintf("Adapter %q saved (start failed: %v)", name, err)}
		}
		return whatsappBindResultMsg{message: fmt.Sprintf("Adapter %q created — scan QR code to pair", name)}
	}
}

func (m *Model) ensureWARuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New("config unavailable")
	}
	if !m.config.IM.Enabled {
		m.config.IM.Enabled = true
		if err := m.saveConfig(); err != nil {
			return fmt.Errorf("enable IM runtime: %w", err)
		}
	}
	bindingsPath, err := im.DefaultBindingsPath()
	if err != nil {
		return fmt.Errorf("resolving IM bindings path: %w", err)
	}
	bindingStore, err := im.NewJSONFileBindingStore(bindingsPath)
	if err != nil {
		return fmt.Errorf("creating IM binding store: %w", err)
	}
	imMgr := im.NewManager()
	if err := imMgr.SetBindingStore(bindingStore); err != nil {
		return fmt.Errorf("loading IM bindings: %w", err)
	}
	pairingPath, err := im.DefaultPairingStatePath()
	if err != nil {
		return fmt.Errorf("resolving IM pairing state path: %w", err)
	}
	pairingStore, err := im.NewJSONFilePairingStore(pairingPath)
	if err != nil {
		return fmt.Errorf("creating IM pairing store: %w", err)
	}
	if err := imMgr.SetPairingStore(pairingStore); err != nil {
		return fmt.Errorf("loading IM pairing state: %w", err)
	}
	imMgr.BindSession(im.SessionBinding{Workspace: m.currentWorkspacePath()})
	if m.config != nil {
		adapters := make(map[string]bool)
		for n, acfg := range m.config.IM.Adapters {
			adapters[n] = acfg.Enabled
		}
		imMgr.ApplyAdapterConfig(adapters)
	}
	if _, err := im.StartCurrentBindingAdapter(context.Background(), m.config.IM, imMgr); err != nil {
		return fmt.Errorf("starting current workspace IM adapter: %w", err)
	}
	imMgr.SetBridge(newTUIIMBridge(func() *tea.Program { return m.program }))
	m.SetIMManager(imMgr)
	return nil
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
