package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/swarm"
)

// swarmPanelState holds UI state for the swarm panel.
type swarmPanelState struct {
	cursor int
	scroll int
}

func (m *Model) openSwarmPanel() {
	m.swarmPanel = &swarmPanelState{cursor: 0, scroll: 0}
}

func (m *Model) closeSwarmPanel() {
	m.swarmPanel = nil
}

func (m *Model) renderSwarmPanel() string {
	if m.swarmPanel == nil || m.swarmMgr == nil {
		return ""
	}

	teams := m.swarmMgr.ListTeams()
	if len(teams) == 0 {
		body := " No teams.\n Use team_create to start a collaboration team."
		return m.renderContextBox("/swarm", body, lipgloss.Color("33"))
	}

	var sb strings.Builder
	sb.WriteString("\n")

	for _, team := range teams {
		// Team header
		sb.WriteString(fmt.Sprintf(" Team: %s (%s)\n", team.Name, team.ID))

		if len(team.Teammates) == 0 {
			sb.WriteString("   (no teammates)\n")
			continue
		}

		// Sort teammates by ID
		sorted := make([]swarm.TeammateSnapshot, len(team.Teammates))
		copy(sorted, team.Teammates)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

		for _, tm := range sorted {
			statusIcon := swarmStatusIcon(tm.Status)
			task := ""
			if tm.CurrentTask != "" {
				task = fmt.Sprintf(" — %s", tm.CurrentTask)
			}
			sb.WriteString(fmt.Sprintf("  %s %s [%s]%s\n", statusIcon, tm.Name, tm.Status, task))
		}

		// Show task count
		if team.TaskCount > 0 {
			sb.WriteString(fmt.Sprintf("  Tasks: %d\n", team.TaskCount))
		}
		sb.WriteString("\n")
	}

	return m.renderContextBox("/swarm", sb.String(), lipgloss.Color("33"))
}

func (m *Model) handleSwarmPanelKey(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.swarmPanel == nil {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "q":
			m.closeSwarmPanel()
		case "j", "down":
			m.swarmPanel.cursor++
		case "k", "up":
			if m.swarmPanel.cursor > 0 {
				m.swarmPanel.cursor--
			}
		}
	}
	return m, nil
}

func swarmStatusIcon(status swarm.TeammateStatus) string {
	switch status {
	case swarm.TeammateIdle:
		return "○"
	case swarm.TeammateWorking:
		return "●"
	case swarm.TeammateShuttingDown:
		return "✕"
	default:
		return "?"
	}
}

// renderSwarmSidebar renders a compact swarm status for the sidebar.
func (m *Model) renderSwarmSidebar() string {
	if m.swarmMgr == nil {
		return ""
	}

	teams := m.swarmMgr.ListTeams()
	activeCount := 0
	for _, team := range teams {
		for _, tm := range team.Teammates {
			if tm.Status == swarm.TeammateWorking || tm.Status == swarm.TeammateIdle {
				activeCount++
			}
		}
	}
	if activeCount == 0 {
		return ""
	}

	width := m.sidebarWidth() - 4
	var rows []string
	rows = append(rows, m.renderSidebarSectionTitle("Swarm"))

	for _, team := range teams {
		if len(team.Teammates) == 0 {
			continue
		}
		for _, tm := range team.Teammates {
			icon := "○"
			if tm.Status == swarm.TeammateWorking {
				icon = "●"
			}
			label := fmt.Sprintf("%s %s", icon, tm.Name)
			status := string(tm.Status)
			if tm.Status == swarm.TeammateWorking && tm.CurrentTask != "" {
				// Show truncated task (rune-safe for CJK)
				task := tm.CurrentTask
				runes := []rune(task)
				if len(runes) > 20 {
					task = string(runes[:17]) + "..."
				}
				status = task
			}
			rows = append(rows, m.renderSidebarDetailRow(label, status, width))
		}
	}

	return strings.Join(rows, "\n") + "\n"
}
