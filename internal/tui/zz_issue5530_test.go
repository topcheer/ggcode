package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/cron"
)

// TestIssue5530StripDocSyncReminderRoundTrip pins the wrap/strip pair the
// cron outcome correlation depends on: whatever withDocSyncReminder appends,
// stripDocSyncReminder must remove exactly, and undecorated prompts must
// pass through untouched.
func TestIssue5530StripDocSyncReminderRoundTrip(t *testing.T) {
	for _, p := range []string{
		"check deps",
		"sentinel: weekly sweep",
		"多行\n任务",
		"",
	} {
		wrapped := withDocSyncReminder(p)
		if got := stripDocSyncReminder(wrapped); got != p {
			t.Fatalf("roundtrip failed for %q: wrapped=%q stripped=%q", p, wrapped, got)
		}
	}

	// Doc-keyword prompts are not decorated and must survive stripping.
	p := "update the readme index"
	if withDocSyncReminder(p) != p {
		t.Fatal("doc-keyword prompt must not be wrapped")
	}
	if got := stripDocSyncReminder(p); got != p {
		t.Fatalf("undecorated prompt must pass through, got %q", got)
	}
}

// TestIssue5530CronSchedulerLiveHolder pins the late-binding holder: a bare
// Model literal resolves no scheduler, SetCronScheduler publishes, and the
// reflection-time getter observes the assignment.
func TestIssue5530CronSchedulerLiveHolder(t *testing.T) {
	m := &Model{}
	if got := m.cronSchedulerLive(); got != nil {
		t.Fatalf("bare model must resolve no scheduler, got %v", got)
	}
	s := cron.NewScheduler(func(string, bool) {}, "")
	m.SetCronScheduler(s)
	if m.cronScheduler != s {
		t.Fatal("SetCronScheduler must keep the direct field for existing readers")
	}
	if got := m.cronSchedulerLive(); got != s {
		t.Fatal("holder must publish the scheduler to run-completion readers")
	}
}

// TestIssue5530ReflectionReportsWrappedCronPrompt drives the exact TUI
// composition from the reflection callback: the run stats carry the cron
// prompt WITH the doc-sync reminder appended (handleCronPromptMsg submits
// withDocSyncReminder(prompt)), and the adapter must strip the decoration
// and record the round against the originating job.
func TestIssue5530ReflectionReportsWrappedCronPrompt(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	const jobPrompt = "sentinel: patrol the backlog"
	job, err := s.Create("0 * * * *", jobPrompt, true, false)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{}
	m.SetCronScheduler(s)

	// Idle no-op round: no files edited -> no-diff streak increment.
	stats := agent.RunStats{UserPromptFull: withDocSyncReminder(jobPrompt)}
	if got := agentruntime.ReportCronRunOutcome(m.cronSchedulerLive, stats, stripDocSyncReminder); got != job.ID {
		t.Fatalf("wrapped cron run must resolve to its job, got %q", got)
	}
	if j, _ := s.Get(job.ID); j.NoDiffStreak != 1 {
		t.Fatalf("no-diff round must be recorded, got %+v", j)
	}

	// Producing round: files edited -> streak reset.
	stats2 := agent.RunStats{
		UserPromptFull: withDocSyncReminder(jobPrompt),
		FilesEdited:    []string{"internal/x.go"},
	}
	if got := agentruntime.ReportCronRunOutcome(m.cronSchedulerLive, stats2, stripDocSyncReminder); got != job.ID {
		t.Fatalf("producing cron run must resolve to its job, got %q", got)
	}
	if j, _ := s.Get(job.ID); j.NoDiffStreak != 0 {
		t.Fatalf("producing round must reset the streak, got %+v", j)
	}
}

// TestIssue5530ReflectionReportsBeforeGateAnchor pins the placement of the
// cron outcome report inside setupReflection's callback: it must run BEFORE
// the ShouldReflect gate, because an idle no-op round (no tool calls, no
// edits) - the exact shape the no-diff guard exists to detect - is rejected
// by that gate.
func TestIssue5530ReflectionReportsBeforeGateAnchor(t *testing.T) {
	src, err := os.ReadFile("reflection.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	reportIdx := strings.Index(text, "ReportCronRunOutcome(")
	gateIdx := strings.Index(text, "ShouldReflect(")
	if reportIdx < 0 {
		t.Fatal("TUI reflection must report cron run outcomes (#5530)")
	}
	if gateIdx < 0 || reportIdx > gateIdx {
		t.Fatal("cron outcome report must run before the ShouldReflect gate")
	}
}
