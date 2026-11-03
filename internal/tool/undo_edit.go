package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type UndoEditTool struct{}

func (t UndoEditTool) Name() string { return "undo_edit" }

func (t UndoEditTool) Description() string {
	return "Undo or revert file edits made by edit_file/write_file/multi_file_edit in this session. " +
		"Actions: 'undo' reverts the most recent edit, 'list' shows recent checkpoints, 'revert' rolls back to a specific checkpoint by ID. " +
		"Use 'undo' when you realise a previous edit was wrong and want to start fresh rather than fix-forward."
}

func (t UndoEditTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["undo", "list", "revert"],
			"description": "undo: revert the most recent file edit. list: show recent checkpoints with IDs. revert: roll back to a specific checkpoint (requires checkpoint_id).",
			"default": "undo"
		},
		"checkpoint_id": {
			"type": "string",
			"description": "Checkpoint ID to revert to (only for action=revert). Get IDs from action=list."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI."
		}
	},
	"required": ["action", "description"]
}`)
}

func (t UndoEditTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {

	return Result{
		IsError: true,
		Content: "undo_edit must be executed through the agent runtime (checkpoint manager is not available in standalone mode).",
	}, nil
}

type CheckpointInfo struct {
	ID        string    `json:"id"`
	FilePath  string    `json:"file_path"`
	ToolCall  string    `json:"tool_call"`
	Timestamp time.Time `json:"timestamp"`
	IsNew     bool      `json:"is_new"`
}

func FormatCheckpointList(checkpoints []CheckpointInfo) string {
	if len(checkpoints) == 0 {
		return "No checkpoints available. File edits made in this session will appear here."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Recent file edits (%d, most recent first):\n\n", len(checkpoints)))

	maxShow := 15
	start := len(checkpoints)
	if start > maxShow {
		start = maxShow
	}
	for i := len(checkpoints) - 1; i >= len(checkpoints)-start; i-- {
		cp := checkpoints[i]
		verb := "edited"
		if cp.IsNew {
			verb = "created"
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s — %s (%s)\n", cp.ID, cp.FilePath, verb, cp.ToolCall))
	}

	if len(checkpoints) > maxShow {
		sb.WriteString(fmt.Sprintf("\n... and %d older checkpoints\n", len(checkpoints)-maxShow))
	}

	sb.WriteString("\nUse action=revert with a checkpoint ID to roll back to that point.")
	return sb.String()
}

func FormatUndoResult(filePath, toolCall string, isNew bool) string {
	verb := "Reverted edit to"
	if isNew {
		verb = "Removed newly-created file"
	}
	return fmt.Sprintf("%s %s (was modified by %s). The file has been restored to its previous state.",
		verb, filePath, toolCall)
}
