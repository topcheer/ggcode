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

// ————————————————————————————————————————
// SwarmTaskCreate
// ————————————————————————————————————————

type SwarmTaskCreateTool struct {
	Manager *swarm.Manager
}

func (t SwarmTaskCreateTool) Name() string { return "swarm_task_create" }
func (t SwarmTaskCreateTool) Description() string {
	return "Create a task on a team's shared task board. Teammates can claim and complete tasks."
}
func (t SwarmTaskCreateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_id": {"type": "string", "description": "Team ID"},
			"subject": {"type": "string", "description": "Brief actionable title"},
			"description": {"type": "string", "description": "Detailed requirements"},
			"assignee": {"type": "string", "description": "Optional teammate ID to assign to"}
		},
		"required": ["team_id", "subject"]
	}`)
}
func (t SwarmTaskCreateTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "swarm_task_create: swarm manager not available"}, nil
	}
	var args struct {
		TeamID      string `json:"team_id"`
		Subject     string `json:"subject"`
		Description string `json:"description"`
		Assignee    string `json:"assignee"`
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
		metadata["assignee"] = args.Assignee
	}

	created := tm.Create(args.Subject, args.Description, "", metadata)

	// If an assignee is specified, push the task directly into their inbox
	// for immediate execution (bypasses the polling delay).
	if args.Assignee != "" {
		prompt := formatTaskPrompt(created)
		err := t.Manager.SendToTeammate(args.TeamID, args.Assignee, swarm.MailMessage{
			From:    "leader",
			Content: prompt,
			Type:    "task",
		})
		if err != nil {
			// Assignee inbox full or not found — task stays on board,
			// poller will pick it up later. Log but don't fail.
			out, _ := json.Marshal(created)
			return Result{Content: string(out) + fmt.Sprintf("\nWarning: could not deliver to %q: %v (task stays on board for polling)\n", args.Assignee, err)}, nil
		}
	}

	out, _ := json.Marshal(created)
	return Result{Content: string(out) + "\n"}, nil
}

// formatTaskPrompt builds the agent prompt from a task for inbox delivery.
func formatTaskPrompt(tk task.Task) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task: %s\n", tk.Subject))
	if tk.Description != "" {
		sb.WriteString(fmt.Sprintf("Description: %s\n", tk.Description))
	}
	sb.WriteString("\nComplete this task now. Use swarm_task_complete when done.")
	return sb.String()
}

// ————————————————————————————————————————
// SwarmTaskList
// ————————————————————————————————————————

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
			"team_id": {"type": "string", "description": "Team ID"}
		},
		"required": ["team_id"]
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
		assignee := ""
		if tk.Metadata["assignee"] != "" {
			assignee = fmt.Sprintf(" → %s", tk.Metadata["assignee"])
		}
		fmt.Fprintf(&sb, "- %s [%s] %s%s\n", tk.ID, tk.Status, tk.Subject, assignee)
	}
	return Result{Content: sb.String()}, nil
}

// ————————————————————————————————————————
// SwarmTaskClaim
// ————————————————————————————————————————

type SwarmTaskClaimTool struct {
	Manager *swarm.Manager
}

func (t SwarmTaskClaimTool) Name() string { return "swarm_task_claim" }
func (t SwarmTaskClaimTool) Description() string {
	return "Claim (start working on) a task on the team's task board. Sets status to in_progress and assigns owner."
}
func (t SwarmTaskClaimTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_id": {"type": "string", "description": "Team ID"},
			"task_id": {"type": "string", "description": "Task ID to claim"},
			"owner": {"type": "string", "description": "Teammate ID claiming the task"}
		},
		"required": ["team_id", "task_id", "owner"]
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

	inProgress := task.TaskStatus(task.StatusInProgress)
	updated, err := tm.Update(args.TaskID, task.UpdateOptions{
		Status: &inProgress,
		Owner:  &args.Owner,
	})
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	out, _ := json.Marshal(updated)
	return Result{Content: string(out) + "\n"}, nil
}

// ————————————————————————————————————————
// SwarmTaskComplete
// ————————————————————————————————————————

type SwarmTaskCompleteTool struct {
	Manager *swarm.Manager
}

func (t SwarmTaskCompleteTool) Name() string { return "swarm_task_complete" }
func (t SwarmTaskCompleteTool) Description() string {
	return "Mark a task on the team's task board as completed."
}
func (t SwarmTaskCompleteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_id": {"type": "string", "description": "Team ID"},
			"task_id": {"type": "string", "description": "Task ID to complete"}
		},
		"required": ["team_id", "task_id"]
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

	completed := task.TaskStatus(task.StatusCompleted)
	updated, err := tm.Update(args.TaskID, task.UpdateOptions{
		Status: &completed,
	})
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Task %s completed.\n", updated.ID)}, nil
}
