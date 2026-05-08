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

type twitchPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
	editState   imAdapterEditState
}

type twitchBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Disabled         bool
	Muted            bool
}

type twitchBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openTwitchPanel() {
	m.twitchPanel = &twitchPanelState{}
}

func (m *Model) closeTwitchPanel() {
	m.twitchPanel = nil
}

func firstNonEmptyTwitch(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxTwitch(v, min int) int {
	if v > min {
		return v
	}
	return min
}

func clampTwitchSelection(selected, total int) int {
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

func defaultTwitchTargetID(workspace string) string {
	if workspace == "" {
		return ""
	}
	return filepath.Base(workspace)
}

func (m Model) renderTwitchPanel() string {
	panel := m.twitchPanel
	if panel == nil {
		return ""
	}

	entries := m.twitchBindingEntries()
	currentBindings := currentTwitchBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}

	wsPath := m.currentWorkspacePath()
	if wsPath == "" {
		wsPath = m.t("panel.twitch.none")
	}

	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.twitch.directory")),
		fmt.Sprintf(" %s", wsPath),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.twitch.bots")),
		fmt.Sprintf(" %s", m.t("panel.twitch.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.twitch.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.twitch.available", maxTwitch(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.twitch.current_binding")),
	}

	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.twitch.none")))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" %s", m.t("panel.twitch.adapter", current.Adapter)),
				fmt.Sprintf(" %s", m.t("panel.twitch.target", firstNonEmptyTwitch(current.TargetID, m.t("panel.twitch.default")))),
				fmt.Sprintf(" %s", m.t("panel.twitch.channel", firstNonEmptyTwitch(current.ChannelID, m.t("panel.twitch.none")))),
			)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.twitch.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.twitch.no_bots")))
	} else {
		selected := clampTwitchSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.twitchBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.twitch.entry.available")
		if entry.Disabled {
			status = m.t("panel.twitch.entry.disabled")
		} else if entry.OccupiedBy != "" {
			status = m.t("panel.twitch.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.twitch.details")),
			fmt.Sprintf(" %s", m.t("panel.twitch.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.twitch.status", status)),
			fmt.Sprintf(" %s", m.t("panel.twitch.transport", m.twitchAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.twitch.bound_directory", firstNonEmptyTwitch(entry.OccupiedBy, m.t("panel.twitch.none")))),
			fmt.Sprintf(" %s", m.t("panel.twitch.current_directory_target", firstNonEmptyTwitch(entry.TargetID, defaultTwitchTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.twitch.current_directory_channel", firstNonEmptyTwitch(entry.WorkspaceChannel, m.t("panel.twitch.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.twitch.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.twitch.occupied_by", entry.OccupiedBy)))
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.twitch.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.twitch.bot_input", panel.createInput+"█"),
			" "+m.t("panel.twitch.create_format"),
			" "+m.t("panel.twitch.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.twitch.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.twitch.actions_hint")))
	}

	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+panel.message))
	}

	return m.renderContextBox("/twitch", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) twitchAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.twitch.status.not_started")
	}
	if state.Healthy {
		return "online"
	}
	if state.LastError != "" {
		return state.LastError
	}
	return m.t("panel.twitch.status.unknown")
}

func (m *Model) handleTwitchPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.twitchPanel
	if panel == nil {
		return *m, nil
	}
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.twitchBindingEntries()

	if panel.createMode {
		switch msg.String() {
		case "esc", "ctrl+c":
			panel.createMode = false
			panel.createInput = ""
			return *m, nil
		case "enter":
			spec := strings.TrimSpace(panel.createInput)
			panel.createMode = false
			panel.createInput = ""
			return *m, m.createTwitchAdapterCmd(spec)
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
			panel.message = m.t("panel.twitch.message.no_bot")
			return *m, nil
		}
		return *m, m.bindTwitchEntry(entries[clampTwitchSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.twitch.message.no_bot")
			return *m, nil
		}
		return *m, m.clearTwitchChannel(entries[clampTwitchSelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.twitch.message.no_bot")
			return *m, nil
		}
		return *m, m.unbindTwitchEntry(entries[clampTwitchSelection(panel.selected, len(entries))].Adapter)
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.twitch.message.no_bot")
			return *m, nil
		}
		entry := entries[clampTwitchSelection(panel.selected, len(entries))]
		edit := m.enterIMEditSelect(entry.Adapter)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "d":
		if len(entries) == 0 {
			panel.message = m.t("panel.twitch.message.no_bot")
			return *m, nil
		}
		entry := entries[clampTwitchSelection(panel.selected, len(entries))]
		return *m, m.toggleIMAdapterEnabled(entry.Adapter)
	case "esc", "ctrl+c":
		m.closeTwitchPanel()
		return *m, nil
	}
	return *m, nil
}

func (m *Model) createTwitchAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return twitchBindResultMsg{err: errors.New(m.t("panel.twitch.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 4 {
			return twitchBindResultMsg{err: errors.New(m.t("panel.twitch.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		token := strings.TrimSpace(fields[1])
		nick := strings.TrimSpace(fields[2])
		channels := strings.TrimSpace(fields[3])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformTwitch),
			Extra: map[string]interface{}{
				"token":    token,
				"nick":     nick,
				"channels": channels,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return twitchBindResultMsg{err: err}
		}
		if err := m.ensureTwitchRuntime(); err != nil {
			return twitchBindResultMsg{err: err}
		}
		if err := m.startTwitchAdapterIfNeeded(name); err != nil {
			return twitchBindResultMsg{err: err}
		}
		return twitchBindResultMsg{message: m.t("panel.twitch.message.added_bot", name)}
	}
}

func (m *Model) startTwitchAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.twitch.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf(m.t("panel.twitch.error.not_configured"), name)
	}
	if !adapterCfg.Enabled {
		return fmt.Errorf(m.t("panel.twitch.error.disabled"), name)
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformTwitch)) {
		return fmt.Errorf(m.t("panel.twitch.error.not_twitch_adapter"), name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m *Model) ensureTwitchRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.twitch.error.config_unavailable"))
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

func (m *Model) bindTwitchEntry(entry twitchBindingEntry) tea.Cmd {
	return func() tea.Msg {
		ws := m.currentWorkspacePath()
		if ws == "" {
			return twitchBindResultMsg{err: errors.New(m.t("panel.twitch.none"))}
		}
		if m.imManager == nil {
			return twitchBindResultMsg{err: errors.New(m.t("panel.twitch.error.config_unavailable"))}
		}
		if err := m.startTwitchAdapterIfNeeded(entry.Adapter); err != nil {
			return twitchBindResultMsg{err: err}
		}
		targetID := defaultTwitchTargetID(ws)
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Workspace: ws,
			Platform:  im.PlatformTwitch,
			Adapter:   entry.Adapter,
			TargetID:  targetID,
		})
		if err != nil {
			return twitchBindResultMsg{err: err}
		}
		return twitchBindResultMsg{message: m.t("panel.twitch.message.bound_success")}
	}
}

func (m *Model) unbindTwitchEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return twitchBindResultMsg{}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return twitchBindResultMsg{err: err}
		}
		return twitchBindResultMsg{message: m.t("panel.twitch.message.unbound")}
	}
}

func (m *Model) clearTwitchChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return twitchBindResultMsg{}
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
		return twitchBindResultMsg{message: m.t("panel.twitch.message.cleared")}
	}
}

func (m Model) twitchBindingEntries() []twitchBindingEntry {
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
		for _, b := range currentTwitchBindings(m.imManager) {
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
		if strings.EqualFold(adapter.Platform, string(im.PlatformTwitch)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]twitchBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultTwitchTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptyTwitch(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		var statePtr *im.AdapterState
		if s, ok := adapterStates[name]; ok {
			statePtr = &s
		}
		entries = append(entries, twitchBindingEntry{
			Adapter:          name,
			Label:            name,
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

func (m Model) twitchBindingLabels(entries []twitchBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Disabled:
			status = m.t("panel.twitch.entry.disabled")
		case entry.Disabled:
			status = m.t("panel.twitch.entry.disabled")
		case entry.Muted:
			status = m.t("panel.twitch.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.twitch.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.twitch.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.twitch.entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func currentTwitchBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	bindings, err := mgr.ListBindings()
	if err != nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range bindings {
		if b.Platform == im.PlatformTwitch {
			result = append(result, b)
		}
	}
	return result
}
