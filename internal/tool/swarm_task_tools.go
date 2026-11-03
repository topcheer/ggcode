package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/task"
)

type SwarmTaskCreateTool struct {
	Manager *swarm.Manager
}

func (t SwarmTaskCreateTool) Name() string { return "swarm_task_create" }
func (t SwarmTaskCreateTool) Description() string {
	return "Create a task on a team's shared task board for coordinated, handoff-ready work. Set assignee to deliver directly to a teammate's inbox (do not also call swarm_task_claim). Leave assignee empty only when no specific teammate is known. Do not duplicate existing tracked tasks or use send_message to repeat an assigned task."
}
func (t SwarmTaskCreateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"team_id": {
			"type": "string",
			"description": "Team ID"
		},
		"subject": {
			"type": "string",
			"description": "Brief actionable title"
		},
		"description": {
			"type": "string",
			"description": "Detailed requirements, structured as a task contract: input boundaries (files/scope), constraints, acceptance criteria (verifiable Done checks), and the evidence to return"
		},
		"assignee": {
			"type": "string",
			"description": "The teammate ID to assign this task to (e.g. tm-2). STRONGLY RECOMMENDED when you know the owner. If set, the task is direct-delivered to that teammate's inbox; do not also call swarm_task_claim. Leave empty only when no specific teammate can be determined."
		},
		"depends_on": {
			"type": "array",
			"items": {"type": "string"},
			"description": "Optional task IDs that must complete before this task can be claimed or delivered. Creates a dependency edge on the board; claiming is rejected while any dependency is not completed (auto-unblocks when they complete). Cyclic dependencies are rejected."
		}
	},
	"required": [
		"team_id",
		"subject",
		"description"
	]
}`)
}
func (t SwarmTaskCreateTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "swarm_task_create: swarm manager not available"}, nil
	}
	var args struct {
		TeamID      string   `json:"team_id"`
		Subject     string   `json:"subject"`
		Description string   `json:"description"`
		Assignee    string   `json:"assignee"`
		DependsOn   []string `json:"depends_on"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Subject) == "" {
		return Result{IsError: true, Content: "subject is required"}, nil
	}

	tm, err := t.Manager.EnsureTaskManager(args.TeamID)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	metadata := map[string]string{}
	if args.Assignee != "" {

		team, ok := t.Manager.GetTeam(args.TeamID)
		if !ok {
			return Result{IsError: true, Content: fmt.Sprintf("team %q not found", args.TeamID)}, nil
		}
		found := false
		for _, tmSnap := range team.Teammates {
			if tmSnap.ID == args.Assignee {
				found = true
				break
			}
		}
		if !found {
			ids := make([]string, 0, len(team.Teammates))
			for _, tmSnap := range team.Teammates {
				ids = append(ids, tmSnap.ID)
			}
			return Result{IsError: true, Content: fmt.Sprintf(
				"assignee %q is not a teammate ID of team %q (have: %v) - the idle runner matches assignees by ID only, so this task would never run",
				args.Assignee, args.TeamID, ids)}, nil
		}
		metadata["assignee"] = args.Assignee
	}

	created := tm.Create(args.Subject, args.Description, "", metadata)

	if len(args.DependsOn) > 0 {
		if _, err := tm.Update(created.ID, task.UpdateOptions{AddBlockedBy: args.DependsOn}); err != nil {

			tm.Delete(created.ID)
			t.Manager.EmitBoardUpdated(args.TeamID)
			return Result{IsError: true, Content: fmt.Sprintf("depends_on rejected (task not created): %v", err)}, nil
		}
	}
	t.Manager.EmitBoardUpdated(args.TeamID)

	if args.Assignee != "" && len(unmetDependencies(tm, created.ID)) == 0 {
		prompt := formatTaskPrompt(created)
		err := t.Manager.SendToTeammate(args.TeamID, args.Assignee, swarm.MailMessage{
			From:    "leader",
			Content: prompt,
			Type:    "task",
		})
		if err != nil {

			out, _ := json.Marshal(created)
			return Result{Content: string(out) + fmt.Sprintf("\nWarning: could not deliver to %q: %v (task stays on board for polling)\n", args.Assignee, err)}, nil
		}
	}

	if args.Assignee == "" && len(unmetDependencies(tm, created.ID)) == 0 {
		t.Manager.NotifyIdleRunners(args.TeamID)
	}
	if unmet := unmetDependencies(tm, created.ID); len(unmet) > 0 {
		out, _ := json.Marshal(created)
		return Result{Content: string(out) + fmt.Sprintf("\nNote: task is blocked by uncompleted dependencies %v; it stays on the board and cannot be claimed or delivered until they complete.\n", unmet)}, nil
	}

	out, _ := json.Marshal(created)
	return Result{Content: string(out) + "\n"}, nil
}

func formatTaskPrompt(tk task.Task) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task: %s\n", tk.Subject))
	if tk.Description != "" {
		sb.WriteString(fmt.Sprintf("Description: %s\n", tk.Description))
	}
	sb.WriteString("\nComplete this task now.")
	sb.WriteString("\nBefore finishing, verify your result against the task's acceptance criteria and state the evidence (tests run, files changed).")
	sb.WriteString("\nIf this task reached you by direct assignment, start it directly and do not re-claim it from the board first.")
	sb.WriteString("\nBefore creating any new follow-up task, check whether related work is already tracked so you avoid duplicate effort.")
	sb.WriteString("\nIf you need help or discover specialized follow-up work, send one targeted request or create one clear handoff task with enough context.")
	sb.WriteString("\nComplete this task now and return the final result. The teammate runner will update the task board when you finish.")
	return sb.String()
}

func unmetDependencies(tm *task.Manager, taskID string) []string {
	tk, ok := tm.Get(taskID)
	if !ok {
		return nil
	}
	var unmet []string
	for _, depID := range tk.BlockedBy {
		dep, ok := tm.Get(depID)
		if !ok || dep.Status != task.StatusCompleted {
			unmet = append(unmet, depID)
		}
	}
	return unmet
}

type SwarmTaskListTool struct {
	Manager *swarm.Manager
}

func (t SwarmTaskListTool) Name() string { return "swarm_task_list" }
func (t SwarmTaskListTool) Description() string {
	return "List all tasks on a team's shared task board."
}
func (t SwarmTaskListTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"team_id": {
			"type": "string",
			"description": "Team ID"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"team_id",
		"description"
	]
}`)
}
func (t SwarmTaskListTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "swarm_task_list: swarm manager not available"}, nil
	}
	var args struct {
		TeamID string `json:"team_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	snap, ok := t.Manager.GetTeam(args.TeamID)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("team %q not found", args.TeamID)}, nil
	}
	if snap.TaskCount == 0 {
		return Result{Content: "No tasks.\n"}, nil
	}

	tm, err := t.Manager.EnsureTaskManager(args.TeamID)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	tasks := tm.List()
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })

	var sb strings.Builder
	for _, tk := range tasks {
		owner := tk.Metadata["assignee"]
		if owner == "" {
			owner = tk.Owner
		}
		ownerSuffix := ""
		if owner != "" {
			ownerSuffix = fmt.Sprintf(" → %s", owner)
		}
		blockedSuffix := ""
		if unmet := unmetDependencies(tm, tk.ID); len(unmet) > 0 {
			blockedSuffix = fmt.Sprintf(" (blocked by: %s)", strings.Join(unmet, ", "))
		}
		fmt.Fprintf(&sb, "- %s [%s] %s%s%s\n", tk.ID, tk.Status, tk.Subject, ownerSuffix, blockedSuffix)
	}
	return Result{Content: sb.String()}, nil
}

type SwarmTaskClaimTool struct {
	Manager *swarm.Manager
}

func (t SwarmTaskClaimTool) Name() string { return "swarm_task_claim" }
func (t SwarmTaskClaimTool) Description() string {
	return "Claim (start working on) an unassigned pending task on the team's task board. Use this only when a teammate is explicitly taking ownership of a pending board task. Do not call this for tasks that were created with an assignee, because those are direct-delivered to the teammate inbox."
}
func (t SwarmTaskClaimTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"team_id": {
			"type": "string",
			"description": "Team ID"
		},
		"task_id": {
			"type": "string",
			"description": "Task ID to claim"
		},
		"owner": {
			"type": "string",
			"description": "Teammate ID claiming the task"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"team_id",
		"task_id",
		"owner",
		"description"
	]
}`)
}
func (t SwarmTaskClaimTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "swarm_task_claim: swarm manager not available"}, nil
	}
	var args struct {
		TeamID string `json:"team_id"`
		TaskID string `json:"task_id"`
		Owner  string `json:"owner"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	tm, err := t.Manager.EnsureTaskManager(args.TeamID)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	if existing, ok := tm.Get(args.TaskID); ok {
		if assignee := existing.Metadata["assignee"]; assignee != "" && assignee != args.Owner {
			return Result{IsError: true, Content: fmt.Sprintf(
				"task %s is directly assigned to %s (direct-delivered to their inbox); do not claim it",
				args.TaskID, assignee)}, nil
		}
		if unmet := unmetDependencies(tm, args.TaskID); len(unmet) > 0 {
			return Result{IsError: true, Content: fmt.Sprintf(
				"task %s is blocked by uncompleted dependencies: %v - complete those first (the board unblocks automatically when they complete)",
				args.TaskID, unmet)}, nil
		}
	}

	updated, err := t.Manager.ClaimTaskSingleWinner(args.TeamID, args.TaskID, args.Owner)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	t.Manager.EmitBoardUpdated(args.TeamID)
	out, _ := json.Marshal(updated)
	return Result{Content: string(out) + "\n"}, nil
}

type SwarmTaskCompleteTool struct {
	Manager *swarm.Manager
}

func (t SwarmTaskCompleteTool) Name() string { return "swarm_task_complete" }
func (t SwarmTaskCompleteTool) Description() string {
	return "Mark a task on the team's task board as completed. This updates board state only; it does not stop a running teammate or retrieve output. Use teammate_results to read the teammate's latest completed output."
}
func (t SwarmTaskCompleteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"team_id": {
			"type": "string",
			"description": "Team ID"
		},
		"task_id": {
			"type": "string",
			"description": "Task ID to complete"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"team_id",
		"task_id",
		"description"
	]
}`)
}
func (t SwarmTaskCompleteTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "swarm_task_complete: swarm manager not available"}, nil
	}
	var args struct {
		TeamID string `json:"team_id"`
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	tm, err := t.Manager.EnsureTaskManager(args.TeamID)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	cur, ok := tm.Get(args.TaskID)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("task %s not found", args.TaskID)}, nil
	}
	switch cur.Status {
	case task.StatusCompleted:
		return Result{IsError: true, Content: fmt.Sprintf("task %s is already completed (owner %q)", cur.ID, cur.Owner)}, nil
	case task.StatusInProgress:
	default:
	}
	expected := task.TaskStatus(cur.Status)
	completed := task.TaskStatus(task.StatusCompleted)
	updated, err := tm.Update(args.TaskID, task.UpdateOptions{
		Status:         &completed,
		ExpectedStatus: &expected,
	})
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	t.Manager.EmitBoardUpdated(args.TeamID)
	return Result{Content: fmt.Sprintf("Task %s completed.\n", updated.ID)}, nil
}
