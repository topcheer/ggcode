package tui

import (
	"fmt"
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
		existing, _, err := autoMem.LoadAll()
		if err == nil && existing != "" {
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
