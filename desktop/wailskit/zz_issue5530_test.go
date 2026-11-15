package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/cron"
)

// TestIssue5530DesktopReflectionRecordsCronOutcomeBeforeGate drives the real
// desktop reflection func with an idle no-op round: ShouldReflect rejects
// that shape, so a recorded streak proves the outcome report runs before the
// gate (#5530).
func TestIssue5530DesktopReflectionRecordsCronOutcomeBeforeGate(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	const jobPrompt = "sentinel: desktop sweep"
	job, err := s.Create("0 * * * *", jobPrompt, true, false)
	if err != nil {
		t.Fatal(err)
	}
	fn := buildReflectionFunc(t.TempDir(), func() *cron.Scheduler { return s })

	// No tool calls, no edits, one iteration: ShouldReflect(stats) is
	// false, so only a pre-gate report can record the outcome.
	fn(agent.RunStats{UserPromptFull: job.Prompt, Iterations: 1})
	if j, _ := s.Get(job.ID); j.NoDiffStreak != 1 {
		t.Fatalf("idle cron round must be recorded before the ShouldReflect gate, got %+v", j)
	}

	// A producing round resets the streak.
	fn(agent.RunStats{UserPromptFull: job.Prompt, FilesEdited: []string{"x.go"}})
	if j, _ := s.Get(job.ID); j.NoDiffStreak != 0 {
		t.Fatalf("producing round must reset the streak, got %+v", j)
	}
}
