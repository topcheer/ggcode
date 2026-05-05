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

type matrixPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
	editState   imAdapterEditState
}

type matrixBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Muted            bool
}

type matrixBindResultMsg struct {
	message string
	err     error
}

func (m *Model) openMatrixPanel() {
	m.matrixPanel = &matrixPanelState{}
}

func (m *Model) closeMatrixPanel() {
	m.matrixPanel = nil
}

func firstNonEmptyMat(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxMat(v, min int) int {
	if v > min {
		return v
	}
	return min
}

func clampMatSelection(selected, total int) int {
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

func defaultMatTargetID(workspace string) string {
	if workspace == "" {
		return ""
	}
	return filepath.Base(workspace)
}

func (m Model) renderMatrixPanel() string {
	panel := m.matrixPanel
	if panel == nil {
		return ""
	}

	entries := m.matrixBindingEntries()
	currentBindings := currentMatBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}

	wsPath := m.currentWorkspacePath()
	if wsPath == "" {
		wsPath = m.t("panel.matrix.none")
	}

	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.matrix.directory")),
		fmt.Sprintf(" %s", wsPath),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.matrix.bots")),
		fmt.Sprintf(" %s", m.t("panel.matrix.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.matrix.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.matrix.available", maxMat(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.matrix.current_binding")),
	}

	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.matrix.none")))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" %s", m.t("panel.matrix.adapter", current.Adapter)),
				fmt.Sprintf(" %s", m.t("panel.matrix.target", firstNonEmptyMat(current.TargetID, m.t("panel.matrix.default")))),
				fmt.Sprintf(" %s", m.t("panel.matrix.channel", firstNonEmptyMat(current.ChannelID, m.t("panel.matrix.none")))),
			)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.matrix.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.matrix.no_bots")))
	} else {
		selected := clampMatSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.matrixBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.matrix.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.matrix.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.matrix.details")),
			fmt.Sprintf(" %s", m.t("panel.matrix.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.matrix.status", status)),
			fmt.Sprintf(" %s", m.t("panel.matrix.transport", m.matAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.matrix.bound_directory", firstNonEmptyMat(entry.OccupiedBy, m.t("panel.matrix.none")))),
			fmt.Sprintf(" %s", m.t("panel.matrix.current_directory_target", firstNonEmptyMat(entry.TargetID, defaultMatTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.matrix.current_directory_channel", firstNonEmptyMat(entry.WorkspaceChannel, m.t("panel.matrix.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.matrix.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.matrix.occupied_by", entry.OccupiedBy)))
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.matrix.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.matrix.bot_input", panel.createInput+"█"),
			" "+m.t("panel.matrix.create_format"),
			" "+m.t("panel.matrix.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.matrix.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.matrix.actions_hint")))
	}

	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	// QR code at top - find first adapter with ContactURI
	var contactURI string
	for _, entry := range entries {
		if entry.AdapterState != nil && entry.AdapterState.ContactURI != "" {
			contactURI = entry.AdapterState.ContactURI
			break
		}
	}
	if contactURI != "" {
		var qrSection []string
		if qr := renderContactQRCode(contactURI); qr != "" {
			qrSection = append(qrSection, qr)
		}
		qrSection = append(qrSection, fmt.Sprintf(" %s", contactURI), "")
		body = append(append(qrSection, body...), "")
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+panel.message))
	}

	return m.renderContextBox("/matrix", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) matAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.matrix.status.not_started")
	}
	if state.Healthy {
		return "online"
	}
	if state.LastError != "" {
		return state.LastError
	}
	return m.t("panel.matrix.status.unknown")
}

func (m *Model) handleMatrixPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.matrixPanel
	if panel == nil {
		return *m, nil
	}
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.matrixBindingEntries()

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
			return *m, m.createMatAdapterCmd(spec)
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
			panel.message = m.t("panel.matrix.message.no_bot")
			return *m, nil
		}
		return *m, m.bindMatEntry(entries[clampMatSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.matrix.message.no_bot")
			return *m, nil
		}
		return *m, m.clearMatChannel(entries[clampMatSelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.matrix.message.no_bot")
			return *m, nil
		}
		return *m, m.unbindMatEntry(entries[clampMatSelection(panel.selected, len(entries))].Adapter)
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.matrix.message.no_bot")
			return *m, nil
		}
		entry := entries[clampMatSelection(panel.selected, len(entries))]
		edit := m.enterIMEditSelect(entry.Adapter)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "esc":
		m.closeMatrixPanel()
		return *m, nil
	}
	return *m, nil
}

func (m *Model) createMatAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return matrixBindResultMsg{err: errors.New(m.t("panel.matrix.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 3 {
			return matrixBindResultMsg{err: errors.New(m.t("panel.matrix.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		hsURL := strings.TrimSpace(fields[1])
		token := strings.TrimSpace(fields[2])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformMatrix),
			Extra: map[string]interface{}{
				"homeserver":   hsURL,
				"access_token": token,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return matrixBindResultMsg{err: err}
		}
		if err := m.ensureMatRuntime(); err != nil {
			return matrixBindResultMsg{err: err}
		}
		if err := m.startMatAdapterIfNeeded(name); err != nil {
			return matrixBindResultMsg{err: err}
		}
		return matrixBindResultMsg{message: m.t("panel.matrix.message.added_bot", name)}
	}
}

func (m *Model) startMatAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.matrix.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf(m.t("panel.matrix.error.not_configured"), name)
	}
	if !adapterCfg.Enabled {
		return fmt.Errorf(m.t("panel.matrix.error.disabled"), name)
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformMatrix)) {
		return fmt.Errorf(m.t("panel.matrix.error.not_matrix_adapter"), name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m *Model) ensureMatRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.matrix.error.config_unavailable"))
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

func (m *Model) bindMatEntry(entry matrixBindingEntry) tea.Cmd {
	return func() tea.Msg {
		ws := m.currentWorkspacePath()
		if ws == "" {
			return matrixBindResultMsg{err: errors.New(m.t("panel.matrix.none"))}
		}
		if m.imManager == nil {
			return matrixBindResultMsg{err: errors.New(m.t("panel.matrix.error.config_unavailable"))}
		}
		if err := m.startMatAdapterIfNeeded(entry.Adapter); err != nil {
			return matrixBindResultMsg{err: err}
		}
		targetID := defaultMatTargetID(ws)
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Workspace: ws,
			Platform:  im.PlatformMatrix,
			Adapter:   entry.Adapter,
			TargetID:  targetID,
		})
		if err != nil {
			return matrixBindResultMsg{err: err}
		}
		return matrixBindResultMsg{message: m.t("panel.matrix.message.bound_success")}
	}
}

func (m *Model) unbindMatEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return matrixBindResultMsg{}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return matrixBindResultMsg{err: err}
		}
		return matrixBindResultMsg{message: m.t("panel.matrix.message.unbound")}
	}
}

func (m *Model) clearMatChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return matrixBindResultMsg{}
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
		return matrixBindResultMsg{message: m.t("panel.matrix.message.cleared")}
	}
}

func (m Model) matrixBindingEntries() []matrixBindingEntry {
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
		for _, b := range currentMatBindings(m.imManager) {
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformMatrix)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]matrixBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultMatTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptyMat(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		var statePtr *im.AdapterState
		if s, ok := adapterStates[name]; ok {
			statePtr = &s
		}
		entries = append(entries, matrixBindingEntry{
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

func (m Model) matrixBindingLabels(entries []matrixBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Muted:
			status = m.t("panel.matrix.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.matrix.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.matrix.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.matrix.entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func currentMatBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	bindings, err := mgr.ListBindings()
	if err != nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range bindings {
		if b.Platform == im.PlatformMatrix {
			result = append(result, b)
		}
	}
	return result
}
