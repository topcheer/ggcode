package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/task"
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

func TestSwarmTaskToolsEmitBoardUpdatedEvents(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")
	var events []swarm.Event
	mgr.SetOnUpdate(func(ev swarm.Event) {
		if ev.Type == "team_board_updated" {
			events = append(events, ev)
		}
	})

	createTool := SwarmTaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"team_id": team.ID,
		"subject": "Emit events",
	})
	createResult, err := createTool.Execute(context.Background(), input)
	if err != nil || createResult.IsError {
		t.Fatalf("create failed: result=%+v err=%v", createResult, err)
	}
	var created task.Task
	if err := json.Unmarshal([]byte(createResult.Content), &created); err != nil {
		t.Fatalf("unmarshal created task: %v", err)
	}

	claimTool := SwarmTaskClaimTool{Manager: mgr}
	claimInput, _ := json.Marshal(map[string]string{
		"team_id": team.ID,
		"task_id": created.ID,
		"owner":   "tm-1",
	})
	if result, err := claimTool.Execute(context.Background(), claimInput); err != nil || result.IsError {
		t.Fatalf("claim failed: result=%+v err=%v", result, err)
	}

	completeTool := SwarmTaskCompleteTool{Manager: mgr}
	completeInput, _ := json.Marshal(map[string]string{
		"team_id": team.ID,
		"task_id": created.ID,
	})
	if result, err := completeTool.Execute(context.Background(), completeInput); err != nil || result.IsError {
		t.Fatalf("complete failed: result=%+v err=%v", result, err)
	}

	if len(events) != 3 {
		t.Fatalf("expected create/claim/complete board events, got %d: %+v", len(events), events)
	}
	for _, ev := range events {
		if ev.TeamID != team.ID {
			t.Fatalf("board event TeamID = %q, want %q", ev.TeamID, team.ID)
		}
	}
}

func TestSwarmTaskCreateToolDescriptionEmphasizesCoordination(t *testing.T) {
	desc := SwarmTaskCreateTool{}.Description()
	for _, want := range []string{
		"shared task board",
		"Set assignee to deliver directly",
		"Do not duplicate existing tracked tasks",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("expected description to contain %q, got %q", want, desc)
		}
	}
}

func TestFormatTaskPromptEmphasizesCollaborationRules(t *testing.T) {
	prompt := formatTaskPrompt(task.Task{
		Subject:     "Investigate API",
		Description: "Look into the REST endpoints",
	})
	for _, want := range []string{
		"do not re-claim it from the board first",
		"avoid duplicate effort",
		"one clear handoff task",
		"teammate runner will update the task board",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
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

func TestSwarmTaskListToolShowsOwnerForClaimedUnassignedTask(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tm, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatalf("EnsureTaskManager failed: %v", err)
	}
	created := tm.Create("Claimed task", "", "", nil)
	inProgress := task.StatusInProgress
	owner := "tm-2"
	if _, err := tm.Update(created.ID, task.UpdateOptions{Status: &inProgress, Owner: &owner}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	tool := SwarmTaskListTool{Manager: mgr}
	input, _ := json.Marshal(map[string]string{"team_id": team.ID})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "Claimed task → tm-2") {
		t.Fatalf("expected claimed task owner in list output, got %q", result.Content)
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

func TestSwarmTaskCreateTool_NoAssigneeNotifiesIdleRunners(t *testing.T) {
	// When no assignee is specified, the tool should call NotifyIdleRunners
	// so idle teammates can claim immediately instead of waiting for poller.
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("test", "leader")

	tool := SwarmTaskCreateTool{Manager: mgr}
	input, _ := json.Marshal(map[string]interface{}{
		"team_id":     team.ID,
		"subject":     "Unassigned task",
		"description": "No assignee, should trigger NotifyIdleRunners",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	// Verify the task was created (no panic from NotifyIdleRunners)
	if !strings.Contains(result.Content, "Unassigned task") {
		t.Errorf("result should contain task subject, got: %s", result.Content)
	}
}

func TestSwarmTaskToolDescriptionsClarifyAssignmentFlow(t *testing.T) {
	createDesc := SwarmTaskCreateTool{}.Description()
	for _, want := range []string{"deliver directly to a teammate's inbox", "do not also call swarm_task_claim", "Leave assignee empty"} {
		if !strings.Contains(createDesc, want) {
			t.Fatalf("swarm_task_create description should mention %q, got %q", want, createDesc)
		}
	}
	createParams := string(SwarmTaskCreateTool{}.Parameters())
	if !strings.Contains(createParams, "direct-delivered") || !strings.Contains(createParams, "do not also call swarm_task_claim") {
		t.Fatalf("swarm_task_create assignee schema should warn about direct delivery: %s", createParams)
	}

	claimDesc := SwarmTaskClaimTool{}.Description()
	if !strings.Contains(claimDesc, "unassigned pending task") || !strings.Contains(claimDesc, "Do not call this for tasks that were created with an assignee") {
		t.Fatalf("swarm_task_claim description should clarify assigned-task flow, got %q", claimDesc)
	}

	completeDesc := SwarmTaskCompleteTool{}.Description()
	if !strings.Contains(completeDesc, "updates board state only") || !strings.Contains(completeDesc, "teammate_results") {
		t.Fatalf("swarm_task_complete description should clarify board/output separation, got %q", completeDesc)
	}
}
