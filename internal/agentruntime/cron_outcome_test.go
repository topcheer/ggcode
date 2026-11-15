package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/cron"
)

func TestReportCronRunOutcomeGuards(t *testing.T) {
	stats := agent.RunStats{UserPromptFull: "sentinel prompt"}
	if got := ReportCronRunOutcome(nil, stats, nil); got != "" {
		t.Fatalf("nil getter must no-op, got %q", got)
	}
	if got := ReportCronRunOutcome(func() *cron.Scheduler { return nil }, stats, nil); got != "" {
		t.Fatalf("nil scheduler must no-op, got %q", got)
	}
	empty := cron.NewScheduler(func(string, bool) {}, "")
	if got := ReportCronRunOutcome(func() *cron.Scheduler { return empty }, agent.RunStats{}, nil); got != "" {
		t.Fatalf("empty prompt must no-op, got %q", got)
	}
}

func TestReportCronRunOutcomeRecordsMatchedJob(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	job, err := s.Create("0 * * * *", "sentinel: hourly sweep", true, false)
	if err != nil {
		t.Fatal(err)
	}
	get := func() *cron.Scheduler { return s }

	// An idle no-op round (no tool calls, no edits): ShouldReflect would
	// reject it, yet the outcome must still be recorded.
	if got := ReportCronRunOutcome(get, agent.RunStats{UserPromptFull: job.Prompt}, nil); got != job.ID {
		t.Fatalf("must resolve job %s, got %q", job.ID, got)
	}
	if j, _ := s.Get(job.ID); j.NoDiffStreak != 1 {
		t.Fatalf("no-diff round must be recorded, got %+v", j)
	}

	// Ordinary user prompts resolve nothing.
	if got := ReportCronRunOutcome(get, agent.RunStats{UserPromptFull: "just chatting"}, nil); got != "" {
		t.Fatalf("unmatched prompt must no-op, got %q", got)
	}

	// UserPromptFull wins; a truncated-only UserPrompt is the fallback.
	if got := ReportCronRunOutcome(get, agent.RunStats{UserPrompt: job.Prompt}, nil); got != job.ID {
		t.Fatalf("truncated-prompt fallback must resolve job, got %q", got)
	}
	if j, _ := s.Get(job.ID); j.NoDiffStreak != 2 {
		t.Fatalf("fallback prompt must record the round, got %+v", j)
	}

	// A producing round (files edited) resets the streak.
	stats := agent.RunStats{UserPromptFull: job.Prompt, FilesEdited: []string{"internal/x.go"}}
	if got := ReportCronRunOutcome(get, stats, nil); got != job.ID {
		t.Fatalf("producing round must resolve job, got %q", got)
	}
	if j, _ := s.Get(job.ID); j.NoDiffStreak != 0 {
		t.Fatalf("producing round must reset the streak, got %+v", j)
	}
}

func TestReportCronRunOutcomeAppliesPromptTransform(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	const prompt = "sentinel: decorated sweep"
	job, err := s.Create("0 * * * *", prompt, true, false)
	if err != nil {
		t.Fatal(err)
	}
	get := func() *cron.Scheduler { return s }

	const decoration = "\n\n[Reminder] surface-local decoration"
	strip := func(p string) string { return strings.TrimSuffix(p, decoration) }

	// The decorated run prompt must NOT match without the transform...
	if got := ReportCronRunOutcome(get, agent.RunStats{UserPromptFull: prompt + decoration}, nil); got != "" {
		t.Fatalf("decorated prompt must not match without transform, got %q", got)
	}
	// ...and must match with it.
	if got := ReportCronRunOutcome(get, agent.RunStats{UserPromptFull: prompt + decoration}, strip); got != job.ID {
		t.Fatalf("transformed prompt must resolve job, got %q", got)
	}
	if j, _ := s.Get(job.ID); j.NoDiffStreak != 1 {
		t.Fatalf("transformed no-diff round must be recorded, got %+v", j)
	}
}
