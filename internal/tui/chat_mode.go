package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// setChatMode toggles LAN Chat quick-send mode.
// Unlike shell mode, chat mode persists after Enter — the user must press Esc to exit.
func (m *Model) setChatMode(enabled bool) {
	m.chatMode = enabled
	m.syncComposerMode()
	if enabled {
		// If there are unread messages, prefill @lastSenderNick
		if m.lanChatUnread > 0 && m.lanChatLastSenderNick != "" {
			suffix := ""
			if m.lanChatLastSenderRole == "agent" {
				suffix = "_agent"
			}
			m.input.SetValue("@" + m.lanChatLastSenderNick + suffix + " ")
			m.input.CursorEnd()
			m.lanChatUnread = 0
		} else {
			// Auto-show user list on first entry
			m.refreshLanChatTargets()
		}
	}
}

// refreshLanChatTargets builds the autocomplete list from online participants.
// The list includes "All" (broadcast) plus all online participants (human + agent nicks).
func (m *Model) refreshLanChatTargets() {
	if m.lanChatHub == nil {
		m.autoCompleteActive = false
		m.autoCompleteItems = nil
		return
	}

	selfID := m.lanChatHub.NodeID()
	all := m.lanChatHub.Participants()

	var targets []string
	// "All" is always first — selecting it means broadcast (no @mention prefix)
	if m.currentLanguage() == LangZhCN {
		targets = append(targets, "所有人")
	} else {
		targets = append(targets, "All")
	}

	for _, part := range all {
		if part.NodeID == selfID || !part.Online {
			continue
		}
		if part.HumanNick != "" {
			targets = append(targets, part.HumanNick)
		}
		if part.AgentNick != "" && part.AgentNick != part.HumanNick {
			targets = append(targets, part.AgentNick)
		}
	}

	sort.Strings(targets[1:]) // keep "All" first, sort the rest

	if len(targets) > 0 {
		m.autoCompleteActive = true
		m.autoCompleteKind = "lanchat"
		m.autoCompleteItems = targets
		m.autoCompleteIndex = 0
	} else {
		m.autoCompleteActive = false
		m.autoCompleteItems = nil
	}
}

// submitChatMessage sends a message via LAN Chat.
// Format: "@nick message" → DM, plain text → broadcast.
// Stays in chat mode after sending.
func (m *Model) submitChatMessage(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	if m.lanChatHub == nil {
		m.chatWriteSystem(nextSystemID(), "LAN Chat is not available.")
		return
	}

	// Parse @mention
	if strings.HasPrefix(text, "@") {
		spaceIdx := strings.Index(text, " ")
		if spaceIdx > 0 {
			mention := text[1:spaceIdx]
			content := strings.TrimSpace(text[spaceIdx:])
			if content == "" {
				return
			}
			// Find target participant
			for _, part := range m.lanChatHub.Participants() {
				if part.HumanNick == mention || part.AgentNick == mention {
					role := lanchat.RoleHuman
					if mention == part.AgentNick {
						role = lanchat.RoleAgent
					}
					m.lanChatHub.SendDirect(context.Background(), part.NodeID, role, content, nil)
					m.chatWriteUser(nextSystemID(), fmt.Sprintf("[DM → %s] %s", mention, content))
					return
				}
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Unknown user: @%s", mention))
			return
		}
		// Just "@nick" with no message — ignore
		return
	}

	// Team-scoped broadcast (default: your own team)
	m.lanChatBroadcastTeam(context.Background(), text)
	m.chatWriteUser(nextSystemID(), "[Broadcast] "+text)
}

// lanChatBroadcastTeam sends a message to all online members of the sender's own team.
// Falls back to global broadcast if the team has no other online members.
func (m *Model) lanChatBroadcastTeam(ctx context.Context, content string) {
	hub := m.lanChatHub
	myTeam := hub.Team()
	if myTeam == "" {
		myTeam = "dev-team"
	}

	selfNodeID := hub.NodeID()
	participants := hub.Participants()

	var sent int
	for _, p := range participants {
		if p.NodeID == selfNodeID || !p.Online || p.Team != myTeam {
			continue
		}
		hub.SendDirect(ctx, p.NodeID, lanchat.RoleAgent, content, nil)
		sent++
	}

	// If no team members found, fall back to global broadcast
	if sent == 0 {
		hub.SendBroadcast(ctx, content, nil)
	}
}
