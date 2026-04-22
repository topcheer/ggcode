package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

func TestCronCreate_Basic(t *testing.T) {
	s := cron.NewScheduler(func(string) {})
	defer s.Shutdown()

	cc := CronCreateTool{Scheduler: s}
	input, _ := json.Marshal(map[string]interface{}{
		"cron":   "*/5 * * * *",
		"prompt": "check status",
	})
	result, err := cc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "cron-1") {
		t.Errorf("expected job ID, got: %s", result.Content)
	}
}

func TestCronCreate_MissingCron(t *testing.T) {
	s := cron.NewScheduler(nil)
	defer s.Shutdown()

	cc := CronCreateTool{Scheduler: s}
	input, _ := json.Marshal(map[string]interface{}{
		"prompt": "check status",
	})
	result, err := cc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing cron")
	}
}

func TestCronCreate_MissingPrompt(t *testing.T) {
	s := cron.NewScheduler(nil)
	defer s.Shutdown()

	cc := CronCreateTool{Scheduler: s}
	input, _ := json.Marshal(map[string]interface{}{
		"cron": "*/5 * * * *",
	})
	result, err := cc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing prompt")
	}
}

func TestCronDelete_Basic(t *testing.T) {
	s := cron.NewScheduler(nil)
	defer s.Shutdown()

	// Create first
	cc := CronCreateTool{Scheduler: s}
	input, _ := json.Marshal(map[string]interface{}{
		"cron":   "0 * * * *",
		"prompt": "hourly",
	})
	cc.Execute(context.Background(), input)

	// Delete
	cd := CronDeleteTool{Scheduler: s}
	input, _ = json.Marshal(map[string]interface{}{
		"jobId": "cron-1",
	})
	result, err := cd.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestCronDelete_NotFound(t *testing.T) {
	s := cron.NewScheduler(nil)
	defer s.Shutdown()

	cd := CronDeleteTool{Scheduler: s}
	input, _ := json.Marshal(map[string]interface{}{
		"jobId": "cron-999",
	})
	result, err := cd.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent job")
	}
}

func TestCronList_Empty(t *testing.T) {
	s := cron.NewScheduler(nil)
	defer s.Shutdown()

	cl := CronListTool{Scheduler: s}
	result, err := cl.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "No scheduled jobs") {
		t.Errorf("expected empty message, got: %s", result.Content)
	}
}

func TestCronList_WithJobs(t *testing.T) {
	s := cron.NewScheduler(nil)
	defer s.Shutdown()

	cc := CronCreateTool{Scheduler: s}
	input, _ := json.Marshal(map[string]interface{}{
		"cron":   "0 9 * * *",
		"prompt": "morning check",
	})
	cc.Execute(context.Background(), input)

	cl := CronListTool{Scheduler: s}
	result, err := cl.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !containsAny(result.Content, "cron-1", "morning check") {
		t.Errorf("expected job listing, got: %s", result.Content)
	}
}
