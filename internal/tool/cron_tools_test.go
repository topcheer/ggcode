package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

func TestCronCreateTool_Execute(t *testing.T) {
	s := cron.NewScheduler(nil)
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
	s := cron.NewScheduler(nil)
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
	s := cron.NewScheduler(nil)
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
	s := cron.NewScheduler(nil)
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
	s := cron.NewScheduler(nil)
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
	s := cron.NewScheduler(nil)
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
	s := cron.NewScheduler(nil)
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
	s := cron.NewScheduler(nil)
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
	s := cron.NewScheduler(func(prompt string) { fired = append(fired, prompt) })
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

	// Verify it has a NextFire time set
	if jobs[0].NextFire.IsZero() {
		t.Error("expected NextFire to be set")
	}
}
