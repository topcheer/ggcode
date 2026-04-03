package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/memory"
)

// SaveMemoryTool lets the agent save experiences to auto memory.
type SaveMemoryTool struct {
	autoMem *memory.AutoMemory
}

// NewSaveMemoryTool creates a save_memory tool.
func NewSaveMemoryTool(am *memory.AutoMemory) *SaveMemoryTool {
	return &SaveMemoryTool{autoMem: am}
}

func (t *SaveMemoryTool) Name() string        { return "save_memory" }
func (t *SaveMemoryTool) Description() string  { return "Save a useful pattern or experience to persistent memory for future sessions." }

func (t *SaveMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"key": {
				"type": "string",
				"description": "Short identifier for this memory (e.g. 'go-error-pattern', 'api-gotcha')"
			},
			"content": {
				"type": "string",
				"description": "The memory content to save"
			}
		},
		"required": ["key", "content"]
	}`)
}

func (t *SaveMemoryTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Key     string `json:"key"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if err := t.autoMem.SaveMemory(params.Key, params.Content); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save memory: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Memory saved: %s", params.Key)}, nil
}
