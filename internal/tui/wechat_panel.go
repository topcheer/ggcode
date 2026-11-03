package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/util"
)

const wechatPollMaxFailures = 8

const wechatILinkBaseURL = "https://ilinkai.weixin.qq.com"

type wechatPanelState struct {
	selected    int
	message     string
	authPhase   string
	qrcodeToken string
	qrcodeImage string
	botToken    string
	editState   imAdapterEditState

	pollFailures int
}

type wechatBindingEntry struct {
	Adapter      string
	TargetID     string
	ChannelID    string
	OccupiedBy   string
	AdapterState *im.AdapterState
	Disabled     bool
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
				case "connecting":

					stateStr = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(" ◐")
				default:
					stateStr = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(" ○")
				}
			}
			disabledStr := ""
			if entry.Disabled {
				disabledStr = " [disabled]"
			}
			mutedStr := ""
			if entry.Muted {
				mutedStr = " [muted]"
			}
			body = append(body, fmt.Sprintf("%s%s%s%s%s", cursor, entry.Adapter, stateStr, disabledStr, mutedStr))
			if entry.OccupiedBy != "" {
				body = append(body, fmt.Sprintf("   → %s", entry.OccupiedBy))
			}
		}
	}

	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

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

	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.wechatBindingEntries()

	switch msg.String() {
	case "d":
		if len(entries) == 0 {
			panel.message = m.t("panel.wechat.message.no_bot")
			return *m, nil
		}
		if panel.selected < len(entries) {
			entry := entries[panel.selected]
			return *m, m.toggleIMAdapterEnabled(entry.Adapter)
		}
		return *m, nil
	case "esc", "ctrl+c":
		m.closeWechatPanel()
		return *m, nil
	case "q":
		var states []*im.AdapterState
		for _, entry := range entries {
			if entry.AdapterState != nil {
				states = append(states, entry.AdapterState)
			}
		}
		if m.openQROverlayFromStates("WeChat", states) {
			return *m, nil
		}
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

		panel.authPhase = "requesting"
		panel.message = ""
		return *m, m.requestWechatQRCode()
	case "enter", "b":

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

		if len(entries) > 0 && panel.selected < len(entries) {
			entry := entries[panel.selected]
			return *m, m.removeWechatEntry(entry)
		}
	}
	return *m, nil
}

func wechatILinkRequest(ctx context.Context, method, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	client := util.NewInsecureAwareClient(15 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := util.ReadAll(resp.Body, 4096)
		return nil, fmt.Errorf("ilink: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return util.ReadAll(resp.Body, util.ReadLimitGeneral)
}

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

		qrImage, err := renderQRFromString(resp.QRCodeImgContent)
		if err != nil {
			debug.Log("wechat", "QR render failed: %v", err)
			qrImage = fmt.Sprintf("[QR render error: %v]", err)
		}
		return wechatQRCodeMsg{qrcodeToken: resp.QRCode, qrcodeImage: qrImage}
	}
}

func wechatPollTick(qrcodeToken string) tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg {

		return wechatPollDueMsg{token: qrcodeToken}
	})
}

type wechatPollDueMsg struct{ token string }

func (m *Model) pollWechatQRStatus(qrcodeToken string) tea.Cmd {
	return func() tea.Msg {

		if qrcodeToken == "" {
			return wechatQRPollMsg{err: errors.New("no QR token")}
		}
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

	m.openQROverlayDirect(
		"WeChat — "+m.t("panel.wechat.scan_qr"),
		m.t("panel.wechat.scan_qr"),
		msg.qrcodeImage,
		"",
	)
	return *m, m.pollWechatQRStatus(msg.qrcodeToken)
}

func (m *Model) handleWechatQRPollMsg(msg wechatQRPollMsg) (Model, tea.Cmd) {
	panel := m.wechatPanel
	if panel == nil {
		return *m, nil
	}

	if msg.err != nil {
		panel.pollFailures++
		if panel.pollFailures >= wechatPollMaxFailures {

			panel.message = fmt.Sprintf("Poll failed %d times, stopped: %v", panel.pollFailures, msg.err)
			panel.authPhase = ""
			return *m, nil
		}
		panel.message = fmt.Sprintf("Poll error: %v", msg.err)
		return *m, m.pollWechatQRStatus(panel.qrcodeToken)
	}
	switch msg.status {
	case "confirmed":
		panel.authPhase = "confirmed"
		panel.botToken = msg.botToken
		m.closeQROverlay()

		return *m, m.saveWechatBotToken(msg.botToken)
	case "scanned":
		panel.authPhase = "polling"
		panel.message = m.t("panel.wechat.scanned")
		return *m, wechatPollTick(panel.qrcodeToken)
	case "wait", "":
		panel.authPhase = "polling"
		panel.pollFailures = 0
		return *m, wechatPollTick(panel.qrcodeToken)
	default:

		panel.authPhase = "failed"
		panel.message = fmt.Sprintf(m.t("panel.wechat.auth_failed"), msg.status)
		if m.qrOverlay != nil {
			m.closeQROverlay()
		}
		return *m, nil
	}
}

func (m *Model) saveWechatBotToken(botToken string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			debug.Log("wechat", "saveWechatBotToken: config is nil")
			return imEditResultMsg{err: fmt.Errorf("config unavailable")}
		}

		return configMutationMsg{
			apply: func(m *Model) error {
				adapterName := "wechat"
				n := 2
				for {
					if _, exists := func() (bool, bool) { _, ok := m.config.GetIMAdapter(adapterName); return ok, ok }(); !exists {
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
					return fmt.Errorf("create adapter config: %w", err)
				}
				m.lastWechatAdapterName = adapterName
				debug.Log("wechat", "saveWechatBotToken: adapter %q created and saved", adapterName)
				return nil
			},
			next: func(m *Model) tea.Cmd {
				return func() tea.Msg {
					adapterName := m.lastWechatAdapterName

					if m.imManager != nil {
						if err := im.StartNamedAdapter(context.Background(), m.config.IMSnapshot(), adapterName, m.imManager); err != nil {
							debug.Log("wechat", "saveWechatBotToken: StartNamedAdapter failed: %v", err)

							return imEditResultMsg{adapterName: adapterName, field: "bot_token", err: fmt.Errorf("saved, but adapter failed to start: %w", err)}
						} else {
							debug.Log("wechat", "saveWechatBotToken: adapter %q started", adapterName)
						}
					} else {
						debug.Log("wechat", "saveWechatBotToken: imManager is nil, adapter not started")
					}

					return imEditResultMsg{adapterName: adapterName, field: "bot_token", value: "***"}
				}
			},
			fail: func(err error) tea.Msg {
				return imEditResultMsg{err: err}
			},
		}
	}
}

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
	adapterCfg, ok := m.config.GetIMAdapter(name)
	if !ok {
		return fmt.Errorf("wechat adapter %s not configured", name)
	}
	if !adapterCfg.Enabled {
		return fmt.Errorf("wechat adapter %s is disabled", name)
	}
	return im.StartNamedAdapter(context.Background(), m.config.IMSnapshot(), name, m.imManager)
}

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

func (m *Model) removeWechatEntry(entry wechatBindingEntry) tea.Cmd {
	return func() tea.Msg {

		return configMutationMsg{
			apply: func(m *Model) error {
				if m.config == nil {
					return nil
				}
				return m.config.RemoveIMAdapter(entry.Adapter)
			},
			next: func(m *Model) tea.Cmd {
				return func() tea.Msg {

					if m.imManager != nil {
						m.imManager.StopAdapter(entry.Adapter)
						_ = m.imManager.UnbindAdapter(entry.Adapter)
					}
					return imEditResultMsg{adapterName: entry.Adapter, field: "remove", value: "ok"}
				}
			},
			fail: func(err error) tea.Msg {
				return imEditResultMsg{err: fmt.Errorf("remove config: %w", err)}
			},
		}
	}
}

func renderQRFromString(content string) (string, error) {
	rendered, err := renderCompactTerminalQRCode(content)
	if err != nil {
		return "", fmt.Errorf("generate QR: %w", err)
	}
	return rendered, nil
}

func (m *Model) wechatBindingEntries() []wechatBindingEntry {
	var entries []wechatBindingEntry
	if m.config == nil {
		return entries
	}

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

	bound := make(map[string]bool)

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

	snapAdapters := m.config.IMSnapshot().Adapters
	keys := make([]string, 0, len(snapAdapters))
	for name, adapter := range snapAdapters {
		if strings.EqualFold(adapter.Platform, string(im.PlatformWechat)) {
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
			Disabled:     !m.config.IMAdapterEnabled(name),
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
