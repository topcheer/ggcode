package main

import (
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
)

// setupDaemonReflection configures post-run reflection for the daemon agent.
func setupDaemonReflection(ag *agent.Agent, workingDir string) {
	ag.SetReflectionFunc(func(stats agent.RunStats) {
		totalToolCalls := 0
		for _, count := range stats.ToolCalls {
			totalToolCalls += count
		}
		if totalToolCalls < 3 && len(stats.FilesEdited) == 0 && len(stats.CommandsRun) == 0 {
			return
		}
		if !stats.Success && stats.Iterations <= 1 {
			return
		}

		insights := agent.GenerateInsights(stats)
		if insights == "" {
			return
		}

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
			insights = mergeDaemonInsights(existing, insights)
		}

		if err := autoMem.SaveMemory(key, insights); err != nil {
			debug.Log("daemon", "reflection: failed to save insights: %v", err)
		} else {
			debug.Log("daemon", "reflection: saved insights (%d chars)", len(insights))
		}
	})
}

func mergeDaemonInsights(existing, newEntry string) string {
	entries := splitDaemonRunEntries(existing)
	entries = append(entries, newEntry)
	if len(entries) > 10 {
		entries = entries[len(entries)-10:]
	}
	return strings.Join(entries, "\n\n")
}

func splitDaemonRunEntries(content string) []string {
	parts := strings.Split(content, "## Run Reflection")
	var entries []string
	for i, part := range parts {
		if i == 0 {
			if strings.TrimSpace(part) != "" {
				entries = append(entries, strings.TrimSpace(part))
			}
			continue
		}
		entry := "## Run Reflection" + part
		entries = append(entries, strings.TrimSpace(entry))
	}
	return entries
}
