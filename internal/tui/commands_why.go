package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
)

// handleWhyCommand (r103) renders structured decision provenance: for each
// recent tool call, the thinking block that preceded it in the same assistant
// message plus the paired result (with file:line anchors on errors). Before
// this, "why did you do X" could only be answered by the model re-reading
// and paraphrasing its own transcript — the association between recorded
// reasoning and recorded action was never surfaced (2026 explainability
// primitive: tool-call attribution + citation anchors).
func (m *Model) handleWhyCommand(parts []string) tea.Cmd {
	out := agent.ExplainDecisions(m.agent.Messages(), agent.WhyCountArg(parts))
	if out == "" {
		m.chatWriteSystem(nextSystemID(), m.t("why.empty"))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), out)
	return nil
}
