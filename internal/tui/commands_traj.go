package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
)

func trajClearArgIsGlobal(parts []string) bool {
	return len(parts) >= 3 && strings.EqualFold(strings.TrimSpace(parts[2]), "global")
}

func (m *Model) handleExportTrainingCommand() tea.Cmd {
	wd := m.agent.WorkingDir()
	if wd == "" {
		m.chatWriteSystem(nextSystemID(), m.t("traj.no_workspace"))
		return nil
	}
	msgs := m.currentSessionMessages()
	if len(msgs) == 0 {
		m.chatWriteSystem(nextSystemID(), m.t("trainexport.empty"))
		return nil
	}
	n, err := agent.ExportSessionTrainingSample(wd, "(tui manual export)", msgs)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("/export-training: %v", err))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf(m.t("trainexport.done"), n))
	return nil
}

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

		if trajClearArgIsGlobal(parts) {
			if err := agent.TrajClearGlobalLearnings(); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("/traj clear global: %v", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("traj.cleared_global"))
			return nil
		}
		if err := agent.TrajClearLearnings(wd); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("/traj clear: %v", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("traj.cleared"))

		if n, gErr := agent.TrajGlobalRemaining(); gErr == nil && n > 0 {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf(m.t("traj.cleared_global_hint"), n))
		}
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
			suffix := ""
			switch {
			case v.Holdout:

				marker = "h"
				suffix = fmt.Sprintf(" (Δ%+.0fpp/%d)", v.DeltaPP, v.HoldRuns)
			case v.Injects && v.General:
				marker = "g"
				injecting++
			case v.Injects:
				marker = ">"
				injecting++
			}
			fmt.Fprintf(&b, "%s %2d. [%s] %s (conf %.2f, x%d)%s\n",
				marker, i+1, v.Type, truncateForDisplay(v.Insight, 80), v.Confidence, v.Reinforced+1, suffix)
		}
		m.chatWriteSystem(nextSystemID(),
			fmt.Sprintf(m.t("traj.header"), len(views), injecting)+"\n"+b.String())
		return nil
	case "export":
		out := agent.TrajGlobalStorePath()
		if len(parts) > 2 {
			out = strings.TrimSpace(parts[2])
		}
		if out == "" {
			m.chatWriteSystem(nextSystemID(), m.t("traj.no_home"))
			return nil
		}
		n, err := agent.TrajExportLearnings(wd, out)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("/traj export: %v", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(m.t("traj.exported"), n, out))
		return nil
	case "import":
		in := agent.TrajGlobalStorePath()
		if len(parts) > 2 {
			in = strings.TrimSpace(parts[2])
		}
		if in == "" {
			m.chatWriteSystem(nextSystemID(), m.t("traj.no_home"))
			return nil
		}
		n, err := agent.TrajMergeInto(wd, in)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("/traj import: %v", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(m.t("traj.imported"), n))
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
