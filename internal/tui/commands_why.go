package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
)

func (m *Model) handleWhyCommand(parts []string) tea.Cmd {
	out := agent.ExplainDecisions(m.agent.Messages(), agent.WhyCountArg(parts))
	if out == "" {
		m.chatWriteSystem(nextSystemID(), m.t("why.empty"))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), out)
	return nil
}
