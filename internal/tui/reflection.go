package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
)

func setupReflection(a *agent.Agent) {
	a.SetReflectionFunc(func(stats agent.RunStats) {
		if !agent.ShouldReflect(stats) {
			return
		}

		repeatedTask := recordExperienceCase(a, stats)

		suggestSkillFromRun(a.WorkingDir(), stats, repeatedTask)

		detectMissedSkills(a.WorkingDir(), stats)

		distillSnippetsFromRun(a, stats)

		captureUserPreferences(a, stats)

		insights := agent.GenerateInsights(stats)
		if insights == "" {
			return
		}

		workingDir := a.WorkingDir()
		if workingDir == "" {
			return
		}

		autoMem := memory.NewProjectAutoMemory(workingDir)
		if autoMem == nil {
			return
		}

		// #5136: pre-#5118 insight entries still embed raw prompts and are
		// re-injected into future prompts - cure them once, idempotently.
		agent.ScrubPromptLeakedMemoryEntries(workingDir)

		if corpus := stats.AssistantCorpus(); corpus != "" {
			autoMem.ScanConsumption(corpus)
			if globalMem := memory.NewAutoMemory(); globalMem != nil {
				globalMem.ScanConsumption(corpus)
			}
		}

		key := "run-insights"

		existing, err := autoMem.LoadKey(key)
		if err != nil {

			debug.Log("tui", "reflection: failed to load existing insights, skipping save: %v", err)
			return
		}
		if existing != "" {
			insights = agent.MergeInsights(existing, insights)
		}

		if err := autoMem.SaveMemory(key, insights); err != nil {
			debug.Log("tui", "reflection: failed to save insights: %v", err)
		} else {

			autoMem.RecordOutcome(key, runOutcome(stats))
			debug.Log("tui", "reflection: saved insights (%d chars, %d tools, %d files, %d commands, outcome=%s)",
				len(insights), len(stats.ToolCalls), len(stats.FilesEdited), len(stats.CommandsRun), runOutcome(stats))
		}
	})
}

func runOutcome(stats agent.RunStats) string {
	switch {
	case stats.Success && stats.ErrorCount == 0:
		return "success"
	case !stats.Success:
		return "failed"
	default:
		return "partial"
	}
}

func recordExperienceCase(a *agent.Agent, stats agent.RunStats) (repeatedTask bool) {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return false
	}
	store := memory.NewProjectExperienceStore(workingDir)
	if store == nil {
		return false
	}

	outcome := runOutcome(stats)

	var b strings.Builder
	fmt.Fprintf(&b, "%d LLM turns, %d tool calls", stats.Iterations, len(stats.ToolCalls))
	if top := topToolCalls(stats.ToolCalls, 3); top != "" {
		fmt.Fprintf(&b, " (top: %s)", top)
	}
	b.WriteString(".")
	if len(stats.FilesEdited) > 0 {
		fmt.Fprintf(&b, " Edited: %s.", strings.Join(stats.FilesEdited, ", "))
	}
	if cmds := successfulCommandsLabel(stats); cmds != "" {
		fmt.Fprintf(&b, " Commands: %s.", cmds)
	}
	if stats.ErrorCount > 0 && len(stats.Errors) > 0 {
		fmt.Fprintf(&b, " First error: %s.", strings.Join(strings.Fields(stats.Errors[0]), " "))
	}

	_, updated, err := store.Record(stats.UserPrompt, b.String(), outcome, stats.FilesEdited)
	if err != nil {
		debug.Log("tui", "experience: record failed: %v", err)
		return false
	}
	return updated
}

func captureUserPreferences(a *agent.Agent, stats agent.RunStats) {
	full := stats.UserPromptFull
	if full == "" {
		full = stats.UserPrompt
	}
	memory.CapturePreferences(a.WorkingDir(), full)
}

func topToolCalls(calls map[string]int, n int) string {
	type tc struct {
		name  string
		count int
	}
	var list []tc
	for name, count := range calls {
		if count > 0 {
			list = append(list, tc{name, count})
		}
	}
	if len(list) == 0 {
		return ""
	}
	slices.SortFunc(list, func(x, y tc) int {
		if x.count != y.count {
			return y.count - x.count
		}
		return strings.Compare(x.name, y.name)
	})
	if len(list) > n {
		list = list[:n]
	}
	parts := make([]string, len(list))
	for i, c := range list {
		parts[i] = fmt.Sprintf("%s(%d)", c.name, c.count)
	}
	return strings.Join(parts, ", ")
}

func (m *Model) handleReflectCommand() tea.Cmd {
	if m.agent == nil {
		m.chatWriteSystem(nextSystemID(), m.t("reflect.no_agent"))
		return nil
	}

	workingDir := m.agent.WorkingDir()
	if workingDir == "" {
		m.chatWriteSystem(nextSystemID(), m.t("reflect.no_workdir"))
		return nil
	}

	autoMem := memory.NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		m.chatWriteSystem(nextSystemID(), m.t("reflect.no_memory"))
		return nil
	}

	content, _, err := autoMem.LoadAll()
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(m.t("reflect.load_failed"), err))
		return nil
	}

	if content == "" {
		m.chatWriteSystem(nextSystemID(), m.t("reflect.empty"))
		return nil
	}

	var b strings.Builder
	b.WriteString(m.t("reflect.title"))
	b.WriteString(content)
	b.WriteString("\n\n---\n")
	b.WriteString(fmt.Sprintf(m.t("reflect.memory_location"), autoMem.Dir()))
	m.chatWriteSystem(nextSystemID(), b.String())
	return nil
}
