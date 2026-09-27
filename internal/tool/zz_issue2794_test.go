package tool

// #2794 regression: the duplicate-complete guard was two-step non-atomic
// (Get check, then an unconditional Update). Two stale-view callers that
// both pass the Get check in the race window BOTH succeeded - the second
// complete sailed through (completed->completed has no guard inside
// Update), double-firing EmitBoardUpdated and contradicting the guard's
// own comment ("must not silently succeed"). The fix is the same
// ExpectedStatus conditional-update primitive the claim path already
// uses (#861): the second complete now fails atomically inside the
// manager's lock.

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	swarmtask "github.com/topcheer/ggcode/internal/task"
)

var issue2794TaskID = regexp.MustCompile(`task-\d+`)

// Guard probe (source-level invariant): the complete path must go through
// the atomic conditional-update primitive. The live race window is too
// narrow to hit deterministically in a unit test (goroutine scheduling
// usually serializes the two Executes before the window matters - the
// issue is rated low for exactly that reason), so following the #2793
// precedent we pin the structure: if the ExpectedStatus CAS is dropped
// from the complete call, this fails. The CAS itself is verified live by
// TestIssue2794_CASRejectsCompletedTask below.
func TestIssue2794_CompleteUsesAtomicCAS(t *testing.T) {
	b, err := os.ReadFile("swarm_task_tools.go")
	if err != nil {
		t.Skipf("source layout changed: %v", err)
	}
	src := string(b)
	i := strings.Index(src, "duplicate-complete guard")
	if i < 0 {
		t.Fatal("guard comment anchor missing - file layout changed")
	}
	// The Update call after the guard comment must carry ExpectedStatus.
	tail := src[i:]
	if !strings.Contains(tail, "ExpectedStatus: &inProgress") {
		t.Fatal("#2794: complete path no longer uses the atomic ExpectedStatus CAS - the Get/Update race window would reopen")
	}
}

// Live semantic probe: the CAS primitive rejects a completed task (this is
// the property the complete path relies on for atomicity).
func TestIssue2794_CASRejectsCompletedTask(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("t2794b", "leader")
	tmMgr, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	tk := tmMgr.Create("s", "d", "", nil)
	inProgress := swarmtask.StatusInProgress
	if _, err := tmMgr.Update(tk.ID, swarmtask.UpdateOptions{Status: &inProgress}); err != nil {
		t.Fatal(err)
	}
	completed := swarmtask.StatusCompleted
	if _, err := tmMgr.Update(tk.ID, swarmtask.UpdateOptions{Status: &completed}); err != nil {
		t.Fatal(err)
	}
	// The complete-path CAS (expect in_progress) must fail on the completed task.
	if _, err := tmMgr.Update(tk.ID, swarmtask.UpdateOptions{Status: &completed, ExpectedStatus: &inProgress}); err == nil {
		t.Fatal("#2794: CAS accepted a completed->completed transition - the atomicity premise of the fix does not hold")
	}
}

// Sequential double-complete must keep its friendly guard error (regression).
func TestIssue2794_SequentialDoubleCompleteStillGuarded(t *testing.T) {
	create, complete, teamID, _ := new1705Env(t)
	created, err := create.Execute(context.Background(), json.RawMessage(
		`{"team_id":"`+teamID+`","subject":"s","description":"d"}`))
	if err != nil || created.IsError {
		t.Fatalf("create: %v %s", err, created.Content)
	}
	m2 := issue2794TaskID.FindString(created.Content)
	if m2 == "" {
		t.Fatalf("cannot find task id in create output: %s", created.Content)
	}
	taskID := m2

	tmMgr, _ := create.Manager.EnsureTaskManager(teamID)
	inProgress := swarmtask.StatusInProgress
	if _, err := tmMgr.Update(taskID, swarmtask.UpdateOptions{Status: &inProgress}); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"team_id": teamID, "task_id": taskID})
	if res, err := complete.Execute(context.Background(), input); err != nil || res.IsError {
		t.Fatalf("first complete must succeed: %v %s", err, res.Content)
	}
	res, err := complete.Execute(context.Background(), input)
	if err == nil && !res.IsError {
		t.Fatal("#2794: sequential double-complete must be rejected")
	}
}
