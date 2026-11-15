package wailskit

import (
	"log"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/memory"
)

func buildReflectionFunc(wd string, cronSchedulerFn func() *cron.Scheduler) func(agent.RunStats) {
	return func(stats agent.RunStats) {
		// Cron no-diff terminal-state wiring (#5530), desktop face: the
		// desktop submits cron prompts verbatim, so the run prompt is the
		// exact job prompt. Reports before the ShouldReflect gate so idle
		// no-op rounds still count toward the streak.
		agentruntime.ReportCronRunOutcome(cronSchedulerFn, stats, nil)

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
