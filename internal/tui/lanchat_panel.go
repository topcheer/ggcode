package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// ---- Messages ----

type lanchatMsg struct{ msg lanchat.Message }
type lanchatReceiptMsg struct{ receipt lanchat.Receipt }
type lanchatPeerJoinMsg struct{ participant lanchat.Participant }
type lanchatPeerLeaveMsg struct{ nodeID, humanNick string }
type lanchatApprovalReqMsg struct{ pending lanchat.PendingAgentMsg }
type lanchatNickChangeMsg struct{ nodeID, oldNick, newNick string }
type lanchatAutoApproveMsg struct{ msg lanchat.Message }

// ---- Panel State ----

type lanChatPanelState struct {
	// message input
	input string

	// @mention autocomplete
	mentionMode  bool
	mentionQuery string
	mentionIdx   int
	mentionList  []mentionTarget

	// approval popup
	approvalPopup bool
	approvalIdx   int

	// notice text (for errors/info within panel)
	notice string
}

// mentionTarget is a single selectable entry in the @mention list.
// One Participant can produce two targets: human and agent.
type mentionTarget struct {
	NodeID string
	Nick   string // the actual nick to @mention
	Role   string // lanchat.RoleHuman or lanchat.RoleAgent
}

func (m *Model) openLanChatPanel() {
	m.lanChatPanel = &lanChatPanelState{}
	m.lanChatUnread = 0 // clear unread when opening
}

// SetLanChatHub wires the lanchat hub and its callbacks into the TUI model.
// sendMsg is r.sendTUI — NOT m.program.Send — because tea.NewProgram()
// copies the model, so m.program is nil in the pre-copy model that the
// closures capture. Using a function indirection lets callbacks deliver
// messages to the real running event loop.
func (m *Model) SetLanChatHub(hub *lanchat.Hub, sendMsg func(tea.Msg)) {
	m.lanChatHub = hub
	if hub == nil {
		return
	}
	hub.SetCallbacks(
		// On message
		func(msg lanchat.Message) {
			if sendMsg != nil {
				sendMsg(lanchatMsg{msg: msg})
			}
		},
		// On receipt
		func(r lanchat.Receipt) {
			if sendMsg != nil {
				sendMsg(lanchatReceiptMsg{receipt: r})
			}
		},
		// On participant add
		func(p lanchat.Participant) {
			if sendMsg != nil {
				sendMsg(lanchatPeerJoinMsg{participant: p})
			}
		},
		// On participant remove
		func(nodeID, humanNick string) {
			if sendMsg != nil {
				sendMsg(lanchatPeerLeaveMsg{nodeID: nodeID, humanNick: humanNick})
			}
		},
		// On approval request
		func(pending lanchat.PendingAgentMsg) {
			if sendMsg != nil {
				sendMsg(lanchatApprovalReqMsg{pending: pending})
			}
		},
		// On nick change
		func(nodeID, oldNick, newNick string) {
			if sendMsg != nil {
				sendMsg(lanchatNickChangeMsg{nodeID: nodeID, oldNick: oldNick, newNick: newNick})
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
			text = fmt.Sprintf("[LAN Chat] %d unread message(s) — # to reply, /chat to view", m.lanChatUnread)
		}
	} else {
		text = fmt.Sprintf("[LAN Chat] %s", text)
	}
	return style.Render(text)
}

// ---- Update handling ----

func (m *Model) handleLanChatPanelUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleLanChatKey(msg)
	case lanchatMsg:
		// Message was already processed by the Hub's HTTP handler before
		// firing the onMessage callback. Do NOT call HandleIncomingMessage
		// here — that would re-store the message and re-fire the callback,
		// creating an infinite loop (hundreds of messages per second).
		//
		// Agent-directed messages (@agent) are handled separately via
		// the approval flow and injected into the agent loop — they will
		// appear as user messages in the conversation. Skip the system
		// notification to avoid duplicate rendering.
		if msg.msg.IsDirectToAgent() && msg.msg.ToNodeID == m.lanChatHub.NodeID() {
			return m, nil
		}
		// Regular message — show a notification in the main panel if
		// the chat panel is not visible.
		if m.lanChatPanel == nil {
			fromNick := msg.msg.FromNick
			if fromNick == "" {
				fromNick = "(unknown)"
			}
			m.lanChatUnread++
			// Track last sender for # chat mode pre-fill
			m.lanChatLastSenderNick = msg.msg.FromNick
			m.lanChatLastSenderRole = msg.msg.ToRole
			m.lanChatLastSenderNodeID = msg.msg.FromNodeID
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("[LAN Chat] %s: %s — # to reply", fromNick, msg.msg.Content))
		}
		return m, nil
	case lanchatApprovalReqMsg:
		// Auto-open the lanchat panel with approval popup so the user
		// can approve/reject immediately. Previously the approval popup
		// was only shown if the panel was already open, which meant
		// @agent messages were silently stuck when the panel was closed.
		if m.lanChatPanel == nil {
			m.openLanChatPanel()
		}
		m.lanChatPanel.approvalPopup = true
		return m, nil
	case lanchatReceiptMsg:
		// Suppress receipts for agent-to-agent messages — only human-to-human
		// message status should be visible to the user.
		if msg.receipt.FromRole == lanchat.RoleAgent {
			return m, nil
		}
		// Show receipt status as a system message in the chat panel.
		msgID := msg.receipt.MessageID
		if utf8.RuneCountInString(msgID) > 8 {
			msgID = string([]rune(msgID)[:8])
		}
		// Include who sent the receipt (from nick) for clarity.
		fromLabel := msg.receipt.FromNick
		if fromLabel == "" {
			fromLabel = msg.receipt.FromNodeID
			if utf8.RuneCountInString(fromLabel) > 8 {
				fromLabel = string([]rune(fromLabel)[:8])
			}
		}
		suffix := msgID
		if fromLabel != "" {
			suffix = fmt.Sprintf("%s from %s", msgID, fromLabel)
		}
		switch msg.receipt.Status {
		case lanchat.StatusDelivered:
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  [delivered] %s", suffix))
		case lanchat.StatusPending:
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  [pending approval] %s", suffix))
		case lanchat.StatusApproved:
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  [approved] %s", suffix))
		case lanchat.StatusProcessing:
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  [agent running] %s", suffix))
		case lanchat.StatusCompleted:
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  [completed] %s", suffix))
		case lanchat.StatusRejected:
			reason := msg.receipt.Reason
			if reason == "" {
				reason = "(no reason given)"
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  [rejected] %s: %s", suffix, reason))
		}
		return m, nil
	case lanchatPeerJoinMsg:
		// System message in main chat — do NOT call UpdatePeers here.
		// The Hub already processed the peer before firing the callback.
		// Calling UpdatePeers([single_peer]) would mark all OTHER peers
		// as "not seen" and trigger mass offline callbacks.
		nick := msg.participant.HumanNick
		if nick == "" {
			nick = "(unknown)"
		}
		ep := msg.participant.Endpoint
		// Trim http:// prefix for display
		ep = strings.TrimPrefix(ep, "http://")
		ep = strings.TrimPrefix(ep, "https://")
		// Include workspace/project name if available
		wsInfo := ""
		if msg.participant.ProjectName != "" {
			wsInfo = fmt.Sprintf(" · %s", msg.participant.ProjectName)
		} else if msg.participant.Workspace != "" {
			parts := strings.Split(msg.participant.Workspace, "/")
			if len(parts) > 0 {
				wsInfo = fmt.Sprintf(" · %s", parts[len(parts)-1])
			}
		}
		if len(msg.participant.Languages) > 0 {
			wsInfo += fmt.Sprintf(" [%s]", strings.Join(msg.participant.Languages, ","))
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("[LAN Chat] %s is online (from %s)%s", nick, ep, wsInfo))
		return m, nil
	case lanchatPeerLeaveMsg:
		nick := msg.humanNick
		if nick == "" {
			nick = "(unknown)"
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("[LAN Chat] %s went offline", nick))
		return m, nil
	case lanchatNickChangeMsg:
		old := msg.oldNick
		if old == "" {
			old = "(unknown)"
		}
		new := msg.newNick
		if new == "" {
			new = "(unknown)"
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("[LAN Chat] %s is now known as %s", old, new))
		return m, nil
	case lanchatAutoApproveMsg:
		// Auto-approved message (policy=always or daemon mode) — inject into agent.
		// Track for completed receipt when agent finishes.
		m.lanChatPendingComplete = msg.msg.ID
		agentText := fmt.Sprintf("[LAN Chat from %s]: %s", msg.msg.FromNick, msg.msg.Content)
		return m, m.submitLanChatAgentText(agentText)
	}
	return m, nil
}

func (m Model) handleLanChatKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	p := m.lanChatPanel

	// If approval popup is open, handle its keys
	if p.approvalPopup {
		return m.handleApprovalKey(msg)
	}

	// In mention mode, Enter and Tab both complete the mention
	if p.mentionMode && len(p.mentionList) > 0 {
		switch msg.String() {
		case "enter", "tab":
			selected := p.mentionList[p.mentionIdx]
			atIdx := strings.LastIndex(p.input, "@")
			p.input = p.input[:atIdx] + "@" + selected.Nick + " "
			p.mentionMode = false
			p.mentionQuery = ""
			return m, nil
		case "up":
			p.mentionIdx = (p.mentionIdx - 1 + len(p.mentionList)) % len(p.mentionList)
			return m, nil
		case "down":
			p.mentionIdx = (p.mentionIdx + 1) % len(p.mentionList)
			return m, nil
		case "esc":
			p.mentionMode = false
			p.mentionQuery = ""
			return m, nil
		}
	}

	keyStr := msg.String()
	switch keyStr {
	case "esc", "ctrl+c":
		m.closeLanChatPanel()
		return m, nil
	case "enter":
		return m.handleLanChatSend()
	case "backspace":
		if len(p.input) > 0 {
			// Remove last rune (handles multi-byte chars)
			runes := []rune(p.input)
			p.input = string(runes[:len(runes)-1])
			// If we're in mention mode and deleted the @, exit
			if p.mentionMode && !strings.Contains(p.input, "@") {
				p.mentionMode = false
				p.mentionQuery = ""
			}
		}
		return m, nil
	case "tab":
		// Tab outside mention mode = no-op
		return m, nil
	case "up", "down":
		// Up/down outside mention mode = no-op
		return m, nil
	default:
		// Use msg.Key().Text for printable text — handles space, multi-byte
		// chars (Chinese, etc.), and IME input correctly.
		// msg.String() returns named keys like "space" for spacebar,
		// which is useless for text input.
		text := ""
		if msg.Key().Text != "" {
			text = msg.Key().Text
		} else if len(keyStr) == 1 {
			text = keyStr
		}
		if text == "" {
			return m, nil
		}
		p.input += text
		// Handle @mention activation
		if text == "@" && !p.mentionMode {
			p.mentionMode = true
			p.mentionQuery = ""
			p.mentionIdx = 0
			m.refreshMentionList()
			return m, nil
		}
		// Update mention query while typing after @
		if p.mentionMode {
			atIdx := strings.LastIndex(p.input, "@")
			if atIdx >= 0 {
				p.mentionQuery = p.input[atIdx+1:]
				m.refreshMentionList()
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
	case "enter", "y", "Y":
		if len(pending) > 0 {
			approved, _ := m.lanChatHub.ApproveMessage(pending[p.approvalIdx].Message.ID)
			p.approvalPopup = false
			if approved != nil {
				// Track for completed receipt when agent finishes
				m.lanChatPendingComplete = approved.ID
				agentText := fmt.Sprintf("[LAN Chat from %s]: %s", approved.FromNick, approved.Content)
				m.closeLanChatPanel()
				return m, m.submitLanChatAgentText(agentText)
			}
		}
		return m, nil
	case "a", "A":
		// Always Approve — set policy then approve this message
		if len(pending) > 0 {
			msg := pending[p.approvalIdx].Message
			m.lanChatHub.SetApprovalPolicy(msg.FromNick, "always")
			approved, _ := m.lanChatHub.ApproveMessage(msg.ID)
			p.approvalPopup = false
			if approved != nil {
				m.lanChatPendingComplete = approved.ID
				agentText := fmt.Sprintf("[LAN Chat from %s]: %s", approved.FromNick, approved.Content)
				m.closeLanChatPanel()
				return m, m.submitLanChatAgentText(agentText)
			}
		}
		return m, nil
	case "n", "N", "r", "R":
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
		input := strings.TrimSpace(strings.TrimPrefix(text, "/nick "))
		if input != "" && m.lanChatHub != nil {
			n, r, t := lanchat.ParseNickRoleTeam(input)
			m.lanChatHub.SetNickRoleTeam(n, r, t)
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

			if content == "" {
				return m, nil // empty message
			}

			// Find target participant
			for _, part := range m.lanChatHub.Participants() {
				if part.HumanNick == mention || part.AgentNick == mention {
					role := lanchat.RoleHuman
					if mention == part.AgentNick {
						role = lanchat.RoleAgent
					}
					m.lanChatHub.SendDirect(context.Background(), part.NodeID, role, content, nil)
					return m, nil
				}
			}
			// Mention not found
			p.notice = fmt.Sprintf("Unknown @mention: %s", mention)
			return m, nil
		}
		// @ with no space and no content — just the @nick, ignore
		return m, nil
	}

	// Team-scoped broadcast (default: your own team)
	m.lanChatBroadcastTeam(context.Background(), text)
	return m, nil
}

func (m *Model) refreshMentionList() {
	p := m.lanChatPanel
	if m.lanChatHub == nil {
		return
	}

	selfID := m.lanChatHub.NodeID()
	all := m.lanChatHub.Participants()
	var targets []mentionTarget
	for _, part := range all {
		// Exclude self from mention list
		if part.NodeID == selfID {
			continue
		}
		if !part.Online {
			continue
		}
		// Human target (only if nick is known)
		if part.HumanNick != "" {
			t := mentionTarget{NodeID: part.NodeID, Nick: part.HumanNick, Role: lanchat.RoleHuman}
			if p.mentionQuery == "" || strings.HasPrefix(strings.ToLower(t.Nick), strings.ToLower(p.mentionQuery)) {
				targets = append(targets, t)
			}
		}
		// Agent target (only if different from human nick)
		if part.AgentNick != "" && part.AgentNick != part.HumanNick {
			t := mentionTarget{NodeID: part.NodeID, Nick: part.AgentNick, Role: lanchat.RoleAgent}
			if p.mentionQuery == "" || strings.HasPrefix(strings.ToLower(t.Nick), strings.ToLower(p.mentionQuery)) {
				targets = append(targets, t)
			}
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Nick < targets[j].Nick
	})
	p.mentionList = targets
	if p.mentionIdx >= len(targets) {
		p.mentionIdx = 0
	}
}

// ---- View ----

func (m *Model) renderLanChatPanel() string {
	p := m.lanChatPanel
	if p == nil {
		return ""
	}

	hub := m.lanChatHub
	var body []string

	// Online participants header — show one entry per human, not per role
	onlineStr := "No one online"
	if hub != nil {
		parts := hub.Participants()
		nicks := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Online && part.HumanNick != "" {
				nick := part.HumanNick
				if part.NodeID == hub.NodeID() {
					nick += " (you)"
				}
				nicks = append(nicks, nick)
			}
		}
		if len(nicks) > 0 {
			sort.Strings(nicks)
			onlineStr = strings.Join(nicks, "  ")
		}
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7DD3FC"))
	body = append(body, headerStyle.Render("Online: "+onlineStr))
	body = append(body, "")

	// Messages — show as many as fit in the panel, minus chrome lines
	// (header + blank + input + hint + input line = ~5 lines)
	if hub != nil {
		msgs := hub.Messages()
		availH := m.panelAvailableHeight()
		// Reserve lines for header, blank, input hint, input line, padding
		availMsgLines := availH - 6
		if availMsgLines < 5 {
			availMsgLines = 5
		}

		if len(msgs) == 0 {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  (no messages yet)"))
		} else {
			// Render messages from newest backward, accumulating until we run
			// out of available lines. Each message may wrap to multiple lines.
			var rendered []string
			usedLines := 0
			for i := len(msgs) - 1; i >= 0; i-- {
				line := m.renderLanChatMessage(msgs[i])
				lineHeight := lipgloss.Height(line) // may be >1 if content is long
				if usedLines+lineHeight > availMsgLines && len(rendered) > 0 {
					// No more room — add truncation indicator
					remaining := len(msgs) - len(rendered)
					if remaining > 0 {
						rendered = append([]string{
							lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
								fmt.Sprintf("  ↑ %d earlier messages hidden", remaining)),
						}, rendered...)
					}
					break
				}
				usedLines += lineHeight
				rendered = append([]string{line}, rendered...)
			}
			body = append(body, rendered...)
		}
	}

	// Approval popup
	if p.approvalPopup && hub != nil {
		pending := hub.PendingApprovals()
		if len(pending) > 0 {
			body = append(body, "")
			current := pending[p.approvalIdx]
			popup := fmt.Sprintf(">> %s -> your agent:\n  %q\n\n  [Enter] Approve Once  [A] Always Approve  [N] Reject  [Esc] Close",
				current.Message.FromNick, current.Message.Content)
			popupStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FBBF24")).
				Bold(true)
			body = append(body, popupStyle.Render(popup))
		}
	}

	// Mention autocomplete — one line per target (human or agent)
	if p.mentionMode && len(p.mentionList) > 0 {
		body = append(body, "")
		for i, t := range p.mentionList {
			prefix := "  "
			style := lipgloss.NewStyle()
			if i == p.mentionIdx {
				prefix = "> "
				style = style.Foreground(lipgloss.Color("#FBBF24")).Bold(true)
			}
			icon := "[H]"
			if t.Role == lanchat.RoleAgent {
				icon = "[A]"
			}
			body = append(body, style.Render(fmt.Sprintf("%s%s %s", prefix, icon, t.Nick)))
		}
	}

	// Input line
	body = append(body, "")
	inputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
	hint := "[@] Mention  [Esc] Close"
	if p.mentionMode {
		hint = "[Enter/Tab] Select  [Up/Down] Navigate  [Esc] Cancel"
	}
	body = append(body, inputStyle.Render(hint))
	body = append(body, fmt.Sprintf("> %s_", p.input))

	return m.renderContextBox("/chat - LAN Chat", strings.Join(body, "\n"), lipgloss.Color("11"))
}

// renderLanChatMessage renders a single chat message with offline indicator.
func (m *Model) renderLanChatMessage(msg lanchat.Message) string {
	ts := time.UnixMilli(msg.Timestamp).Format("15:04")
	icon := "[H]"
	if msg.FromRole == lanchat.RoleAgent {
		icon = "[A]"
	}

	senderStyle := lipgloss.NewStyle().Bold(true)
	contentStyle := lipgloss.NewStyle()

	// Dim messages from offline users
	senderOnline := m.lanChatHub != nil && m.lanChatHub.IsOnline(msg.FromNodeID)
	if !senderOnline {
		senderStyle = senderStyle.Foreground(lipgloss.Color("8")) // grey
		contentStyle = contentStyle.Foreground(lipgloss.Color("8"))
	}

	if msg.IsBroadcast() {
		return fmt.Sprintf("  [%s] %s%s: %s", ts, icon, senderStyle.Render(msg.FromNick), contentStyle.Render(msg.Content))
	}

	// Direct message
	dmTag := "->"
	if msg.ToRole == lanchat.RoleAgent {
		dmTag = "->agent"
	}
	return fmt.Sprintf("  [%s] %s%s %s: %s", ts, icon, senderStyle.Render(msg.FromNick), dmTag, contentStyle.Render(msg.Content))
}

// handleNickCommand processes /nick <name> from the main chat input.
func (m Model) handleNickCommand(parts []string) {
	if m.lanChatHub == nil {
		m.chatWriteSystem(nextSystemID(), "LAN chat is not available (A2A not enabled)")
		return
	}
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		// Show current nick, role, and team
		current := m.lanChatHub.HumanNick()
		role := m.lanChatHub.Role()
		team := m.lanChatHub.Team()
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Current: %s (role: %s, team: %s, agent: %s)", current, role, team, m.lanChatHub.AgentNick()))
		return
	}
	input := strings.TrimSpace(parts[1])
	nick, role, team := lanchat.ParseNickRoleTeam(input)
	if nick == "" {
		m.chatWriteSystem(nextSystemID(), "Nickname cannot be empty (format: /nick name[@role][@team])")
		return
	}
	if err := m.lanChatHub.SetNickRoleTeam(nick, role, team); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to set nickname: %v", err))
		return
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Set: %s (role: %s, team: %s, agent: %s)", m.lanChatHub.HumanNick(), role, team, m.lanChatHub.AgentNick()))
}
