package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
)

// ReportCronRunOutcome feeds a completed run's produced-diff signal into the
// scheduler's no-diff terminal-state machinery (#5273/#5530): the run's
// prompt is correlated back to its originating cron job by exact match and
// the outcome recorded via RecordFireOutcome. Runs that match no cron job
// (ordinary user prompts) are ignored, so this is safe to invoke for every
// completed run.
//
// fn resolves the live scheduler at run-completion time; surfaces whose
// scheduler is wired after the agent is constructed (TUI, daemon) pass a
// closure over the late-bound variable. promptTransform lets a surface
// normalize its own submission decoration before the exact match - the TUI
// strips its doc-sync reminder suffix; verbatim surfaces pass nil.
//
// Callers must invoke this BEFORE any ShouldReflect-style gate: an idle
// no-op round (the very shape the no-diff guard exists to detect) has no
// tool calls or edits and would otherwise be skipped by that gate.
func ReportCronRunOutcome(fn func() *cron.Scheduler, stats agent.RunStats, promptTransform func(string) string) string {
	if fn == nil {
		return ""
	}
	s := fn()
	if s == nil {
		return ""
	}
	prompt := stats.UserPromptFull
	if prompt == "" {
		prompt = stats.UserPrompt
	}
	if promptTransform != nil {
		prompt = promptTransform(prompt)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	producedDiff := len(stats.FilesEdited) > 0
	id := s.RecordRunOutcome(prompt, producedDiff)
	if id != "" {
		debug.Log("cron", "run outcome recorded for job %s (producedDiff=%t)", id, producedDiff)
	}
	return id
}
