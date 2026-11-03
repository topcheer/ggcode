package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	TaskStatusWorking       = "working"
	TaskStatusInputRequired = "input_required"
	TaskStatusCompleted     = "completed"
	TaskStatusFailed        = "failed"
	TaskStatusCancelled     = "cancelled"
)

const ResultTypeTask = "task"

const taskPollBudget = 10 * time.Minute

const (
	taskPollMin = 200 * time.Millisecond
	taskPollMax = 10 * time.Second

	taskPollDefault = 1 * time.Second
)

type Task struct {
	TaskID        string `json:"taskId"`
	Status        string `json:"status"`
	StatusMessage string `json:"statusMessage,omitempty"`

	PollInterval int64 `json:"pollInterval,omitempty"`

	TTL int64 `json:"ttl,omitempty"`

	CreatedAt     string `json:"createdAt,omitempty"`
	LastUpdatedAt string `json:"lastUpdatedAt,omitempty"`
}

func (t *Task) UnmarshalJSON(data []byte) error {
	type taskAlias Task
	var a taskAlias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	var ms struct {
		TTLMs          int64 `json:"ttlMs,omitempty"`
		PollIntervalMs int64 `json:"pollIntervalMs,omitempty"`
	}
	_ = json.Unmarshal(data, &ms)
	if a.TTL == 0 {
		a.TTL = ms.TTLMs
	}
	if a.PollInterval == 0 {
		a.PollInterval = ms.PollIntervalMs
	}
	*t = Task(a)
	return nil
}

type CreateTaskResult struct {
	Task
	ResultType string `json:"resultType"`
}

type ListTasksResult struct {
	Tasks      []Task `json:"tasks"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type TasksCapability struct {
	List   *struct{} `json:"list,omitempty"`
	Cancel *struct{} `json:"cancel,omitempty"`
}

type TaskRequestOptions = json.RawMessage

func isTaskEnvelope(raw json.RawMessage) bool {
	var probe struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.ResultType == ResultTypeTask
}

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

func (c *Client) awaitTaskLoop(ctx context.Context, seed Task, taskID string, getTask func(context.Context, string) (Task, error)) (Task, error) {
	deadline := time.Now().Add(taskPollBudget)
	task := seed
	task.TaskID = taskID
	for {
		switch task.Status {
		case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled:
			return task, nil
		case TaskStatusInputRequired:

			return task, fmt.Errorf("mcp[%s]: task %s is input_required: %s", c.name, task.TaskID, task.StatusMessage)
		case TaskStatusWorking, "":

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

func (c *Client) taskPollDelay(task Task) time.Duration {
	if c.taskPollOverride > 0 {
		return c.taskPollOverride
	}
	return task.pollInterval()
}

func taskLookupError(server, taskID, method string, err error) error {
	if je, ok := jsonRPCErrorOf(err); ok && je.Code == ErrCodeInvalidParams {
		return fmt.Errorf("mcp[%s]: %s: task %s expired or deleted (server TTL; re-issue the tool call if needed)", server, method, taskID)
	}
	return fmt.Errorf("mcp[%s]: %s: %w", server, method, err)
}

func (c *Client) GetTask(ctx context.Context, taskID string) (Task, error) {
	var result Task
	params := map[string]string{"taskId": taskID}
	if err := c.sendRequest(ctx, "tasks/get", params, &result); err != nil {
		return Task{}, taskLookupError(c.name, taskID, "tasks/get", err)
	}
	if result.TaskID == "" {
		result.TaskID = taskID
	}
	return result, nil
}

func (c *Client) TaskResult(ctx context.Context, taskID string) (*CallToolResult, error) {
	var result CallToolResult
	params := map[string]string{"taskId": taskID}
	if err := c.sendRequest(ctx, "tasks/result", params, &result); err != nil {
		return nil, taskLookupError(c.name, taskID, "tasks/result", err)
	}
	return &result, nil
}

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

func (c *Client) HasTasks() bool {
	_, caps := c.negotiatedState()
	return caps.Tasks != nil
}
