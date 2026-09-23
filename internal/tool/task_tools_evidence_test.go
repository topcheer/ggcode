package tool

// Tests for the task_update completion evidence gate (sa-183).
//
// Research basis: LongHorizon-Harness (arXiv:2608.01964) - task state must be
// updated only with facts independently verified from the environment.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

func newEvidenceTestTask(t *testing.T) (string, *task.Manager) {
	t.Helper()
	mgr := task.NewManager()
	created := TaskCreateTool{Manager: mgr}
	res, err := created.Execute(context.Background(), []byte(`{"subject":"do thing","description":"d"}`))
	if err != nil || res.IsError {
		t.Fatalf("create task: err=%v res=%+v", err, res)
	}
	// task IDs are sequential from 1
	return "task-1", mgr
}

func updateStatus(t *testing.T, mgr *task.Manager, evidence func() bool, taskID, status string) Result {
	t.Helper()
	tool := TaskUpdateTool{Manager: mgr, EvidenceFn: evidence}
	res, err := tool.Execute(context.Background(), []byte(`{"taskId":"`+taskID+`","status":"`+status+`"}`))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	return res
}

func metadataOf(t *testing.T, res Result) map[string]string {
	t.Helper()
	// The advisory (if any) is appended after the JSON line.
	first := strings.SplitN(res.Content, "\n", 2)[0]
	updated := struct {
		Metadata map[string]string `json:"metadata"`
	}{}
	if err := json.Unmarshal([]byte(first), &updated); err != nil {
		t.Fatalf("unmarshal %q: %v", first, err)
	}
	return updated.Metadata
}

func TestTaskUpdateCompletionGate_NilEvidenceFnIsInert(t *testing.T) {
	resetTaskCompletionGate()
	id, mgr := newEvidenceTestTask(t)
	res := updateStatus(t, mgr, nil, id, "completed")
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if strings.Contains(res.Content, "[task-verification]") {
		t.Error("nil EvidenceFn must not emit advisory")
	}
	if m := metadataOf(t, res); m["verification"] != "" {
		t.Errorf("nil EvidenceFn must not stamp verification metadata, got %q", m["verification"])
	}
}

func TestTaskUpdateCompletionGate_VerifiedFlip(t *testing.T) {
	resetTaskCompletionGate()
	id, mgr := newEvidenceTestTask(t)
	res := updateStatus(t, mgr, func() bool { return true }, id, "completed")
	if m := metadataOf(t, res); m["verification"] != "verified" {
		t.Errorf("metadata verification = %q, want verified", m["verification"])
	}
	if strings.Contains(res.Content, "[task-verification]") {
		t.Error("evidenced flip must not emit advisory")
	}
}

func TestTaskUpdateCompletionGate_UnverifiedFlipAdvises(t *testing.T) {
	resetTaskCompletionGate()
	id, mgr := newEvidenceTestTask(t)
	res := updateStatus(t, mgr, func() bool { return false }, id, "completed")
	if m := metadataOf(t, res); m["verification"] != "unverified" {
		t.Errorf("metadata verification = %q, want unverified", m["verification"])
	}
	if !strings.Contains(res.Content, "[task-verification]") {
		t.Errorf("unverified flip must append advisory, got: %s", res.Content)
	}
	// Advisory is one-shot per task.
	res2 := updateStatus(t, mgr, func() bool { return false }, id, "in_progress")
	_ = res2
	res3 := updateStatus(t, mgr, func() bool { return false }, id, "completed")
	if strings.Contains(res3.Content, "[task-verification]") {
		t.Error("second flip of the same task must not re-advise")
	}
	if m := metadataOf(t, res3); m["verification"] != "unverified" {
		t.Errorf("metadata still stamped on repeat flip, got %q", m["verification"])
	}
}

func TestTaskUpdateCompletionGate_NonCompletedStatusNotGated(t *testing.T) {
	resetTaskCompletionGate()
	id, mgr := newEvidenceTestTask(t)
	res := updateStatus(t, mgr, func() bool { return false }, id, "in_progress")
	if strings.Contains(res.Content, "[task-verification]") {
		t.Error("non-completed status change must not trigger the gate")
	}
	if m := metadataOf(t, res); m["verification"] != "" {
		t.Errorf("non-completed status must not stamp metadata, got %q", m["verification"])
	}
}

func TestTaskUpdateCompletionGate_AdvisoryBoundedPerProcess(t *testing.T) {
	resetTaskCompletionGate()
	mgr := task.NewManager()
	advised := 0
	for i := 1; i <= taskGateMaxWarns+2; i++ {
		id := "task-" + string(rune('0'+i))
		mgr.Create("s", "d", "", nil)
		res := updateStatus(t, mgr, func() bool { return false }, id, "completed")
		if strings.Contains(res.Content, "[task-verification]") {
			advised++
		}
	}
	if advised != taskGateMaxWarns {
		t.Errorf("advisories emitted = %d, want cap %d", advised, taskGateMaxWarns)
	}
}
