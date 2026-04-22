package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/subagent"
)

// agentDetailPanelState holds the state for the sub-agent detail panel.
type agentDetailPanelState struct {
	agentID string
	scrollY int
}

// openAgentDetailPanel opens the detail panel for a specific sub-agent.
func (m *Model) openAgentDetailPanel(agentID string) {
	m.agentDetailPanel = &agentDetailPanelState{agentID: agentID}
}

// closeAgentDetailPanel closes the sub-agent detail panel.
func (m *Model) closeAgentDetailPanel() {
	m.agentDetailPanel = nil
}

// renderAgentDetailPanel renders the sub-agent event detail panel.
func (m *Model) renderAgentDetailPanel() string {
	if m.agentDetailPanel == nil {
		return ""
	}
	if m.subAgentMgr == nil {
		return ""
	}

	sa, ok := m.subAgentMgr.Get(m.agentDetailPanel.agentID)
	if !ok {
		return m.t("agent.not_found", m.agentDetailPanel.agentID)
	}
	events := sa.Events()
	snap, _ := m.subAgentMgr.Snapshot(m.agentDetailPanel.agentID)

	var b strings.Builder

	// Header
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("Sub-Agent %s  •  %s", snap.ID, snap.Task),
	))
	b.WriteString("\n")

	statusStyle := lipgloss.NewStyle()
	switch snap.Status {
	case subagent.StatusRunning:
		statusStyle = statusStyle.Foreground(lipgloss.Color("14"))
	case subagent.StatusCompleted:
		statusStyle = statusStyle.Foreground(lipgloss.Color("10"))
	case subagent.StatusFailed, subagent.StatusCancelled:
		statusStyle = statusStyle.Foreground(lipgloss.Color("9"))
	default:
		statusStyle = statusStyle.Foreground(lipgloss.Color("8"))
	}
	b.WriteString(statusStyle.Render(string(snap.Status)))
	if snap.ToolCallCount > 0 {
		b.WriteString(fmt.Sprintf("  •  %d tools", snap.ToolCallCount))
	}
	b.WriteString("\n\n")

	// Event list
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

	for _, ev := range events {
		switch ev.Type {
		case subagent.AgentEventText:
			text := ev.Text
			if text == "" {
				continue
			}
			text = strings.TrimRight(text, "\n")
			if text == "" {
				continue
			}
			b.WriteString(dimStyle.Render(text))
			b.WriteString("\n")

		case subagent.AgentEventToolCall:
			b.WriteString(toolStyle.Render("● "))
			b.WriteString(ev.ToolName)
			if ev.ToolArgs != "" {
				argsPreview := ev.ToolArgs
				if len(argsPreview) > 120 {
					argsPreview = argsPreview[:120] + "..."
				}
				argsPreview = strings.ReplaceAll(argsPreview, "\n", " ")
				b.WriteString("\n  │ ")
				b.WriteString(dimStyle.Render(argsPreview))
			}
			b.WriteString("\n")

		case subagent.AgentEventToolResult:
			prefix := "  └ "
			if ev.IsError {
				b.WriteString(errorStyle.Render(prefix + "error"))
				if ev.Result != "" {
					resultPreview := ev.Result
					if len(resultPreview) > 200 {
						resultPreview = resultPreview[:200] + "..."
					}
					b.WriteString(": ")
					b.WriteString(errorStyle.Render(resultPreview))
				}
			} else {
				b.WriteString(dimStyle.Render(prefix + "done"))
				if ev.Result != "" {
					resultPreview := ev.Result
					if len(resultPreview) > 200 {
						resultPreview = resultPreview[:200] + "..."
					}
					resultPreview = strings.ReplaceAll(resultPreview, "\n", " ")
					b.WriteString(dimStyle.Render(": " + resultPreview))
				}
			}
			b.WriteString("\n")

		case subagent.AgentEventError:
			b.WriteString(errorStyle.Render("✗ " + ev.Text))
			b.WriteString("\n")
		}
	}

	if len(events) == 0 {
		b.WriteString(dimStyle.Render("No events recorded yet..."))
	}

	return b.String()
}

// handleAgentDetailPanelKey handles key events when the agent detail panel is open.
func (m *Model) handleAgentDetailPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeAgentDetailPanel()
	case "j", "down":
		m.agentDetailPanel.scrollY++
	case "k", "up":
		if m.agentDetailPanel.scrollY > 0 {
			m.agentDetailPanel.scrollY--
		}
	case "g":
		m.agentDetailPanel.scrollY = 0
	case "G":
		m.agentDetailPanel.scrollY = 99999
	}
	return *m, nil
}
