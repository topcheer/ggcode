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

// setupReflection configures the agent's post-run reflection handler.
// After each RunStreamWithContent call, the handler analyzes what happened
// (tools used, files edited, commands run, errors encountered) and saves a
// concise summary to project memory so knowledge compounds across sessions.
//
// This implements the "hill climbing loop" from loop engineering: every run's
// learnings become persistent context for future sessions.
//
// Only runs with >=3 tool calls or any file edits get a memory entry.
func setupReflection(a *agent.Agent) {
	a.SetReflectionFunc(func(stats agent.RunStats) {
		if !agent.ShouldReflect(stats) {
			return
		}

		// Experience Case Bank capture (Memento-style case-based memory,
		// arXiv:2508.16153): distill this run into a per-task case
		// (task/approach/outcome) in the project experience store. Runs
		// independently of the rolling run-insights blob below so a case is
		// recorded even when insight generation yields nothing.
		recordExperienceCase(a, stats)

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

		key := "run-insights"
		// #1388: LoadAll merges EVERY active memory key ("### {key}" per
		// entry) - using it here ingested all unrelated memories into
		// run-insights on every reflection, duplicating them in prompt
		// injection (original key + run-insights copy). Single-key load.
		existing, err := autoMem.LoadKey(key)
		if err != nil {
			// #1388 side-fix: a read error used to be swallowed and the save
			// below then OVERWROTE the accumulated insights with the fresh
			// batch (narrow window, but silent loss). Abort this round - the
			// next reflection retries; old accumulation stays intact.
			debug.Log("tui", "reflection: failed to load existing insights, skipping save: %v", err)
			return
		}
		if existing != "" {
			insights = agent.MergeInsights(existing, insights)
		}

		if err := autoMem.SaveMemory(key, insights); err != nil {
			debug.Log("tui", "reflection: failed to save insights: %v", err)
		} else {
			debug.Log("tui", "reflection: saved insights (%d chars, %d tools, %d files, %d commands)",
				len(insights), len(stats.ToolCalls), len(stats.FilesEdited), len(stats.CommandsRun))
		}
	})
}

// recordExperienceCase distills a completed run into the project experience
// store as a per-task case: task (the user prompt), approach (what the agent
// actually did — turns, tools, files, commands), and outcome. Failures are
// debug-logged only; experience capture must never disturb the session.
func recordExperienceCase(a *agent.Agent, stats agent.RunStats) {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return
	}
	store := memory.NewProjectExperienceStore(workingDir)
	if store == nil {
		return
	}

	outcome := "partial"
	switch {
	case stats.Success && stats.ErrorCount == 0:
		outcome = "success"
	case !stats.Success:
		outcome = "failed"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d LLM turns, %d tool calls", stats.Iterations, len(stats.ToolCalls))
	if top := topToolCalls(stats.ToolCalls, 3); top != "" {
		fmt.Fprintf(&b, " (top: %s)", top)
	}
	b.WriteString(".")
	if len(stats.FilesEdited) > 0 {
		fmt.Fprintf(&b, " Edited: %s.", strings.Join(stats.FilesEdited, ", "))
	}
	if len(stats.CommandsRun) > 0 {
		fmt.Fprintf(&b, " Commands: %s.", strings.Join(stats.CommandsRun, "; "))
	}
	if stats.ErrorCount > 0 && len(stats.Errors) > 0 {
		fmt.Fprintf(&b, " First error: %s.", strings.Join(strings.Fields(stats.Errors[0]), " "))
	}

	if _, _, err := store.Record(stats.UserPrompt, b.String(), outcome, stats.FilesEdited); err != nil {
		debug.Log("tui", "experience: record failed: %v", err)
	}
}

// topToolCalls renders the n most-used tools as "name(count)" pairs.
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

// handleReflectCommand displays accumulated run insights.
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
