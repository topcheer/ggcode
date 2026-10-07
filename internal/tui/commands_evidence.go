package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
)

// handleEvidenceCommand (sa-114) renders claim-level citations for the
// last answer: each code anchor in the reply (file.go:42, `symbol`) is
// linked to the recorded tool call that produced the supporting output.
// /why explains tool-call decisions; /evidence lets the user verify the
// ANSWER's factual claims against recorded evidence without re-asking.
func (m *Model) handleEvidenceCommand() tea.Cmd {
	out := agent.FormatCitations(agent.EvidenceCitations(m.agent.Messages()))
	if out == "" {
		m.chatWriteSystem(nextSystemID(), m.t("evidence.empty"))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), out)
	return nil
}
