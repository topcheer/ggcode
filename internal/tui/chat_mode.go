package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/lanchat"
)

func (m *Model) setChatMode(enabled bool) {
	m.chatMode = enabled
	m.syncComposerMode()
	if enabled {

		if m.lanChatUnread > 0 && m.lanChatLastSenderNick != "" {
			suffix := ""
			if m.lanChatLastSenderRole == "agent" {
				suffix = "_agent"
			}
			m.input.SetValue("@" + m.lanChatLastSenderNick + suffix + " ")
			m.input.CursorEnd()
			m.lanChatUnread = 0
		} else {

			m.refreshLanChatTargets()
		}
	}
}

func (m *Model) refreshLanChatTargets() {
	if m.lanChatHub == nil {
		m.autoCompleteActive = false
		m.autoCompleteItems = nil
		return
	}

	selfID := m.lanChatHub.NodeID()
	all := m.lanChatHub.Participants()

	var targets []string

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

	sort.Strings(targets[1:])

	if len(targets) > 1 {
		m.autoCompleteActive = true
		m.autoCompleteKind = "lanchat"
		m.autoCompleteItems = targets
		m.autoCompleteIndex = 0
	} else {
		m.autoCompleteActive = false
		m.autoCompleteItems = nil
	}
}

func (m *Model) submitChatMessage(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	if m.lanChatHub == nil {
		m.chatWriteSystem(nextSystemID(), m.t("lanchat.unavailable"))
		return
	}

	if strings.HasPrefix(text, "@") {
		spaceIdx := strings.Index(text, " ")
		if spaceIdx > 0 {
			mention := text[1:spaceIdx]
			content := strings.TrimSpace(text[spaceIdx:])
			if content == "" {
				return
			}

			for _, part := range m.lanChatHub.Participants() {
				if part.HumanNick == mention || part.AgentNick == mention {
					role := lanchat.RoleHuman
					if mention == part.AgentNick {
						role = lanchat.RoleAgent
					}

					if err := m.lanChatHub.SendDirect(context.Background(), part.NodeID, role, content, nil); err != nil {
						m.chatWriteSystem(nextSystemID(), fmt.Sprintf("[DM → %s] send failed: %v", mention, err))
						return
					}
					m.chatWriteUser(nextSystemID(), fmt.Sprintf("[DM → %s] %s", mention, content))
					return
				}
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Unknown user: @%s", mention))
			return
		}

		return
	}

	if err := m.lanChatBroadcastTeam(context.Background(), text); err != nil {
		m.chatWriteSystem(nextSystemID(), "[Broadcast] send failed: "+err.Error())
		return
	}
	m.chatWriteUser(nextSystemID(), "[Broadcast] "+text)
}

func (m *Model) lanChatBroadcastTeam(ctx context.Context, content string) error {
	hub := m.lanChatHub
	myTeam := hub.Team()
	if myTeam == "" {
		myTeam = "dev-team"
	}

	selfNodeID := hub.NodeID()
	participants := hub.Participants()
	var firstErr error

	var sent int
	for _, p := range participants {
		if p.NodeID == selfNodeID || !p.Online || p.Team != myTeam {
			continue
		}

		if err := hub.SendDirect(ctx, p.NodeID, lanchat.RoleHuman, content, nil); err != nil {
			firstErr = err
		}
		sent++
	}

	if sent == 0 {
		return hub.SendBroadcast(ctx, content, nil)
	}
	return firstErr
}
