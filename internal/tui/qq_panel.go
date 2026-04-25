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
	qrcode "github.com/skip2/go-qrcode"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

type qqPanelState struct {
	selected     int
	message      string
	createMode   bool
	createInput  string
	shareAdapter string
	shareLink    string
	shareQRCode  string
	editState    imAdapterEditState
}

type qqBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Muted            bool
}

type qqBindResultMsg struct {
	message      string
	shareAdapter string
	shareLink    string
	shareQRCode  string
	err          error
}

func (m *Model) openQQPanel() {
	m.qqPanel = &qqPanelState{}
}

func (m *Model) closeQQPanel() {
	m.qqPanel = nil
}

func (m Model) renderQQPanel() string {
	panel := m.qqPanel
	if panel == nil {
		return ""
	}
	entries := m.qqBindingEntries()
	currentBindings := currentQQBindings(m.imManager)
	currentWS := m.currentWorkspacePath()
	boundCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.OccupiedBy) != "" {
			boundCount++
		}
	}
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.qq.directory")),
		fmt.Sprintf(" %s", firstNonEmptyQQ(currentWS, m.t("panel.qq.none"))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.qq.bots")),
		fmt.Sprintf(" %s", m.t("panel.qq.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.qq.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.qq.available", maxQQ(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.qq.current_binding")),
	}
	if len(currentBindings) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.qq.none")))
	} else {
		for _, current := range currentBindings {
			status := "active"
			if current.Muted {
				status = "muted"
			}
			body = append(body,
				fmt.Sprintf(" %s (%s)", current.Adapter, status),
				fmt.Sprintf(" %s", m.t("panel.qq.target", firstNonEmptyQQ(current.TargetID, m.t("panel.qq.default")))),
				fmt.Sprintf(" %s", m.t("panel.qq.channel", firstNonEmptyQQ(current.ChannelID, m.t("panel.qq.none")))),
			)
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.qq.bot_list")))
	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.qq.no_bots")))
	} else {
		selected := clampQQSelection(panel.selected, len(entries))
		body = append(body, m.renderProviderList(m.qqBindingLabels(entries), selected, true))
		entry := entries[selected]
		currentWS := m.currentWorkspacePath()
		var status string
		switch {
		case entry.Muted:
			status = m.t("panel.qq.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.qq.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.qq.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.qq.entry.available")
		}
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.qq.details")),
			fmt.Sprintf(" %s", m.t("panel.qq.adapter", entry.Adapter)),
			fmt.Sprintf(" %s", m.t("panel.qq.status", status)),
			fmt.Sprintf(" %s", m.t("panel.qq.transport", m.qqAdapterStatus(entry.AdapterState))),
			fmt.Sprintf(" %s", m.t("panel.qq.bound_directory", firstNonEmptyQQ(entry.OccupiedBy, m.t("panel.qq.none")))),
			fmt.Sprintf(" %s", m.t("panel.qq.current_directory_target", firstNonEmptyQQ(entry.TargetID, defaultQQTargetID(m.currentWorkspacePath())))),
			fmt.Sprintf(" %s", m.t("panel.qq.current_directory_channel", firstNonEmptyQQ(entry.WorkspaceChannel, m.t("panel.qq.waiting_for_pairing")))),
		)
		if entry.AdapterState != nil && strings.TrimSpace(entry.AdapterState.LastError) != "" {
			body = append(body, fmt.Sprintf(" %s", m.t("panel.qq.last_error", strings.TrimSpace(entry.AdapterState.LastError))))
		}
		if entry.OccupiedBy != "" && entry.OccupiedBy != m.currentWorkspacePath() {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" "+m.t("panel.qq.occupied_by", entry.OccupiedBy)))
		}
	}
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(m.t("panel.qq.create")))
	if panel.createMode {
		body = append(body,
			" "+m.t("panel.qq.bot_input", panel.createInput+"█"),
			" "+m.t("panel.qq.create_format"),
			" "+m.t("panel.qq.create_example"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.qq.create_hint")),
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.qq.actions_hint")))
	}
	// Edit config section
	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}
	if strings.TrimSpace(panel.shareLink) != "" {
		body = append(body,
			"",
			lipgloss.NewStyle().Bold(true).Render(m.t("panel.qq.bind_channel")),
			fmt.Sprintf(" %s", m.t("panel.qq.adapter", firstNonEmptyQQ(panel.shareAdapter, m.t("panel.qq.none")))),
			" "+m.t("panel.qq.scan_hint"),
		)
		if qr := strings.TrimRight(panel.shareQRCode, "\n"); qr != "" {
			body = append(body, " "+m.t("panel.qq.qr_code"), indentQQBlock(qr, "  "))
		}
		body = append(body, " "+m.t("panel.qq.share_link"), indentQQBlock(wrapQQLink(panel.shareLink, maxQQ(m.boxInnerWidth(m.mainColumnWidth())-6, 24)), "  "))
	}
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return m.renderContextBox("/qq", strings.Join(body, "\n"), lipgloss.Color("13"))
}

func (m *Model) handleQQPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.qqPanel
	if panel == nil {
		return *m, nil
	}
	// Edit mode takes priority
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}
	entries := m.qqBindingEntries()
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
			return *m, m.createQQAdapter(spec)
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
		clearQQPanelShare(panel)
	case "down", "j", "tab":
		if len(entries) > 0 {
			panel.selected = (panel.selected + 1) % len(entries)
		}
		clearQQPanelShare(panel)
	case "enter", "b", "B":
		if len(entries) == 0 {
			panel.message = m.t("panel.qq.message.no_bot")
			return *m, nil
		}
		clearQQPanelShare(panel)
		return *m, m.bindQQEntry(entries[clampQQSelection(panel.selected, len(entries))])
	case "c", "C":
		if len(entries) == 0 {
			panel.message = m.t("panel.qq.message.no_bot")
			return *m, nil
		}
		return *m, m.generateQQShareLink(entries[clampQQSelection(panel.selected, len(entries))])
	case "x", "X":
		clearQQPanelShare(panel)
		return *m, m.clearQQChannel()
	case "u", "U":
		clearQQPanelShare(panel)
		return *m, m.unbindQQEntry()
	case "i", "I":
		panel.createMode = true
		panel.createInput = ""
		panel.message = ""
		clearQQPanelShare(panel)
		return *m, nil
	case "e", "E":
		if len(entries) == 0 {
			panel.message = m.t("panel.qq.message.no_bot")
			return *m, nil
		}
		clearQQPanelShare(panel)
		entry := entries[clampQQSelection(panel.selected, len(entries))]
		edit := m.enterIMEditSelect(entry.Adapter)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "esc":
		m.closeQQPanel()
	}
	return *m, nil
}

func (m *Model) bindQQEntry(entry qqBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureQQBotBinding(entry.Adapter); err != nil {
			return qqBindResultMsg{err: err}
		}
		if m.agent != nil {
			if err := m.waitForAdapterHealthy(m.imManager, entry.Adapter, 10*time.Second); err != nil {
				return qqBindResultMsg{err: err}
			}
			if err := m.imManager.SyncSessionHistory(context.Background(), m.agent.Messages()); err != nil && err != im.ErrNoChannelBound {
				return qqBindResultMsg{err: err}
			}
		}
		return qqBindResultMsg{message: m.t("panel.qq.message.bound_success")}
	}
}

func (m *Model) generateQQShareLink(entry qqBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureQQBotBinding(entry.Adapter); err != nil {
			return qqBindResultMsg{err: err}
		}
		callbackData := qqShareCallbackData(m.currentWorkspacePath())
		link, err := m.imManager.GenerateShareLink(context.Background(), entry.Adapter, callbackData)
		if err != nil {
			return qqBindResultMsg{err: err}
		}
		qr, err := renderQQShareQRCode(link)
		if err != nil {
			return qqBindResultMsg{err: err}
		}
		return qqBindResultMsg{
			message:      m.t("panel.qq.message.share_generated"),
			shareAdapter: entry.Adapter,
			shareLink:    link,
			shareQRCode:  qr,
		}
	}
}

func (m *Model) unbindQQEntry() tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureQQRuntime(true); err != nil {
			return qqBindResultMsg{err: err}
		}
		if err := m.imManager.UnbindChannel(m.currentWorkspacePath()); err != nil {
			return qqBindResultMsg{err: err}
		}
		return qqBindResultMsg{message: m.t("panel.qq.message.unbound")}
	}
}

func (m *Model) clearQQChannel() tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureQQRuntime(true); err != nil {
			return qqBindResultMsg{err: err}
		}
		if err := m.imManager.ClearChannel(m.currentWorkspacePath()); err != nil {
			return qqBindResultMsg{err: err}
		}
		return qqBindResultMsg{message: m.t("panel.qq.message.cleared")}
	}
}

func (m *Model) createQQAdapter(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return qqBindResultMsg{err: errors.New(m.t("panel.qq.error.config_unavailable"))}
		}
		fields := strings.Fields(spec)
		if len(fields) < 3 {
			return qqBindResultMsg{err: errors.New(m.t("panel.qq.error.config_format"))}
		}
		name := strings.TrimSpace(fields[0])
		appID := strings.TrimSpace(fields[1])
		appSecret := strings.TrimSpace(fields[2])
		adapter := config.IMAdapterConfig{
			Enabled:  true,
			Platform: string(im.PlatformQQ),
			Extra: map[string]interface{}{
				"appid":     appID,
				"appsecret": appSecret,
			},
		}
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return qqBindResultMsg{err: err}
		}
		if err := m.ensureQQRuntime(false); err != nil {
			return qqBindResultMsg{err: err}
		}
		if err := m.startQQAdapterIfNeeded(name); err != nil {
			return qqBindResultMsg{err: err}
		}
		return qqBindResultMsg{message: m.t("panel.qq.message.added_bot", name)}
	}
}

func (m *Model) startQQAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel.qq.error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return errors.New(m.t("panel.qq.error.not_configured", name))
	}
	if !adapterCfg.Enabled {
		return errors.New(m.t("panel.qq.error.disabled", name))
	}
	if !strings.EqualFold(adapterCfg.Platform, string(im.PlatformQQ)) {
		return errors.New(m.t("panel.qq.error.not_qq_adapter", name))
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

func (m Model) qqBindingEntries() []qqBindingEntry {
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
		for _, b := range currentQQBindings(m.imManager) {
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
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformQQ)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]qqBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultQQTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = firstNonEmptyQQ(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		entries = append(entries, qqBindingEntry{
			Adapter:          name,
			Label:            name,
			TargetID:         targetID,
			WorkspaceChannel: workspaceChannel,
			OccupiedBy:       occupied[name],
			AdapterState:     qqStatePtr(adapterStates[name]),
			Muted:            bindingByAdapter[name].Muted,
		})
	}
	return entries
}

func (m Model) qqBindingLabels(entries []qqBindingEntry) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Muted:
			status = m.t("panel.qq.entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel.qq.entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel.qq.entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel.qq.entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func clampQQSelection(selected, total int) int {
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

func currentQQBindings(mgr *im.Manager) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range mgr.CurrentBindings() {
		if b.Platform == im.PlatformQQ {
			result = append(result, b)
		}
	}
	return result
}

func (m Model) currentWorkspacePath() string {
	if m.session != nil {
		return m.session.Workspace
	}
	return ""
}

func firstNonEmptyQQ(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultQQTargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}

func clearQQPanelShare(panel *qqPanelState) {
	if panel == nil {
		return
	}
	panel.shareAdapter = ""
	panel.shareLink = ""
	panel.shareQRCode = ""
}

func qqShareCallbackData(workspace string) string {
	callbackData := defaultQQTargetID(workspace)
	if len(callbackData) > 32 {
		return ""
	}
	return callbackData
}

func renderQQShareQRCode(link string) (string, error) {
	code, err := qrcode.New(strings.TrimSpace(link), qrcode.Low)
	if err != nil {
		return "", err
	}
	bitmap := code.Bitmap()
	if len(bitmap) == 0 {
		return "", fmt.Errorf("QQ share QR bitmap is empty")
	}

	moduleCount := len(bitmap)
	if moduleCount == 0 {
		return "", fmt.Errorf("QQ share QR bitmap is empty")
	}
	if moduleCount%2 == 1 {
		padding := make([]bool, moduleCount)
		bitmap = append(bitmap, padding)
	}

	const (
		whiteAll   = "█"
		whiteBlack = "▀"
		blackWhite = "▄"
		blackAll   = " "
	)

	borderTop := strings.Repeat(blackWhite, moduleCount+3)
	borderBottom := strings.Repeat(whiteBlack, moduleCount+3)
	var lines []string

	lines = append(lines, borderTop)
	for row := 0; row < moduleCount; row += 2 {
		var b strings.Builder
		b.WriteString(whiteAll)
		for col := 0; col < moduleCount; col++ {
			top := bitmap[row][col]
			bottom := bitmap[row+1][col]
			switch {
			case !top && !bottom:
				b.WriteString(whiteAll)
			case !top && bottom:
				b.WriteString(whiteBlack)
			case top && !bottom:
				b.WriteString(blackWhite)
			default:
				b.WriteString(blackAll)
			}
		}
		b.WriteString(whiteAll)
		lines = append(lines, b.String())
	}
	lines = append(lines, borderBottom)
	return strings.Join(lines, "\n"), nil
}

func wrapQQLink(text string, width int) string {
	text = strings.TrimSpace(text)
	if text == "" || width <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	lines := make([]string, 0, (len(runes)+width-1)/width)
	for start := 0; start < len(runes); start += width {
		end := start + width
		if end > len(runes) {
			end = len(runes)
		}
		lines = append(lines, string(runes[start:end]))
	}
	return strings.Join(lines, "\n")
}

func indentQQBlock(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (m *Model) ensureQQBotBinding(adapter string) error {
	if err := m.ensureQQRuntime(true); err != nil {
		return err
	}
	if err := m.startQQAdapterIfNeeded(adapter); err != nil {
		return err
	}
	workspace := m.currentWorkspacePath()
	for _, b := range currentQQBindings(m.imManager) {
		if strings.TrimSpace(b.Workspace) == strings.TrimSpace(workspace) && b.Adapter == adapter {
			return nil
		}
	}
	_, err := m.imManager.BindChannel(im.ChannelBinding{
		Platform: im.PlatformQQ,
		Adapter:  adapter,
		TargetID: defaultQQTargetID(workspace),
	})
	return err
}

func (m *Model) ensureQQRuntime(autoEnable bool) error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(m.t("panel.qq.error.config_unavailable"))
	}
	if !m.config.IM.Enabled {
		if !autoEnable {
			return fmt.Errorf("%s", m.qqRuntimeStatus())
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

func (m Model) qqRuntimeStatus() string {
	if m.imManager != nil {
		return m.t("panel.qq.runtime.available")
	}
	if m.config == nil || !m.config.IM.Enabled {
		return m.t("panel.qq.runtime.disabled")
	}
	return m.t("panel.qq.runtime.not_started")
}

func maxQQ(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func minQQ(v, max int) int {
	if v > max {
		return max
	}
	return v
}

func (m Model) qqAdapterStatus(state *im.AdapterState) string {
	if state == nil {
		return m.t("panel.qq.status.not_started")
	}
	status := strings.TrimSpace(state.Status)
	if status == "" {
		status = m.t("panel.qq.status.unknown")
	}
	if state.Healthy {
		return status
	}
	return status
}

func qqStatePtr(state im.AdapterState) *im.AdapterState {
	if strings.TrimSpace(state.Name) == "" {
		return nil
	}
	copy := state
	return &copy
}

func (m Model) waitForAdapterHealthy(mgr *im.Manager, adapter string, timeout time.Duration) error {
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
			return errors.New(m.t("panel.qq.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.LastError)))
		}
		if strings.TrimSpace(lastStatus.Status) != "" {
			return errors.New(m.t("panel.qq.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.Status)))
		}
	}
	return errors.New(m.t("panel.qq.error.not_online", adapter))
}
