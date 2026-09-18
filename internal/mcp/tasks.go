package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// MCP Tasks (SEP-1686, protocol revision 2025-11-25 / ext-tasks draft):
// asynchronous task execution. A compliant server may answer a task-augmented
// tools/call (params.task request option) not with the tool result but with a
// task descriptor: a flat result carrying resultType "task" plus the Task
// fields (taskId, status, ...). The client then polls tasks/get until the
// task reaches a terminal state and fetches the actual tool result with
// tasks/result.
//
// Task state machine (5 states):
//
//	working -> completed | failed | cancelled
//	working -> input_required -> (client resolves) -> working
//	terminal states are final.
//
// Notes:
//   - The task descriptor envelope is flat: CreateTaskResult = Result & Task &
//     {resultType: "task"} — no nested "task" object.
//   - statusMessage is opaque server data; log lengths, not contents.
//   - All polling requests inherit the per-request mcpRequestTimeout budget;
//     the overall wait is additionally bounded by taskPollBudget so a hostile
//     or stuck server cannot keep a call alive forever.

// Task lifecycle states (SEP-1686).
const (
	TaskStatusWorking       = "working"
	TaskStatusInputRequired = "input_required"
	TaskStatusCompleted     = "completed"
	TaskStatusFailed        = "failed"
	TaskStatusCancelled     = "cancelled"
)

// ResultTypeTask is the resultType discriminator marking a result that is a
// task descriptor instead of the request's logical result.
const ResultTypeTask = "task"

// taskPollBudget bounds the TOTAL wall-clock time spent polling one task,
// regardless of server-provided pollInterval hints. Generous for legitimate
// long-running tools; still finite against abuse.
const taskPollBudget = 10 * time.Minute

// Default bounds for the poll sleep interval. The server may hint a
// pollInterval (milliseconds); we clamp it into [200ms, 10s] so a hostile
// server can neither hot-loop us nor stall us past the budget granularity.
const (
	taskPollMin = 200 * time.Millisecond
	taskPollMax = 10 * time.Second
	// used when the server provides no pollInterval hint
	taskPollDefault = 1 * time.Second
)

// Task is the SEP-1686 task descriptor object.
type Task struct {
	TaskID        string `json:"taskId"`
	Status        string `json:"status"`
	StatusMessage string `json:"statusMessage,omitempty"`
	// PollInterval is the server's hint, in milliseconds, for how long the
	// client should wait between tasks/get polls (0 = no hint).
	PollInterval int64 `json:"pollInterval,omitempty"`
	// TTL is the server-side retention window for the task's result, in
	// milliseconds from completion, after which tasks/result may 404.
	TTL int64 `json:"ttl,omitempty"`
	// CreatedAt / LastUpdatedAt are RFC3339 timestamps.
	CreatedAt     string `json:"createdAt,omitempty"`
	LastUpdatedAt string `json:"lastUpdatedAt,omitempty"`
}

// CreateTaskResult is the flat result returned by a task-augmented
// tools/call: the standard Result members (none required here) merged with
// the Task fields and the resultType discriminator.
type CreateTaskResult struct {
	Task
	ResultType string `json:"resultType"`
}

// ListTasksResult is the tasks/list result.
type ListTasksResult struct {
	Tasks      []Task `json:"tasks"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// TasksCapability is the server's initialize capability object for tasks
// (SEP-1686). Presence of the key is what gates client features; the inner
// shape (list/cancel/requests) is tolerated but not interpreted.
type TasksCapability struct {
	List   *struct{} `json:"list,omitempty"`
	Cancel *struct{} `json:"cancel,omitempty"`
}

// TaskRequestOptions is the params.task request option on tools/call
// (SEP-1686): `true` or an object carrying per-task options such as ttl.
// Encoded as json.RawMessage so both shapes pass through untouched.
type TaskRequestOptions = json.RawMessage

// isTaskEnvelope reports whether a raw result carries the task discriminator.
// The discriminator is required by the flat envelope shape; a bare Task
// without resultType is NOT treated as a task (defense against servers that
// echo taskId on regular results).
func isTaskEnvelope(raw json.RawMessage) bool {
	var probe struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.ResultType == ResultTypeTask
}

// pollInterval returns the sleep duration derived from a Task's hint.
func (t Task) pollInterval() time.Duration {
	d := taskPollDefault
	if t.PollInterval > 0 {
		d = time.Duration(t.PollInterval) * time.Millisecond
	}
	if d < taskPollMin {
		d = taskPollMin
	}
	if d > taskPollMax {
		d = taskPollMax
	}
	return d
}

// CallToolAsTask runs a tool call with the SEP-1686 task request option:
// sends tools/call with params.task set, and when the server answers with a
// task descriptor, polls tasks/get until a terminal state, then fetches the
// final tool result via tasks/result. task is the terminal descriptor (may
// be nil when the server answered synchronously with a plain result).
func (c *Client) CallToolAsTask(ctx context.Context, name string, args map[string]interface{}, opts TaskRequestOptions) (*CallToolResult, *Task, error) {
	if c.closed.Load() {
		return nil, nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	params := CallToolParams{
		Name:      name,
		Arguments: args,
		Task:      opts,
	}
	var raw json.RawMessage
	if err := c.callWithMRTR(ctx, "tools/call", &params, &raw); err != nil {
		return nil, nil, err
	}
	if !isTaskEnvelope(raw) {
		// Server executed synchronously despite the task option — a plain
		// CallToolResult is a legal answer. Treat it as the final result.
		var out CallToolResult
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, nil, fmt.Errorf("mcp[%s]: tools/call returned malformed result: %w", c.name, err)
		}
		return &out, nil, nil
	}
	var created CreateTaskResult
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, nil, fmt.Errorf("mcp[%s]: tools/call task envelope malformed: %w", c.name, err)
	}
	if created.TaskID == "" {
		return nil, nil, fmt.Errorf("mcp[%s]: tools/call returned task envelope without taskId", c.name)
	}
	task, err := c.awaitTaskLoop(ctx, created.Task, created.TaskID, c.GetTask)
	if err != nil {
		return nil, &task, err
	}
	switch task.Status {
	case TaskStatusCompleted:
		result, err := c.TaskResult(ctx, task.TaskID)
		if err != nil {
			return nil, &task, err
		}
		return result, &task, nil
	case TaskStatusFailed:
		return nil, &task, fmt.Errorf("mcp[%s]: task %s failed: %s", c.name, task.TaskID, task.StatusMessage)
	case TaskStatusCancelled:
		return nil, &task, fmt.Errorf("mcp[%s]: task %s was cancelled", c.name, task.TaskID)
	default:
		return nil, &task, fmt.Errorf("mcp[%s]: task %s ended in unexpected status %q", c.name, task.TaskID, task.Status)
	}
}

// awaitTaskLoop is the terminal-state poll state machine with an injected
// task fetcher (mrtrLoop-style send func) so tests can drive status
// transitions without a transport. seed supplies the initial status and poll
// hint; taskID identifies the polled task.
func (c *Client) awaitTaskLoop(ctx context.Context, seed Task, taskID string, getTask func(context.Context, string) (Task, error)) (Task, error) {
	deadline := time.Now().Add(taskPollBudget)
	task := seed
	task.TaskID = taskID
	for {
		switch task.Status {
		case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled:
			return task, nil
		case TaskStatusInputRequired:
			// SEP-1686 lets tasks pause for client input. Resolving task-level
			// input requests requires an interactive flow the caller must
			// drive; auto-polling through it would spin forever.
			return task, fmt.Errorf("mcp[%s]: task %s is input_required: %s", c.name, task.TaskID, task.StatusMessage)
		case TaskStatusWorking, "":
			// continue polling
		default:
			return task, fmt.Errorf("mcp[%s]: task %s has unknown status %q (treated as invalid protocol response)", c.name, task.TaskID, task.Status)
		}
		if time.Now().After(deadline) {
			return task, fmt.Errorf("mcp[%s]: task %s exceeded %s poll budget (task poll guard)", c.name, task.TaskID, taskPollBudget)
		}
		if err := ctx.Err(); err != nil {
			return task, fmt.Errorf("mcp[%s]: task %s polling cancelled: %w", c.name, task.TaskID, err)
		}
		select {
		case <-ctx.Done():
			return task, fmt.Errorf("mcp[%s]: task %s polling cancelled: %w", c.name, task.TaskID, ctx.Err())
		case <-time.After(c.taskPollDelay(task)):
		}
		next, err := getTask(ctx, task.TaskID)
		if err != nil {
			return task, fmt.Errorf("mcp[%s]: task %s poll: %w", c.name, task.TaskID, err)
		}
		debug.Log("mcp-client", "server=%s task=%s status=%s (poll)", c.name, next.TaskID, next.Status)
		task = next
	}
}

// taskPollDelay picks the sleep interval, honoring the server hint clamped
// into sane bounds, or the test override when set.
func (c *Client) taskPollDelay(task Task) time.Duration {
	if c.taskPollOverride > 0 {
		return c.taskPollOverride
	}
	return task.pollInterval()
}

// GetTask fetches the current task descriptor via tasks/get.
func (c *Client) GetTask(ctx context.Context, taskID string) (Task, error) {
	var result Task
	params := map[string]string{"taskId": taskID}
	if err := c.sendRequest(ctx, "tasks/get", params, &result); err != nil {
		return Task{}, fmt.Errorf("mcp[%s]: tasks/get: %w", c.name, err)
	}
	if result.TaskID == "" {
		result.TaskID = taskID
	}
	return result, nil
}

// TaskResult fetches the final result of a completed task via tasks/result.
// The result of a task-augmented tools/call is a CallToolResult.
func (c *Client) TaskResult(ctx context.Context, taskID string) (*CallToolResult, error) {
	var result CallToolResult
	params := map[string]string{"taskId": taskID}
	if err := c.sendRequest(ctx, "tasks/result", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: tasks/result: %w", c.name, err)
	}
	return &result, nil
}

// CancelTask requests cancellation via tasks/cancel and returns the updated
// descriptor. Cancellation is best-effort per spec: the returned status may
// still be working briefly.
func (c *Client) CancelTask(ctx context.Context, taskID string) (Task, error) {
	var result Task
	params := map[string]string{"taskId": taskID}
	if err := c.sendRequest(ctx, "tasks/cancel", params, &result); err != nil {
		return Task{}, fmt.Errorf("mcp[%s]: tasks/cancel: %w", c.name, err)
	}
	if result.TaskID == "" {
		result.TaskID = taskID
	}
	return result, nil
}

// ListTasks returns the server's known tasks via tasks/list, following
// nextCursor pagination under the same maxPaginationPages guard as the
// other List* methods (#562 Bug A semantics).
func (c *Client) ListTasks(ctx context.Context) ([]Task, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	_, caps := c.negotiatedState()
	if caps.Tasks == nil {
		debug.Log("mcp-client", "server=%s tasks capability not advertised; returning empty task list", c.name)
		return []Task{}, nil
	}
	var all []Task
	cursor := ""
	for page := 0; ; page++ {
		if page >= maxPaginationPages {
			return all, fmt.Errorf("mcp[%s]: tasks/list exceeded %d pagination pages", c.name, maxPaginationPages)
		}
		params := ListToolsParams{Cursor: cursor}
		var result ListTasksResult
		if err := c.sendRequest(ctx, "tasks/list", params, &result); err != nil {
			return all, fmt.Errorf("mcp[%s]: tasks/list: %w", c.name, err)
		}
		all = append(all, result.Tasks...)
		if result.NextCursor == "" {
			return all, nil
		}
		cursor = result.NextCursor
	}
}

// HasTasks reports whether the server advertised the tasks capability
// (SEP-1686), i.e. whether task-augmented tool calls and task management
// methods are available.
func (c *Client) HasTasks() bool {
	_, caps := c.negotiatedState()
	return caps.Tasks != nil
}
