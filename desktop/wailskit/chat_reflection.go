package wailskit

import (
	"log"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/memory"
)

// buildReflectionFunc returns the post-run reflection callback - save insights
// to project memory so knowledge compounds across sessions. Same logic as TUI
// (internal/tui/reflection.go, #1388) and daemon (cmd/ggcode/daemon_reflection.go,
// #1752 case 3). Extracted from ChatBridge.InitAgent for testability (#2715).
func buildReflectionFunc(wd string) func(agent.RunStats) {
	return func(stats agent.RunStats) {
		if !agent.ShouldReflect(stats) {
			return
		}
		insights := agent.GenerateInsights(stats)
		if insights == "" {
			return
		}
		autoMem := memory.NewProjectAutoMemory(wd)
		if autoMem == nil {
			return
		}
		key := "run-insights"

		existing, err := autoMem.LoadKey(key)
		if err != nil {

			log.Printf("[reflection] failed to load existing insights, skipping save: %v", err)
			return
		}
		if existing != "" {
			insights = agent.MergeInsights(existing, insights)
		}
		if err := autoMem.SaveMemoryWithSource(key, insights, "run-reflection"); err != nil {
			log.Printf("[reflection] failed to save insights: %v", err)
		}
	}
}
