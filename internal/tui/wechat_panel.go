package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
)

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
		if strings.TrimSpace(entry.OccupiedBy) != "" {
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

	// QR code auth section
	if panel.authPhase == "showing_qr" || panel.authPhase == "polling" {
		body = append(body, "",
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render(m.t("panel.wechat.scan_qr")),
		)
		if panel.qrcodeImage != "" {
			body = append(body, panel.qrcodeImage)
		}
		if panel.authPhase == "polling" {
			body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("panel.wechat.waiting_scan")))
		}
	} else if panel.authPhase == "confirmed" {
		body = append(body, "",
			lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(m.t("panel.wechat.auth_confirmed")),
		)
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
		// Start QR auth flow
		return *m, m.requestWechatQRCode()
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
		// Remove bot binding
		if len(entries) > 0 && panel.selected < len(entries) {
			entry := entries[panel.selected]
			if m.imManager != nil {
				if err := m.imManager.UnbindAdapter(entry.Adapter); err != nil {
					panel.message = fmt.Sprintf("Error: %v", err)
				} else {
					panel.message = m.t("panel.wechat.removed", entry.Adapter)
					if panel.selected >= len(entries)-1 && panel.selected > 0 {
						panel.selected--
					}
				}
			}
		}
	}
	return *m, nil
}

// requestWechatQRCode starts the QR code authentication flow.
func (m *Model) requestWechatQRCode() tea.Cmd {
	return func() tea.Msg {
		adapter := m.getWechatAdapter()
		if adapter == nil {
			return wechatQRCodeMsg{err: fmt.Errorf("no wechat adapter configured — add 'wechat' platform to im config")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		qrcodeToken, imgBase64, err := adapter.AuthenticateQRCode(ctx)
		if err != nil {
			return wechatQRCodeMsg{err: err}
		}
		qrImage, err := renderWechatQRFromBase64(imgBase64)
		if err != nil {
			debug.Log("wechat", "QR render failed: %v", err)
			qrImage = fmt.Sprintf("[QR decode error: %v]", err)
		}
		return wechatQRCodeMsg{qrcodeToken: qrcodeToken, qrcodeImage: qrImage}
	}
}

// pollWechatQRStatus polls the QR code scan status.
func (m *Model) pollWechatQRStatus(qrcodeToken string) tea.Cmd {
	return func() tea.Msg {
		adapter := m.getWechatAdapter()
		if adapter == nil {
			return wechatQRPollMsg{err: fmt.Errorf("adapter lost")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		status, botToken, err := adapter.PollQRCodeStatus(ctx, qrcodeToken)
		if err != nil {
			return wechatQRPollMsg{err: err}
		}
		return wechatQRPollMsg{status: status, botToken: botToken}
	}
}

func (m *Model) getWechatAdapter() *im.WechatAdapter {
	if m.imManager == nil {
		return nil
	}
	return m.imManager.WechatAdapter()
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
		panel.message = m.t("panel.wechat.auth_success")
		adapter := m.getWechatAdapter()
		if adapter != nil {
			adapter.SetBotToken(msg.botToken)
		}
		return *m, nil
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

// renderWechatQRFromBase64 decodes a base64 PNG and renders it as Unicode block characters.
func renderWechatQRFromBase64(imgBase64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(imgBase64)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	img, err := png.Decode(strings.NewReader(string(data)))
	if err != nil {
		return "", fmt.Errorf("png decode: %w", err)
	}
	return renderQRFromImage(img), nil
}

// renderQRFromImage converts an image to Unicode block characters for terminal display.
func renderQRFromImage(img image.Image) string {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	threshold := 128
	rows := h
	cols := w

	bitmap := make([][]bool, rows)
	for y := 0; y < rows; y++ {
		bitmap[y] = make([]bool, cols)
		for x := 0; x < cols; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			lum := (int(r>>8)*299 + int(g>>8)*587 + int(b>>8)*114) / 1000
			bitmap[y][x] = lum < threshold
		}
	}

	if rows%2 == 1 {
		padding := make([]bool, cols)
		bitmap = append(bitmap, padding)
		rows++
	}

	const (
		whiteAll   = "█"
		whiteBlack = "▀"
		blackWhite = "▄"
		blackAll   = " "
	)

	maxWidth := 60
	if cols > maxWidth {
		scale := (cols + maxWidth - 1) / maxWidth
		newCols := cols / scale
		newRows := rows / scale
		scaled := make([][]bool, newRows)
		for y := 0; y < newRows; y++ {
			scaled[y] = make([]bool, newCols)
			for x := 0; x < newCols; x++ {
				sy := y * scale
				sx := x * scale
				if sy < rows && sx < cols {
					scaled[y][x] = bitmap[sy][sx]
				}
			}
		}
		bitmap = scaled
		rows = newRows
		cols = newCols
		if rows%2 == 1 {
			padding := make([]bool, cols)
			bitmap = append(bitmap, padding)
			rows++
		}
	}

	borderTop := strings.Repeat(blackWhite, cols+2)
	borderBottom := strings.Repeat(whiteBlack, cols+2)
	var lines []string

	lines = append(lines, borderTop)
	for row := 0; row < rows; row += 2 {
		var b strings.Builder
		b.WriteString(whiteAll)
		for col := 0; col < cols; col++ {
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

	return strings.Join(lines, "\n")
}

func (m *Model) wechatBindingEntries() []wechatBindingEntry {
	var entries []wechatBindingEntry
	if m.imManager == nil {
		return entries
	}

	adapterStates := make(map[string]im.AdapterState)
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == "" {
			continue
		}
		adapterStates[state.Name] = state
	}

	wsPath := m.currentWorkspacePath()
	bindings, err := m.imManager.ListBindings()
	if err != nil {
		return entries
	}

	for _, b := range bindings {
		if b.Platform != im.PlatformWechat {
			continue
		}
		occupiedBy := ""
		if b.Workspace != "" && b.Workspace != wsPath {
			occupiedBy = b.Workspace
		}
		var state *im.AdapterState
		if s, ok := adapterStates[b.Adapter]; ok {
			state = &s
		}
		entries = append(entries, wechatBindingEntry{
			Adapter:      b.Adapter,
			TargetID:     b.TargetID,
			ChannelID:    b.ChannelID,
			OccupiedBy:   occupiedBy,
			AdapterState: state,
			Muted:        b.Muted,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Adapter < entries[j].Adapter
	})
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
