package tool

import (
	"context"
	"encoding/json"
	"strings"
)

type CompactContextTool struct {
	Requester func(reason string) string
}

func (t *CompactContextTool) Name() string { return "compact_context" }

func (t *CompactContextTool) Description() string {
	return "Reclaim conversation context budget. Runs a cheap mechanical pass immediately (drops superseded file-read results) and, when usage is at/above the auto-compact threshold, schedules background LLM summarization that applies at the next turn boundary. Use deliberately at semantically clean boundaries: after finishing a subtask, after a large investigation or refactor has landed, or before starting a broad new exploration. Do NOT use when you still need earlier details verbatim - summarization is lossy. Costs one summarization LLM call when scheduled."
}

func (t *CompactContextTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"reason": {
			"type": "string",
			"description": "Brief reason for compacting now, e.g. 'investigation phase done, starting implementation'"
		}
	}
}`)
}

func (t *CompactContextTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Requester == nil {
		return Result{Content: "compact_context unavailable in this runtime (no compaction requester bound).", IsError: true}, nil
	}
	var reason string
	if len(input) > 0 {
		var args struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(input, &args); err == nil {
			reason = strings.TrimSpace(args.Reason)
		}
	}
	status := t.Requester(reason)
	if strings.TrimSpace(status) == "" {
		return Result{Content: "context compaction could not be scheduled.", IsError: true}, nil
	}
	return Result{Content: status}, nil
}
