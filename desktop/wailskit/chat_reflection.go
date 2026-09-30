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
		// #2715 (same as #1752 case 3 / #1388): LoadAll merges EVERY
		// active memory key - writing the merge back into run-insights
		// cross-pollutes all project memories into run-insights, which
		// is then reinjected with every prompt and snowballs. The TUI
		// and daemon reflection paths already use LoadKey.
		existing, err := autoMem.LoadKey(key)
		if err != nil {
			// #2715 hardening (mirrors TUI #1388): a read error used to be
			// swallowed and the save below then OVERWROTE the accumulated
			// insights with the fresh batch (silent loss). Abort this
			// round - the next reflection retries; old accumulation stays.
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
