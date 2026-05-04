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

type mattermostPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
	editState   imAdapterEditState
}

type mattermostBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Muted            bool
}

type mattermostBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openMattermostPanel() {
	m.mattermostPanel = &mattermostPanelState{}
}

func (m *Model) closeMattermostPanel() {
	m.mattermostPanel = nil
}

func firstNonEmptyMM(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxMM(v, min int) int {
	if v > min {
		return v
	}
	return min
}

func clampMMSelection(selected, total int) int {
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

func defaultMMTargetID(workspace string) string {
	if workspace == "" {
		return ""
	}
	return filepath.Base(workspace)
}

func (m Model) renderMattermostPanel() string {
	panel := m.mattermostPanel
	if panel == nil {
		return ""
	}

	entries := m.mattermostBindingEntries()
	currentBindings := currentMMBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}

	wsPath := m.currentWorkspacePath()
	if wsPath == "" {
		wsPath = m.t("panel.mattermost.none")
	}

	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.mattermost.directory")),
		fmt.Sprintf(" %s", wsPath),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.mattermost.bots")),
		fmt.Sprintf(" %s", m.t("panel.mattermost.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.mattermost.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.mattermost.available", maxMM(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.mattermost.current_binding")),
	}

	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.mattermost.none")))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" %s", m.t("panel.mattermost.adapter", current.Adapter)),
				fmt.Sprintf(" %s", m.t("panel.mattermost.target", firstNonEmptyMM(current.TargetID, m.t("panel.mattermost.default")))),
				fmt.Sprintf(" %s", m.t("panel.mattermost.channel", firstNonEmptyMM(current.ChannelID, m.t("panel.mattermost.none")))),
			)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.mattermost.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.mattermost.no_bots")))
	} else {
		selected := clampMMSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.mattermostBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.mattermost.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.mattermost.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.mattermost.details")),
			fmt.Sprintf(" %s", m.t("panel.mattermost.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.mattermost.status", status)),
			fmt.Sprintf(" %s", m.t("panel.mattermost.transport", m.mmAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.mattermost.bound_directory", firstNonEmptyMM(entry.OccupiedBy, m.t("panel.mattermost.none")))),
			fmt.Sprintf(" %s", m.t("panel.mattermost.current_directory_target", firstNonEmptyMM(entry.TargetID, defaultMMTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.mattermost.current_directory_channel", firstNonEmptyMM(entry.WorkspaceChannel, m.t("panel.mattermost.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.mattermost.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.mattermost.occupied_by", entry.OccupiedBy)))
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.mattermost.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.mattermost.bot_input", panel.createInput+"█"),
			" "+m.t("panel.mattermost.create_format"),
			" "+m.t("panel.mattermost.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.mattermost.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.mattermost.actions_hint")))
	}

	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+panel.message))
	}

	return m.renderContextBox("/mattermost", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) mmAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.mattermost.status.not_started")
	}
	if state.Healthy {
		return "online"
	}
	if state.LastError != "" {
		return state.LastError
	}
	return m.t("panel.mattermost.status.unknown")
}

func (m *Model) handleMattermostPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.mattermostPanel
	if panel == nil {
		return *m, nil
	}
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.mattermostBindingEntries()

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
			return *m, m.createMMAdapterCmd(spec)
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
			panel.message = m.t("panel.mattermost.message.no_bot")
			return *m, nil
		}
		return *m, m.bindMMEntry(entries[clampMMSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.mattermost.message.no_bot")
			return *m, nil
		}
		return *m, m.clearMMChannel(entries[clampMMSelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.mattermost.message.no_bot")
			return *m, nil
		}
		return *m, m.unbindMMEntry(entries[clampMMSelection(panel.selected, len(entries))].Adapter)
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.mattermost.message.no_bot")
			return *m, nil
		}
		entry := entries[clampMMSelection(panel.selected, len(entries))]
		edit := m.enterIMEditSelect(entry.Adapter)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "esc":
		m.closeMattermostPanel()
		return *m, nil
	}
	return *m, nil
}

func (m *Model) createMMAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return mattermostBindResultMsg{err: errors.New(m.t("panel.mattermost.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 3 {
			return mattermostBindResultMsg{err: errors.New(m.t("panel.mattermost.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		url := strings.TrimSpace(fields[1])
		token := strings.TrimSpace(fields[2])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformMattermost),
			Extra: map[string]interface{}{
				"url":   url,
				"token": token,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return mattermostBindResultMsg{err: err}
		}
		if err := m.ensureMMRuntime(); err != nil {
			return mattermostBindResultMsg{err: err}
		}
		if err := m.startMMAdapterIfNeeded(name); err != nil {
			return mattermostBindResultMsg{err: err}
		}
		return mattermostBindResultMsg{message: m.t("panel.mattermost.message.added_bot", name)}
	}
}

func (m *Model) startMMAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.mattermost.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf(m.t("panel.mattermost.error.not_configured"), name)
	}
	if !adapterCfg.Enabled {
		return fmt.Errorf(m.t("panel.mattermost.error.disabled"), name)
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformMattermost)) {
		return fmt.Errorf(m.t("panel.mattermost.error.not_mm_adapter"), name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m *Model) ensureMMRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.mattermost.error.config_unavailable"))
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

func (m *Model) bindMMEntry(entry mattermostBindingEntry) tea.Cmd {
	return func() tea.Msg {
		ws := m.currentWorkspacePath()
		if ws == "" {
			return mattermostBindResultMsg{err: errors.New(m.t("panel.mattermost.none"))}
		}
		if m.imManager == nil {
			return mattermostBindResultMsg{err: errors.New(m.t("panel.mattermost.error.config_unavailable"))}
		}
		targetID := defaultMMTargetID(ws)
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Workspace: ws,
			Platform:  im.PlatformMattermost,
			Adapter:   entry.Adapter,
			TargetID:  targetID,
		})
		if err != nil {
			return mattermostBindResultMsg{err: err}
		}
		return mattermostBindResultMsg{message: m.t("panel.mattermost.message.bound_success")}
	}
}

func (m *Model) unbindMMEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return mattermostBindResultMsg{}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return mattermostBindResultMsg{err: err}
		}
		return mattermostBindResultMsg{message: m.t("panel.mattermost.message.unbound")}
	}
}

func (m *Model) clearMMChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return mattermostBindResultMsg{}
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
		return mattermostBindResultMsg{message: m.t("panel.mattermost.message.cleared")}
	}
}

func (m Model) mattermostBindingEntries() []mattermostBindingEntry {
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
		for _, b := range currentMMBindings(m.imManager) {
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformMattermost)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]mattermostBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultMMTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptyMM(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		var statePtr *im.AdapterState
		if s, ok := adapterStates[name]; ok {
			statePtr = &s
		}
		entries = append(entries, mattermostBindingEntry{
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

func (m Model) mattermostBindingLabels(entries []mattermostBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Muted:
			status = m.t("panel.mattermost.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.mattermost.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.mattermost.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.mattermost.entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func currentMMBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	bindings, err := mgr.ListBindings()
	if err != nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range bindings {
		if b.Platform == im.PlatformMattermost {
			result = append(result, b)
		}
	}
	return result
}
