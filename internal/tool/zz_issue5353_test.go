package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
)

// #5353: far-future NextFire one-shot trap (OBS-041, OBS-049). Two seats
// deleted+recreated one-shot crons whose NextFire landed >1 year out after an
// HH:MM-vs-day parsing slip. cron_create with a one-shot schedule must reject
// a computed NextFire beyond 90 days unless far=true, and far=true must carry
// a loud warning.
//
// Fixtures are computed dynamically (cron expression minted from
// now+N days) so the distances are deterministic regardless of run date.

// oneShotExprFor builds a 5-field cron expression whose next fire from now is
// the target date (within the day), i.e. ~daysOut days away.
func oneShotExprFor(daysOut int) string {
	target := time.Now().Add(time.Duration(daysOut) * 24 * time.Hour)
	return fmt.Sprintf("%d %d %d %d *", target.Minute(), target.Hour(), target.Day(), int(target.Month()))
}

func TestCronCreateOneShotRejectsFarFutureNextFire(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	farExpr := oneShotExprFor(200) // ~200 days out, deterministic
	next, err := cron.NextTime(farExpr, time.Now())
	if err != nil {
		t.Fatalf("fixture expression must parse: %v", err)
	}
	if d := time.Until(next).Hours() / 24; d <= float64(90) {
		t.Fatalf("fixture must produce a >%d-day NextFire for a meaningful rejection test, got %.0f days", 90, d)
	}

	result, execErr := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "far follow-up", "one_shot": true}`, farExpr)))
	if execErr != nil {
		t.Fatal(execErr)
	}
	if !result.IsError {
		t.Fatalf("far-future one-shot must be rejected, got success: %s", result.Content)
	}
	for _, want := range []string{
		"one-shot cron rejected",
		fmt.Sprintf("beyond the %d-day horizon", 90),
		"cron parsing slip",
		`"far": true`,
	} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("rejection should mention %q, got: %s", want, result.Content)
		}
	}
	if !strings.Contains(result.Content, next.Format(time.RFC3339)) {
		t.Errorf("rejection should surface the computed NextFire %s, got: %s", next.Format(time.RFC3339), result.Content)
	}
	if jobs := s.List(); len(jobs) != 0 {
		t.Errorf("rejected one-shot must not be created, found %d job(s)", len(jobs))
	}
}

func TestCronCreateOneShotFarFutureAllowedWithFarFlagAndWarns(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "far follow-up", "one_shot": true, "far": true}`, oneShotExprFor(200))))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("far=true must allow far-future one-shot, got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "WARNING: far-future one-shot accepted via far=true") {
		t.Errorf("far=true success should carry the loud warning, got: %s", result.Content)
	}
	jobs := s.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job with far=true, got %d", len(jobs))
	}
}

func TestCronCreateOneShotNearFutureUnaffected(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// Near-future one-shot (both one_shot=true and the legacy
	// recurring=false spelling) must keep working without the far flag.
	for _, input := range []string{
		`{"cron": "*/5 * * * *", "prompt": "near", "one_shot": true}`,
		`{"cron": "0 9 * * *", "prompt": "tomorrow morning", "recurring": false}`,
	} {
		result, err := tool.Execute(context.Background(), json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Errorf("near-future one-shot must succeed without far=true: %s", result.Content)
		}
		if strings.Contains(result.Content, "WARNING") {
			t.Errorf("near-future one-shot must not warn, got: %s", result.Content)
		}
	}
	if jobs := s.List(); len(jobs) != 2 {
		t.Fatalf("expected 2 near-future one-shot jobs, got %d", len(jobs))
	}
}

func TestCronCreateRecurringFarFirstFireUnaffected(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// Recurring jobs are out of scope of the trap: a legitimately rare
	// recurring schedule (e.g. yearly) must not be rejected.
	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"cron": "0 0 1 1 *", "prompt": "yearly", "recurring": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("recurring far-first-fire must not be rejected, got: %s", result.Content)
	}
	if jobs := s.List(); len(jobs) != 1 {
		t.Fatalf("expected 1 recurring job, got %d", len(jobs))
	}
}

func TestCronCreateOneShotHorizonBoundary(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// Boundary is strictly greater-than: 85 days (under horizon) passes,
	// 95 days (over horizon) is rejected. Dynamic fixtures keep both sides
	// deterministic on any run date.
	result, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "just inside", "one_shot": true}`, oneShotExprFor(85))))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("85-day one-shot must be accepted, got: %s", result.Content)
	}

	result, err = tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "just outside", "one_shot": true}`, oneShotExprFor(95))))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Errorf("95-day one-shot must be rejected, got success: %s", result.Content)
	}
}

func TestCronUpdateOneShotRejectsFarFutureNextFire(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	createTool := CronCreateTool{Scheduler: s}
	createResult, err := createTool.Execute(context.Background(), json.RawMessage(
		`{"cron": "*/5 * * * *", "prompt": "near", "one_shot": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if createResult.IsError {
		t.Fatalf("setup create failed: %s", createResult.Content)
	}
	var job struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(strings.TrimSpace(createResult.Content), "\n", 2)[0]), &job); err != nil {
		t.Fatalf("parse created job JSON: %v (content: %s)", err, createResult.Content)
	}
	if job.ID == "" {
		t.Fatalf("created job JSON missing ID: %s", createResult.Content)
	}

	updateTool := CronUpdateTool{Scheduler: s}
	result, err := updateTool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"jobId": %q, "cron": %q}`, job.ID, oneShotExprFor(200))))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("far-future update of a one-shot job must be rejected, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "one-shot cron rejected") {
		t.Errorf("update rejection should use the far-future rejection copy, got: %s", result.Content)
	}

	// far=true overrides, with the loud warning in the update output.
	result, err = updateTool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"jobId": %q, "cron": %q, "far": true}`, job.ID, oneShotExprFor(200))))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("far=true must allow the far-future update, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "WARNING: far-future one-shot accepted via far=true") {
		t.Errorf("far update success should carry the warning, got: %s", result.Content)
	}

	// Recurring targets are not guarded on update.
	recResult, err := createTool.Execute(context.Background(), json.RawMessage(
		`{"cron": "*/5 * * * *", "prompt": "loop", "recurring": true}`))
	if err != nil {
		t.Fatal(err)
	}
	var recJob struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(strings.TrimSpace(recResult.Content), "\n", 2)[0]), &recJob); err != nil {
		t.Fatalf("parse recurring job JSON: %v (content: %s)", err, recResult.Content)
	}
	result, err = updateTool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"jobId": %q, "cron": %q}`, recJob.ID, oneShotExprFor(200))))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("recurring job update must not be far-guarded, got: %s", result.Content)
	}
}

func TestCronCreateSchemaDocumentsFarAndOneShot(t *testing.T) {
	params := string(CronCreateTool{}.Parameters())
	for _, want := range []string{
		`"one_shot"`,
		`"far"`,
		"within 90 days unless far=true",
	} {
		if !strings.Contains(params, want) {
			t.Errorf("cron_create schema should document %q, got: %s", want, params)
		}
	}
	desc := CronCreateTool{}.Description()
	if !strings.Contains(desc, "far-future one-shot schedules are almost always a cron field parsing slip") {
		t.Errorf("cron_create description should disclose the far-future guard, got: %q", desc)
	}
}
