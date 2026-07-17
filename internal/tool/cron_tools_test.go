package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

func TestCronToolDescriptionsClarifyPersistenceAndLoadedState(t *testing.T) {
	createDesc := CronCreateTool{}.Description()
	for _, want := range []string{"recurring=false", "not persisted", "not persisted"} {
		if !strings.Contains(createDesc, want) {
			t.Fatalf("cron_create description should mention %q, got %q", want, createDesc)
		}
	}

	deleteDesc := CronDeleteTool{}.Description()
	for _, want := range []string{"future schedule only", "does not cancel prompts", "already started"} {
		if !strings.Contains(deleteDesc, want) {
			t.Fatalf("cron_delete description should mention %q, got %q", want, deleteDesc)
		}
	}

	listDesc := CronListTool{}.Description()
	for _, want := range []string{"currently loaded", "for this workspace", "recurring and one-shot"} {
		if !strings.Contains(listDesc, want) {
			t.Fatalf("cron_list description should mention %q, got %q", want, listDesc)
		}
	}
}

func TestCronCreateTool_Execute(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// Basic create — fires in ~1 minute
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"cron": "* * * * *",
		"prompt": "check status",
		"recurring": false
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "cron-1") {
		t.Errorf("expected job ID in result, got: %s", result.Content)
	}
}

func TestCronCreateTool_NilScheduler(t *testing.T) {
	tool := CronCreateTool{}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nil scheduler")
	}
}

func TestCronCreateTool_InvalidInput(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(`invalid json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid JSON")
	}
}

func TestCronCreateTool_EmptyCron(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"cron":"","prompt":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for empty cron")
	}
}

func TestCronCreateTool_EmptyPrompt(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for empty prompt")
	}
}

func TestCronCreateTool_DefaultRecurring(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// No recurring field → defaults to true
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	jobs := s.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if !jobs[0].Recurring {
		t.Error("expected default recurring=true")
	}
}

func TestCronDeleteTool_Execute(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronDeleteTool{Scheduler: s}

	// Create a job first
	createTool := CronCreateTool{Scheduler: s}
	result, _ := createTool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":"test"}`))

	// Extract job ID
	var job struct{ ID string }
	json.Unmarshal([]byte(strings.TrimSpace(result.Content)), &job)

	// Delete it
	delResult, err := tool.Execute(context.Background(), json.RawMessage(`{"jobId":"`+job.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if delResult.IsError {
		t.Fatalf("unexpected error: %s", delResult.Content)
	}
	if !strings.Contains(delResult.Content, "deleted") {
		t.Errorf("expected 'deleted' in result, got: %s", delResult.Content)
	}

	// Verify it's gone
	if len(s.List()) != 0 {
		t.Error("job should be deleted")
	}
}

func TestCronDeleteTool_NotFound(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronDeleteTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"jobId":"nonexistent"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent job")
	}
}

func TestCronDeleteTool_NilScheduler(t *testing.T) {
	tool := CronDeleteTool{}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"jobId":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nil scheduler")
	}
}

func TestCronListTool_Execute(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()
	tool := CronListTool{Scheduler: s}

	// Empty list
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "No scheduled jobs") {
		t.Errorf("expected empty list message, got: %s", result.Content)
	}

	// Create two jobs
	createTool := CronCreateTool{Scheduler: s}
	createTool.Execute(context.Background(), json.RawMessage(`{"cron":"*/5 * * * *","prompt":"check every 5","recurring":true}`))
	createTool.Execute(context.Background(), json.RawMessage(`{"cron":"0 9 * * *","prompt":"morning report","recurring":false}`))

	// List
	result, err = tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "cron-1") || !strings.Contains(result.Content, "cron-2") {
		t.Errorf("expected both jobs in list, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "recurring") || !strings.Contains(result.Content, "one-shot") {
		t.Errorf("expected recurring/one-shot labels, got: %s", result.Content)
	}
}

func TestCronListTool_NilScheduler(t *testing.T) {
	tool := CronListTool{}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nil scheduler")
	}
}

func TestCronCreateTool_ExecuteFiresOneShot(t *testing.T) {
	var fired []string
	s := cron.NewScheduler(func(prompt string, _ bool) { fired = append(fired, prompt) }, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// Create a recurring job that fires every minute
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"cron": "* * * * *",
		"prompt": "hello from cron",
		"recurring": true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	// Verify the job exists
	jobs := s.List()
	if len(jobs) != 1 {
		t.Errorf("expected 1 job, got %d", len(jobs))
	}
	if !jobs[0].Recurring {
		t.Error("expected recurring=true")
	}

	if jobs[0].NextFire.IsZero() {
		t.Error("expected NextFire to be set")
	}
}

func TestCronCreateToolDescriptionClarifiesPersistence(t *testing.T) {
	desc := CronCreateTool{}.Description()
	for _, want := range []string{"not persisted", "not persisted"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("cron_create description should mention %q, got %q", want, desc)
		}
	}
	params := string(CronCreateTool{}.Parameters())
	for _, want := range []string{"never persisted", "recurring=false jobs are never persisted"} {
		if !strings.Contains(params, want) {
			t.Fatalf("cron_create schema should mention %q, got %s", want, params)
		}
	}
}

// --- cron_update ---

func TestCronUpdateTool_ChangesPrompt(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	createTool := CronCreateTool{Scheduler: s}
	createResult, _ := createTool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":"old prompt"}`))
	var job struct{ ID string }
	json.Unmarshal([]byte(strings.TrimSpace(createResult.Content)), &job)

	updateTool := CronUpdateTool{Scheduler: s}
	result, err := updateTool.Execute(context.Background(), json.RawMessage(`{"jobId":"`+job.ID+`","prompt":"new prompt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	got, ok := s.Get(job.ID)
	if !ok {
		t.Fatal("job should exist after update")
	}
	if got.Prompt != "new prompt" {
		t.Errorf("expected prompt 'new prompt', got %q", got.Prompt)
	}
	// Cron should be unchanged
	if got.CronExpr != "* * * * *" {
		t.Errorf("cron expression should be unchanged, got %q", got.CronExpr)
	}
}

func TestCronUpdateTool_ChangesCronExpr(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	createTool := CronCreateTool{Scheduler: s}
	createResult, _ := createTool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":"test"}`))
	var job struct{ ID string }
	json.Unmarshal([]byte(strings.TrimSpace(createResult.Content)), &job)

	updateTool := CronUpdateTool{Scheduler: s}
	result, _ := updateTool.Execute(context.Background(), json.RawMessage(`{"jobId":"`+job.ID+`","cron":"0 9 * * *"}`))
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	got, _ := s.Get(job.ID)
	if got.CronExpr != "0 9 * * *" {
		t.Errorf("expected cron '0 9 * * *', got %q", got.CronExpr)
	}
}

func TestCronUpdateTool_NotFound(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	updateTool := CronUpdateTool{Scheduler: s}
	result, _ := updateTool.Execute(context.Background(), json.RawMessage(`{"jobId":"nonexistent","prompt":"test"}`))
	if !result.IsError {
		t.Error("expected error for nonexistent job")
	}
}

func TestCronUpdateTool_NoFieldsProvided(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	createTool := CronCreateTool{Scheduler: s}
	createTool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":"test"}`))

	updateTool := CronUpdateTool{Scheduler: s}
	result, _ := updateTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	if !result.IsError {
		t.Error("expected error when no update fields provided")
	}
}

func TestCronUpdateTool_InvalidCronExpr(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	createTool := CronCreateTool{Scheduler: s}
	createTool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":"test"}`))

	updateTool := CronUpdateTool{Scheduler: s}
	result, _ := updateTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1","cron":"invalid expr"}`))
	if !result.IsError {
		t.Error("expected error for invalid cron expression")
	}

	// Original should be unchanged
	got, _ := s.Get("cron-1")
	if got.CronExpr != "* * * * *" {
		t.Errorf("cron expression should be unchanged after failed update, got %q", got.CronExpr)
	}
}

func TestCronUpdateTool_NilScheduler(t *testing.T) {
	tool := CronUpdateTool{}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1","prompt":"test"}`))
	if !result.IsError {
		t.Error("expected error for nil scheduler")
	}
}

// --- cron_pause / cron_resume ---

func TestCronPauseResumeTool(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	createTool := CronCreateTool{Scheduler: s}
	createTool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":"test","recurring":true}`))

	// Pause
	pauseTool := CronPauseTool{Scheduler: s}
	result, _ := pauseTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	if result.IsError {
		t.Fatalf("pause failed: %s", result.Content)
	}

	got, _ := s.Get("cron-1")
	if !got.Paused {
		t.Error("expected Paused=true after pause")
	}

	// Resume
	resumeTool := CronResumeTool{Scheduler: s}
	result, _ = resumeTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	if result.IsError {
		t.Fatalf("resume failed: %s", result.Content)
	}

	got, _ = s.Get("cron-1")
	if got.Paused {
		t.Error("expected Paused=false after resume")
	}
}

func TestCronPauseTool_NotFound(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	pauseTool := CronPauseTool{Scheduler: s}
	result, _ := pauseTool.Execute(context.Background(), json.RawMessage(`{"jobId":"nonexistent"}`))
	if !result.IsError {
		t.Error("expected error for pausing nonexistent job")
	}
}

func TestCronResumeTool_NotFound(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	resumeTool := CronResumeTool{Scheduler: s}
	result, _ := resumeTool.Execute(context.Background(), json.RawMessage(`{"jobId":"nonexistent"}`))
	if !result.IsError {
		t.Error("expected error for resuming nonexistent job")
	}
}

func TestCronPauseTool_NilScheduler(t *testing.T) {
	tool := CronPauseTool{}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	if !result.IsError {
		t.Error("expected error for nil scheduler")
	}
}

func TestCronResumeTool_NilScheduler(t *testing.T) {
	tool := CronResumeTool{}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	if !result.IsError {
		t.Error("expected error for nil scheduler")
	}
}

func TestCronPauseResume_Idempotent(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	createTool := CronCreateTool{Scheduler: s}
	createTool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":"test","recurring":true}`))

	// Double pause should be fine
	pauseTool := CronPauseTool{Scheduler: s}
	pauseTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	result, _ := pauseTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	if result.IsError {
		t.Error("double pause should not error")
	}

	// Double resume should be fine
	resumeTool := CronResumeTool{Scheduler: s}
	resumeTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	result, _ = resumeTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	if result.IsError {
		t.Error("double resume should not error")
	}
}

// --- cron_get ---

func TestCronGetTool_ShowsFullPrompt(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	createTool := CronCreateTool{Scheduler: s}
	createResult, _ := createTool.Execute(context.Background(), json.RawMessage(`{"cron":"*/5 * * * *","prompt":"short prompt here","recurring":true}`))
	var job struct{ ID string }
	json.Unmarshal([]byte(strings.TrimSpace(createResult.Content)), &job)

	getTool := CronGetTool{Scheduler: s}
	result, _ := getTool.Execute(context.Background(), json.RawMessage(`{"jobId":"`+job.ID+`"}`))
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Full prompt must be shown
	if !strings.Contains(result.Content, "short prompt here") {
		t.Errorf("result should contain full prompt, got: %s", result.Content)
	}
	// Should show state, type, cron expr
	for _, want := range []string{"State:", "Type:", "Cron expr:", "*/5 * * * *", "Next fire:"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("result should contain %q, got: %s", want, result.Content)
		}
	}
}

func TestCronGetTool_ShowsPausedState(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	createTool := CronCreateTool{Scheduler: s}
	createTool.Execute(context.Background(), json.RawMessage(`{"cron":"* * * * *","prompt":"test","recurring":true}`))

	pauseTool := CronPauseTool{Scheduler: s}
	pauseTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))

	getTool := CronGetTool{Scheduler: s}
	result, _ := getTool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	if !strings.Contains(result.Content, "paused") {
		t.Errorf("result should show 'paused' state, got: %s", result.Content)
	}
}

func TestCronGetTool_NotFound(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	getTool := CronGetTool{Scheduler: s}
	result, _ := getTool.Execute(context.Background(), json.RawMessage(`{"jobId":"nonexistent"}`))
	if !result.IsError {
		t.Error("expected error for nonexistent job")
	}
}

func TestCronGetTool_NilScheduler(t *testing.T) {
	tool := CronGetTool{}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"jobId":"cron-1"}`))
	if !result.IsError {
		t.Error("expected error for nil scheduler")
	}
}
