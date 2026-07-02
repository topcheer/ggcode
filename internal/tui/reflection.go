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
		m.chatWriteSystem(nextSystemID(), "Agent not initialized.")
		return nil
	}

	workingDir := m.agent.WorkingDir()
	if workingDir == "" {
		m.chatWriteSystem(nextSystemID(), "Working directory not set.")
		return nil
	}

	autoMem := memory.NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		m.chatWriteSystem(nextSystemID(), "Project memory not available for this directory.")
		return nil
	}

	content, _, err := autoMem.LoadAll()
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to load insights: %v", err))
		return nil
	}

	if content == "" {
		m.chatWriteSystem(nextSystemID(),
			"No run insights yet. Insights are automatically generated after each agent run "+
				"with 3+ tool calls or file edits.")
		return nil
	}

	var b strings.Builder
	b.WriteString("## Accumulated Run Insights\n\n")
	b.WriteString(content)
	b.WriteString("\n\n---\n")
	b.WriteString(fmt.Sprintf("Memory location: %s\n", autoMem.Dir()))
	m.chatWriteSystem(nextSystemID(), b.String())
	return nil
}
