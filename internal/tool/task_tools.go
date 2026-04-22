package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/task"
)

// ————————————————————————————————————————
// TaskCreate
// ————————————————————————————————————————

type TaskCreateTool struct {
	Manager *task.Manager
}

func (t TaskCreateTool) Name() string { return "task_create" }
func (t TaskCreateTool) Description() string {
	return "Create a structured task to track work within the session. " +
		"Returns the task ID for use with task_update, task_get, etc."
}
func (t TaskCreateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"subject": {"type": "string", "description": "Brief actionable title for the task"},
			"description": {"type": "string", "description": "Detailed requirements and context"},
			"activeForm": {"type": "string", "description": "Present continuous form shown in spinner (e.g. 'Running tests')"},
			"metadata": {"type": "object", "additionalProperties": {"type": "string"}, "description": "Arbitrary key-value metadata"}
		},
		"required": ["subject"]
	}`)
}
func (t TaskCreateTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "task_create: task manager not available"}, nil
	}
	var args struct {
		Subject     string            `json:"subject"`
		Description string            `json:"description"`
		ActiveForm  string            `json:"activeForm"`
		Metadata    map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Subject) == "" {
		return Result{IsError: true, Content: "subject is required"}, nil
	}

	created := t.Manager.Create(args.Subject, args.Description, args.ActiveForm, args.Metadata)
	out, _ := json.Marshal(created)
	return Result{Content: string(out) + "\n"}, nil
}

// ————————————————————————————————————————
// TaskGet
// ————————————————————————————————————————

type TaskGetTool struct {
	Manager *task.Manager
}

func (t TaskGetTool) Name() string { return "task_get" }
func (t TaskGetTool) Description() string {
	return "Retrieve a task by ID with full details including description, status, blocks, and blockedBy."
}
func (t TaskGetTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"taskId": {"type": "string", "description": "Task ID returned by task_create"}
		},
		"required": ["taskId"]
	}`)
}
func (t TaskGetTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "task_get: task manager not available"}, nil
	}
	var args struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	tk, ok := t.Manager.Get(args.TaskID)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("task %q not found", args.TaskID)}, nil
	}
	out, _ := json.Marshal(tk)
	return Result{Content: string(out) + "\n"}, nil
}

// ————————————————————————————————————————
// TaskList
// ————————————————————————————————————————

type TaskListTool struct {
	Manager *task.Manager
}

func (t TaskListTool) Name() string { return "task_list" }
func (t TaskListTool) Description() string {
	return "List all tasks in the session with ID, subject, status, and blockedBy info."
}
func (t TaskListTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}
func (t TaskListTool) Execute(_ context.Context, _ json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "task_list: task manager not available"}, nil
	}
	tasks := t.Manager.List()
	if len(tasks) == 0 {
		return Result{Content: "No tasks.\n"}, nil
	}
	// Sort by ID
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })

	var sb strings.Builder
	for _, tk := range tasks {
		status := string(tk.Status)
		if tk.Status == task.StatusInProgress {
			status = "in_progress"
		}
		blockedBy := ""
		if len(tk.BlockedBy) > 0 {
			blockedBy = fmt.Sprintf(" (blocked by %s)", strings.Join(tk.BlockedBy, ", "))
		}
		fmt.Fprintf(&sb, "- %s [%s] %s%s\n", tk.ID, status, tk.Subject, blockedBy)
	}
	return Result{Content: sb.String()}, nil
}

// ————————————————————————————————————————
// TaskUpdate
// ————————————————————————————————————————

type TaskUpdateTool struct {
	Manager *task.Manager
}

func (t TaskUpdateTool) Name() string { return "task_update" }
func (t TaskUpdateTool) Description() string {
	return "Update a task's status, subject, description, owner, dependencies, or metadata. " +
		"Use status 'completed' to mark done. Use addBlocks/addBlockedBy to declare dependencies."
}
func (t TaskUpdateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"taskId": {"type": "string", "description": "Task ID to update"},
			"status": {"type": "string", "enum": ["pending", "in_progress", "completed"], "description": "New status"},
			"subject": {"type": "string", "description": "New subject"},
			"description": {"type": "string", "description": "New description"},
			"owner": {"type": "string", "description": "Agent ID owning this task"},
			"activeForm": {"type": "string", "description": "New spinner text"},
			"addBlocks": {"type": "array", "items": {"type": "string"}, "description": "Task IDs this task blocks"},
			"addBlockedBy": {"type": "array", "items": {"type": "string"}, "description": "Task IDs that block this task"},
			"metadata": {"type": "object", "additionalProperties": {"type": "string"}, "description": "Metadata to merge into the task"}
		},
		"required": ["taskId"]
	}`)
}
func (t TaskUpdateTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "task_update: task manager not available"}, nil
	}
	var args struct {
		TaskID       string            `json:"taskId"`
		Status       *string           `json:"status"`
		Subject      *string           `json:"subject"`
		Description  *string           `json:"description"`
		Owner        *string           `json:"owner"`
		ActiveForm   *string           `json:"activeForm"`
		AddBlocks    []string          `json:"addBlocks"`
		AddBlockedBy []string          `json:"addBlockedBy"`
		Metadata     map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	opts := task.UpdateOptions{
		Subject:      args.Subject,
		Description:  args.Description,
		Owner:        args.Owner,
		ActiveForm:   args.ActiveForm,
		AddBlocks:    args.AddBlocks,
		AddBlockedBy: args.AddBlockedBy,
		Metadata:     args.Metadata,
	}
	if args.Status != nil {
		s := task.TaskStatus(*args.Status)
		opts.Status = &s
	}

	updated, err := t.Manager.Update(args.TaskID, opts)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	out, _ := json.Marshal(updated)
	return Result{Content: string(out) + "\n"}, nil
}

// ————————————————————————————————————————
// TaskStop
// ————————————————————————————————————————

type TaskStopTool struct {
	Manager *task.Manager
}

func (t TaskStopTool) Name() string { return "task_stop" }
func (t TaskStopTool) Description() string {
	return "Stop a running task, resetting its status from in_progress to pending."
}
func (t TaskStopTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"taskId": {"type": "string", "description": "Task ID to stop"}
		},
		"required": ["taskId"]
	}`)
}
func (t TaskStopTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "task_stop: task manager not available"}, nil
	}
	var args struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if _, ok := t.Manager.Get(args.TaskID); !ok {
		return Result{IsError: true, Content: fmt.Sprintf("task %q not found", args.TaskID)}, nil
	}

	pending := task.TaskStatus(task.StatusPending)
	inProgress := task.TaskStatus(task.StatusInProgress)
	updated, err := t.Manager.Update(args.TaskID, task.UpdateOptions{
		Status:         &pending,
		ExpectedStatus: &inProgress,
	})
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Task %s stopped (reset to pending)\n", updated.ID)}, nil
}

// BackgroundTaskProvider retrieves output from background tasks (subagents, shell jobs).
type BackgroundTaskProvider interface {
	GetTaskOutput(taskID string) (string, bool)
}

// TaskOutputTool retrieves the output of a background task by ID.
type TaskOutputTool struct {
	Provider BackgroundTaskProvider
}

func (t TaskOutputTool) Name() string { return "task_output" }
func (t TaskOutputTool) Description() string {
	return "Get the output of a background task (subagent or shell command) by its task ID. " +
		"Returns the result if the task has completed, or partial output if still running."
}
func (t TaskOutputTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task_id": {"type": "string", "description": "ID of the background task"}
		},
		"required": ["task_id"]
	}`)
}
func (t TaskOutputTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.TaskID == "" {
		return Result{IsError: true, Content: "task_id is required"}, nil
	}

	if t.Provider == nil {
		return Result{IsError: true, Content: "no background task provider configured"}, nil
	}

	output, found := t.Provider.GetTaskOutput(args.TaskID)
	if !found {
		return Result{IsError: true, Content: fmt.Sprintf("task %q not found or has no output", args.TaskID)}, nil
	}

	return Result{Content: output}, nil
}
