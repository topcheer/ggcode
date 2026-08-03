package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/memory"
)

// DeleteMemoryTool lets the agent remove a specific memory entry that is
// outdated, incorrect, or no longer relevant. This complements save_memory to
// give the agent full lifecycle control over persistent memories.
type DeleteMemoryTool struct {
	globalMem  *memory.AutoMemory
	projectMem *memory.AutoMemory
	afterSave  func()
}

// NewDeleteMemoryTool creates a delete_memory tool with global and project memory.
func NewDeleteMemoryTool(globalMem, projectMem *memory.AutoMemory) *DeleteMemoryTool {
	return &DeleteMemoryTool{globalMem: globalMem, projectMem: projectMem}
}

// SetAfterSave configures a callback that runs after memory is deleted.
// Callers can use this to refresh any in-memory prompt state that includes
// auto memory from disk.
func (t *DeleteMemoryTool) SetAfterSave(fn func()) {
	t.afterSave = fn
}

func (t *DeleteMemoryTool) Name() string { return "delete_memory" }
func (t *DeleteMemoryTool) Description() string {
	return "Delete a specific memory entry by key. Use this to remove outdated, incorrect, or no-longer-relevant memories. Does NOT delete project memory files (GGCODE.md, AGENTS.md, etc.) - only auto-saved memories from save_memory."
}

func (t *DeleteMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"key": {
				"type": "string",
				"description": "The memory key to delete (same key used when saving)"
			},
			"scope": {
				"type": "string",
				"description": "Which memory scope to delete from: 'project' (default) or 'global'",
				"enum": ["project", "global"]
			}
		},
		"required": ["key"]
	}`)
}

func (t *DeleteMemoryTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Key   string `json:"key"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if params.Key == "" {
		return Result{IsError: true, Content: "key is required"}, nil
	}

	if params.Scope == "" {
		params.Scope = "project"
	}

	var target *memory.AutoMemory
	var scopeLabel string
	switch params.Scope {
	case "global":
		target = t.globalMem
		scopeLabel = "global"
	case "project":
		target = t.projectMem
		scopeLabel = "project"
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid scope %q: must be 'global' or 'project'", params.Scope)}, nil
	}

	if target == nil {
		return Result{IsError: true, Content: fmt.Sprintf("%s memory not available", scopeLabel)}, nil
	}

	if err := target.DeleteMemory(params.Key); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to delete %s memory %q: %v", scopeLabel, params.Key, err)}, nil
	}

	if t.afterSave != nil {
		t.afterSave()
	}

	return Result{Content: fmt.Sprintf("Deleted %s memory: %s", scopeLabel, params.Key)}, nil
}
