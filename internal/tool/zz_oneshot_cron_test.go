package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

// Sleep-discipline routing (user directive 2026-11-05): any wait expected to
// exceed 5 minutes must go through a one-shot cron that fires the follow-up
// and self-deletes, never a long sleep.
func TestCronCreateOneShotSelfDeletes(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, t.TempDir()+"/cron.json")
	tool := CronCreateTool{Scheduler: s}
	input, _ := json.Marshal(map[string]any{
		"cron":        "*/5 * * * *",
		"prompt":      "check the long-running build",
		"one_shot":    true,
		"description": "t",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "one-shot: fires once and is removed automatically") {
		t.Fatalf("one_shot=true output should disclose the self-deleting semantics, got: %s", result.Content)
	}
	var job struct {
		Recurring bool `json:"recurring"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(result.Content, "\n", 2)[0]), &job); err != nil {
		t.Fatalf("job JSON parse: %v", err)
	}
	if job.Recurring {
		t.Fatal("one_shot=true must create a non-recurring (self-deleting on fire) job")
	}
}
