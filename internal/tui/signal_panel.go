package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
		cmd := exec.CommandContext(ctx, "sh", "-c", im.SignalDaemonInstallCommand())
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

func firstNonEmptySignal(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// renderQRCodeASCII converts a PNG QR code image to Unicode block-character ASCII art.
func renderQRCodeASCII(pngData []byte) string {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return "(QR code decode failed)"
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Sample the image into a black/white grid (2 rows per pixel using ▀/█/ space)
	// First find module size by scanning from top-left
	moduleSize := 1
	ox, oy := 0, 0
	// Find first black pixel
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if isBlack(img, x, y) {
				ox, oy = x, y
				break
			}
		}
		if ox > 0 {
			break
		}
	}
	// Count consecutive black pixels = module size
	for x := ox + 1; x < w; x++ {
		if isBlack(img, x, oy) {
			moduleSize++
		} else {
			break
		}
	}
	if moduleSize < 1 {
		moduleSize = 1
	}

	// Sample center of each module
	modulesW := w / moduleSize
	modulesH := h / moduleSize

	var b strings.Builder
	for y := 0; y < modulesH; y += 2 {
		for x := 0; x < modulesW; x++ {
			sx := ox + x*moduleSize + moduleSize/2
			syTop := oy + y*moduleSize + moduleSize/2
			syBot := oy + (y+1)*moduleSize + moduleSize/2

			top := sx < w && syTop < h && isBlack(img, sx, syTop)
			bot := sx < w && syBot < h && isBlack(img, sx, syBot)

			if top && bot {
				b.WriteString("█")
			} else if top {
				b.WriteString("▀")
			} else if bot {
				b.WriteString("▄")
			} else {
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func isBlack(img image.Image, x, y int) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	// Luminance threshold
	lum := int(0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b))
	return lum < 32768 // < 50% brightness = black
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

	// QR code at the top
	if panel.qrFetching {
		body = append(body,
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.qr_title")),
			" "+m.t("panel.signal.qr_fetching"),
		)
	} else if panel.qrError != "" {
		body = append(body,
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.qr_title")),
			lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(" "+panel.qrError),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.signal.qr_retry_hint")),
		)
	} else if panel.qrCode != "" {
		body = append(body,
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.qr_title")),
			"",
			panel.qrCode,
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.signal.qr_scan_hint")),
		)
	} else if panel.daemonOK != nil && !*panel.daemonOK {
		body = append(body,
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.qr_title")),
			lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(" "+m.t("panel.signal.qr_no_daemon")),
		)
	} else if panel.daemonOK != nil && *panel.daemonOK {
		body = append(body,
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.qr_title")),
			" "+m.t("panel.signal.qr_press_q"),
		)
	}

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
				fmt.Sprintf(" %s", m.t("panel.signal.target", firstNonEmptySignal(current.TargetID, m.t("panel.signal.default")))),
				fmt.Sprintf(" %s", m.t("panel.signal.channel", firstNonEmptySignal(current.ChannelID, m.t("panel.signal.none")))),
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
		if entry.OccupiedBy != "" {
			status = m.t("panel.signal.entry.bound")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.signal.details")),
			fmt.Sprintf(" %s", m.t("panel.signal.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.signal.status", status)),
			fmt.Sprintf(" %s", m.t("panel.signal.transport", m.sigAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.signal.bound_directory", firstNonEmptySignal(entry.OccupiedBy, m.t("panel.signal.none")))),
			fmt.Sprintf(" %s", m.t("panel.signal.current_directory_target", firstNonEmptySignal(entry.TargetID, defaultSignalTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.signal.current_directory_channel", firstNonEmptySignal(entry.WorkspaceChannel, m.t("panel.signal.waiting_for_pairing")))),
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
		case "esc":
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
	case "esc":
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
		return fmt.Errorf(m.t("panel.signal.error.disabled"), name)
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
		return fmt.Errorf(m.t("panel.signal.error.disabled"), name)
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformSignal)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]signalBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultSignalTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptySignal(b.TargetID, targetID)
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
