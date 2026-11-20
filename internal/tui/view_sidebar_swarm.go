package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/swarm"
)

// Sidebar budget for the swarm team panel (#5851). The caps mirror the MCP
// server list so a wide swarm cannot push the rest of the sidebar off
// screen; hidden rows collapse into a "+N more" line.
const (
	sidebarSwarmMaxTeams   = 3
	sidebarSwarmMaxMembers = 5
)

// renderSidebarSwarmSection surfaces the active persistent-team swarm in
// the sidebar (#5851, cold-start loop face 4): one header row per team and
// one line per surfaced member - name, status, current task.
//
// Data is pulled live from the swarm manager on every view pass, which
// makes the panel event-driven end to end: every swarm lifecycle event
// (teammate_spawned / teammate_working / teammate_idle /
// teammate_shutdown / team_created / team_restored) reaches the TUI as a
// swarmTunnelEventMsg (wired in REPL.SetSwarmManager), each Update cycle
// re-renders the view, and teams that were restored BEFORE the program
// started (AutoRestoreSessionTeams runs before SetSwarmManager in
// cmd/ggcode, so those events never had a listener) are visible on the
// first frame instead of waiting for a late event.
func (m Model) renderSidebarSwarmSection() string {
	if m.swarmMgr == nil {
		return ""
	}
	teams := m.swarmMgr.ListTeamStatuses()
	if len(teams) == 0 {
		return ""
	}
	// ListTeamStatuses iterates a map; sort so consecutive event-driven
	// re-renders keep the rows stable instead of reshuffling.
	sort.Slice(teams, func(i, j int) bool { return teams[i].ID < teams[j].ID })

	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle(m.t("panel.swarm"))}

	visibleTeams := teams
	if len(visibleTeams) > sidebarSwarmMaxTeams {
		visibleTeams = visibleTeams[:sidebarSwarmMaxTeams]
	}
	for _, team := range visibleTeams {
		rows = append(rows, m.renderSidebarSwarmTeamHeader(team, width))
		members := team.Teammates
		if len(members) > sidebarSwarmMaxMembers {
			members = members[:sidebarSwarmMaxMembers]
		}
		for _, tm := range members {
			rows = append(rows, m.renderSidebarSwarmMemberRow(tm, width))
		}
		if hidden := len(team.Teammates) - sidebarSwarmMaxMembers; hidden > 0 {
			rows = append(rows, truncateDisplayWidth("  "+m.t("swarm.more_members", hidden), width))
		}
	}
	if hidden := len(teams) - len(visibleTeams); hidden > 0 {
		rows = append(rows, truncateDisplayWidth(m.t("swarm.more_teams", hidden), width))
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderSidebarSwarmTeamHeader(team swarm.TeamStatusInfo, width int) string {
	name := compactSingleLine(strings.TrimSpace(team.Name))
	if name == "" {
		name = sidebarShortID(team.ID)
	}
	header := fmt.Sprintf("%s (%s)", name, m.t("swarm.team_summary", len(team.Teammates), team.TaskCount))
	return truncateDisplayWidth(header, width)
}

func (m Model) renderSidebarSwarmMemberRow(tm swarm.TeammateStatusInfo, width int) string {
	icon, statusLabel := m.sidebarSwarmStatusDecor(tm.Status)
	name := compactSingleLine(strings.TrimSpace(tm.Name))
	if name == "" {
		name = tm.ID
	}
	line := fmt.Sprintf("%s [%s]", name, statusLabel)
	if task := compactSingleLine(strings.TrimSpace(tm.CurrentTask)); task != "" {
		line += " " + task
	}
	// Truncate only the plain-text payload, then attach the colored icon:
	// truncating a styled string can cut an ANSI escape sequence in half.
	textWidth := width - 4 // leading indent + icon + separating space
	if textWidth < 4 {
		textWidth = 4
	}
	return "  " + icon + " " + truncateDisplayWidth(line, textWidth)
}

// sidebarSwarmStatusDecor maps a teammate lifecycle status to sidebar
// decor, mirroring sidebarTaskStatusDecor's color language: yellow for
// active work, muted for idle or winding-down members. Labels ride the
// normal i18n catalog so every language gets its usual fallback.
func (m Model) sidebarSwarmStatusDecor(status swarm.TeammateStatus) (string, string) {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	var label string
	switch status {
	case swarm.TeammateWorking:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
		label = m.t("swarm.status.working")
	case swarm.TeammateIdle:
		label = m.t("swarm.status.idle")
	case swarm.TeammateShuttingDown:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		label = m.t("swarm.status.shutting_down")
	default:
		label = compactSingleLine(string(status))
	}
	icon := "•"
	switch status {
	case swarm.TeammateWorking:
		icon = "●"
	case swarm.TeammateIdle:
		icon = "○"
	case swarm.TeammateShuttingDown:
		icon = "⊘"
	}
	return style.Render(icon), label
}
