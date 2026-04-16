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

type slackPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
}

type slackBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
}

type slackBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openSlackPanel() {
	m.slackPanel = &slackPanelState{}
}

func (m *Model) closeSlackPanel() {
	m.slackPanel = nil
}

func (m Model) renderSlackPanel() string {
	panel := m.slackPanel
	if panel == nil {
		return ""
	}
	entries := m.slackBindingEntries()
	current := currentSlackBinding(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.slack.directory")),
		fmt.Sprintf(" %s", firstNonEmptySlack(m.currentWorkspacePath(), m.t("panel.slack.none"))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.slack.bots")),
		fmt.Sprintf(" %s", m.t("panel.slack.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.slack.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.slack.available", maxSlack(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.slack.current_binding")),
	}
	if current == nil {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.slack.none")))
	} else {
		body = append(body,
			fmt.Sprintf(" %s", m.t("panel.slack.adapter", current.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.slack.target", firstNonEmptySlack(current.TargetID, m.t("panel.slack.default")))),
			fmt.Sprintf(" %s", m.t("panel.slack.channel", firstNonEmptySlack(current.ChannelID, m.t("panel.slack.none")))),
		)
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.slack.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.slack.no_bots")))
	} else {
		selected := clampSlackSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.slackBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.slack.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.slack.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.slack.details")),
			fmt.Sprintf(" %s", m.t("panel.slack.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.slack.status", status)),
			fmt.Sprintf(" %s", m.t("panel.slack.transport", m.slackAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.slack.bound_directory", firstNonEmptySlack(entry.OccupiedBy, m.t("panel.slack.none")))),
			fmt.Sprintf(" %s", m.t("panel.slack.current_directory_target", firstNonEmptySlack(entry.TargetID, defaultSlackTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.slack.current_directory_channel", firstNonEmptySlack(entry.WorkspaceChannel, m.t("panel.slack.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.slack.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.slack.occupied_by", entry.OccupiedBy)))
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.slack.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.slack.bot_input", panel.createInput+"█"),
			" "+m.t("panel.slack.create_format"),
			" "+m.t("panel.slack.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.slack.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.slack.actions_hint")))
	}
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return m.renderContextBox("/slack", strings.Join(body, "\n"), lipgloss.Color("2"))
}

func (m *Model) handleSlackPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.slackPanel
	if panel == nil {
		return *m, nil
	}
	entries := m.slackBindingEntries()
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
			return *m, m.createSlackAdapterCmd(spec)
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
			panel.message = m.t("panel.slack.message.no_bot")
			return *m, nil
		}
		return *m, m.bindSlackEntry(entries[clampSlackSelection(panel.selected, len(entries))])
	case "x", "X":
		return *m, m.clearSlackChannel()
	case "u", "U":
		return *m, m.unbindSlackEntry()
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "esc":
		m.closeSlackPanel()
	}
	return *m, nil
}

func (m *Model) bindSlackEntry(entry slackBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureSlackBotBinding(entry.Adapter); err != nil {
			return slackBindResultMsg{err: err}
		}
		if m.agent != nil {
			if err := m.waitForSlackAdapterHealthy(m.imManager, entry.Adapter, 10*time.Second); err != nil {
				return slackBindResultMsg{err: err}
			}
			if err := m.imManager.SyncSessionHistory(context.Background(), m.agent.Messages()); err != nil && err != im.ErrNoChannelBound {
				return slackBindResultMsg{err: err}
			}
		}
		return slackBindResultMsg{message: m.t("panel.slack.message.bound_success")}
	}
}

func (m *Model) unbindSlackEntry() tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureSlackRuntime(); err != nil {
			return slackBindResultMsg{err: err}
		}
		if err := m.imManager.UnbindChannel(m.currentWorkspacePath()); err != nil {
			return slackBindResultMsg{err: err}
		}
		return slackBindResultMsg{message: m.t("panel.slack.message.unbound")}
	}
}

func (m *Model) clearSlackChannel() tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureSlackRuntime(); err != nil {
			return slackBindResultMsg{err: err}
		}
		if err := m.imManager.ClearChannel(m.currentWorkspacePath()); err != nil {
			return slackBindResultMsg{err: err}
		}
		return slackBindResultMsg{message: m.t("panel.slack.message.cleared")}
	}
}

func (m *Model) createSlackAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return slackBindResultMsg{err: errors.New(m.t("panel.slack.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 3 {
			return slackBindResultMsg{err: errors.New(m.t("panel.slack.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		botToken := strings.TrimSpace(fields[1])
		appToken := strings.TrimSpace(fields[2])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformSlack),
			Extra: map[string]interface{}{
				"bot_token": botToken,
				"app_token": appToken,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return slackBindResultMsg{err: err}
		}
		if err := m.ensureSlackRuntime(); err != nil {
			return slackBindResultMsg{err: err}
		}
		if err := m.startSlackAdapterIfNeeded(name); err != nil {
			return slackBindResultMsg{err: err}
		}
		return slackBindResultMsg{message: m.t("panel.slack.message.added_bot", name)}
	}
}

func (m *Model) startSlackAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.slack.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return errors.New(m.t("panel.slack.error.not_configured", name))
	}
	if !adapterCfg.Enabled {
		return errors.New(m.t("panel.slack.error.disabled", name))
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformSlack)) {
		return errors.New(m.t("panel.slack.error.not_slack_adapter", name))
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m Model) slackBindingEntries() []slackBindingEntry {
	if m.config == nil {
		return nil
	}
	occupied := make(map[string]string)
	adapterStates := make(map[string]im.AdapterState)
	currentWorkspace := strings.TrimSpace(m.currentWorkspacePath())
	var currentBinding *im.ChannelBinding
	if m.imManager != nil {
		snapshot := m.imManager.Snapshot()
		for _, state := range snapshot.Adapters {
			adapterStates[state.Name] = state
		}
		currentBinding = m.imManager.CurrentBinding()
		if bindings, err := m.imManager.ListBindings(); err == nil {
			for _, binding := range bindings {
				occupied[binding.Adapter] = binding.Workspace
			}
		}
	}
	keys := make([]string, 0, len(m.config.IM.Adapters))
	for name, adapter := range m.config.IM.Adapters {
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformSlack)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]slackBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultSlackTargetID(currentWorkspace)
		workspaceChannel := ""
		if currentBinding != nil && strings.TrimSpace(currentBinding.Workspace) == currentWorkspace && currentBinding.Adapter == name {
			targetID = firstNonEmptySlack(currentBinding.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(currentBinding.ChannelID)
		}
		entries = append(entries, slackBindingEntry{
			Adapter:          name,
			Label:            name,
			TargetID:         targetID,
			WorkspaceChannel: workspaceChannel,
			OccupiedBy:       occupied[name],
			AdapterState:     slackStatePtr(adapterStates[name]),
		})
	}
	return entries
}

func (m Model) slackBindingLabels(entries []slackBindingEntry) []string {
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		status := m.t("panel.slack.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.slack.entry.bound")
		}
		label := fmt.Sprintf("%s · %s", entry.Adapter, status)
		labels = append(labels, label)
	}
	return labels
}

func clampSlackSelection(selected, total int) int {
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

func currentSlackBinding(mgr *im.Manager) *im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	b := mgr.CurrentBinding()
	if b != nil && b.Platform == im.PlatformSlack {
		return b
	}
	return nil
}

func firstNonEmptySlack(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultSlackTargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}

func (m *Model) ensureSlackBotBinding(adapter string) error {
	if err := m.ensureSlackRuntime(); err != nil {
		return err
	}
	if err := m.startSlackAdapterIfNeeded(adapter); err != nil {
		return err
	}
	workspace := m.currentWorkspacePath()
	current := currentSlackBinding(m.imManager)
	if current != nil && strings.TrimSpace(current.Workspace) == strings.TrimSpace(workspace) && current.Adapter == adapter {
		return nil
	}
	_, err := m.imManager.BindChannel(im.ChannelBinding{
		Platform: im.PlatformSlack,
		Adapter:  adapter,
		TargetID: defaultSlackTargetID(workspace),
	})
	return err
}

func (m *Model) ensureSlackRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.slack.error.config_unavailable"))
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

func maxSlack(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func (m Model) slackAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.slack.status.not_started")
	}
	status := strings.TrimSpace(state.Status)
	if status == "" {
		status = m.t("panel.slack.status.unknown")
	}
	if state.Healthy {
		return status
	}
	return status
}

func slackStatePtr(state im.AdapterState) *im.AdapterState {
	if strings.TrimSpace(state.Name) == "" {
		return nil
	}
	copy := state
	return &copy
}

func (m Model) waitForSlackAdapterHealthy(mgr *im.Manager, adapter string, timeout time.Duration) error {
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
			return errors.New(m.t("panel.slack.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.LastError)))
		}
		if strings.TrimSpace(lastStatus.Status) != "" {
			return errors.New(m.t("panel.slack.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.Status)))
		}
	}
	return errors.New(m.t("panel.slack.error.not_online", adapter))
}
