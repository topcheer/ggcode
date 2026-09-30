package tool

// #2794: swarm_task_complete duplicate guard was Get+Update - a non-atomic
// window let a second complete (or a racing parking Write) flip an already
// completed task back through the unlocked Status write. The fix pins the
// Update with ExpectedStatus=in_progress (CAS inside m.mu). These tests pin
// the observable semantics of that CAS from the tool layer.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func completeInput(teamID, taskID string) []byte {
	b, _ := json.Marshal(map[string]string{
		"team_id": teamID,
		"task_id": taskID,
	})
	return b
}

// A pending (unclaimed) task must NOT be completable: completing unclaimed
// work would fabricate output nobody produced. The CAS rejects it because
// the status is pending, not in_progress.
func TestIssue2794CompletePendingTaskRejected(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("t2794-pending", "leader")

	createTool := SwarmTaskCreateTool{Manager: mgr}
	createResult, _ := createTool.Execute(context.Background(), mustJSON2794(t, map[string]interface{}{
		"team_id": team.ID,
		"subject": "never claimed",
	}))
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(createResult.Content), &created)

	tool := SwarmTaskCompleteTool{Manager: mgr}
	result, err := tool.Execute(context.Background(), completeInput(team.ID, created.ID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("completing a pending (unclaimed) task should fail via ExpectedStatus CAS, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "expected") {
		t.Fatalf("error should surface the status mismatch, got: %s", result.Content)
	}
}

// Double complete: first succeeds, second must fail. The Get pre-check
// renders the specific already-completed error; the CAS is the atomic
// backstop when the status changed between Get and Update.
func TestIssue2794DoubleCompleteRejected(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("t2794-double", "leader")

	createTool := SwarmTaskCreateTool{Manager: mgr}
	createResult, _ := createTool.Execute(context.Background(), mustJSON2794(t, map[string]interface{}{
		"team_id": team.ID,
		"subject": "double fire",
	}))
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(createResult.Content), &created)

	claimTool := SwarmTaskClaimTool{Manager: mgr}
	claimTool.Execute(context.Background(), mustJSON2794(t, map[string]string{
		"team_id": team.ID,
		"task_id": created.ID,
		"owner":   "tm-2794",
	}))

	tool := SwarmTaskCompleteTool{Manager: mgr}
	first, err := tool.Execute(context.Background(), completeInput(team.ID, created.ID))
	if err != nil || first.IsError {
		t.Fatalf("first complete should succeed, err=%v content=%s", err, first.Content)
	}
	second, err := tool.Execute(context.Background(), completeInput(team.ID, created.ID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !second.IsError {
		t.Fatalf("second complete must be rejected, got success: %s", second.Content)
	}
	if !strings.Contains(second.Content, "already completed") {
		t.Fatalf("second complete should hit the already-completed guard, got: %s", second.Content)
	}
}

func mustJSON2794(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
