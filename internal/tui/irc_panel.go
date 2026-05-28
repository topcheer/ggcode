package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

type ircPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
	editState   imAdapterEditState
}

type ircBindingEntry struct {
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

type ircBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openIRCPanel() {
	m.ircPanel = &ircPanelState{}
}

func (m *Model) closeIRCPanel() {
	m.ircPanel = nil
}
func maxIRC(v, min int) int {
	if v > min {
		return v
	}
	return min
}

func clampIRCSelection(selected, total int) int {
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

func defaultIRCTargetID(workspace string) string {
	if workspace == "" {
		return ""
	}
	return filepath.Base(workspace)
}

func (m Model) renderIRCPanel() string {
	panel := m.ircPanel
	if panel == nil {
		return ""
	}

	entries := m.ircBindingEntries()
	currentBindings := currentIRCBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}

	wsPath := m.currentWorkspacePath()
	if wsPath == "" {
		wsPath = m.t("panel.irc.none")
	}

	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.irc.directory")),
		fmt.Sprintf(" %s", wsPath),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.irc.bots")),
		fmt.Sprintf(" %s", m.t("panel.irc.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.irc.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.irc.available", maxIRC(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.irc.current_binding")),
	}

	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.irc.none")))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" %s", m.t("panel.irc.adapter", current.Adapter)),
				fmt.Sprintf(" %s", m.t("panel.irc.target", util.FirstNonEmpty(current.TargetID, m.t("panel.irc.default")))),
				fmt.Sprintf(" %s", m.t("panel.irc.channel", util.FirstNonEmpty(current.ChannelID, m.t("panel.irc.none")))),
			)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.irc.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.irc.no_bots")))
	} else {
		selected := clampIRCSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.ircBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.irc.entry.available")
		if entry.Disabled {
			status = m.t("panel.irc.entry.disabled")
		} else if entry.OccupiedBy != "" {
			status = m.t("panel.irc.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.irc.details")),
			fmt.Sprintf(" %s", m.t("panel.irc.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.irc.status", status)),
			fmt.Sprintf(" %s", m.t("panel.irc.transport", m.ircAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.irc.bound_directory", util.FirstNonEmpty(entry.OccupiedBy, m.t("panel.irc.none")))),
			fmt.Sprintf(" %s", m.t("panel.irc.current_directory_target", util.FirstNonEmpty(entry.TargetID, defaultIRCTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.irc.current_directory_channel", util.FirstNonEmpty(entry.WorkspaceChannel, m.t("panel.irc.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.irc.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.irc.occupied_by", entry.OccupiedBy)))
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.irc.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.irc.bot_input", panel.createInput+"█"),
			" "+m.t("panel.irc.create_format"),
			" "+m.t("panel.irc.create_example"),
			renderPasteShortcutHint(m.currentLanguage()),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.irc.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.irc.actions_hint")))
	}

	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+panel.message))
	}

	return m.renderContextBox("/irc", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) ircAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.irc.status.not_started")
	}
	if state.Healthy {
		return "online"
	}
	if state.LastError != "" {
		return state.LastError
	}
	return m.t("panel.irc.status.unknown")
}

func (m *Model) handleIRCPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.ircPanel
	if panel == nil {
		return *m, nil
	}
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.ircBindingEntries()

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
			return *m, m.createIRCAdapterCmd(spec)
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
			panel.message = m.t("panel.irc.message.no_bot")
			return *m, nil
		}
		return *m, m.bindIRCEntry(entries[clampIRCSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.irc.message.no_bot")
			return *m, nil
		}
		return *m, m.clearIRCChannel(entries[clampIRCSelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.irc.message.no_bot")
			return *m, nil
		}
		return *m, m.unbindIRCEntry(entries[clampIRCSelection(panel.selected, len(entries))].Adapter)
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.irc.message.no_bot")
			return *m, nil
		}
		entry := entries[clampIRCSelection(panel.selected, len(entries))]
		edit := m.enterIMEditSelect(entry.Adapter)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "d":
		if len(entries) == 0 {
			panel.message = m.t("panel.irc.message.no_bot")
			return *m, nil
		}
		entry := entries[clampIRCSelection(panel.selected, len(entries))]
		return *m, m.toggleIMAdapterEnabled(entry.Adapter)
	case "esc", "ctrl+c":
		m.closeIRCPanel()
		return *m, nil
	}
	return *m, nil
}

func (m *Model) createIRCAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return ircBindResultMsg{err: errors.New(m.t("panel.irc.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 4 {
			return ircBindResultMsg{err: errors.New(m.t("panel.irc.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		host := strings.TrimSpace(fields[1])
		nick := strings.TrimSpace(fields[2])
		channels := strings.TrimSpace(fields[3])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformIRC),
			Extra: map[string]interface{}{
				"host":     host,
				"nick":     nick,
				"channels": channels,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return ircBindResultMsg{err: err}
		}
		if err := m.ensureIRCRuntime(); err != nil {
			return ircBindResultMsg{err: err}
		}
		if err := m.startIRCAdapterIfNeeded(name); err != nil {
			return ircBindResultMsg{err: err}
		}
		return ircBindResultMsg{message: m.t("panel.irc.message.added_bot", name)}
	}
}

func (m *Model) startIRCAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.irc.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf(m.t("panel.irc.error.not_configured"), name)
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
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformIRC)) {
		return fmt.Errorf(m.t("panel.irc.error.not_irc_adapter"), name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m *Model) ensureIRCRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.irc.error.config_unavailable"))
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

func (m *Model) bindIRCEntry(entry ircBindingEntry) tea.Cmd {
	return func() tea.Msg {
		ws := m.currentWorkspacePath()
		if ws == "" {
			return ircBindResultMsg{err: errors.New(m.t("panel.irc.none"))}
		}
		if m.imManager == nil {
			return ircBindResultMsg{err: errors.New(m.t("panel.irc.error.config_unavailable"))}
		}
		// Start the adapter if not already running.
		if err := m.startIRCAdapterIfNeeded(entry.Adapter); err != nil {
			return ircBindResultMsg{err: err}
		}
		targetID := defaultIRCTargetID(ws)
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Workspace: ws,
			Platform:  im.PlatformIRC,
			Adapter:   entry.Adapter,
			TargetID:  targetID,
		})
		if err != nil {
			return ircBindResultMsg{err: err}
		}
		return ircBindResultMsg{message: m.t("panel.irc.message.bound_success")}
	}
}

func (m *Model) unbindIRCEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return ircBindResultMsg{}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return ircBindResultMsg{err: err}
		}
		return ircBindResultMsg{message: m.t("panel.irc.message.unbound")}
	}
}

func (m *Model) clearIRCChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return ircBindResultMsg{}
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
		return ircBindResultMsg{message: m.t("panel.irc.message.cleared")}
	}
}

func (m Model) ircBindingEntries() []ircBindingEntry {
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
		for _, b := range currentIRCBindings(m.imManager) {
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
		if strings.EqualFold(adapter.Platform, string(im.PlatformIRC)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]ircBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultIRCTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = util.FirstNonEmpty(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		var statePtr *im.AdapterState
		if s, ok := adapterStates[name]; ok {
			statePtr = &s
		}
		entries = append(entries, ircBindingEntry{
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

func (m Model) ircBindingLabels(entries []ircBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Disabled:
			status = m.t("panel.irc.entry.disabled")
		case entry.Disabled:
			status = m.t("panel.irc.entry.disabled")
		case entry.Muted:
			status = m.t("panel.irc.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.irc.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.irc.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.irc.entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func currentIRCBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	bindings, err := mgr.ListBindings()
	if err != nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range bindings {
		if b.Platform == im.PlatformIRC {
			result = append(result, b)
		}
	}
	return result
}
