package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// r-sa-99: task_output is a side path that must not bypass delegation
// validation. When the provider can supply the task text of a completed
// sub-agent run whose task carried acceptance criteria, the same acceptance
// checkpoint wait_agent appends must appear here too.

type fakeDelegationProvider struct {
	output map[string]string
	tasks  map[string]string
}

func (f *fakeDelegationProvider) GetTaskOutput(taskID string) (string, bool) {
	out, ok := f.output[taskID]
	return out, ok
}

func (f *fakeDelegationProvider) GetTaskText(taskID string) (string, bool) {
	task, ok := f.tasks[taskID]
	return task, ok
}

func execTaskOutput(t *testing.T, provider BackgroundTaskProvider, taskID string) string {
	t.Helper()
	tool := TaskOutputTool{Provider: provider}
	raw, err := json.Marshal(map[string]interface{}{
		"task_id":     taskID,
		"description": "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	return res.Content
}

func TestTaskOutputAttachesAcceptanceCheckpoint(t *testing.T) {
	p := &fakeDelegationProvider{
		output: map[string]string{"agent-1": "done: tests pass, diff attached"},
		tasks: map[string]string{"agent-1": `## Objective
Fix X
## Acceptance Criteria
- go test passes
- no new lint warnings`},
	}
	out := execTaskOutput(t, p, "agent-1")
	if !strings.Contains(out, acceptanceMarker) {
		t.Fatalf("task_output must attach acceptance checkpoint for completed delegation: %q", out)
	}
	if !strings.Contains(out, "go test passes") {
		t.Fatal("checkpoint must echo the original criteria")
	}
}

func TestTaskOutputNoTaskTextNoCheckpoint(t *testing.T) {
	// Shell jobs / providers without DelegationTaskTextProvider: nothing to
	// validate against, output unchanged.
	type plainProvider struct{ out string }

	p := &fakeDelegationProvider{
		output: map[string]string{"job-1": "build ok"},
		// no tasks entry -> GetTaskText ok=false
	}
	out := execTaskOutput(t, p, "job-1")
	if strings.Contains(out, acceptanceMarker) {
		t.Fatalf("no task text must not attach checkpoint: %q", out)
	}
}

func TestTaskOutputNoCriteriaNoCheckpoint(t *testing.T) {
	// Task text without an acceptance section: reminder is a no-op.
	p := &fakeDelegationProvider{
		output: map[string]string{"agent-2": "summary of work"},
		tasks:  map[string]string{"agent-2": "do a thing and report back"},
	}
	out := execTaskOutput(t, p, "agent-2")
	if strings.Contains(out, acceptanceMarker) {
		t.Fatalf("task without criteria must not attach checkpoint: %q", out)
	}
}

func TestTaskOutputTailLinesSkipsCheckpoint(t *testing.T) {
	// tail_lines bisects the output; appending the checkpoint after the
	// truncation banner would be noise, so it is skipped there.
	tool := TaskOutputTool{Provider: &fakeDelegationProvider{
		output: map[string]string{"agent-3": strings.Repeat("line\n", 30)},
		tasks: map[string]string{"agent-3": `## Acceptance Criteria
- something verifiable`},
	}}
	raw, err := json.Marshal(map[string]interface{}{
		"task_id":     "agent-3",
		"tail_lines":  5,
		"description": "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil || res.IsError {
		t.Fatalf("execute: %v %s", err, res.Content)
	}
	if strings.Contains(res.Content, acceptanceMarker) {
		t.Fatalf("tail_lines pagination must skip checkpoint: %q", res.Content)
	}
}
