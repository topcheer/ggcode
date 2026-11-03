package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
)

func (m *Model) handleEvidenceCommand() tea.Cmd {
	out := agent.FormatCitations(agent.EvidenceCitations(m.agent.Messages()))
	if out == "" {
		m.chatWriteSystem(nextSystemID(), m.t("evidence.empty"))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), out)
	return nil
}
