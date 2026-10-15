package tool

// recall_compacted: agent-invocable re-retrieval of compacted context.
//
// Research basis: Context Engineering (Anthropic, 2025) — compaction is
// "necessary but not sufficient"; the complementary decision is what to
// RE-RETRIEVE. When an LLM summarization replaces earlier turns or a
// mechanical pass stubs out a superseded tool_result, verbatim details (a
// command's exact output, an error string, a file path) leave the live
// window. The archive in internal/context/compacted_archive.go keeps that
// text retrievable; this tool exposes it to the model.
//
// Design mirrors compact_context (CAT): a late-bound Searcher closure is
// attached by cmd wiring (agentruntime.RegisterRecallCompactedTool) after
// agent construction. Read-only, session-scoped, no filesystem or external
// side effects; always allowed (permission.IsAlwaysAllowedTool). Blocked
// for sub-agents is NOT needed — the archive lives on the sub-agent's own
// context manager and reading it is harmless.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// RecallCompactedTool implements the recall_compacted tool.
type RecallCompactedTool struct {
	// Searcher queries the owning agent's compacted archive and returns the
	// model-readable result body. Empty return means nothing was found or
	// the runtime has no archive.
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
