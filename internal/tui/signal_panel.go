package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"image/png"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	goqrcode "github.com/skip2/go-qrcode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

type signalPanelState struct {
	selected    int
	message     string
	createMode  bool
	createInput string
	editState   imAdapterEditState
	daemonOK    *bool  // nil=checking, true=ok, false=unreachable
	installing  bool   // true while Docker install is running
	qrCode      string // ASCII art QR code, empty if not fetched
	qrFetching  bool   // true while fetching QR code
	qrError     string // error if QR fetch failed
}

type signalBindingEntry struct {
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

type signalBindResultMsg struct {
	message string
	err     error
}

type signalDaemonCheckMsg struct {
	ok  bool
	err error
}

type signalQRCodeMsg struct {
	qr  string // ASCII art
	err error
}

func checkSignalDaemonCmd() tea.Cmd {
	return func() tea.Msg {
		err := im.CheckSignalDaemon("")
		return signalDaemonCheckMsg{ok: err == nil, err: err}
	}
}

func fetchSignalQRCmd(baseURL string) tea.Cmd {
	return func() tea.Msg {
		png, err := im.FetchSignalQRCode(baseURL, "ggcode")
		if err != nil {
			return signalQRCodeMsg{err: err}
		}
		return signalQRCodeMsg{qr: renderQRCodeASCII(png)}
	}
}

func (m *Model) openSignalPanel() tea.Cmd {
	m.signalPanel = &signalPanelState{}
	return checkSignalDaemonCmd()
}

func (m *Model) closeSignalPanel() {
	m.signalPanel = nil
}

func (m *Model) installSignalDaemon() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd, _, shellErr := util.NewShellCommandContext(ctx, im.SignalDaemonInstallCommand())
		if shellErr != nil {
			return signalBindResultMsg{err: fmt.Errorf("install: shell not found: %w", shellErr)}
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			return signalBindResultMsg{err: fmt.Errorf("install failed: %s: %s", err, strings.TrimSpace(string(output)))}
		}
		// Re-check daemon after install
		time.Sleep(3 * time.Second)
		checkErr := im.CheckSignalDaemon("")
		if checkErr != nil {
			return signalBindResultMsg{message: "Docker container started. Daemon may need a few seconds to become ready. Press r to re-check."}
		}
		return signalBindResultMsg{message: "signal-cli-rest-api installed and running. Press q to generate QR code."}
	}
}

// renderQRCodeASCII converts a PNG QR code image to Unicode block-character ASCII art.
func renderQRCodeASCII(pngData []byte) string {
	// Step 1: Decode PNG image
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return "(QR code PNG decode failed)"
	}

	// Step 2: Decode QR code content from the image using gozxing
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "(QR code bitmap failed)"
	}
	reader := qrcode.NewQRCodeReader()
	result, err := reader.Decode(bmp, nil)
	if err != nil {
		return "(QR code content decode failed)"
	}
	content := result.String()

	// Step 3: Re-generate QR code as ASCII using go-qrcode
	qr, err := goqrcode.New(content, goqrcode.Medium)
	if err != nil {
		return "(QR code regenerate failed)"
	}
	return qr.ToSmallString(false)
}

func maxSignal(v, min int) int {
	if v > min {
		return v
	}
	return min
}

func clampSignalSelection(selected, total int) int {
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

func defaultSignalTargetID(workspace string) string {
	if workspace == "" {
		return ""
	}
	return filepath.Base(workspace)
}

func (m Model) renderSignalPanel() string {
	panel := m.signalPanel
	if panel == nil {
		return ""
	}

	entries := m.signalBindingEntries()
	currentBindings := currentSignalBindings(m.imManager)
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}

	wsPath := m.currentWorkspacePath()
	if wsPath == "" {
		wsPath = m.t("panel.signal.none")
	}

	body := []string{}

	body = append(body,
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.directory")),
		fmt.Sprintf(" %s", wsPath),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.bots")),
		fmt.Sprintf(" %s", m.t("panel.signal.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.signal.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.signal.available", maxSignal(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.current_binding")),
	)

	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.signal.none")))
	} else {
		for _, current := range currentBindings {
			body = append(body,
				fmt.Sprintf(" %s", m.t("panel.signal.adapter", current.Adapter)),
				fmt.Sprintf(" %s", m.t("panel.signal.target", util.FirstNonEmpty(current.TargetID, m.t("panel.signal.default")))),
				fmt.Sprintf(" %s", m.t("panel.signal.channel", util.FirstNonEmpty(current.ChannelID, m.t("panel.signal.none")))),
			)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.signal.no_bots")))
	} else {
		selected := clampSignalSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.signalBindingLabels(entries), selected, true))
		entry := entries[selected]
		status := m.t("panel.signal.entry.available")
		if entry.Disabled {
			status = m.t("panel.signal.entry.disabled")
		} else if entry.OccupiedBy != "" {
			status = m.t("panel.signal.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.details")),
			fmt.Sprintf(" %s", m.t("panel.signal.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.signal.status", status)),
			fmt.Sprintf(" %s", m.t("panel.signal.transport", m.sigAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.signal.bound_directory", util.FirstNonEmpty(entry.OccupiedBy, m.t("panel.signal.none")))),
			fmt.Sprintf(" %s", m.t("panel.signal.current_directory_target", util.FirstNonEmpty(entry.TargetID, defaultSignalTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.signal.current_directory_channel", util.FirstNonEmpty(entry.WorkspaceChannel, m.t("panel.signal.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.signal.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.signal.occupied_by", entry.OccupiedBy)))
		}
	}

	// Daemon status
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.daemon")))
	if panel.installing {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.signal.daemon_installing")))
	} else if panel.daemonOK == nil {
		body = append(body, " "+m.t("panel.signal.daemon_checking"))
	} else if *panel.daemonOK {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(" "+m.t("panel.signal.daemon_ok")))
	} else {
		body = append(body,
			lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(" "+m.t("panel.signal.daemon_unavailable")),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.signal.daemon_install_hint")),
		)
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.signal.bot_input", panel.createInput+"█"),
			" "+m.t("panel.signal.create_format"),
			" "+m.t("panel.signal.create_example"),
			renderPasteShortcutHint(m.currentLanguage()),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.signal.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.signal.actions_hint")))
	}

	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+panel.message))
	}

	return m.renderContextBox("/signal", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) sigAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.signal.status.not_started")
	}
	if state.Healthy {
		return "online"
	}
	if state.LastError != "" {
		return state.LastError
	}
	return m.t("panel.signal.status.unknown")
}

func (m *Model) handleSignalPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.signalPanel
	if panel == nil {
		return *m, nil
	}
	if panel.installing {
		return *m, nil
	}
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.signalBindingEntries()

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
			return *m, m.createSigAdapterCmd(spec)
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
			panel.message = m.t("panel.signal.message.no_bot")
			return *m, nil
		}
		return *m, m.bindSigEntry(entries[clampSignalSelection(panel.selected, len(entries))])
	case "x", "X":
		if len(entries) == 0 {
			panel.message = m.t("panel.signal.message.no_bot")
			return *m, nil
		}
		return *m, m.clearSigChannel(entries[clampSignalSelection(panel.selected, len(entries))].Adapter)
	case "u", "U":
		if len(entries) == 0 {
			panel.message = m.t("panel.signal.message.no_bot")
			return *m, nil
		}
		return *m, m.unbindSigEntry(entries[clampSignalSelection(panel.selected, len(entries))].Adapter)
	case "d", "D":
		panel.installing = true
		panel.message = ""
		return *m, m.installSignalDaemon()
	case "r", "R":
		panel.daemonOK = nil
		return *m, checkSignalDaemonCmd()
	case "q", "Q":
		if panel.daemonOK != nil && *panel.daemonOK {
			panel.qrFetching = true
			panel.qrError = ""
			panel.qrCode = ""
			return *m, fetchSignalQRCmd("")
		}
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		return *m, nil
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.signal.message.no_bot")
			return *m, nil
		}
		entry := entries[clampSignalSelection(panel.selected, len(entries))]
		edit := m.enterIMEditSelect(entry.Adapter)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "t":
		if len(entries) == 0 {
			panel.message = m.t("panel.signal.message.no_bot")
			return *m, nil
		}
		entry := entries[clampSignalSelection(panel.selected, len(entries))]
		return *m, m.toggleIMAdapterEnabled(entry.Adapter)
	case "esc", "ctrl+c":
		m.closeSignalPanel()
		return *m, nil
	}
	return *m, nil
}

func (m *Model) createSigAdapterCmd(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return signalBindResultMsg{err: errors.New(m.t("panel.signal.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 3 {
			return signalBindResultMsg{err: errors.New(m.t("panel.signal.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		baseURL := strings.TrimSpace(fields[1])
		account := strings.TrimSpace(fields[2])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformSignal),
			Extra: map[string]interface{}{
				"base_url": baseURL,
				"account":  account,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return signalBindResultMsg{err: err}
		}
		if err := m.ensureSigRuntime(); err != nil {
			return signalBindResultMsg{err: err}
		}
		if err := m.startSigAdapterIfNeeded(name); err != nil {
			return signalBindResultMsg{err: err}
		}
		return signalBindResultMsg{message: m.t("panel.signal.message.added_bot", name)}
	}
}

func (m *Model) startSigAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.signal.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf(m.t("panel.signal.error.not_configured"), name)
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
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformSignal)) {
		return fmt.Errorf(m.t("panel.signal.error.not_signal_adapter"), name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m *Model) ensureSigRuntime() error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.signal.error.config_unavailable"))
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

func (m *Model) startSignalAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.signal.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf(m.t("panel.signal.error.not_configured"), name)
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
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformSignal)) {
		return fmt.Errorf(m.t("panel.signal.error.not_signal_adapter"), name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m *Model) bindSigEntry(entry signalBindingEntry) tea.Cmd {
	return func() tea.Msg {
		ws := m.currentWorkspacePath()
		if ws == "" {
			return signalBindResultMsg{err: errors.New(m.t("panel.signal.none"))}
		}
		if m.imManager == nil {
			return signalBindResultMsg{err: errors.New(m.t("panel.signal.error.config_unavailable"))}
		}
		if err := m.startSignalAdapterIfNeeded(entry.Adapter); err != nil {
			return signalBindResultMsg{err: err}
		}
		targetID := defaultSignalTargetID(ws)
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Workspace: ws,
			Platform:  im.PlatformSignal,
			Adapter:   entry.Adapter,
			TargetID:  targetID,
		})
		if err != nil {
			return signalBindResultMsg{err: err}
		}
		return signalBindResultMsg{message: m.t("panel.signal.message.bound_success")}
	}
}

func (m *Model) unbindSigEntry(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return signalBindResultMsg{}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return signalBindResultMsg{err: err}
		}
		return signalBindResultMsg{message: m.t("panel.signal.message.unbound")}
	}
}

func (m *Model) clearSigChannel(adapterName string) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return signalBindResultMsg{}
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
		return signalBindResultMsg{message: m.t("panel.signal.message.cleared")}
	}
}

func (m Model) signalBindingEntries() []signalBindingEntry {
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
		for _, b := range currentSignalBindings(m.imManager) {
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
		if strings.EqualFold(adapter.Platform, string(im.PlatformSignal)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]signalBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultSignalTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = util.FirstNonEmpty(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		var statePtr *im.AdapterState
		if s, ok := adapterStates[name]; ok {
			statePtr = &s
		}
		entries = append(entries, signalBindingEntry{
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

func (m Model) signalBindingLabels(entries []signalBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Disabled:
			status = m.t("panel.signal.entry.disabled")
		case entry.Disabled:
			status = m.t("panel.signal.entry.disabled")
		case entry.Muted:
			status = m.t("panel.signal.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.signal.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.signal.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.signal.entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func currentSignalBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	bindings, err := mgr.ListBindings()
	if err != nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range bindings {
		if b.Platform == im.PlatformSignal {
			result = append(result, b)
		}
	}
	return result
}
