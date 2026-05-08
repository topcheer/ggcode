package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/memory"
)

// SaveMemoryTool lets the agent save experiences to persistent memory.
type SaveMemoryTool struct {
	globalMem  *memory.AutoMemory
	projectMem *memory.AutoMemory
}

// NewSaveMemoryTool creates a save_memory tool with global and project memory.
func NewSaveMemoryTool(globalMem, projectMem *memory.AutoMemory) *SaveMemoryTool {
	return &SaveMemoryTool{globalMem: globalMem, projectMem: projectMem}
}

func (t *SaveMemoryTool) Name() string { return "save_memory" }
func (t *SaveMemoryTool) Description() string {
	return "Save a useful pattern or experience to persistent memory for future sessions." +
		" Default scope is 'project' — use this for project-specific knowledge (e.g. build commands, architecture decisions, debugging tips)." +
		" Only use scope='global' for truly universal, cross-project patterns (e.g. language-level best practices)." +
		" Be careful with global: it is loaded into EVERY project's system prompt, so prefer 'project' unless you are certain the knowledge applies universally."
}

func (t *SaveMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"key": {
					"type": "string",
					"description": "Short identifier for this memory (e.g. 'build-process', 'api-gotcha')"
				},
				"content": {
					"type": "string",
					"description": "The memory content to save"
				},
				"scope": {
					"type": "string",
					"description": "Where to store: 'project' for project-specific knowledge, 'global' for cross-project patterns. Default: 'project'.",
					"enum": ["project", "global"]
				}
			},
			"required": ["key", "content"]
		}`)
}

func (t *SaveMemoryTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Key     string `json:"key"`
		Content string `json:"content"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	// Default scope is project
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

	if err := target.SaveMemory(params.Key, params.Content); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save %s memory: %v", scopeLabel, err)}, nil
	}

	return Result{Content: fmt.Sprintf("%s memory saved: %s", scopeLabel, params.Key)}, nil
}
