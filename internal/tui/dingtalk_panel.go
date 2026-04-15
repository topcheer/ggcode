package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

type dingtalkPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
}

type dingtalkBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
}

type dingtalkBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openDingtalkPanel() {
	m.dingtalkPanel = &dingtalkPanelState{}
}

func (m *Model) closeDingtalkPanel() {
	m.dingtalkPanel = nil
}

func (m Model) renderDingtalkPanel() string {
	panel := m.dingtalkPanel
	if panel == nil {
		return ""
	}
	entries := m.dingtalkBindingEntries()
	current := currentDingtalkBinding(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.dingtalk.directory")),
		fmt.Sprintf(" %s", firstNonEmptyDingtalk(m.currentWorkspacePath(), m.t("panel.dingtalk.none"))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.dingtalk.bots")),
		fmt.Sprintf(" %s", m.t("panel.dingtalk.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.dingtalk.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.dingtalk.available", maxDingtalk(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.dingtalk.current_binding")),
	}
	if current == nil {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.dingtalk.none")))
	} else {
		body = append(body,
			fmt.Sprintf(" %s", m.t("panel.dingtalk.adapter", current.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.dingtalk.target", firstNonEmptyDingtalk(current.TargetID, m.t("panel.dingtalk.default")))),
			fmt.Sprintf(" %s", m.t("panel.dingtalk.channel", firstNonEmptyDingtalk(current.ChannelID, m.t("panel.dingtalk.none")))),
		)
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.dingtalk.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.dingtalk.no_bots")))
	} else {
		selected := clampDingtalkSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.dingtalkBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.dingtalk.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.dingtalk.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.dingtalk.details")),
			fmt.Sprintf(" %s", m.t("panel.dingtalk.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.dingtalk.status", status)),
			fmt.Sprintf(" %s", m.t("panel.dingtalk.transport", m.dingtalkAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.dingtalk.bound_directory", firstNonEmptyDingtalk(entry.OccupiedBy, m.t("panel.dingtalk.none")))),
			fmt.Sprintf(" %s", m.t("panel.dingtalk.current_directory_target", firstNonEmptyDingtalk(entry.TargetID, defaultDingtalkTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.dingtalk.current_directory_channel", firstNonEmptyDingtalk(entry.WorkspaceChannel, m.t("panel.dingtalk.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.dingtalk.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.dingtalk.occupied_by", entry.OccupiedBy)))
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.dingtalk.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.dingtalk.bot_input", panel.createInput+"█"),
			" "+m.t("panel.dingtalk.create_format"),
			" "+m.t("panel.dingtalk.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.dingtalk.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.dingtalk.actions_hint")))
	}
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return m.renderContextBox("/dingtalk", strings.Join(body, "\n"), lipgloss.Color("14"))
}

func (m *Model) handleDingtalkPanelKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	panel := m.dingtalkPanel
	if panel == nil {
		return *m, nil
	}
	entries := m.dingtalkBindingEntries()
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
			return *m, m.createDingtalkAdapterCmd(spec)
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
		if len(msg.Runes) > 0 {
			panel.createInput += string(msg.Runes)
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
			panel.message = m.t("panel.dingtalk.message.no_bot")
			return *m, nil
		}
		return *m, m.bindDingtalkEntry(entries[clampDingtalkSelection(panel.selected, len(entries))])
	case "x", "X":
		return *m, m.clearDingtalkChannel()
	case "u", "U":
		return *m, m.unbindDingtalkEntry()
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "esc":
		m.closeDingtalkPanel()
	}
	return *m, nil
}

func (m *Model) bindDingtalkEntry(entry dingtalkBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureDingtalkBotBinding(entry.Adapter); err != nil {
			return dingtalkBindResultMsg{err: err}
		}
		if m.agent != nil {
			if err := m.waitForDingtalkAdapterHealthy(m.imManager, entry.Adapter, 10*time.Second); err != nil {
				return dingtalkBindResultMsg{err: err}
			}
			if err := m.imManager.SyncSessionHistory(context.Background(), m.agent.Messages()); err != nil && err != im.ErrNoChannelBound {
				return dingtalkBindResultMsg{err: err}
			}
		}
		return dingtalkBindResultMsg{message: m.t("panel.dingtalk.message.bound_success")}
	}
}

func (m *Model) unbindDingtalkEntry() tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureDingtalkRuntime(); err != nil {
			return dingtalkBindResultMsg{err: err}
		}
		if err := m.imManager.UnbindChannel(m.currentWorkspacePath()); err != nil {
			return dingtalkBindResultMsg{err: err}
		}
		return dingtalkBindResultMsg{message: m.t("panel.dingtalk.message.unbound")}
	}
}

func (m *Model) clearDingtalkChannel() tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureDingtalkRuntime(); err != nil {
			return dingtalkBindResultMsg{err: err}
		}
		if err := m.imManager.ClearChannel(m.currentWorkspacePath()); err != nil {
			return dingtalkBindResultMsg{err: err}
		}
		return dingtalkBindResultMsg{message: m.t("panel.dingtalk.message.cleared")}
	}
}

func (m *Model) createDingtalkAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return dingtalkBindResultMsg{err: errors.New(m.t("panel.dingtalk.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 3 {
			return dingtalkBindResultMsg{err: errors.New(m.t("panel.dingtalk.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		appKey := strings.TrimSpace(fields[1])
		appSecret := strings.TrimSpace(fields[2])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformDingTalk),
			Extra: map[string]interface{}{
				"app_key":    appKey,
				"app_secret": appSecret,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return dingtalkBindResultMsg{err: err}
		}
		if err := m.ensureDingtalkRuntime(); err != nil {
			return dingtalkBindResultMsg{err: err}
		}
		if err := m.startDingtalkAdapterIfNeeded(name); err != nil {
			return dingtalkBindResultMsg{err: err}
		}
		return dingtalkBindResultMsg{message: m.t("panel.dingtalk.message.added_bot", name)}
	}
}

func (m *Model) startDingtalkAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.dingtalk.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return errors.New(m.t("panel.dingtalk.error.not_configured", name))
	}
	if !adapterCfg.Enabled {
		return errors.New(m.t("panel.dingtalk.error.disabled", name))
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformDingTalk)) {
		return errors.New(m.t("panel.dingtalk.error.not_dingtalk_adapter", name))
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m Model) dingtalkBindingEntries() []dingtalkBindingEntry {
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformDingTalk)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]dingtalkBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultDingtalkTargetID(currentWorkspace)
		workspaceChannel := ""
		if currentBinding != nil && strings.TrimSpace(currentBinding.Workspace) == currentWorkspace && currentBinding.Adapter == name {
			targetID = firstNonEmptyDingtalk(currentBinding.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(currentBinding.ChannelID)
		}
		entries = append(entries, dingtalkBindingEntry{
			Adapter:          name,
			Label:            name,
			TargetID:         targetID,
			WorkspaceChannel: workspaceChannel,
			OccupiedBy:       occupied[name],
			AdapterState:     dingtalkStatePtr(adapterStates[name]),
		})
	}
	return entries
}

func (m Model) dingtalkBindingLabels(entries []dingtalkBindingEntry) []string {
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		status := m.t("panel.dingtalk.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.dingtalk.entry.bound")
		}
		label := fmt.Sprintf("%s · %s", entry.Adapter, status)
		labels = append(labels, label)
	}
	return labels
}

func clampDingtalkSelection(selected, total int) int {
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

func currentDingtalkBinding(mgr *im.Manager) *im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	b := mgr.CurrentBinding()
	if b != nil && b.Platform == im.PlatformDingTalk {
		return b
	}
	return nil
}

func firstNonEmptyDingtalk(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultDingtalkTargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}

func (m *Model) ensureDingtalkBotBinding(adapter string) error {
	if err := m.ensureDingtalkRuntime(); err != nil {
		return err
	}
	if err := m.startDingtalkAdapterIfNeeded(adapter); err != nil {
		return err
	}
	workspace := m.currentWorkspacePath()
	current := currentDingtalkBinding(m.imManager)
	if current != nil && strings.TrimSpace(current.Workspace) == strings.TrimSpace(workspace) && current.Adapter == adapter {
		return nil
	}
	_, err := m.imManager.BindChannel(im.ChannelBinding{
		Platform: im.PlatformDingTalk,
		Adapter:  adapter,
		TargetID: defaultDingtalkTargetID(workspace),
	})
	return err
}

func (m *Model) ensureDingtalkRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.dingtalk.error.config_unavailable"))
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

func maxDingtalk(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func (m Model) dingtalkAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.dingtalk.status.not_started")
	}
	status := strings.TrimSpace(state.Status)
	if status == "" {
		status = m.t("panel.dingtalk.status.unknown")
	}
	if state.Healthy {
		return status
	}
	return status
}

func dingtalkStatePtr(state im.AdapterState) *im.AdapterState {
	if strings.TrimSpace(state.Name) == "" {
		return nil
	}
	copy := state
	return &copy
}

func (m Model) waitForDingtalkAdapterHealthy(mgr *im.Manager, adapter string, timeout time.Duration) error {
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
			return errors.New(m.t("panel.dingtalk.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.LastError)))
		}
		if strings.TrimSpace(lastStatus.Status) != "" {
			return errors.New(m.t("panel.dingtalk.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.Status)))
		}
	}
	return errors.New(m.t("panel.dingtalk.error.not_online", adapter))
}
