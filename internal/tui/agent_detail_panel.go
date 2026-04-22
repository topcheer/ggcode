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
	// track last known event count for auto-scroll
	lastEventCount int
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

	width := m.boxInnerWidth(m.mainColumnWidth())
	innerWidth := max(20, width-4) // box padding

	// Auto-scroll: if new events arrived, scroll to bottom
	panel := m.agentDetailPanel
	if len(events) > panel.lastEventCount {
		panel.scrollY = 99999
		panel.lastEventCount = len(events)
	}

	// --- Render header ---
	var header strings.Builder
	header.WriteString(lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("Agent %s", snap.ID),
	))
	if snap.Task != "" {
		taskPreview := snap.Task
		// Truncate task to fit on one line
		taskPreview = truncateString(taskPreview, innerWidth-lipgloss.Width(header.String())-5)
		header.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
			fmt.Sprintf("  •  %s", taskPreview),
		))
	}
	header.WriteString("\n")

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

	statusLine := statusStyle.Render(string(snap.Status))
	if snap.ToolCallCount > 0 {
		statusLine += fmt.Sprintf("  •  %d tools", snap.ToolCallCount)
	}
	if snap.ProgressSummary != "" {
		summary := truncateString(snap.ProgressSummary, innerWidth-lipgloss.Width(statusLine)-5)
		statusLine += lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
			fmt.Sprintf("  •  %s", summary),
		)
	}
	header.WriteString(statusLine)
	header.WriteString("\n")

	// Body height — use total terminal height minus header/footer overhead.
	// Do NOT call conversationPanelHeight() — that triggers infinite recursion
	// via renderContextPanel → renderAgentDetailPanel.
	overhead := 8 // status bar(2) + action bar(1) + box border(2) + header(~2) + footer(1)
	bodyHeight := m.height - overhead
	if bodyHeight < 5 {
		bodyHeight = 5
	}

	// --- Render event body ---
	bodyContent := m.renderAgentDetailEvents(events, innerWidth, bodyHeight)

	// --- Apply scroll ---
	bodyLines := strings.Split(bodyContent, "\n")
	totalLines := len(bodyLines)

	// Clamp scrollY
	maxScroll := totalLines - bodyHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if panel.scrollY > maxScroll {
		panel.scrollY = maxScroll
	}

	// Window the visible lines
	visibleFrom := panel.scrollY
	visibleTo := visibleFrom + bodyHeight
	if visibleTo > totalLines {
		visibleTo = totalLines
	}
	visibleBody := strings.Join(bodyLines[visibleFrom:visibleTo], "\n")

	// Pad if not enough lines
	visibleCount := visibleTo - visibleFrom
	for i := visibleCount; i < bodyHeight; i++ {
		visibleBody += "\n"
	}

	// --- Assemble full panel ---
	var full strings.Builder
	full.WriteString(header.String())
	full.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		strings.Repeat("─", innerWidth),
	))
	full.WriteString("\n")
	full.WriteString(visibleBody)
	full.WriteString("\n")

	// Footer
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	hints := "esc close  •  ↑↓/jk scroll  •  g top  •  G bottom"
	if snap.Status == subagent.StatusRunning {
		hints = "⏳ " + hints
	}
	scrollInfo := ""
	if totalLines > bodyHeight {
		scrollInfo = fmt.Sprintf("  [%d/%d]", visibleFrom+1, totalLines)
	}
	full.WriteString(hintStyle.Render(hints + scrollInfo))

	return m.renderContextBox("/agent "+snap.ID, full.String(), lipgloss.Color("12"))
}

// renderAgentDetailEvents renders the event list as styled lines.
func (m *Model) renderAgentDetailEvents(events []subagent.AgentEvent, width, maxLines int) string {
	if len(events) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Waiting for events...")
	}

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	resultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))

	var lines []string
	lineCount := 0

	// Accumulate text events for markdown batch rendering
	var textBuf strings.Builder

	flushText := func() {
		text := strings.TrimRight(textBuf.String(), "\n")
		textBuf.Reset()
		if text == "" {
			return
		}
		// Render markdown
		rendered := trimLeadingRenderedSpacing(RenderMarkdownWidth(text, max(20, width-2)))
		for _, line := range strings.Split(rendered, "\n") {
			if lineCount >= maxLines*3 { // allow overflow for scroll
				return
			}
			lines = append(lines, line)
			lineCount++
		}
	}

	for _, ev := range events {
		switch ev.Type {
		case subagent.AgentEventText:
			text := ev.Text
			if text == "" {
				continue
			}
			textBuf.WriteString(text)

		case subagent.AgentEventToolCall:
			flushText()
			// Tool call header
			toolLine := toolStyle.Render("● ") + ev.ToolName
			lines = append(lines, toolLine)
			lineCount++

			// Args preview — pretty format JSON if possible
			if ev.ToolArgs != "" {
				argsPreview := ev.ToolArgs
				if len(argsPreview) > 300 {
					argsPreview = argsPreview[:300] + "…"
				}
				argsPreview = strings.ReplaceAll(argsPreview, "\n", " ")
				// Indent and wrap
				argLines := wrapHarnessPanelText(dimStyle.Render("  │ "+argsPreview), max(20, width-4), 3)
				for _, al := range argLines {
					lines = append(lines, al)
					lineCount++
				}
			}

		case subagent.AgentEventToolResult:
			flushText()
			prefix := "  └ "
			if ev.IsError {
				resultLine := errorStyle.Render(prefix+"error") + ": "
				if ev.Result != "" {
					resultPreview := ev.Result
					if len(resultPreview) > 300 {
						resultPreview = resultPreview[:300] + "…"
					}
					resultLine += errorStyle.Render(strings.ReplaceAll(resultPreview, "\n", " "))
				}
				lines = append(lines, resultLine)
				lineCount++
			} else {
				resultLine := resultStyle.Render(prefix + "✓ done")
				if ev.Result != "" {
					resultPreview := ev.Result
					if len(resultPreview) > 300 {
						resultPreview = resultPreview[:300] + "…"
					}
					resultLine += dimStyle.Render(": " + strings.ReplaceAll(resultPreview, "\n", " "))
				}
				lines = append(lines, resultLine)
				lineCount++
			}

		case subagent.AgentEventError:
			flushText()
			lines = append(lines, errorStyle.Render("✗ "+ev.Text))
			lineCount++
		}
	}
	flushText()

	return strings.Join(lines, "\n")
}

// handleAgentDetailPanelKey handles key events when the agent detail panel is open.
func (m *Model) handleAgentDetailPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.agentDetailPanel == nil {
		return *m, nil
	}
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
