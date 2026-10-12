package tool

// Companion tests for exposing task-board dependency semantics on the
// swarm tool surface (#research-task-deps). The task engine
// (internal/task manager.go: AddBlockedBy + cycle detection) and the
// idle-runner poller (allBlockersComplete) have honored BlockedBy edges
// all along - only the agent-facing tools never exposed them, so agents
// could not declare ordering across teammates. These tests pin the three
// surface changes: create accepts depends_on, claim is gated while any
// dependency is incomplete (auto-unblocking on completion), and list
// annotates blocked tasks.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

func TestSwarmTaskDependsOnCreatesChainAndGatesClaim(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("deps", "leader")
	tm, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	dep := tm.Create("prerequisite", "", "", nil)

	createTool := SwarmTaskCreateTool{Manager: mgr}
	ci, _ := json.Marshal(map[string]interface{}{
		"team_id":     team.ID,
		"subject":     "downstream",
		"description": "needs the prerequisite done first",
		"depends_on":  []string{dep.ID},
	})
	cr, err := createTool.Execute(context.Background(), ci)
	if err != nil {
		t.Fatal(err)
	}
	if cr.IsError {
		t.Fatalf("create with depends_on failed: %s", cr.Content)
	}
	var created struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(cr.Content, "\n", 2)[0]), &created); err != nil {
		t.Fatalf("parse created task: %v\ncontent: %s", err, cr.Content)
	}
	got, ok := tm.Get(created.ID)
	if !ok || len(got.BlockedBy) != 1 || got.BlockedBy[0] != dep.ID {
		t.Fatalf("dependency edge not established: %+v", got)
	}
	if !strings.Contains(cr.Content, "blocked by uncompleted dependencies") {
		t.Fatalf("create output must explain the blocked state, got: %s", cr.Content)
	}

	// Claim while the dependency is pending: rejected.
	claimTool := SwarmTaskClaimTool{Manager: mgr}
	claimInput := func(taskID, owner string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"team_id":%q,"task_id":%q,"owner":%q,"description":"x"}`, team.ID, taskID, owner))
	}
	res, _ := claimTool.Execute(context.Background(), claimInput(created.ID, "tm-9"))
	if !res.IsError || !strings.Contains(res.Content, "blocked by uncompleted dependencies") {
		t.Fatalf("claim must be gated while dependency pending, got: %+v", res)
	}
	if updated, _ := tm.Get(created.ID); updated.Status == task.StatusInProgress {
		t.Fatal("gated claim must not move the task to in_progress")
	}

	// List annotates the blocked task.
	listTool := SwarmTaskListTool{Manager: mgr}
	li, _ := json.Marshal(map[string]string{"team_id": team.ID})
	lr, _ := listTool.Execute(context.Background(), li)
	if !strings.Contains(lr.Content, fmt.Sprintf("(blocked by: %s)", dep.ID)) {
		t.Fatalf("list must annotate blocked tasks, got: %s", lr.Content)
	}

	// Complete the dependency: the board auto-unblocks and claim succeeds.
	completed := task.StatusCompleted
	if _, err := tm.Update(dep.ID, task.UpdateOptions{Status: &completed}); err != nil {
		t.Fatal(err)
	}
	res, _ = claimTool.Execute(context.Background(), claimInput(created.ID, "tm-9"))
	if res.IsError {
		t.Fatalf("claim after dependency completion must succeed, got: %s", res.Content)
	}
	if updated, _ := tm.Get(created.ID); updated.Status != task.StatusInProgress {
		t.Fatalf("task must be in_progress after unblocked claim: %+v", updated)
	}
}

func TestSwarmTaskDependsOnRejectsUnknownIDAndPassesCompletedDep(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("cycle", "leader")
	tm, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := tm.Create("A", "", "", nil)

	createTool := SwarmTaskCreateTool{Manager: mgr}
	mkInput := func(dep string) json.RawMessage {
		ci, _ := json.Marshal(map[string]interface{}{
			"team_id":     team.ID,
			"subject":     "edge case",
			"description": "input validation",
			"depends_on":  []string{dep},
		})
		return ci
	}
	// Unknown dependency ID: rejected, and the half-wired task must not
	// linger on the board. (A brand-new task can never itself close a
	// dependency cycle - nothing can depend on it yet - so the cycle
	// detector guards the engine's Update path, exercised by internal/task.)
	res, _ := createTool.Execute(context.Background(), mkInput("task-nope"))
	if !res.IsError || !strings.Contains(res.Content, "depends_on rejected") {
		t.Fatalf("unknown dependency ID must be rejected, got: %+v", res)
	}
	for _, tk := range tm.List() {
		if tk.Subject == "edge case" {
			t.Fatalf("rejected task must not remain on the board: %+v", tk)
		}
	}

	// An already-completed dependency must not block: no note, no gating.
	completed := task.StatusCompleted
	if _, err := tm.Update(a.ID, task.UpdateOptions{Status: &completed}); err != nil {
		t.Fatal(err)
	}
	res, _ = createTool.Execute(context.Background(), mkInput(a.ID))
	if res.IsError {
		t.Fatalf("completed dependency must not block creation: %s", res.Content)
	}
	if strings.Contains(res.Content, "blocked by uncompleted dependencies") {
		t.Fatalf("completed dependency must not produce a blocked note: %s", res.Content)
	}
}

func TestSwarmTaskDependsOnSkipsAssigneeDeliveryWhileBlocked(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("delivery", "leader")
	worker, err := mgr.SpawnTeammate(team.ID, "worker", "32", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	tm, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	dep := tm.Create("prerequisite", "", "", nil)

	createTool := SwarmTaskCreateTool{Manager: mgr}
	ci, _ := json.Marshal(map[string]interface{}{
		"team_id":     team.ID,
		"subject":     "blocked handoff",
		"description": "must not be delivered while dep pending",
		"assignee":    worker.ID,
		"depends_on":  []string{dep.ID},
	})
	cr, _ := createTool.Execute(context.Background(), ci)
	if cr.IsError {
		t.Fatalf("create failed: %s", cr.Content)
	}
	if !strings.Contains(cr.Content, "blocked by uncompleted dependencies") {
		t.Fatalf("blocked assigned task must carry the blocked note, got: %s", cr.Content)
	}
	// The task stays pending on the board; the teammate was NOT handed a
	// runnable prompt while its prerequisite is incomplete (delivery gate:
	// unmetDependencies must be empty at delivery time).
	var created struct {
		ID     string `json:"ID"`
		Status string `json:"Status"`
	}
	json.Unmarshal([]byte(strings.SplitN(cr.Content, "\n", 2)[0]), &created)
	if created.Status != string(task.StatusPending) {
		t.Fatalf("blocked task must stay pending, got %q", created.Status)
	}
}
