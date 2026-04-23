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

type tgPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
}

type tgBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Muted            bool
}

type tgBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openTGPanel() {
	m.tgPanel = &tgPanelState{}
}

func (m *Model) closeTGPanel() {
	m.tgPanel = nil
}

func (m Model) renderTGPanel() string {
	panel := m.tgPanel
	if panel == nil {
		return ""
	}
	entries := m.tgBindingEntries()
	currentBindings := currentTGBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.tg.directory")),
		fmt.Sprintf(" %s", firstNonEmptyTG(m.currentWorkspacePath(), m.t("panel.tg.none"))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.tg.bots")),
		fmt.Sprintf(" %s", m.t("panel.tg.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.tg.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.tg.available", maxTG(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.tg.current_binding")),
	}
	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.tg.none")))
	} else {
		for _, current := range currentBindings {
			status := "active"
			if current.Muted {
				status = "muted"
			}
			body = append(body,
				fmt.Sprintf(" %s (%s)", current.Adapter, status),
				fmt.Sprintf(" %s", m.t("panel.tg.target", firstNonEmptyTG(current.TargetID, m.t("panel.tg.default")))),
				fmt.Sprintf(" %s", m.t("panel.tg.channel", firstNonEmptyTG(current.ChannelID, m.t("panel.tg.none")))),
			)
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.tg.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.tg.no_bots")))
	} else {
		selected := clampTGSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.tgBindingLabels(entries), selected, true))
		entry := entries[selected]
		currentWS := m.currentWorkspacePath()
		var status string
		switch {
		case entry.Muted:
			status = m.t("panel.tg.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.tg.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.tg.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.tg.entry.available")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.tg.details")),
			fmt.Sprintf(" %s", m.t("panel.tg.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.tg.status", status)),
			fmt.Sprintf(" %s", m.t("panel.tg.transport", m.tgAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.tg.bound_directory", firstNonEmptyTG(entry.OccupiedBy, m.t("panel.tg.none")))),
			fmt.Sprintf(" %s", m.t("panel.tg.current_directory_target", firstNonEmptyTG(entry.TargetID, defaultTGTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.tg.current_directory_channel", firstNonEmptyTG(entry.WorkspaceChannel, m.t("panel.tg.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.tg.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.tg.occupied_by", entry.OccupiedBy)))
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.tg.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.tg.bot_input", panel.createInput+"█"),
			" "+m.t("panel.tg.create_format"),
			" "+m.t("panel.tg.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.tg.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.tg.actions_hint")))
	}
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return m.renderContextBox("/telegram", strings.Join(body, "\n"), lipgloss.Color("6"))
}

func (m *Model) handleTGPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.tgPanel
	if panel == nil {
		return *m, nil
	}
	entries := m.tgBindingEntries()
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
			return *m, m.createTGAdapterCmd(spec)
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
			panel.message = m.t("panel.tg.message.no_bot")
			return *m, nil
		}
		return *m, m.bindTGEntry(entries[clampTGSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.tg.message.no_bot")
			return *m, nil
		}
		return *m, m.clearTGChannel(entries[clampTGSelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.tg.message.no_bot")
			return *m, nil
		}
		return *m, m.unbindTGEntry(entries[clampTGSelection(panel.selected, len(entries))].Adapter)
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "esc":
		m.closeTGPanel()
	}
	return *m, nil
}

func (m *Model) bindTGEntry(entry tgBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureTGBotBinding(entry.Adapter); err != nil {
			return tgBindResultMsg{err: err}
		}
		if m.agent != nil {
			if err := m.waitForTGAdapterHealthy(m.imManager, entry.Adapter, 10*time.Second); err != nil {
				return tgBindResultMsg{err: err}
			}
			if err := m.imManager.SyncSessionHistory(context.Background(), m.agent.Messages()); err != nil && err != im.ErrNoChannelBound {
				return tgBindResultMsg{err: err}
			}
		}
		return tgBindResultMsg{message: m.t("panel.tg.message.bound_success")}
	}
}

func (m *Model) unbindTGEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureTGRuntime(true); err != nil {
			return tgBindResultMsg{err: err}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return tgBindResultMsg{err: err}
		}
		return tgBindResultMsg{message: m.t("panel.tg.message.unbound")}
	}
}

func (m *Model) clearTGChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureTGRuntime(true); err != nil {
			return tgBindResultMsg{err: err}
		}
		if err := m.imManager.ClearChannelByAdapter(adapterName); err != nil {
			return tgBindResultMsg{err: err}
		}
		return tgBindResultMsg{message: m.t("panel.tg.message.cleared")}
	}
}

func (m *Model) createTGAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return tgBindResultMsg{err: errors.New(m.t("panel.tg.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 2 {
			return tgBindResultMsg{err: errors.New(m.t("panel.tg.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		botToken := strings.TrimSpace(fields[1])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformTelegram),
			Extra: map[string]interface{}{
				"bot_token": botToken,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return tgBindResultMsg{err: err}
		}
		if err := m.ensureTGRuntime(false); err != nil {
			return tgBindResultMsg{err: err}
		}
		if err := m.startTGAdapterIfNeeded(name); err != nil {
			return tgBindResultMsg{err: err}
		}
		return tgBindResultMsg{message: m.t("panel.tg.message.added_bot", name)}
	}
}

func (m *Model) startTGAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.tg.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return errors.New(m.t("panel.tg.error.not_configured", name))
	}
	if !adapterCfg.Enabled {
		return errors.New(m.t("panel.tg.error.disabled", name))
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformTelegram)) {
		return errors.New(m.t("panel.tg.error.not_tg_adapter", name))
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m Model) tgBindingEntries() []tgBindingEntry {
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
		for _, b := range currentTGBindings(m.imManager) {
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformTelegram)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]tgBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultTGTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptyTG(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		entries = append(entries, tgBindingEntry{
			Adapter:          name,
			Label:            name,
			TargetID:         targetID,
			WorkspaceChannel: workspaceChannel,
			OccupiedBy:       occupied[name],
			AdapterState:     tgStatePtr(adapterStates[name]),
			Muted:            bindingByAdapter[name].Muted,
		})
	}
	return entries
}

func (m Model) tgBindingLabels(entries []tgBindingEntry) []string {
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		status := m.t("panel.tg.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.tg.entry.bound")
		}
		label := fmt.Sprintf("%s · %s", entry.Adapter, status)
		labels = append(labels, label)
	}
	return labels
}

func clampTGSelection(selected, total int) int {
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

func currentTGBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range mgr.CurrentBindings() {
		if b.Platform == im.PlatformTelegram {
			result = append(result, b)
		}
	}
	return result
}

func firstNonEmptyTG(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultTGTargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}

func (m *Model) ensureTGBotBinding(adapter string) error {
	if err := m.ensureTGRuntime(true); err != nil {
		return err
	}
	if err := m.startTGAdapterIfNeeded(adapter); err != nil {
		return err
	}
	workspace := m.currentWorkspacePath()
	for _, b := range currentTGBindings(m.imManager) {
		if strings.TrimSpace(b.Workspace) == strings.TrimSpace(workspace) && b.Adapter == adapter {
			return nil
		}
	}
	_, err := m.imManager.BindChannel(im.ChannelBinding{
		Platform: im.PlatformTelegram,
		Adapter:  adapter,
		TargetID: defaultTGTargetID(workspace),
	})
	return err
}

func (m *Model) ensureTGRuntime(autoEnable bool) error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.tg.error.config_unavailable"))
	}
	if !m.config.IM.Enabled {
		if !autoEnable {
			return fmt.Errorf("%s", m.tgRuntimeStatus())
		}
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

func (m Model) tgRuntimeStatus() string {
	if m.imManager != nil {
		return m.t("panel.tg.runtime.available")
	}
	if m.config == nil || !m.config.IM.Enabled {
		return m.t("panel.tg.runtime.disabled")
	}
	return m.t("panel.tg.runtime.not_started")
}

func maxTG(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func (m Model) tgAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.tg.status.not_started")
	}
	status := strings.TrimSpace(state.Status)
	if status == "" {
		status = m.t("panel.tg.status.unknown")
	}
	if state.Healthy {
		return status
	}
	return status
}

func tgStatePtr(state im.AdapterState) *im.AdapterState {
	if strings.TrimSpace(state.Name) == "" {
		return nil
	}
	copy := state
	return &copy
}

func (m Model) waitForTGAdapterHealthy(mgr *im.Manager, adapter string, timeout time.Duration) error {
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
			return errors.New(m.t("panel.tg.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.LastError)))
		}
		if strings.TrimSpace(lastStatus.Status) != "" {
			return errors.New(m.t("panel.tg.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.Status)))
		}
	}
	return errors.New(m.t("panel.tg.error.not_online", adapter))
}
