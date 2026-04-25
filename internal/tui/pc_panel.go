package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/skip2/go-qrcode"

	"github.com/topcheer/ggcode/internal/im"
)

type pcPanelState struct {
	selected    int
	message     string
	showQR      bool
	qrCode      string
	inviteURI   string
	createMode  bool
	createInput string
	createGroup bool
	editState   imAdapterEditState
}

type pcResultMsg struct {
	message   string
	err       error
	qrCode    string
	inviteURI string
	showQR    bool
}

type pcSessionListMsg struct {
	sessions []im.PCSessionInfo
}

func (m *Model) openPCPanel() {
	m.pcPanel = &pcPanelState{}
}

func (m *Model) closePCPanel() {
	m.pcPanel = nil
}

func (m Model) renderPCPanel() string {
	panel := m.pcPanel
	if panel == nil {
		return ""
	}

	// Header
	body := []string{
		lipgloss.NewStyle().Bold(true).Render("PrivateClaw Sessions"),
		"",
		lipgloss.NewStyle().Bold(true).Render("Connection"),
		fmt.Sprintf("  %s", m.pcConnectionStatus()),
		"",
		lipgloss.NewStyle().Bold(true).Render("Sessions"),
	}

	sessions := m.pcSessionEntries()
	if len(sessions) == 0 {
		body = append(body, "  (no sessions)")
	} else {
		selected := clampPCSelection(panel.selected, len(sessions))
		labels := make([]string, len(sessions))
		for i, s := range sessions {
			mode := "1:1"
			if s.GroupMode {
				mode = "group"
			}
			indicator := " "
			if i == selected {
				indicator = ">"
			}
			stateIcon := "?"
			switch s.State {
			case "awaiting_hello":
				stateIcon = "~"
			case "active":
				stateIcon = "*"
			}
			labels[i] = fmt.Sprintf(" %s %s %s  %s  participants:%d", indicator, stateIcon, s.SessionID[:12], mode, s.ParticipantCount)
		}
		body = append(body, labels...)

		// Details of selected session
		s := sessions[selected]
		body = append(body, "",
			lipgloss.NewStyle().Bold(true).Render("Details"),
			fmt.Sprintf("  Session:  %s", s.SessionID),
			fmt.Sprintf("  State:    %s", s.State),
			fmt.Sprintf("  Mode:     %s", func() string {
				if s.GroupMode {
					return "group"
				}
				return "1:1"
			}()),
			fmt.Sprintf("  Participants: %d", s.ParticipantCount),
			fmt.Sprintf("  Expires:  %s", s.ExpiresAt.Format("2006-01-02 15:04:05")),
		)
		if s.Label != "" {
			body = append(body, fmt.Sprintf("  Label:    %s", s.Label))
		}
	}

	// QR display
	if panel.showQR && panel.qrCode != "" {
		body = append(body, "", lipgloss.NewStyle().Bold(true).Render("QR Code"))
		body = append(body, panel.qrCode)
		if panel.inviteURI != "" {
			body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  URI: "+panel.inviteURI))
		}
	}

	// Create mode
	body = append(body, "", lipgloss.NewStyle().Bold(true).Render("Actions"))
	if panel.createMode {
		groupLabel := "1:1"
		if panel.createGroup {
			groupLabel = "group"
		}
		body = append(body,
			fmt.Sprintf("  Create session (%s): %s█", groupLabel, panel.createInput),
			"  Enter: confirm  Esc: cancel  G: toggle group",
		)
	} else {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
			"  n:new  q:QR  r:renew  x:close  g:group  e:edit config  enter:bind  esc:close",
		))
	}

	// Edit config section
	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render("  "+panel.message))
	}

	return m.renderContextBox("/pc", strings.Join(body, "\n"), lipgloss.Color("5"))
}

func (m *Model) handlePCPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.pcPanel
	if panel == nil {
		return *m, nil
	}

	// QR view mode - any key exits
	if panel.showQR {
		panel.showQR = false
		panel.qrCode = ""
		panel.inviteURI = ""
		return *m, nil
	}

	// Create mode
	if panel.createMode {
		switch msg.String() {
		case "esc":
			panel.createMode = false
			panel.createInput = ""
			return *m, nil
		case "enter":
			label := strings.TrimSpace(panel.createInput)
			panel.createMode = false
			panel.createInput = ""
			return *m, m.pcCreateSessionCmd(label, panel.createGroup)
		case "backspace":
			runes := []rune(panel.createInput)
			if len(runes) > 0 {
				panel.createInput = string(runes[:len(runes)-1])
			}
			return *m, nil
		case "g", "G":
			panel.createGroup = !panel.createGroup
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

	// Edit mode
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	sessions := m.pcSessionEntries()

	switch msg.String() {
	case "up", "k":
		if len(sessions) > 0 {
			panel.selected = (panel.selected - 1 + len(sessions)) % len(sessions)
		}
	case "down", "j", "tab":
		if len(sessions) > 0 {
			panel.selected = (panel.selected + 1) % len(sessions)
		}
	case "n", "N":
		panel.createMode = true
		panel.createInput = ""
		panel.createGroup = false
		panel.message = ""
		return *m, nil
	case "q", "Q":
		if len(sessions) == 0 {
			panel.message = "No session selected"
			return *m, nil
		}
		return *m, m.pcShowQRCmd(sessions[clampPCSelection(panel.selected, len(sessions))].SessionID)
	case "r", "R":
		if len(sessions) == 0 {
			panel.message = "No session selected"
			return *m, nil
		}
		return *m, m.pcRenewSessionCmd(sessions[clampPCSelection(panel.selected, len(sessions))].SessionID)
	case "x", "X":
		if len(sessions) == 0 {
			panel.message = "No session selected"
			return *m, nil
		}
		return *m, m.pcCloseSessionCmd(sessions[clampPCSelection(panel.selected, len(sessions))].SessionID)
	case "enter", "b", "B":
		if len(sessions) == 0 {
			panel.message = "No session to bind"
			return *m, nil
		}
		return *m, m.pcBindSessionCmd(sessions[clampPCSelection(panel.selected, len(sessions))])
	case "e", "E":
		adapterName := m.pcAdapterName()
		if adapterName == "" {
			panel.message = "No PC adapter configured"
			return *m, nil
		}
		edit := m.enterIMEditSelect(adapterName)
		panel.editState = edit
		panel.message = ""
		return *m, nil
	case "esc":
		m.closePCPanel()
	}
	return *m, nil
}

// Commands

func (m *Model) ensurePCReady() error {
	// Ensure IM Manager is initialized
	if m.imManager == nil {
		if m.config == nil {
			return errors.New("config not available")
		}
		m.config.IM.Enabled = true
		if err := m.config.Save(); err != nil {
			return fmt.Errorf("enable IM: %w", err)
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
		imMgr.SetBridge(newTUIIMBridge(func() *tea.Program { return m.program }))
		m.SetIMManager(imMgr)
	}

	// Ensure PC adapter is started
	if m.pcAdapter() != nil {
		return nil
	}
	_, err := im.StartPCAdapterOnly(context.Background(), m.config.IM, m.imManager)
	if err != nil {
		return fmt.Errorf("starting PrivateClaw adapter: %w", err)
	}
	// Wait briefly for adapter to register
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.pcAdapter() != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("PrivateClaw adapter failed to start")
}

func (m *Model) pcCreateSessionCmd(label string, groupMode bool) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensurePCReady(); err != nil {
			return pcResultMsg{err: err}
		}
		adapter := m.pcAdapter()
		if adapter == nil {
			return pcResultMsg{err: errors.New("PrivateClaw adapter not available")}
		}
		invite, sessionID, err := adapter.CreateSession(context.Background(), label, groupMode)
		if err != nil {
			return pcResultMsg{err: err}
		}
		uri, _ := pcEncodeInviteToURIFromInvite(invite)
		return pcResultMsg{message: fmt.Sprintf("Session created: %s", sessionID), inviteURI: uri}
	}
}

func (m *Model) pcShowQRCmd(sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensurePCReady(); err != nil {
			return pcResultMsg{err: err}
		}
		adapter := m.pcAdapter()
		if adapter == nil {
			return pcResultMsg{err: errors.New("PrivateClaw adapter not available")}
		}
		uri, err := adapter.GetInviteURI(sessionID)
		if err != nil {
			return pcResultMsg{err: err}
		}
		qr, err := qrcode.New(uri, qrcode.Medium)
		if err != nil {
			return pcResultMsg{err: err}
		}
		qr.DisableBorder = true
		return pcResultMsg{
			message:   "QR Code generated",
			qrCode:    qr.ToSmallString(false),
			inviteURI: uri,
			showQR:    true,
		}
	}
}

func (m *Model) pcRenewSessionCmd(sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensurePCReady(); err != nil {
			return pcResultMsg{err: err}
		}
		adapter := m.pcAdapter()
		if adapter == nil {
			return pcResultMsg{err: errors.New("PrivateClaw adapter not available")}
		}
		if err := adapter.RenewSession(context.Background(), sessionID); err != nil {
			return pcResultMsg{err: err}
		}
		return pcResultMsg{message: fmt.Sprintf("Session %s renewed", sessionID[:12])}
	}
}

func (m *Model) pcCloseSessionCmd(sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensurePCReady(); err != nil {
			return pcResultMsg{err: err}
		}
		adapter := m.pcAdapter()
		if adapter == nil {
			return pcResultMsg{err: errors.New("PrivateClaw adapter not available")}
		}
		if err := adapter.CloseSession(sessionID); err != nil {
			return pcResultMsg{err: err}
		}
		return pcResultMsg{message: fmt.Sprintf("Session %s closed", sessionID[:12])}
	}
}

func (m *Model) pcBindSessionCmd(session im.PCSessionInfo) tea.Cmd {
	return func() tea.Msg {
		if m.imManager == nil {
			return pcResultMsg{err: errors.New("IM manager not available")}
		}
		_, err := m.imManager.BindChannel(im.ChannelBinding{
			Platform:  im.PlatformPrivateClaw,
			Adapter:   m.pcAdapterName(),
			TargetID:  session.SessionID,
			ChannelID: session.SessionID,
		})
		if err != nil {
			return pcResultMsg{err: err}
		}
		return pcResultMsg{message: fmt.Sprintf("Bound to session %s", session.SessionID[:12])}
	}
}

// Helpers

func (m *Model) pcAdapter() im.PCAdapterAPI {
	if m.imManager == nil {
		return nil
	}
	return m.imManager.PCAdapter()
}

func (m *Model) pcAdapterName() string {
	// Prefer explicit config adapter name
	if m.config != nil {
		for name, adapter := range m.config.IM.Adapters {
			if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformPrivateClaw)) {
				return name
			}
		}
	}
	// Built-in adapter is always available
	if m.pcAdapter() != nil {
		return "_pc_builtin"
	}
	return ""
}

func (m Model) pcConnectionStatus() string {
	adapter := m.pcAdapter()
	if adapter == nil {
		return "starting..."
	}
	// Check adapter state from IM manager snapshot
	if m.imManager != nil {
		for _, a := range m.imManager.Snapshot().Adapters {
			if a.Platform == im.PlatformPrivateClaw {
				switch a.Status {
				case "connected":
					sessions := adapter.ListSessions()
					if len(sessions) == 0 {
						return "connected (no sessions)"
					}
					return fmt.Sprintf("connected (%d session(s))", len(sessions))
				case "error":
					return fmt.Sprintf("error: %s", a.LastError)
				case "stopped":
					return "stopped"
				default:
					return a.Status
				}
			}
		}
	}
	return "starting..."
}

func (m Model) pcSessionEntries() []im.PCSessionInfo {
	adapter := m.pcAdapter()
	if adapter == nil {
		return nil
	}
	return adapter.ListSessions()
}

func clampPCSelection(selected, total int) int {
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

func pcEncodeInviteToURIFromInvite(invite *im.PCInvite) (string, error) {
	if invite == nil {
		return "", nil
	}
	return im.PCEncodeInviteToURI(*invite)
}
