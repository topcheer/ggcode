package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// ——— SwarmTaskCreate ———

func TestSwarmTaskCreateTool(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tool := SwarmTaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"team_id":     team.ID,
		"subject":     "Investigate API",
		"description": "Look into the REST endpoints",
		"assignee":    "tm-1",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestSwarmTaskCreateTool_EmptySubject(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tool := SwarmTaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"team_id": team.ID,
		"subject": "   ",
	})

	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("expected error for empty subject")
	}
}

func TestSwarmTaskCreateTool_TeamNotFound(t *testing.T) {
	mgr := swarmTestManager(t)
	tool := SwarmTaskCreateTool{Manager: mgr}

	input, _ := json.Marshal(map[string]interface{}{
		"team_id": "nonexistent",
		"subject": "Do stuff",
	})

	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("expected error for nonexistent team")
	}
}

// ——— SwarmTaskList ———

func TestSwarmTaskListTool(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	// Create some tasks first
	createTool := SwarmTaskCreateTool{Manager: mgr}
	for i := 0; i < 3; i++ {
		input, _ := json.Marshal(map[string]interface{}{
			"team_id": team.ID,
			"subject": "Task " + string(rune('A'+i)),
		})
		createTool.Execute(context.Background(), input)
	}

	tool := SwarmTaskListTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{"team_id": team.ID})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestSwarmTaskListTool_Empty(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tool := SwarmTaskListTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{"team_id": team.ID})

	result, _ := tool.Execute(context.Background(), input)
	if result.Content != "No tasks.\n" {
		t.Errorf("expected 'No tasks.', got %q", result.Content)
	}
}

// ——— SwarmTaskClaim ———

func TestSwarmTaskClaimTool(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	// Create a task
	createTool := SwarmTaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"team_id": team.ID,
		"subject": "Do the thing",
	})
	createResult, _ := createTool.Execute(context.Background(), input)

	// Parse task ID from result
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(createResult.Content), &created)
	taskID := created.ID
	if taskID == "" {
		t.Fatalf("failed to parse task ID from: %q", createResult.Content)
	}

	// Claim it
	tool := SwarmTaskClaimTool{Manager: mgr}
	input, _ = json.Marshal(map[string]string{
		"team_id": team.ID,
		"task_id": taskID,
		"owner":   "tm-researcher",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

// ——— SwarmTaskComplete ———

func TestSwarmTaskCompleteTool(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	// Create and claim a task
	createTool := SwarmTaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"team_id": team.ID,
		"subject": "Do the thing",
	})
	createResult, _ := createTool.Execute(context.Background(), input)

	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(createResult.Content), &created)
	taskID := created.ID

	claimTool := SwarmTaskClaimTool{Manager: mgr}
	claimInput, _ := json.Marshal(map[string]string{
		"team_id": team.ID,
		"task_id": taskID,
		"owner":   "tm-1",
	})
	claimTool.Execute(context.Background(), claimInput)

	// Complete it
	tool := SwarmTaskCompleteTool{Manager: mgr}
	input, _ = json.Marshal(map[string]string{
		"team_id": team.ID,
		"task_id": taskID,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

// ——— Integration: Full swarm workflow ———

func TestSwarmWorkflow(t *testing.T) {
	mgr := swarmTestManager(t)

	// 1. Create team
	teamCreate := TeamCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{
		"name":      "workflow-team",
		"leader_id": "leader-1",
	})
	result, _ := teamCreate.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("step 1 failed: %s", result.Content)
	}

	var teamSnap struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(result.Content), &teamSnap)
	teamID := teamSnap.ID
	if teamID == "" {
		t.Fatalf("failed to parse team ID from: %q", result.Content)
	}

	// 2. Create tasks
	taskCreate := SwarmTaskCreateTool{Manager: mgr}
	for _, subj := range []string{"Research", "Implement", "Test"} {
		input, _ := json.Marshal(map[string]interface{}{
			"team_id": teamID,
			"subject": subj,
		})
		result, _ = taskCreate.Execute(context.Background(), input)
		if result.IsError {
			t.Fatalf("step 2 failed (%s): %s", subj, result.Content)
		}
	}

	// 3. List tasks
	taskList := SwarmTaskListTool{Manager: mgr}
	input, _ = json.Marshal(map[string]string{"team_id": teamID})
	result, _ = taskList.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("step 3 failed: %s", result.Content)
	}

	// 4. Delete team
	teamDelete := TeamDeleteTool{Manager: mgr}
	input, _ = json.Marshal(map[string]string{"team_id": teamID})
	result, _ = teamDelete.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("step 4 failed: %s", result.Content)
	}
}
