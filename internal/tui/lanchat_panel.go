package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// ---- Messages ----

type lanchatMsg struct{ msg lanchat.Message }
type lanchatReceiptMsg struct{ receipt lanchat.Receipt }
type lanchatPeerJoinMsg struct{ participant lanchat.Participant }
type lanchatPeerLeaveMsg struct{ nodeID string }
type lanchatApprovalReqMsg struct{ pending lanchat.PendingAgentMsg }

// ---- Panel State ----

type lanChatPanelState struct {
	// message input
	input string

	// @mention autocomplete
	mentionMode  bool
	mentionQuery string
	mentionIdx   int
	mentionList  []lanchat.Participant

	// approval popup (option B)
	approvalPopup bool
	approvalIdx   int

	// notice text (for errors/info within panel)
	notice string

	// scroll/view
	width  int
	height int
}

func (m *Model) openLanChatPanel() {
	m.lanChatPanel = &lanChatPanelState{}
	m.lanChatUnread = 0 // clear unread when opening
}

// SetLanChatHub wires the lanchat hub and its callbacks into the TUI model.
func (m *Model) SetLanChatHub(hub *lanchat.Hub) {
	m.lanChatHub = hub
	if hub == nil {
		return
	}
	hub.SetCallbacks(
		// On message
		func(msg lanchat.Message) {
			if m.program != nil {
				m.program.Send(lanchatMsg{msg: msg})
			}
		},
		// On receipt
		func(r lanchat.Receipt) {
			if m.program != nil {
				m.program.Send(lanchatReceiptMsg{receipt: r})
			}
		},
		// On participant add
		func(p lanchat.Participant) {
			if m.program != nil {
				m.program.Send(lanchatPeerJoinMsg{participant: p})
			}
		},
		// On participant remove
		func(nodeID string) {
			if m.program != nil {
				m.program.Send(lanchatPeerLeaveMsg{nodeID: nodeID})
			}
		},
		// On approval request
		func(pending lanchat.PendingAgentMsg) {
			if m.program != nil {
				m.program.Send(lanchatApprovalReqMsg{pending: pending})
				// Also show notification if panel is closed
				m.lanChatUnread++
			}
		},
	)
}

func (m *Model) closeLanChatPanel() {
	m.lanChatPanel = nil
}

// renderLanChatNotice shows a notification bar in the main chat when the
// lanchat panel is closed and there are unread messages.
func (m Model) renderLanChatNotice() string {
	if m.lanChatPanel != nil {
		return "" // panel open, no bar needed
	}
	if m.lanChatUnread == 0 && m.lanChatNotice == "" {
		return ""
	}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FBBF24")).
		Bold(true)
	text := m.lanChatNotice
	if text == "" {
		if m.lanChatUnread > 0 {
			text = fmt.Sprintf("💬 [LAN Chat] %d unread message(s) — /chat to view", m.lanChatUnread)
		}
	} else {
		text = fmt.Sprintf("💬 [LAN Chat] %s", text)
	}
	return style.Render(text)
}

// ---- Update handling ----

func (m *Model) handleLanChatPanelUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleLanChatKey(msg)
	case lanchatMsg:
		// New message received
		if m.lanChatHub != nil {
			m.lanChatHub.HandleIncomingMessage(msg.msg)
		}
		return m, nil
	case lanchatApprovalReqMsg:
		if m.lanChatPanel != nil {
			m.lanChatPanel.approvalPopup = true
		} else {
			// Show system message in main chat
			m.lanChatNotice = fmt.Sprintf("🔔 [LAN Chat] %s sent a message to your agent — /chat to view", msg.pending.Message.FromNick)
			m.lanChatUnread++
		}
		return m, nil
	case lanchatReceiptMsg:
		if m.lanChatHub != nil {
			m.lanChatHub.HandleReceipt(msg.receipt)
		}
		return m, nil
	case lanchatPeerJoinMsg:
		if m.lanChatHub != nil {
			m.lanChatHub.UpdatePeers([]lanchat.Participant{msg.participant})
		}
		return m, nil
	case lanchatPeerLeaveMsg:
		return m, nil
	}
	return m, nil
}

func (m Model) handleLanChatKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	p := m.lanChatPanel

	// If approval popup is open, handle its keys
	if p.approvalPopup {
		return m.handleApprovalKey(msg)
	}

	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeLanChatPanel()
		return m, nil
	case "enter":
		return m.handleLanChatSend()
	case "backspace":
		if len(p.input) > 0 {
			p.input = p.input[:len(p.input)-1]
			// If we're in mention mode and deleted the @, exit
			if p.mentionMode && !strings.Contains(p.input, "@") {
				p.mentionMode = false
				p.mentionQuery = ""
			}
		}
		return m, nil
	case "@":
		p.input += "@"
		p.mentionMode = true
		p.mentionQuery = ""
		p.mentionIdx = 0
		m.refreshMentionList()
		return m, nil
	case "tab":
		if p.mentionMode && len(p.mentionList) > 0 {
			selected := p.mentionList[p.mentionIdx]
			// Replace @query with @nick
			atIdx := strings.LastIndex(p.input, "@")
			p.input = p.input[:atIdx] + "@" + selected.HumanNick + " "
			p.mentionMode = false
			p.mentionQuery = ""
		}
		return m, nil
	case "up":
		if p.mentionMode && len(p.mentionList) > 0 {
			p.mentionIdx = (p.mentionIdx - 1 + len(p.mentionList)) % len(p.mentionList)
		}
		return m, nil
	case "down":
		if p.mentionMode && len(p.mentionList) > 0 {
			p.mentionIdx = (p.mentionIdx + 1) % len(p.mentionList)
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			p.input += msg.String()
			// Update mention query
			if p.mentionMode {
				atIdx := strings.LastIndex(p.input, "@")
				if atIdx >= 0 {
					p.mentionQuery = p.input[atIdx+1:]
					m.refreshMentionList()
				}
			}
		}
		return m, nil
	}
}

func (m Model) handleApprovalKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	p := m.lanChatPanel
	pending := m.lanChatHub.PendingApprovals()

	switch msg.String() {
	case "esc":
		p.approvalPopup = false
		return m, nil
	case "enter", "y":
		if len(pending) > 0 {
			approved, _ := m.lanChatHub.ApproveMessage(pending[p.approvalIdx].Message.ID)
			p.approvalPopup = false
			if approved != nil {
				// Inject into agent loop
				agentText := fmt.Sprintf("[LAN Chat from %s]: %s", approved.FromNick, approved.Content)
				m.closeLanChatPanel()
				return m, m.startAgent(agentText)
			}
		}
		return m, nil
	case "n", "r":
		if len(pending) > 0 {
			m.lanChatHub.RejectMessage(pending[p.approvalIdx].Message.ID, "rejected by host")
			p.approvalPopup = false
		}
		return m, nil
	case "up":
		if len(pending) > 0 {
			p.approvalIdx = (p.approvalIdx - 1 + len(pending)) % len(pending)
		}
		return m, nil
	case "down":
		if len(pending) > 0 {
			p.approvalIdx = (p.approvalIdx + 1) % len(pending)
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handleLanChatSend() (Model, tea.Cmd) {
	p := m.lanChatPanel
	text := strings.TrimSpace(p.input)
	p.input = ""

	if text == "" {
		return m, nil
	}

	// Handle /nick command
	if strings.HasPrefix(text, "/nick ") {
		nick := strings.TrimSpace(strings.TrimPrefix(text, "/nick "))
		if nick != "" && m.lanChatHub != nil {
			m.lanChatHub.SetNick(nick)
		}
		return m, nil
	}

	if m.lanChatHub == nil {
		return m, nil
	}

	// Parse @mention
	if strings.HasPrefix(text, "@") {
		// Find the first space after the @mention
		spaceIdx := strings.Index(text, " ")
		if spaceIdx > 0 {
			mention := text[1:spaceIdx]
			content := strings.TrimSpace(text[spaceIdx:])

			// Find target participant
			for _, part := range m.lanChatHub.Participants() {
				if part.HumanNick == mention || part.AgentNick == mention {
					role := lanchat.RoleHuman
					if strings.HasSuffix(mention, "_agent") {
						role = lanchat.RoleAgent
					}
					m.lanChatHub.SendDirect(nil, part.NodeID, role, content, nil)
					return m, nil
				}
			}
			// Mention not found
			p.notice = fmt.Sprintf("Unknown @mention: %s", mention)
			return m, nil
		}
	}

	// Broadcast
	m.lanChatHub.SendBroadcast(nil, text, nil)
	return m, nil
}

func (m *Model) refreshMentionList() {
	p := m.lanChatPanel
	if m.lanChatHub == nil {
		return
	}

	all := m.lanChatHub.Participants()
	// Filter by query and sort
	var filtered []lanchat.Participant
	for _, part := range all {
		nick := part.HumanNick
		if strings.HasPrefix(strings.ToLower(nick), strings.ToLower(p.mentionQuery)) {
			filtered = append(filtered, part)
		}
		// Also include agent nick
		if strings.HasPrefix(strings.ToLower(part.AgentNick), strings.ToLower(p.mentionQuery)) {
			filtered = append(filtered, part)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].HumanNick < filtered[j].HumanNick
	})
	p.mentionList = filtered
	p.mentionIdx = 0
}

// ---- View ----

func (m *Model) renderLanChatPanel() string {
	p := m.lanChatPanel
	if p == nil {
		return ""
	}

	hub := m.lanChatHub
	var b strings.Builder

	// Header
	onlineStr := "No one online"
	if hub != nil {
		parts := hub.Participants()
		nicks := make([]string, 0, len(parts)*2)
		for _, part := range parts {
			if part.Online {
				nicks = append(nicks, "👤"+part.HumanNick)
				nicks = append(nicks, "🤖"+part.AgentNick)
			}
		}
		if len(nicks) > 0 {
			onlineStr = strings.Join(nicks, "  ")
		}
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7DD3FC")).
		Width(p.width)
	b.WriteString(headerStyle.Render("🌐 LAN Chat — " + onlineStr))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", p.width))
	b.WriteString("\n")

	// Messages
	if hub != nil {
		msgs := hub.Messages()
		// Show last N messages that fit
		maxLines := p.height - 5
		start := 0
		if len(msgs) > maxLines {
			start = len(msgs) - maxLines
		}
		for i := start; i < len(msgs); i++ {
			msg := msgs[i]
			b.WriteString(renderLanChatMessage(msg))
		}
	}

	// Approval popup (option B)
	if p.approvalPopup {
		pending := m.lanChatHub.PendingApprovals()
		if len(pending) > 0 {
			b.WriteString("\n")
			popupStyle := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#FBBF24")).
				Padding(1, 2).
				Width(p.width - 4)
			current := pending[p.approvalIdx]
			popup := fmt.Sprintf("📨 %s → your agent:\n  %q\n\n  [Enter] Approve  [N] Reject  [↑↓] Navigate  [Esc] Close",
				current.Message.FromNick, current.Message.Content)
			b.WriteString(popupStyle.Render(popup))
			b.WriteString("\n")
		}
	}

	// Mention autocomplete
	if p.mentionMode && len(p.mentionList) > 0 {
		b.WriteString("\n")
		for i, part := range p.mentionList {
			prefix := "  "
			style := lipgloss.NewStyle()
			if i == p.mentionIdx {
				prefix = "▶ "
				style = style.Foreground(lipgloss.Color("#FBBF24")).Bold(true)
			}
			b.WriteString(style.Render(fmt.Sprintf("%s👤%s  🤖%s", prefix, part.HumanNick, part.AgentNick)))
			b.WriteString("\n")
		}
	}

	// Input
	b.WriteString(strings.Repeat("─", p.width))
	b.WriteString("\n")
	inputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
	hint := "[Tab] @mention  [↑↓] Select  [Esc] Close"
	if p.mentionMode {
		hint = "[↑↓] Select  [Tab] Confirm  [Esc] Cancel mention"
	}
	b.WriteString(inputStyle.Render(hint + "\n"))
	b.WriteString(fmt.Sprintf("> %s█", p.input))

	return b.String()
}

func renderLanChatMessage(msg lanchat.Message) string {
	ts := time.UnixMilli(msg.Timestamp).Format("15:04")
	icon := "👤"
	if msg.FromRole == lanchat.RoleAgent {
		icon = "🤖"
	}

	// Color: own messages vs others, agent vs human
	senderStyle := lipgloss.NewStyle().Bold(true)
	contentStyle := lipgloss.NewStyle()

	if msg.IsBroadcast() {
		return fmt.Sprintf("  [%s] %s%s: %s\n", ts, icon, senderStyle.Render(msg.FromNick), contentStyle.Render(msg.Content))
	}

	// Direct message
	dmTag := "→"
	if msg.ToRole == lanchat.RoleAgent {
		dmTag = "→agent"
	}
	return fmt.Sprintf("  [%s] %s%s %s: %s\n", ts, icon, senderStyle.Render(msg.FromNick), dmTag, contentStyle.Render(msg.Content))
}
