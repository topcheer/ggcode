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
	return "Create a task on a team's shared task board so work is visible, coordinated, and easy to hand off. " +
		"Set 'assignee' when there is a clear best owner for the task. " +
		"Only leave 'assignee' empty when the right owner is genuinely unclear — in that case any suitable idle teammate may claim it. " +
		"When assignee is set, the task is pushed directly to that teammate's inbox for immediate execution. " +
		"Before creating a new task, make sure the work is not already tracked on the board. " +
		"Use this for real handoffs, help requests, or distinct follow-up work — not for duplicate reminders. " +
		"Do NOT use send_message to follow up on a task with an assignee unless you have new material information — the task is already delivered automatically."
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
			"description": "Detailed requirements"
		},
		"assignee": {
			"type": "string",
			"description": "The teammate ID to assign this task to (e.g. tm-2). STRONGLY RECOMMENDED — always set this when you know who should do the task. Only leave empty when no specific teammate can be determined."
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

	// No specific assignee: notify idle runners so they can claim immediately
	// instead of waiting for the next poller tick.
	if args.Assignee == "" {
		t.Manager.NotifyIdleRunners(args.TeamID)
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
	sb.WriteString("\nComplete this task now.")
	sb.WriteString("\nIf this task reached you by direct assignment, start it directly and do not re-claim it from the board first.")
	sb.WriteString("\nBefore creating any new follow-up task, check whether related work is already tracked so you avoid duplicate effort.")
	sb.WriteString("\nIf you need help or discover specialized follow-up work, send one targeted request or create one clear handoff task with enough context.")
	sb.WriteString("\nUse swarm_task_complete when done.")
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
		"team_id": {
			"type": "string",
			"description": "Team ID"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
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
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
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
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
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

	completed := task.TaskStatus(task.StatusCompleted)
	updated, err := tm.Update(args.TaskID, task.UpdateOptions{
		Status: &completed,
	})
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Task %s completed.\n", updated.ID)}, nil
}
