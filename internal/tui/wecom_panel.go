package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

type wecomPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
	editState   imAdapterEditState
}

type wecomBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Muted            bool
}

type wecomBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openWeComPanel() {
	m.wecomPanel = &wecomPanelState{}
}

func (m *Model) closeWeComPanel() {
	m.wecomPanel = nil
}

func firstNonEmptyWeCom(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxWeCom(v, min int) int {
	if v > min {
		return v
	}
	return min
}

func clampWeComSelection(selected, total int) int {
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

func defaultWeComTargetID(workspace string) string {
	if workspace == "" {
		return ""
	}
	return filepath.Base(workspace)
}

func (m Model) renderWeComPanel() string {
	panel := m.wecomPanel
	if panel == nil {
		return ""
	}

	entries := m.wecomBindingEntries()
	currentBindings := currentWeComBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}

	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.wecom.directory")),
		fmt.Sprintf(" %s", firstNonEmptyWeCom(m.currentWorkspacePath(), m.t("panel.wecom.none"))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.wecom.bots")),
		fmt.Sprintf(" %s", m.t("panel.wecom.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.wecom.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.wecom.available", maxWeCom(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.wecom.current_binding")),
	}

	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.wecom.none")))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" %s", m.t("panel.wecom.adapter", current.Adapter)),
				fmt.Sprintf(" %s", m.t("panel.wecom.target", firstNonEmptyWeCom(current.TargetID, m.t("panel.wecom.default")))),
				fmt.Sprintf(" %s", m.t("panel.wecom.channel", firstNonEmptyWeCom(current.ChannelID, m.t("panel.wecom.none")))),
			)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.wecom.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.wecom.no_bots")))
	} else {
		selected := clampWeComSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.wecomBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.wecom.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.wecom.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.wecom.details")),
			fmt.Sprintf(" %s", m.t("panel.wecom.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.wecom.status", status)),
			fmt.Sprintf(" %s", m.t("panel.wecom.transport", m.wecomAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.wecom.bound_directory", firstNonEmptyWeCom(entry.OccupiedBy, m.t("panel.wecom.none")))),
			fmt.Sprintf(" %s", m.t("panel.wecom.current_directory_target", firstNonEmptyWeCom(entry.TargetID, defaultWeComTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.wecom.current_directory_channel", firstNonEmptyWeCom(entry.WorkspaceChannel, m.t("panel.wecom.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.wecom.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.wecom.occupied_by", entry.OccupiedBy)))
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.wecom.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.wecom.bot_input", panel.createInput+"█"),
			" "+m.t("panel.wecom.create_format"),
			" "+m.t("panel.wecom.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.wecom.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.wecom.actions_hint")))
	}

	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+panel.message))
	}

	return m.renderContextBox("/wecom", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) wecomAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.wecom.status.not_started")
	}
	if state.Healthy {
		return "online"
	}
	if state.LastError != "" {
		return state.LastError
	}
	return m.t("panel.wecom.status.unknown")
}

func (m *Model) handleWeComPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.wecomPanel
	if panel == nil {
		return *m, nil
	}
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.wecomBindingEntries()

	if panel.createMode {
		switch msg.String() {
		case "esc":
			panel.createMode = false
			panel.createInput = ""
			return *m, nil
		case "enter":
			spec := strings.TrimSpace(panel.createInput)
			panel.createMode = false
			panel.createInput = ""
			return *m, m.createWeComAdapterCmd(spec)
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
			panel.message = m.t("panel.wecom.message.no_bot")
			return *m, nil
		}
		return *m, m.bindWeComEntry(entries[clampWeComSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.wecom.message.no_bot")
			return *m, nil
		}
		return *m, m.clearWeComChannel(entries[clampWeComSelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.wecom.message.no_bot")
			return *m, nil
		}
		return *m, m.unbindWeComEntry(entries[clampWeComSelection(panel.selected, len(entries))].Adapter)
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.wecom.message.no_bot")
			return *m, nil
		}
		entry := entries[clampWeComSelection(panel.selected, len(entries))]
		edit := m.enterIMEditSelect(entry.Adapter)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "esc":
		m.closeWeComPanel()
		return *m, nil
	}
	return *m, nil
}

func (m *Model) createWeComAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return wecomBindResultMsg{err: errors.New(m.t("panel.wecom.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 3 {
			return wecomBindResultMsg{err: errors.New(m.t("panel.wecom.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		botID := strings.TrimSpace(fields[1])
		secret := strings.TrimSpace(fields[2])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformWeCom),
			Extra: map[string]interface{}{
				"bot_id": botID,
				"secret": secret,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return wecomBindResultMsg{err: err}
		}
		if err := m.ensureWeComRuntime(); err != nil {
			return wecomBindResultMsg{err: err}
		}
		if err := m.startWeComAdapterIfNeeded(name); err != nil {
			return wecomBindResultMsg{err: err}
		}
		return wecomBindResultMsg{message: m.t("panel.wecom.message.added_bot", name)}
	}
}

func (m *Model) startWeComAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.wecom.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf(m.t("panel.wecom.error.not_configured"), name)
	}
	if !adapterCfg.Enabled {
		return fmt.Errorf(m.t("panel.wecom.error.disabled"), name)
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformWeCom)) {
		return fmt.Errorf(m.t("panel.wecom.error.not_wecom_adapter"), name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m *Model) ensureWeComRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.wecom.error.config_unavailable"))
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
	imMgr.SetPairingStore(pairingStore)
	m.imManager = imMgr
	return nil
}

func (m *Model) bindWeComEntry(entry wecomBindingEntry) tea.Cmd {
	return func() tea.Msg {
		ws := m.currentWorkspacePath()
		if ws == "" {
			return wecomBindResultMsg{err: errors.New(m.t("panel.wecom.none"))}
		}
		if m.imManager == nil {
			return wecomBindResultMsg{err: errors.New(m.t("panel.wecom.error.config_unavailable"))}
		}
		if err := m.startWeComAdapterIfNeeded(entry.Adapter); err != nil {
			return wecomBindResultMsg{err: err}
		}
		targetID := defaultWeComTargetID(ws)
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Workspace: ws,
			Platform:  im.PlatformWeCom,
			Adapter:   entry.Adapter,
			TargetID:  targetID,
		})
		if err != nil {
			return wecomBindResultMsg{err: err}
		}
		return wecomBindResultMsg{message: m.t("panel.wecom.message.bound_success")}
	}
}

func (m *Model) unbindWeComEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return wecomBindResultMsg{}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return wecomBindResultMsg{err: err}
		}
		return wecomBindResultMsg{message: m.t("panel.wecom.message.unbound")}
	}
}

func (m *Model) clearWeComChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return wecomBindResultMsg{}
		}
		ws := m.currentWorkspacePath()
		if bindings, err := m.imManager.ListBindings(); err == nil {
			for _, b := range bindings {
				if b.Adapter == adapterName && b.Workspace == ws {
					_ = m.imManager.UnbindAdapter(adapterName)
					break
				}
			}
		}
		return wecomBindResultMsg{message: m.t("panel.wecom.message.cleared")}
	}
}

func (m Model) wecomBindingEntries() []wecomBindingEntry {
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
		for _, b := range currentWeComBindings(m.imManager) {
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformWeCom)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]wecomBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultWeComTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptyWeCom(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		var statePtr *im.AdapterState
		if s, ok := adapterStates[name]; ok {
			statePtr = &s
		}
		entries = append(entries, wecomBindingEntry{
			Adapter:          name,
			Label:            name,
			TargetID:         targetID,
			WorkspaceChannel: workspaceChannel,
			OccupiedBy:       occupied[name],
			AdapterState:     statePtr,
			Muted:            bindingByAdapter[name].Muted,
		})
	}
	return entries
}

func (m Model) wecomBindingLabels(entries []wecomBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Muted:
			status = m.t("panel.wecom.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.wecom.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.wecom.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.wecom.entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func currentWeComBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	bindings, err := mgr.ListBindings()
	if err != nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range bindings {
		if b.Platform == im.PlatformWeCom {
			result = append(result, b)
		}
	}
	return result
}
