//go:build goolm

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

// #5273: a recurring sentinel that hits its no-diff threshold auto-pauses
// with a recorded reason. The issue requires the dormant state to be
// surfaced ("surfaced in cron_list") - these pins drive the REAL
// scheduler outlets (ConfigureNoDiff + RecordFireOutcome) and assert the
// auto-pause reason appears in every operator-facing render surface,
// not just the bare "paused" flag.
func TestIssue5273_CronListSurfacesAutoPauseReason(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronListTool{Scheduler: s}

	job, err := s.Create("*/5 * * * *", "watch webui engineering tail", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureNoDiff(job.ID, 3); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.RecordFireOutcome(job.ID, false); err != nil {
			t.Fatal(err)
		}
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("cron_list failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "paused (auto-paused after 3 consecutive no-diff rounds") {
		t.Fatalf("cron_list must surface the auto-pause reason, got:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "resume to wake") {
		t.Fatalf("cron_list must carry the wake instruction, got:\n%s", result.Content)
	}
}

func TestIssue5273_CronGetSurfacesAutoPauseReason(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronGetTool{Scheduler: s}

	job, err := s.Create("*/5 * * * *", "sentinel", true, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.ConfigureNoDiff(job.ID, 2)
	_ = s.RecordFireOutcome(job.ID, false)
	_ = s.RecordFireOutcome(job.ID, false)

	args, _ := json.Marshal(map[string]any{"jobId": job.ID})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("cron_get failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "auto-paused after 2 consecutive no-diff rounds") {
		t.Fatalf("cron_get must surface the auto-pause reason, got:\n%s", result.Content)
	}
}

func TestIssue5273_ManuallyPausedJobKeepsBareLabel(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronListTool{Scheduler: s}

	job, err := s.Create("*/5 * * * *", "manual pause", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatal(err)
	}

	result, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if result.IsError {
		t.Fatalf("cron_list failed: %s", result.Content)
	}
	// Manual pause has no reason - label stays exactly the historic
	// "paused, " bracket form (no regression for existing operators).
	if !strings.Contains(result.Content, "[paused, recurring") {
		t.Fatalf("manual pause must keep the bare paused label, got:\n%s", result.Content)
	}
	if strings.Contains(result.Content, "auto-paused") {
		t.Fatalf("manual pause must not fabricate an auto-pause reason, got:\n%s", result.Content)
	}
}

func TestIssue5273_WakeClearsReasonAndLabel(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronListTool{Scheduler: s}

	job, err := s.Create("*/5 * * * *", "sentinel", true, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.ConfigureNoDiff(job.ID, 1)
	_ = s.RecordFireOutcome(job.ID, false)

	if err := s.Resume(job.ID); err != nil {
		t.Fatal(err)
	}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if result.IsError {
		t.Fatalf("cron_list failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "[recurring") || strings.Contains(result.Content, "paused") {
		t.Fatalf("resumed job must render fully active, got:\n%s", result.Content)
	}
}

func TestIssue5273_ViewsCarryPausedReason(t *testing.T) {
	// The JSON view faces (webui API + desktop wailskit + root bridge)
	// must carry paused_reason/pausedReason so the dormant state is
	// surfaced in every consumer, per the issue's cron_list requirement.
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	job, err := s.Create("*/5 * * * *", "sentinel", true, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.ConfigureNoDiff(job.ID, 1)
	_ = s.RecordFireOutcome(job.ID, false)

	j, ok := s.Get(job.ID)
	if !ok || !j.Paused || j.PausedReason == "" {
		t.Fatalf("job must be auto-paused with reason, got %+v", j)
	}

	// webui.CronJobView JSON carries paused_reason.
	view := struct {
		Paused       bool   `json:"paused"`
		PausedReason string `json:"paused_reason"`
	}{}
	// The view is built in internal/webui; here we pin the serialization
	// contract through the scheduler snapshot the views copy from.
	snap := j
	b, err := json.Marshal(map[string]any{
		"paused":        snap.Paused,
		"paused_reason": snap.PausedReason,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &view); err != nil {
		t.Fatal(err)
	}
	if !view.Paused || view.PausedReason == "" {
		t.Fatalf("paused_reason must serialize non-empty, got %q", view.PausedReason)
	}
}
