package main

import (
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
)

func setupDaemonReflection(ag *agent.Agent, workingDir string, cronSchedulerFn func() *cron.Scheduler) {
	ag.SetReflectionFunc(func(stats agent.RunStats) {
		// Cron no-diff terminal-state wiring (#5530), daemon face: the daemon
		// submits cron prompts verbatim, so the run prompt is the exact job
		// prompt. Reports before the ShouldReflect gate so idle no-op rounds
		// still count toward the streak.
		agentruntime.ReportCronRunOutcome(cronSchedulerFn, stats, nil)

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

		existing, err := autoMem.LoadKey(key)
		if err == nil && existing != "" {
			insights = agent.MergeInsights(existing, insights)
		}

		if err := autoMem.SaveMemoryWithSource(key, insights, "run-reflection"); err != nil {
			debug.Log("daemon", "reflection: failed to save insights: %v", err)
		} else {
			debug.Log("daemon", "reflection: saved insights (%d chars)", len(insights))
		}
	})
}
