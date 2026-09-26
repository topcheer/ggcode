package tool

// compact_context: agent-invocable context management (CAT).
//
// Research basis: "Context as a Tool" (CAT, ACL 2026 Findings) and the
// SWE-Compressor results show that letting the policy MODEL decide WHEN to
// compress - by exposing context management as a callable action instead of a
// purely threshold-triggered background heuristic - improves task completion
// under bounded context budgets. Threshold-triggered compaction (ggcode's
// precompact) reacts late, right before the context cliff; an agent that
// knows it just crossed a phase boundary (long investigation done, refactor
// landed) can reclaim budget at a semantically clean moment, keeping recent
// detail usable for longer.
//
// Design:
//   - The tool never rewrites history synchronously mid-turn. It delegates
//     to Agent.RequestAutoCompact, which (a) runs the cheap mechanical
//     reclaim pass (superseded reads) immediately and (b) optionally
//     schedules full LLM summarization through the existing background
//     precompact machinery, applied at the next turn boundary
//     (protocol-safe: tool_use/tool_result pairing is never broken).
//   - Blocked for sub-agents (subAgentBlockedTools): the requester is bound
//     to the parent agent's context; a one-shot sub-agent must not compact
//     its parent's conversation.
//   - Always allowed (permission.IsAlwaysAllowedTool): no filesystem or
//     external side effects beyond the session's own provider call for
//     summarization.

import (
	"context"
	"encoding/json"
	"strings"
)

// CompactContextTool implements the compact_context tool.
// Requester is bound by cmd wiring after agent construction (late-binding,
// same pattern as spawn_agent's ProviderGetter).
type CompactContextTool struct {
	// Requester schedules context reclamation for the owning agent and
	// returns the model-readable status line (tool-result body). An empty
	// return means the runtime could not schedule anything.
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
