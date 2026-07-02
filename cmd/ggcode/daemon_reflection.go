package main

import (
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
)

// setupDaemonReflection configures post-run reflection for the daemon agent.
func setupDaemonReflection(ag *agent.Agent, workingDir string) {
	ag.SetReflectionFunc(func(stats agent.RunStats) {
		if !agent.ShouldReflect(stats) {
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
			insights = agent.MergeInsights(existing, insights)
		}

		if err := autoMem.SaveMemory(key, insights); err != nil {
			debug.Log("daemon", "reflection: failed to save insights: %v", err)
		} else {
			debug.Log("daemon", "reflection: saved insights (%d chars)", len(insights))
		}
	})
}
