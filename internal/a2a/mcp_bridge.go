package a2a

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/tool"
)

// MCPBridgeTools returns MCP-compatible tool definitions that wrap A2A client operations.
// These tools can be registered in the tool registry so any MCP client can use them.
func MCPBridgeTools(client *Client) []tool.Tool {
	return []tool.Tool{
		&a2aDiscoverTool{client: client},
		&a2aSendTaskTool{client: client},
		&a2aGetTaskTool{client: client},
		&a2aCancelTaskTool{client: client},
	}
}

// ---------------------------------------------------------------------------
// a2a_discover tool
// ---------------------------------------------------------------------------

type a2aDiscoverTool struct {
	client *Client
}

func (t *a2aDiscoverTool) Name() string { return "a2a_discover" }

func (t *a2aDiscoverTool) Description() string {
	return "Discover a remote ggcode agent's capabilities. Fetches the Agent Card to see what skills are available. Use this before sending tasks."
}

func (t *a2aDiscoverTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

func (t *a2aDiscoverTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	card, err := t.client.Discover(ctx)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("Discovery failed: %v", err), IsError: true}, nil
	}

	var skills []string
	for _, s := range card.Skills {
		skills = append(skills, fmt.Sprintf("  - %s (%s): %s", s.ID, s.Name, s.Description))
	}

	meta := ""
	if card.Metadata != nil {
		metaJSON, _ := json.MarshalIndent(card.Metadata, "  ", "  ")
		meta = fmt.Sprintf("\nMetadata:\n  %s", string(metaJSON))
	}

	output := fmt.Sprintf("Agent: %s\nDescription: %s\nURL: %s\nStreaming: %v\nSkills:\n%s%s",
		card.Name, card.Description, card.URL,
		card.Capabilities.Streaming,
		fmt.Sprintf("%v", skills), meta)

	return tool.Result{Content: output}, nil
}

// ---------------------------------------------------------------------------
// a2a_send_task tool
// ---------------------------------------------------------------------------

type a2aSendTaskTool struct {
	client *Client
}

func (t *a2aSendTaskTool) Name() string { return "a2a_send_task" }

func (t *a2aSendTaskTool) Description() string {
	return "Send a task to a remote ggcode agent. The task runs asynchronously; use a2a_get_task to check status. Skills: code-edit, file-search, command-exec, git-ops, code-review, full-task."
}

func (t *a2aSendTaskTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"skill": {
				"type": "string",
				"description": "The skill to invoke: code-edit, file-search, command-exec, git-ops, code-review, or full-task",
				"enum": ["code-edit", "file-search", "command-exec", "git-ops", "code-review", "full-task"]
			},
			"message": {
				"type": "string",
				"description": "The task description or instruction to send"
			},
			"task_id": {
				"type": "string",
				"description": "Optional existing task ID to continue a multi-turn conversation (input-required flow)"
			}
		},
		"required": ["skill", "message"]
	}`)
}

type sendTaskInput struct {
	Skill   string `json:"skill"`
	Message string `json:"message"`
	TaskID  string `json:"task_id,omitempty"`
}

func (t *a2aSendTaskTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var params sendTaskInput
	if err := json.Unmarshal(input, &params); err != nil {
		return tool.Result{Content: fmt.Sprintf("Invalid input: %v", err), IsError: true}, nil
	}

	var task *Task
	var err error
	if params.TaskID != "" {
		task, err = t.client.SendMessage(ctx, params.Skill, params.Message, params.TaskID)
	} else {
		task, err = t.client.SendMessage(ctx, params.Skill, params.Message)
	}
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("Task failed: %v", err), IsError: true}, nil
	}

	result := formatTaskResult(task)
	return tool.Result{Content: result}, nil
}

// ---------------------------------------------------------------------------
// a2a_get_task tool
// ---------------------------------------------------------------------------

type a2aGetTaskTool struct {
	client *Client
}

func (t *a2aGetTaskTool) Name() string { return "a2a_get_task" }

func (t *a2aGetTaskTool) Description() string {
	return "Get the current status and results of a previously submitted A2A task."
}

func (t *a2aGetTaskTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task_id": {
				"type": "string",
				"description": "The ID of the task to check"
			}
		},
		"required": ["task_id"]
	}`)
}

type getTaskInput struct {
	TaskID string `json:"task_id"`
}

func (t *a2aGetTaskTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var params getTaskInput
	if err := json.Unmarshal(input, &params); err != nil {
		return tool.Result{Content: fmt.Sprintf("Invalid input: %v", err), IsError: true}, nil
	}

	task, err := t.client.GetTask(ctx, params.TaskID)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("Get task failed: %v", err), IsError: true}, nil
	}

	return tool.Result{Content: formatTaskResult(task)}, nil
}

// ---------------------------------------------------------------------------
// a2a_cancel_task tool
// ---------------------------------------------------------------------------

type a2aCancelTaskTool struct {
	client *Client
}

func (t *a2aCancelTaskTool) Name() string { return "a2a_cancel_task" }

func (t *a2aCancelTaskTool) Description() string {
	return "Cancel a running A2A task on a remote ggcode agent."
}

func (t *a2aCancelTaskTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task_id": {
				"type": "string",
				"description": "The ID of the task to cancel"
			}
		},
		"required": ["task_id"]
	}`)
}

func (t *a2aCancelTaskTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var params getTaskInput
	if err := json.Unmarshal(input, &params); err != nil {
		return tool.Result{Content: fmt.Sprintf("Invalid input: %v", err), IsError: true}, nil
	}

	task, err := t.client.CancelTask(ctx, params.TaskID)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("Cancel failed: %v", err), IsError: true}, nil
	}

	return tool.Result{Content: fmt.Sprintf("Task %s canceled (state: %s)", task.ID, task.Status.State)}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func formatTaskResult(t *Task) string {
	result := fmt.Sprintf("Task %s\nStatus: %s\nSkill: %s", t.ID, t.Status.State, t.Skill)

	if len(t.Artifacts) > 0 {
		result += "\n\nResults:"
		for _, a := range t.Artifacts {
			for _, p := range a.Parts {
				if p.Kind == "text" && p.Text != "" {
					result += "\n" + p.Text
				}
			}
		}
	}

	return result
}
