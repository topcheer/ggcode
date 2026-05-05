package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
)

const wechatILinkBaseURL = "https://ilinkai.weixin.qq.com"

// wechatPanelState tracks the WeChat iLink panel UI state.
type wechatPanelState struct {
	selected    int
	message     string
	authPhase   string // "", "requesting", "showing_qr", "polling", "confirmed"
	qrcodeToken string
	qrcodeImage string // terminal-rendered QR (Unicode block chars)
	botToken    string
	editState   imAdapterEditState
}

type wechatBindingEntry struct {
	Adapter      string
	TargetID     string
	ChannelID    string
	OccupiedBy   string
	AdapterState *im.AdapterState
	Muted        bool
	Bound        bool
}

type wechatQRCodeMsg struct {
	qrcodeToken string
	qrcodeImage string
	err         error
}

type wechatQRPollMsg struct {
	status   string
	botToken string
	err      error
}

func (m *Model) openWechatPanel() {
	m.wechatPanel = &wechatPanelState{}
}

func (m *Model) closeWechatPanel() {
	m.wechatPanel = nil
}

func (m Model) renderWechatPanel() string {
	panel := m.wechatPanel
	if panel == nil {
		return ""
	}

	entries := m.wechatBindingEntries()
	boundCount := 0
	for _, entry := range entries {
		if entry.Bound {
			boundCount++
		}
	}

	currentWS := m.currentWorkspacePath()
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.wechat.directory")),
		fmt.Sprintf(" %s", nonEmptyFirst(currentWS, m.t("panel.wechat.none"))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.wechat.bots")),
		fmt.Sprintf(" %s", m.t("panel.wechat.created", len(entries))),
		fmt.Sprintf(" %s", m.t("panel.wechat.bound", boundCount)),
		fmt.Sprintf(" %s", m.t("panel.wechat.available", maxInt(len(entries)-boundCount, 0))),
		"",
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.wechat.bot_list")),
	}

	if len(entries) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.wechat.no_bots")))
	} else {
		for i, entry := range entries {
			cursor := "  "
			if i == panel.selected {
				cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render("❯ ")
			}
			stateStr := ""
			if entry.AdapterState != nil {
				switch entry.AdapterState.Status {
				case "connected":
					stateStr = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(" ●")
				case "waiting_for_auth":
					stateStr = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" ○")
				default:
					stateStr = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(" ○")
				}
			}
			mutedStr := ""
			if entry.Muted {
				mutedStr = " [muted]"
			}
			body = append(body, fmt.Sprintf("%s%s%s%s", cursor, entry.Adapter, stateStr, mutedStr))
			if entry.OccupiedBy != "" {
				body = append(body, fmt.Sprintf("   → %s", entry.OccupiedBy))
			}
		}
	}

	// QR code auth section — render separately so we can place it at top
	var qrSection []string
	if panel.authPhase == "showing_qr" || panel.authPhase == "polling" {
		qrSection = append(qrSection, "",
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render(m.t("panel.wechat.scan_qr")),
		)
		if panel.qrcodeImage != "" {
			qrSection = append(qrSection, panel.qrcodeImage)
		}
		if panel.authPhase == "polling" {
			qrSection = append(qrSection, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("panel.wechat.waiting_scan")))
		}
	} else if panel.authPhase == "confirmed" {
		qrSection = append(qrSection, "",
			lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(m.t("panel.wechat.auth_confirmed")),
		)
	}

	// Build body with QR at top if present
	if len(qrSection) > 0 {
		body = append(append(qrSection, ""), body...)
	}

	// Edit mode
	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	// Help
	help := m.t("panel.wechat.help")
	body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(help))

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}

	return m.renderContextBox("/wechat", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) handleWechatPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.wechatPanel
	if panel == nil {
		return *m, nil
	}

	// Edit mode intercepts all keys
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.wechatBindingEntries()

	switch msg.String() {
	case "esc", "q":
		m.closeWechatPanel()
		return *m, nil
	case "up", "k":
		if len(entries) > 0 {
			panel.selected = (panel.selected - 1 + len(entries)) % len(entries)
		}
		panel.message = ""
	case "down", "j":
		if len(entries) > 0 {
			panel.selected = (panel.selected + 1) % len(entries)
		}
		panel.message = ""
	case "a":
		// Start QR auth flow — no pre-existing adapter needed
		panel.authPhase = "requesting"
		panel.message = ""
		return *m, m.requestWechatQRCode()
	case "enter", "b":
		// Bind selected bot to current workspace
		if len(entries) > 0 && panel.selected < len(entries) {
			entry := entries[panel.selected]
			return *m, m.bindWechatEntry(entry)
		}
	case "e":
		if len(entries) > 0 && panel.selected < len(entries) {
			entry := entries[panel.selected]
			panel.editState = imAdapterEditState{
				mode:          imEditSelect,
				adapterName:   entry.Adapter,
				originalExtra: make(map[string]string),
			}
		}
	case "r":
		// Remove: unbind + stop adapter + delete config
		if len(entries) > 0 && panel.selected < len(entries) {
			entry := entries[panel.selected]
			return *m, m.removeWechatEntry(entry)
		}
	}
	return *m, nil
}

// ---- iLink HTTP helpers (no adapter dependency) ----

func wechatILinkRequest(ctx context.Context, method, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// requestWechatQRCode requests a QR code directly from iLink (no adapter needed).
func (m *Model) requestWechatQRCode() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		url := wechatILinkBaseURL + "/ilink/bot/get_bot_qrcode?bot_type=3"
		data, err := wechatILinkRequest(ctx, http.MethodGet, url)
		if err != nil {
			return wechatQRCodeMsg{err: fmt.Errorf("QR code request failed: %w", err)}
		}

		var resp struct {
			QRCode           string `json:"qrcode"`
			QRCodeImgContent string `json:"qrcode_img_content"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return wechatQRCodeMsg{err: fmt.Errorf("QR code decode failed: %w", err)}
		}
		if resp.QRCode == "" {
			return wechatQRCodeMsg{err: fmt.Errorf("empty QR code token in response")}
		}

		// qrcode_img_content is the text content (URL) for generating QR,
		// NOT a base64-encoded image. Use go-qrcode to render it.
		qrImage, err := renderQRFromString(resp.QRCodeImgContent)
		if err != nil {
			debug.Log("wechat", "QR render failed: %v", err)
			qrImage = fmt.Sprintf("[QR render error: %v]", err)
		}
		return wechatQRCodeMsg{qrcodeToken: resp.QRCode, qrcodeImage: qrImage}
	}
}

// pollWechatQRStatus polls the QR code scan status directly (no adapter needed).
func (m *Model) pollWechatQRStatus(qrcodeToken string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		url := wechatILinkBaseURL + "/ilink/bot/get_qrcode_status?qrcode=" + qrcodeToken
		data, err := wechatILinkRequest(ctx, http.MethodGet, url)
		if err != nil {
			return wechatQRPollMsg{err: err}
		}

		var resp struct {
			Status   string `json:"status"`
			BotToken string `json:"bot_token"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return wechatQRPollMsg{err: fmt.Errorf("decode status: %w", err)}
		}
		return wechatQRPollMsg{status: resp.Status, botToken: resp.BotToken}
	}
}

// ---- QR message handlers ----

func (m *Model) handleWechatQRCodeMsg(msg wechatQRCodeMsg) (Model, tea.Cmd) {
	panel := m.wechatPanel
	if panel == nil {
		return *m, nil
	}
	if msg.err != nil {
		panel.authPhase = ""
		panel.message = fmt.Sprintf("QR code error: %v", msg.err)
		return *m, nil
	}
	panel.qrcodeToken = msg.qrcodeToken
	panel.qrcodeImage = msg.qrcodeImage
	panel.authPhase = "showing_qr"
	panel.message = ""
	return *m, m.pollWechatQRStatus(msg.qrcodeToken)
}

func (m *Model) handleWechatQRPollMsg(msg wechatQRPollMsg) (Model, tea.Cmd) {
	panel := m.wechatPanel
	if panel == nil {
		return *m, nil
	}

	switch msg.status {
	case "confirmed":
		panel.authPhase = "confirmed"
		panel.botToken = msg.botToken
		// Auto-create adapter config and start it
		return *m, m.saveWechatBotToken(msg.botToken)
	case "scanned":
		panel.authPhase = "polling"
		panel.message = m.t("panel.wechat.scanned")
		return *m, m.pollWechatQRStatus(panel.qrcodeToken)
	case "wait", "":
		panel.authPhase = "polling"
		return *m, m.pollWechatQRStatus(panel.qrcodeToken)
	default:
		if msg.err != nil {
			panel.message = fmt.Sprintf("Poll error: %v", msg.err)
			return *m, m.pollWechatQRStatus(panel.qrcodeToken)
		}
		return *m, nil
	}
}

// saveWechatBotToken creates the wechat adapter config and starts the adapter.
func (m *Model) saveWechatBotToken(botToken string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			debug.Log("wechat", "saveWechatBotToken: config is nil")
			return imEditResultMsg{err: fmt.Errorf("config unavailable")}
		}

		// Auto-generate name: wechat → wechat-2 → wechat-3 ...
		adapterName := "wechat"
		n := 2
		for {
			if _, exists := m.config.IM.Adapters[adapterName]; !exists {
				break
			}
			adapterName = fmt.Sprintf("wechat-%d", n)
			n++
		}

		if err := m.config.AddIMAdapter(adapterName, config.IMAdapterConfig{
			Enabled:  true,
			Platform: "wechat",
			Extra: map[string]interface{}{
				"bot_token": botToken,
			},
		}); err != nil {
			debug.Log("wechat", "saveWechatBotToken: AddIMAdapter failed: %v", err)
			return imEditResultMsg{err: fmt.Errorf("create adapter config: %w", err)}
		}
		debug.Log("wechat", "saveWechatBotToken: adapter %q created and saved", adapterName)

		// Start the adapter (binding happens when the first inbound message triggers pairing)
		if m.imManager != nil {
			if err := im.StartNamedAdapter(context.Background(), m.config.IM, adapterName, m.imManager); err != nil {
				debug.Log("wechat", "saveWechatBotToken: StartNamedAdapter failed: %v", err)
			} else {
				debug.Log("wechat", "saveWechatBotToken: adapter %q started", adapterName)
			}
		} else {
			debug.Log("wechat", "saveWechatBotToken: imManager is nil, adapter not started")
		}

		return imEditResultMsg{adapterName: adapterName, field: "bot_token", value: "***"}
	}
}

// bindWechatEntry binds the selected bot to the current workspace.
// bindWechatEntry binds the wechat adapter to the current workspace.
func (m *Model) startWechatAdapterIfNeeded(name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("adapter name required")
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return fmt.Errorf("wechat adapter %s not configured", name)
	}
	if !adapterCfg.Enabled {
		return fmt.Errorf("wechat adapter %s is disabled", name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

// This only registers the workspace association — the ChannelID/TargetID
// are set later when the first inbound message triggers the pairing flow.
func (m *Model) bindWechatEntry(entry wechatBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return imEditResultMsg{err: fmt.Errorf("IM manager not available")}
		}
		ws := m.currentWorkspacePath()
		if ws == "" {
			return imEditResultMsg{err: fmt.Errorf("no active workspace")}
		}
		if err := m.startWechatAdapterIfNeeded(entry.Adapter); err != nil {
			return imEditResultMsg{err: err}
		}
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Workspace: ws,
			Platform:  im.PlatformWechat,
			Adapter:   entry.Adapter,
			TargetID:  defaultWechatTargetID(ws),
		})
		if err != nil {
			return imEditResultMsg{err: err}
		}
		return imEditResultMsg{adapterName: entry.Adapter, field: "bind", value: ws}
	}
}

// removeWechatEntry fully removes a bot: unbind, stop adapter, delete config.
func (m *Model) removeWechatEntry(entry wechatBindingEntry) tea.Cmd {
	return func() tea.Msg {
		// 1. Unbind + stop (ignore error if no binding exists)
		if m.imManager != nil {
			m.imManager.StopAdapter(entry.Adapter)
			_ = m.imManager.UnbindAdapter(entry.Adapter)
		}
		// 2. Remove from config
		if m.config != nil {
			if err := m.config.RemoveIMAdapter(entry.Adapter); err != nil {
				return imEditResultMsg{err: fmt.Errorf("remove config: %w", err)}
			}
		}
		return imEditResultMsg{adapterName: entry.Adapter, field: "remove", value: "ok"}
	}
}

// ---- QR rendering ----

// renderQRFromString generates a terminal-rendered QR code from a text string (URL).
// Uses go-qrcode to generate the bitmap, then renders as Unicode half-block chars.
func renderQRFromString(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("empty QR content")
	}
	code, err := qrcode.New(strings.TrimSpace(content), qrcode.Low)
	if err != nil {
		return "", fmt.Errorf("generate QR: %w", err)
	}
	bitmap := code.Bitmap()
	moduleCount := len(bitmap)
	if moduleCount == 0 {
		return "", fmt.Errorf("empty QR bitmap")
	}

	// Ensure even row count for half-block rendering
	if moduleCount%2 == 1 {
		padding := make([]bool, moduleCount)
		bitmap = append(bitmap, padding)
		moduleCount++
	}

	const (
		whiteAll   = "█"
		whiteBlack = "▀"
		blackWhite = "▄"
		blackAll   = " "
	)

	var lines []string
	// Top border
	lines = append(lines, strings.Repeat(whiteAll, moduleCount+2))

	for row := 0; row < moduleCount; row += 2 {
		var b strings.Builder
		b.WriteString(whiteAll)
		for col := 0; col < len(bitmap[row]); col++ {
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
	// Bottom border
	lines = append(lines, strings.Repeat(whiteAll, moduleCount+2))

	return strings.Join(lines, "\n"), nil
}

// ---- Binding entries ----

func (m *Model) wechatBindingEntries() []wechatBindingEntry {
	var entries []wechatBindingEntry
	if m.config == nil {
		return entries
	}

	// Collect runtime adapter states
	adapterStates := make(map[string]im.AdapterState)
	if m.imManager != nil {
		snapshot := m.imManager.Snapshot()
		for _, state := range snapshot.Adapters {
			if state.Name == "" {
				continue
			}
			adapterStates[state.Name] = state
		}
	}

	// Check which workspaces have bindings (for showing "occupied by other workspace")
	// Track bound adapters (any workspace, including current)
	bound := make(map[string]bool)
	// Track adapters bound to a DIFFERENT workspace (for "occupied by" label)
	occupied := make(map[string]string)
	wsPath := m.currentWorkspacePath()
	if m.imManager != nil {
		if bindings, err := m.imManager.ListBindings(); err == nil {
			for _, b := range bindings {
				if b.Platform == im.PlatformWechat && b.Workspace != "" {
					bound[b.Adapter] = true
					if b.Workspace != wsPath {
						occupied[b.Adapter] = b.Workspace
					}
				}
			}
		}
	}

	// List all wechat adapters from config (not just bound ones)
	keys := make([]string, 0, len(m.config.IM.Adapters))
	for name, adapter := range m.config.IM.Adapters {
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformWechat)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)

	for _, name := range keys {
		var state *im.AdapterState
		if s, ok := adapterStates[name]; ok {
			state = &s
		}
		entries = append(entries, wechatBindingEntry{
			Adapter:      name,
			OccupiedBy:   occupied[name],
			AdapterState: state,
			Bound:        bound[name],
		})
	}
	return entries
}

func nonEmptyFirst(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func defaultWechatTargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}
