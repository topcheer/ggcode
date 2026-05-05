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

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

type nostrPanelState struct {
	selected      int
	message       string
	createMode    bool
	createInput   string
	editState     imAdapterEditState
	qrCode        string // npub QR code rendered as text
	generatedNpub string // npub to display alongside QR
}

type nostrBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Muted            bool
}

type nostrBindResultMsg struct {
	message string
	err     error
	qrCode  string // rendered QR code for npub
	npub    string // npub public key
}

func (m *Model) openNostrPanel() {
	m.nostrPanel = &nostrPanelState{}
}

func (m *Model) closeNostrPanel() {
	m.nostrPanel = nil
}

func firstNonEmptyNostr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxNostr(v, min int) int {
	if v > min {
		return v
	}
	return min
}

func clampNostrSelection(selected, total int) int {
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

func defaultNostrTargetID(workspace string) string {
	if workspace == "" {
		return ""
	}
	return filepath.Base(workspace)
}

func (m Model) renderNostrPanel() string {
	panel := m.nostrPanel
	if panel == nil {
		return ""
	}

	entries := m.nostrBindingEntries()
	currentBindings := currentNostrBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}

	wsPath := m.currentWorkspacePath()
	if wsPath == "" {
		wsPath = m.t("panel.nostr.none")
	}

	body := []string{}

	body = append(body,
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.nostr.directory")),
		fmt.Sprintf(" %s", wsPath),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.nostr.bots")),
		fmt.Sprintf(" %s", m.t("panel.nostr.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.nostr.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.nostr.available", maxNostr(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.nostr.current_binding")),
	)

	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.nostr.none")))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" %s", m.t("panel.nostr.adapter", current.Adapter)),
				fmt.Sprintf(" %s", m.t("panel.nostr.target", firstNonEmptyNostr(current.TargetID, m.t("panel.nostr.default")))),
				fmt.Sprintf(" %s", m.t("panel.nostr.channel", firstNonEmptyNostr(current.ChannelID, m.t("panel.nostr.none")))),
			)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.nostr.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.nostr.no_bots")))
	} else {
		selected := clampNostrSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.nostrBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.nostr.entry.available")
		if entry.OccupiedBy != "" {
			status = m.t("panel.nostr.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.nostr.details")),
			fmt.Sprintf(" %s", m.t("panel.nostr.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.nostr.status", status)),
			fmt.Sprintf(" %s", m.t("panel.nostr.transport", m.nostrAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.nostr.bound_directory", firstNonEmptyNostr(entry.OccupiedBy, m.t("panel.nostr.none")))),
			fmt.Sprintf(" %s", m.t("panel.nostr.current_directory_target", firstNonEmptyNostr(entry.TargetID, defaultNostrTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.nostr.current_directory_channel", firstNonEmptyNostr(entry.WorkspaceChannel, m.t("panel.nostr.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.nostr.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.nostr.occupied_by", entry.OccupiedBy)))
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.nostr.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.nostr.bot_input", panel.createInput+"█"),
			" "+m.t("panel.nostr.create_format"),
			" "+m.t("panel.nostr.create_example"),
			" "+m.t("panel.nostr.create_full_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.nostr.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.nostr.actions_hint")))
	}

	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+panel.message))
	}

	return m.renderContextBox("/nostr", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) nostrAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.nostr.status.not_started")
	}
	if state.Healthy {
		return "online"
	}
	if state.LastError != "" {
		return state.LastError
	}
	return m.t("panel.nostr.status.unknown")
}

func (m *Model) handleNostrPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.nostrPanel
	if panel == nil {
		return *m, nil
	}
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.nostrBindingEntries()

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
			return *m, m.createNostrAdapterCmd(spec)
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
			panel.message = m.t("panel.nostr.message.no_bot")
			return *m, nil
		}
		return *m, m.bindNostrEntry(entries[clampNostrSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.nostr.message.no_bot")
			return *m, nil
		}
		return *m, m.clearNostrChannel(entries[clampNostrSelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.nostr.message.no_bot")
			return *m, nil
		}
		return *m, m.unbindNostrEntry(entries[clampNostrSelection(panel.selected, len(entries))].Adapter)
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.nostr.message.no_bot")
			return *m, nil
		}
		entry := entries[clampNostrSelection(panel.selected, len(entries))]
		edit := m.enterIMEditSelect(entry.Adapter)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "q":
		var states []*im.AdapterState
		for _, entry := range entries {
			if entry.AdapterState != nil {
				states = append(states, entry.AdapterState)
			}
		}
		if m.openQROverlayFromStates("Nostr", states) {
			return *m, nil
		}
	case "esc":
		m.closeNostrPanel()
		return *m, nil
	}
	return *m, nil
}

func (m *Model) createNostrAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return nostrBindResultMsg{err: errors.New(m.t("panel.nostr.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 1 {
			return nostrBindResultMsg{err: errors.New(m.t("panel.nostr.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])

		var privateKey string
		var relays string
		switch len(fields) {
		case 1:
			// Auto-generate key, use default relays
			privateKey = nostr.GeneratePrivateKey()
			relays = "wss://relay.damus.io,wss://nos.lol,wss://relay.nostr.band"
		case 2:
			privateKey = strings.TrimSpace(fields[1])
			relays = "wss://relay.damus.io,wss://nos.lol,wss://relay.nostr.band"
		default:
			privateKey = strings.TrimSpace(fields[1])
			relays = strings.TrimSpace(fields[2])
		}

		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformNostr),
			Extra: map[string]interface{}{
				"private_key": privateKey,
				"relays":      relays,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return nostrBindResultMsg{err: err}
		}
		if err := m.ensureNostrRuntime(); err != nil {
			return nostrBindResultMsg{err: err}
		}
		if err := m.startNostrAdapterIfNeeded(name); err != nil {
			return nostrBindResultMsg{err: err}
		}

		// Derive public key for QR code
		pubKey, _ := nostr.GetPublicKey(privateKey)
		npub, _ := nip19.EncodePublicKey(pubKey)
		var qrText string
		qr, err := qrcode.New("nostr:"+npub, qrcode.Medium)
		if err == nil {
			qr.DisableBorder = true
			qrText = qr.ToSmallString(false)
		}

		var msg string
		if len(fields) == 1 {
			// Show nsec for auto-generated key so user can back it up
			nsec, _ := nip19.EncodePrivateKey(privateKey)
			msg = m.t("panel.nostr.message.added_bot_key", name, nsec)
		} else {
			msg = m.t("panel.nostr.message.added_bot", name)
		}

		return nostrBindResultMsg{
			message: msg,
			qrCode:  qrText,
			npub:    npub,
		}
	}
}

func (m *Model) startNostrAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.nostr.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf(m.t("panel.nostr.error.not_configured"), name)
	}
	if !adapterCfg.Enabled {
		return fmt.Errorf(m.t("panel.nostr.error.disabled"), name)
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformNostr)) {
		return fmt.Errorf(m.t("panel.nostr.error.not_nostr_adapter"), name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m *Model) ensureNostrRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.nostr.error.config_unavailable"))
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

func (m *Model) bindNostrEntry(entry nostrBindingEntry) tea.Cmd {
	return func() tea.Msg {
		ws := m.currentWorkspacePath()
		if ws == "" {
			return nostrBindResultMsg{err: errors.New(m.t("panel.nostr.none"))}
		}
		if m.imManager == nil {
			return nostrBindResultMsg{err: errors.New(m.t("panel.nostr.error.config_unavailable"))}
		}
		if err := m.startNostrAdapterIfNeeded(entry.Adapter); err != nil {
			return nostrBindResultMsg{err: err}
		}
		targetID := defaultNostrTargetID(ws)
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Workspace: ws,
			Platform:  im.PlatformNostr,
			Adapter:   entry.Adapter,
			TargetID:  targetID,
		})
		if err != nil {
			return nostrBindResultMsg{err: err}
		}
		return nostrBindResultMsg{message: m.t("panel.nostr.message.bound_success")}
	}
}

func (m *Model) unbindNostrEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return nostrBindResultMsg{}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return nostrBindResultMsg{err: err}
		}
		return nostrBindResultMsg{message: m.t("panel.nostr.message.unbound")}
	}
}

func (m *Model) clearNostrChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return nostrBindResultMsg{}
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
		return nostrBindResultMsg{message: m.t("panel.nostr.message.cleared")}
	}
}

func (m Model) nostrBindingEntries() []nostrBindingEntry {
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
		for _, b := range currentNostrBindings(m.imManager) {
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformNostr)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]nostrBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultNostrTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptyNostr(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		var statePtr *im.AdapterState
		if s, ok := adapterStates[name]; ok {
			statePtr = &s
		}
		entries = append(entries, nostrBindingEntry{
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

func (m Model) nostrBindingLabels(entries []nostrBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Muted:
			status = m.t("panel.nostr.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.nostr.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.nostr.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.nostr.entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func currentNostrBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	bindings, err := mgr.ListBindings()
	if err != nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range bindings {
		if b.Platform == im.PlatformNostr {
			result = append(result, b)
		}
	}
	return result
}
