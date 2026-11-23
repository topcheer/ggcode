package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

// The review gate the master protocol relies on: a teammate's self-complete
// is a delivery; `changes` sends it back and reopens the task, `approve`
// accepts it. Exercises the real swarm.Manager board, not a stub.
func TestTeamReviewGate_BounceReopensAndApproveCompletes(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("gate", "leader")
	tmSnap, err := mgr.SpawnTeammate(team.ID, "worker", "32", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Unassigned task so the nil-factory teammate does not consume it; the
	// teammate claims and self-completes it like idle_runner would.
	board, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("task manager: %v", err)
	}
	createdTask := board.Create("implement gate", "do it", "", nil)
	taskID := createdTask.ID

	claim := SwarmTaskClaimTool{Manager: mgr}
	if res, err := claim.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"team_id": team.ID, "task_id": taskID, "owner": tmSnap.ID,
	})); err != nil || res.IsError {
		t.Fatalf("claim: %v %s", err, res.Content)
	}
	complete := SwarmTaskCompleteTool{Manager: mgr}
	if res, err := complete.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"team_id": team.ID, "task_id": taskID,
	})); err != nil || res.IsError {
		t.Fatalf("self-complete: %v %s", err, res.Content)
	}
	assertBoardStatus(t, board, taskID, task.StatusCompleted)

	decide := TeamReviewDecideTool{Manager: mgr}

	// changes without a note is refused
	if res, _ := decide.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"team_id": team.ID, "teammate_id": tmSnap.ID, "verdict": "changes",
	})); !res.IsError {
		t.Fatalf("changes without note must be refused, got %s", res.Content)
	}

	// changes with a note reopens the delivered task
	res, err := decide.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"team_id": team.ID, "teammate_id": tmSnap.ID, "verdict": "changes", "note": "add tests",
	}))
	if err != nil || res.IsError {
		t.Fatalf("changes: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "Changes") {
		t.Fatalf("want bounce confirmation, got %s", res.Content)
	}
	assertBoardStatus(t, board, taskID, task.StatusPending)

	// the teammate re-delivers, the leader approves -> completed again
	if res, err := complete.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"team_id": team.ID, "task_id": taskID,
	})); err != nil || res.IsError {
		t.Fatalf("re-complete: %v %s", err, res.Content)
	}
	res, err = decide.Execute(context.Background(), mustJSON(t, map[string]interface{}{
		"team_id": team.ID, "teammate_id": tmSnap.ID, "verdict": "approve",
	}))
	if err != nil || res.IsError || !strings.Contains(res.Content, "APPROVED") {
		t.Fatalf("approve: %v %s", err, res.Content)
	}
	assertBoardStatus(t, board, taskID, task.StatusCompleted)
}

func TestTeamReviewDigest_ListsTeammatesAndNextStep(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("digest", "leader")
	if _, err := mgr.SpawnTeammate(team.ID, "worker", "32", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	res, err := TeamReviewDigestTool{Manager: mgr}.Execute(context.Background(), mustJSON(t, map[string]interface{}{"team_id": team.ID}))
	if err != nil || res.IsError {
		t.Fatalf("digest: %v %s", err, res.Content)
	}
	for _, want := range []string{"worker", "unverified", "(no result yet)", "team_review_decide"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("digest must contain %q, got:\n%s", want, res.Content)
		}
	}
}

func assertBoardStatus(t *testing.T, board *task.Manager, taskID string, want task.TaskStatus) {
	t.Helper()
	got, ok := board.Get(taskID)
	if !ok {
		t.Fatalf("task %s vanished from the board", taskID)
	}
	if got.Status != want {
		t.Fatalf("task %s status = %q, want %q", taskID, got.Status, want)
	}
}
