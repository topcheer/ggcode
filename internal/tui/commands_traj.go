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
// trajClearArg reports whether the /traj clear invocation carries the
// "global" argument. parts is the FULL command split (parts[0] =
// "/traj"), so the argument lives at parts[2] - the first version of
// #3278 checked parts[1] ("clear"), an unreachable branch inside
// case "clear" (终裁打回：接线死分支)。Extracted so the parsing has
// its own probe.
func trajClearArgIsGlobal(parts []string) bool {
	return len(parts) >= 3 && strings.EqualFold(strings.TrimSpace(parts[2]), "global")
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
		// #3278: /traj clear global purges the user-level global tier
		// (bare clear only removes the workspace file - global fillers
		// kept injecting and were previously unkillable without rm).
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
		// #3278: say it when the global tier still holds entries - a
		// silent partial purge left g-marked items whispering into every
		// future prompt with no visible remedy.
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
				// r462 control arm: held out of injection, delta vs holdout.
				marker = "h"
				suffix = fmt.Sprintf(" (Δ%+.0fpp/%d)", v.DeltaPP, v.HoldRuns)
			case v.Injects && v.General:
				marker = "g" // #3266(H): global-tier entry that injects
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
