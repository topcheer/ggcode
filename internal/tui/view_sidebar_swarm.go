package tui

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/util"
)

// maxSwarmSidebarTeammates caps how many teammates are listed per team in
// the sidebar before collapsing the rest into a "+N more" line.
const maxSwarmSidebarTeammates = 8

// swarmTeammateIcon returns the status bullet shown before each member row
// (same icon vocabulary as the A2A section events).
func swarmTeammateIcon(status swarm.TeammateStatus) string {
	switch status {
	case swarm.TeammateWorking:
		return "▶"
	case swarm.TeammateShuttingDown:
		return "⊘"
	default: // TeammateIdle
		return "●"
	}
}

// swarmTeammateStatusLabel maps a TeammateStatus to the short word shown in
// the member row.
func swarmTeammateStatusLabel(status swarm.TeammateStatus) string {
	switch status {
	case swarm.TeammateWorking:
		return "working"
	case swarm.TeammateShuttingDown:
		return "stopping"
	default: // TeammateIdle
		return "idle"
	}
}

// swarmSidebarMemberLine renders one teammate as a single line:
// name / status / current-task summary (#5851).
func swarmSidebarMemberLine(tm swarm.TeammateSnapshot) string {
	line := fmt.Sprintf("  %s %s %s", swarmTeammateIcon(tm.Status), tm.Name, swarmTeammateStatusLabel(tm.Status))
	if task := strings.TrimSpace(tm.CurrentTask); task != "" {
		line += " — " + task
	}
	return line
}

// renderSidebarSwarmSection renders the Swarm Teams sidebar section: one
// header row per active team, then one line per teammate. Hidden entirely
// while no teams exist.
func (m Model) renderSidebarSwarmSection() string {
	if len(m.swarmTeams) == 0 {
		return ""
	}
	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle("Swarm Teams")}
	for _, team := range m.swarmTeams {
		rows = append(rows, util.Truncate(fmt.Sprintf("%s (%d)", team.Name, len(team.Teammates)), width))
		visible := team.Teammates
		if len(visible) > maxSwarmSidebarTeammates {
			visible = visible[:maxSwarmSidebarTeammates]
		}
		for _, tm := range visible {
			rows = append(rows, util.Truncate(swarmSidebarMemberLine(tm), width))
		}
		if hidden := len(team.Teammates) - len(visible); hidden > 0 {
			rows = append(rows, util.Truncate(fmt.Sprintf("  … +%d more", hidden), width))
		}
	}
	return strings.Join(rows, "\n")
}
