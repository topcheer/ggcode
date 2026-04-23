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

type feishuPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
}

type feishuBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Muted            bool
}

type feishuBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openFeishuPanel() {
	m.feishuPanel = &feishuPanelState{}
}

func (m *Model) closeFeishuPanel() {
	m.feishuPanel = nil
}

func (m Model) renderFeishuPanel() string {
	panel := m.feishuPanel
	if panel == nil {
		return ""
	}
	entries := m.feishuBindingEntries()
	currentBindings := currentFeishuBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.feishu.directory")),
		fmt.Sprintf(" %s", firstNonEmptyFeishu(m.currentWorkspacePath(), m.t("panel.feishu.none"))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.feishu.bots")),
		fmt.Sprintf(" %s", m.t("panel.feishu.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.feishu.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.feishu.available", maxFeishu(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.feishu.current_binding")),
	}
	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.feishu.none")))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" %s", m.t("panel.feishu.adapter", current.Adapter)),
				fmt.Sprintf(" %s", m.t("panel.feishu.target", firstNonEmptyFeishu(current.TargetID, m.t("panel.feishu.default")))),
				fmt.Sprintf(" %s", m.t("panel.feishu.channel", firstNonEmptyFeishu(current.ChannelID, m.t("panel.feishu.none")))),
			)
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.feishu.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.feishu.no_bots")))
	} else {
		selected := clampFeishuSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.feishuBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.feishu.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.feishu.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.feishu.details")),
			fmt.Sprintf(" %s", m.t("panel.feishu.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.feishu.status", status)),
			fmt.Sprintf(" %s", m.t("panel.feishu.transport", m.feishuAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.feishu.bound_directory", firstNonEmptyFeishu(entry.OccupiedBy, m.t("panel.feishu.none")))),
			fmt.Sprintf(" %s", m.t("panel.feishu.current_directory_target", firstNonEmptyFeishu(entry.TargetID, defaultFeishuTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.feishu.current_directory_channel", firstNonEmptyFeishu(entry.WorkspaceChannel, m.t("panel.feishu.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.feishu.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.feishu.occupied_by", entry.OccupiedBy)))
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.feishu.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.feishu.bot_input", panel.createInput+"█"),
			" "+m.t("panel.feishu.create_format"),
			" "+m.t("panel.feishu.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.feishu.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.feishu.actions_hint")))
	}
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return m.renderContextBox("/feishu", strings.Join(body, "\n"), lipgloss.Color("11"))
}

func (m *Model) handleFeishuPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.feishuPanel
	if panel == nil {
		return *m, nil
	}
	entries := m.feishuBindingEntries()
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
			return *m, m.createFeishuAdapterCmd(spec)
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
			panel.message = m.t("panel.feishu.message.no_bot")
			return *m, nil
		}
		return *m, m.bindFeishuEntry(entries[clampFeishuSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) > 0 {
			return *m, m.clearFeishuChannel(entries[clampFeishuSelection(panel.selected, len(entries))].Adapter)
		}
	case "u", "U":
		if len(entries) > 0 {
			return *m, m.unbindFeishuEntry(entries[clampFeishuSelection(panel.selected, len(entries))].Adapter)
		}
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "esc":
		m.closeFeishuPanel()
	}
	return *m, nil
}

func (m *Model) bindFeishuEntry(entry feishuBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureFeishuBotBinding(entry.Adapter); err != nil {
			return feishuBindResultMsg{err: err}
		}
		if m.agent != nil {
			if err := m.waitForFeishuAdapterHealthy(m.imManager, entry.Adapter, 10*time.Second); err != nil {
				return feishuBindResultMsg{err: err}
			}
			if err := m.imManager.SyncSessionHistory(context.Background(), m.agent.Messages()); err != nil && err != im.ErrNoChannelBound {
				return feishuBindResultMsg{err: err}
			}
		}
		return feishuBindResultMsg{message: m.t("panel.feishu.message.bound_success")}
	}
}

func (m *Model) unbindFeishuEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureFeishuRuntime(); err != nil {
			return feishuBindResultMsg{err: err}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return feishuBindResultMsg{err: err}
		}
		return feishuBindResultMsg{message: m.t("panel.feishu.message.unbound")}
	}
}

func (m *Model) clearFeishuChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureFeishuRuntime(); err != nil {
			return feishuBindResultMsg{err: err}
		}
		if err := m.imManager.ClearChannelByAdapter(adapterName); err != nil {
			return feishuBindResultMsg{err: err}
		}
		return feishuBindResultMsg{message: m.t("panel.feishu.message.cleared")}
	}
}

func (m *Model) createFeishuAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return feishuBindResultMsg{err: errors.New(m.t("panel.feishu.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 3 {
			return feishuBindResultMsg{err: errors.New(m.t("panel.feishu.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		appID := strings.TrimSpace(fields[1])
		appSecret := strings.TrimSpace(fields[2])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformFeishu),
			Extra: map[string]interface{}{
				"app_id":     appID,
				"app_secret": appSecret,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return feishuBindResultMsg{err: err}
		}
		if err := m.ensureFeishuRuntime(); err != nil {
			return feishuBindResultMsg{err: err}
		}
		if err := m.startFeishuAdapterIfNeeded(name); err != nil {
			return feishuBindResultMsg{err: err}
		}
		return feishuBindResultMsg{message: m.t("panel.feishu.message.added_bot", name)}
	}
}

func (m *Model) startFeishuAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.feishu.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return errors.New(m.t("panel.feishu.error.not_configured", name))
	}
	if !adapterCfg.Enabled {
		return errors.New(m.t("panel.feishu.error.disabled", name))
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformFeishu)) {
		return errors.New(m.t("panel.feishu.error.not_feishu_adapter", name))
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m Model) feishuBindingEntries() []feishuBindingEntry {
	if m.config == nil {
		return nil
	}
	occupied := make(map[string]string)
	adapterStates := make(map[string]im.AdapterState)
	bindingByAdapter := make(map[string]im.ChannelBinding)
	currentWorkspace := strings.TrimSpace(m.currentWorkspacePath())
	if m.imManager != nil {
		snapshot := m.imManager.Snapshot()
		for _, state := range snapshot.Adapters {
			adapterStates[state.Name] = state
		}
		for _, b := range currentFeishuBindings(m.imManager) {
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformFeishu)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]feishuBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultFeishuTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptyFeishu(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		entries = append(entries, feishuBindingEntry{
			Adapter:          name,
			Label:            name,
			TargetID:         targetID,
			WorkspaceChannel: workspaceChannel,
			OccupiedBy:       occupied[name],
			AdapterState:     feishuStatePtr(adapterStates[name]),
			Muted:            bindingByAdapter[name].Muted,
		})
	}
	return entries
}

func (m Model) feishuBindingLabels(entries []feishuBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Muted:
			status = m.t("panel.feishu.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.feishu.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.feishu.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.feishu.entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func clampFeishuSelection(selected, total int) int {
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

func currentFeishuBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range mgr.CurrentBindings() {
		if b.Platform == im.PlatformFeishu {
			result = append(result, b)
		}
	}
	return result
}

func firstNonEmptyFeishu(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultFeishuTargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}

func (m *Model) ensureFeishuBotBinding(adapter string) error {
	if err := m.ensureFeishuRuntime(); err != nil {
		return err
	}
	if err := m.startFeishuAdapterIfNeeded(adapter); err != nil {
		return err
	}
	workspace := m.currentWorkspacePath()
	for _, b := range currentFeishuBindings(m.imManager) {
		if strings.TrimSpace(b.Workspace) == strings.TrimSpace(workspace) && b.Adapter == adapter {
			return nil
		}
	}
	_, err := m.imManager.BindChannel(im.ChannelBinding{
		Platform: im.PlatformFeishu,
		Adapter:  adapter,
		TargetID: defaultFeishuTargetID(workspace),
	})
	return err
}

func (m *Model) ensureFeishuRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.feishu.error.config_unavailable"))
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

func maxFeishu(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func (m Model) feishuAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.feishu.status.not_started")
	}
	status := strings.TrimSpace(state.Status)
	if status == "" {
		status = m.t("panel.feishu.status.unknown")
	}
	if state.Healthy {
		return status
	}
	return status
}

func feishuStatePtr(state im.AdapterState) *im.AdapterState {
	if strings.TrimSpace(state.Name) == "" {
		return nil
	}
	copy := state
	return &copy
}

func (m Model) waitForFeishuAdapterHealthy(mgr *im.Manager, adapter string, timeout time.Duration) error {
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
			return errors.New(m.t("panel.feishu.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.LastError)))
		}
		if strings.TrimSpace(lastStatus.Status) != "" {
			return errors.New(m.t("panel.feishu.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.Status)))
		}
	}
	return errors.New(m.t("panel.feishu.error.not_online", adapter))
}
