package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

type discordPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
}

type discordBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
}

type discordBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openDiscordPanel() {
	m.discordPanel = &discordPanelState{}
}

func (m *Model) closeDiscordPanel() {
	m.discordPanel = nil
}

func (m Model) renderDiscordPanel() string {
	panel := m.discordPanel
	if panel == nil {
		return ""
	}
	entries := m.discordBindingEntries()
	currentBindings := currentDiscordBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.discord.directory")),
		fmt.Sprintf(" %s", firstNonEmptyDiscord(m.currentWorkspacePath(), m.t("panel.discord.none"))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.discord.bots")),
		fmt.Sprintf(" %s", m.t("panel.discord.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.discord.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.discord.available", maxDiscord(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.discord.current_binding")),
	}
	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.discord.none")))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" %s", m.t("panel.discord.adapter", current.Adapter)),
				fmt.Sprintf(" %s", m.t("panel.discord.target", firstNonEmptyDiscord(current.TargetID, m.t("panel.discord.default")))),
				fmt.Sprintf(" %s", m.t("panel.discord.channel", firstNonEmptyDiscord(current.ChannelID, m.t("panel.discord.none")))),
			)
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.discord.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.discord.no_bots")))
	} else {
		selected := clampDiscordSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.discordBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.discord.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.discord.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.discord.details")),
			fmt.Sprintf(" %s", m.t("panel.discord.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.discord.status", status)),
			fmt.Sprintf(" %s", m.t("panel.discord.transport", m.discordAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.discord.bound_directory", firstNonEmptyDiscord(entry.OccupiedBy, m.t("panel.discord.none")))),
			fmt.Sprintf(" %s", m.t("panel.discord.current_directory_target", firstNonEmptyDiscord(entry.TargetID, defaultDiscordTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.discord.current_directory_channel", firstNonEmptyDiscord(entry.WorkspaceChannel, m.t("panel.discord.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.discord.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.discord.occupied_by", entry.OccupiedBy)))
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.discord.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.discord.bot_input", panel.createInput+"█"),
			" "+m.t("panel.discord.create_format"),
			" "+m.t("panel.discord.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.discord.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.discord.actions_hint")))
	}
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return m.renderContextBox("/discord", strings.Join(body, "\n"), lipgloss.Color("55"))
}

func (m *Model) handleDiscordPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.discordPanel
	if panel == nil {
		return *m, nil
	}
	entries := m.discordBindingEntries()
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
			return *m, m.createDiscordAdapterCmd(spec)
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
			panel.message = m.t("panel.discord.message.no_bot")
			return *m, nil
		}
		return *m, m.bindDiscordEntry(entries[clampDiscordSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.discord.message.no_bot")
			return *m, nil
		}
		return *m, m.clearDiscordChannel(entries[clampDiscordSelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.discord.message.no_bot")
			return *m, nil
		}
		return *m, m.unbindDiscordEntry(entries[clampDiscordSelection(panel.selected, len(entries))].Adapter)
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "esc":
		m.closeDiscordPanel()
	}
	return *m, nil
}

func (m *Model) bindDiscordEntry(entry discordBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureDiscordBotBinding(entry.Adapter); err != nil {
			return discordBindResultMsg{err: err}
		}
		if m.agent != nil {
			if err := m.waitForDiscordAdapterHealthy(m.imManager, entry.Adapter, 10*time.Second); err != nil {
				return discordBindResultMsg{err: err}
			}
			if err := m.imManager.SyncSessionHistory(context.Background(), m.agent.Messages()); err != nil && err != im.ErrNoChannelBound {
				return discordBindResultMsg{err: err}
			}
		}
		return discordBindResultMsg{message: m.t("panel.discord.message.bound_success")}
	}
}

func (m *Model) unbindDiscordEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureDiscordRuntime(); err != nil {
			return discordBindResultMsg{err: err}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return discordBindResultMsg{err: err}
		}
		return discordBindResultMsg{message: m.t("panel.discord.message.unbound")}
	}
}

func (m *Model) clearDiscordChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureDiscordRuntime(); err != nil {
			return discordBindResultMsg{err: err}
		}
		if err := m.imManager.ClearChannelByAdapter(adapterName); err != nil {
			return discordBindResultMsg{err: err}
		}
		return discordBindResultMsg{message: m.t("panel.discord.message.cleared")}
	}
}

func (m *Model) createDiscordAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return discordBindResultMsg{err: errors.New(m.t("panel.discord.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 2 {
			return discordBindResultMsg{err: errors.New(m.t("panel.discord.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		token := strings.TrimSpace(fields[1])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformDiscord),
			Extra: map[string]interface{}{
				"token": token,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return discordBindResultMsg{err: err}
		}
		if err := m.ensureDiscordRuntime(); err != nil {
			return discordBindResultMsg{err: err}
		}
		if err := m.startDiscordAdapterIfNeeded(name); err != nil {
			return discordBindResultMsg{err: err}
		}
		return discordBindResultMsg{message: m.t("panel.discord.message.added_bot", name)}
	}
}

func (m *Model) startDiscordAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.discord.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return errors.New(m.t("panel.discord.error.not_configured", name))
	}
	if !adapterCfg.Enabled {
		return errors.New(m.t("panel.discord.error.disabled", name))
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformDiscord)) {
		return errors.New(m.t("panel.discord.error.not_discord_adapter", name))
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m Model) discordBindingEntries() []discordBindingEntry {
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
		for _, b := range currentDiscordBindings(m.imManager) {
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformDiscord)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]discordBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultDiscordTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptyDiscord(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		entries = append(entries, discordBindingEntry{
			Adapter:          name,
			Label:            name,
			TargetID:         targetID,
			WorkspaceChannel: workspaceChannel,
			OccupiedBy:       occupied[name],
			AdapterState:     discordStatePtr(adapterStates[name]),
		})
	}
	return entries
}

func (m Model) discordBindingLabels(entries []discordBindingEntry) []string {
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		status := m.t("panel.discord.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.discord.entry.bound")
		}
		label := fmt.Sprintf("%s · %s", entry.Adapter, status)
		labels = append(labels, label)
	}
	return labels
}

func clampDiscordSelection(selected, total int) int {
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

func currentDiscordBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range mgr.CurrentBindings() {
		if b.Platform == im.PlatformDiscord {
			result = append(result, b)
		}
	}
	return result
}

func firstNonEmptyDiscord(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultDiscordTargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}

func (m *Model) ensureDiscordBotBinding(adapter string) error {
	if err := m.ensureDiscordRuntime(); err != nil {
		return err
	}
	if err := m.startDiscordAdapterIfNeeded(adapter); err != nil {
		return err
	}
	workspace := m.currentWorkspacePath()
	for _, b := range currentDiscordBindings(m.imManager) {
		if strings.TrimSpace(b.Workspace) == strings.TrimSpace(workspace) && b.Adapter == adapter {
			return nil
		}
	}
	_, err := m.imManager.BindChannel(im.ChannelBinding{
		Platform: im.PlatformDiscord,
		Adapter:  adapter,
		TargetID: defaultDiscordTargetID(workspace),
	})
	return err
}

func (m *Model) ensureDiscordRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.discord.error.config_unavailable"))
	}
	if !m.config.IM.Enabled {
		m.config.IM.Enabled = true
		if err := m.config.Save(); err != nil {
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
	if _, err := im.StartCurrentBindingAdapter(context.Background(), m.config.IM, imMgr); err != nil {
		return fmt.Errorf("starting current workspace IM adapter: %w", err)
	}
	imMgr.SetBridge(newTUIIMBridge(func() *tea.Program { return m.program }))
	m.SetIMManager(imMgr)
	return nil
}

func maxDiscord(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func (m Model) discordAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.discord.status.not_started")
	}
	status := strings.TrimSpace(state.Status)
	if status == "" {
		status = m.t("panel.discord.status.unknown")
	}
	if state.Healthy {
		return status
	}
	return status
}

func discordStatePtr(state im.AdapterState) *im.AdapterState {
	if strings.TrimSpace(state.Name) == "" {
		return nil
	}
	copy := state
	return &copy
}

func (m Model) waitForDiscordAdapterHealthy(mgr *im.Manager, adapter string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastStatus im.AdapterState
	for time.Now().Before(deadline) {
		snapshot := mgr.Snapshot()
		for _, state := range snapshot.Adapters {
			if state.Name != adapter {
				continue
			}
			lastStatus = state
			if state.Healthy {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastStatus.Name != "" {
		if strings.TrimSpace(lastStatus.LastError) != "" {
			return errors.New(m.t("panel.discord.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.LastError)))
		}
		if strings.TrimSpace(lastStatus.Status) != "" {
			return errors.New(m.t("panel.discord.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.Status)))
		}
	}
	return errors.New(m.t("panel.discord.error.not_online", adapter))
}
