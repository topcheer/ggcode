package tool

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

type RecallCompactedTool struct {
	Searcher func(query string, limit int) string
}

func (t *RecallCompactedTool) Name() string { return "recall_compacted" }

func (t *RecallCompactedTool) Description() string {
	return "Search the archive of conversation content that was removed by context compaction (summarized-away turns and superseded tool results). Use when you remember that an earlier command output, error message, or file path existed but it is no longer in the visible conversation - this retrieves the verbatim original text. Read-only and cheap; prefer it over re-running work from scratch."
}

func (t *RecallCompactedTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"query": {
			"type": "string",
			"description": "Case-insensitive substring to look for in the archived text, e.g. a fragment of an error message, a file path, or a command name"
		},
		"limit": {
			"type": "integer",
			"description": "Max hits to return (default 5)"
		}
	},
	"required": ["query"]
}`)
}

func (t *RecallCompactedTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Searcher == nil {
		return Result{Content: "recall_compacted unavailable in this runtime (no archive searcher bound).", IsError: true}, nil
	}
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return Result{Content: "recall_compacted: invalid arguments: " + err.Error(), IsError: true}, nil
		}
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return Result{Content: "recall_compacted: 'query' is required.", IsError: true}, nil
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}
	out := t.Searcher(query, limit)
	if strings.TrimSpace(out) == "" {
		return Result{Content: "No archived compacted content matched " + strconv.Quote(query) + ".", IsError: false}, nil
	}
	return Result{Content: out}, nil
}
