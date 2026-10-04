package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
)

// handleTrajCommand (r459) exposes the trajectory-learning store to the
// user: /traj lists what gets injected into the system prompt (with
// confidence), /traj clear purges it. Before this, injected learnings
// were invisible and unkillable - a polluted store kept whispering into
// every run's prompt.
func (m *Model) handleTrajCommand(parts []string) tea.Cmd {
	wd := m.agent.WorkingDir()
	if wd == "" {
		m.chatWriteSystem(nextSystemID(), m.t("traj.no_workspace"))
		return nil
	}
	sub := "list"
	if len(parts) > 1 {
		sub = strings.ToLower(strings.TrimSpace(parts[1]))
	}
	switch sub {
	case "clear", "purge":
		if err := agent.TrajClearLearnings(wd); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("/traj clear: %v", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("traj.cleared"))
		return nil
	case "list", "":
		views := agent.TrajListLearnings(wd)
		if len(views) == 0 {
			m.chatWriteSystem(nextSystemID(), m.t("traj.empty"))
			return nil
		}
		var b strings.Builder
		injecting := 0
		for i, v := range views {
			marker := " "
			if v.Injects {
				marker = ">"
				injecting++
			}
			fmt.Fprintf(&b, "%s %2d. [%s] %s (conf %.2f, x%d)\n",
				marker, i+1, v.Type, truncateForDisplay(v.Insight, 80), v.Confidence, v.Reinforced+1)
		}
		m.chatWriteSystem(nextSystemID(),
			fmt.Sprintf(m.t("traj.header"), len(views), injecting)+"\n"+b.String())
		return nil
	default:
		m.chatWriteSystem(nextSystemID(), m.t("traj.usage"))
		return nil
	}
}

func truncateForDisplay(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}
