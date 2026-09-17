package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Anthropic context management (context editing).
//
// Server-side strategies that rewrite the conversation before it reaches the
// model: older tool results and thinking blocks are cleared once Claude has
// processed them, keeping long agent sessions inside the window and cutting
// input-token spend without any client-side compaction.
//
// Requires the beta flag context-management-2025-06-27 (set on the transport
// header by SetContextEditing) and the top-level context_management request
// field (attached per call by contextEditingOptions via option.WithJSONSet —
// the SDK's typed MessageNewParams predates the parameter).
//
// Docs: https://platform.claude.com/docs/en/build-with-claude/context-editing

const (
	// anthropicContextManagementBeta is the beta header token enabling both
	// the request field and the response's context_management block.
	anthropicContextManagementBeta = "context-management-2025-06-27"
	// Server-side edit strategy identifiers (documented API values).
	contextEditStrategyToolUses = "clear_tool_uses_20250919"
	contextEditStrategyThinking = "clear_thinking_20251015"
)

// ContextEditingConfig opts an Anthropic endpoint into server-side context
// management. Mode selects which strategies are attached:
//
//	"" / "off"          — feature disabled (default; ParseContextEditing → nil)
//	"tool_results"      — clear_tool_uses_20250919 only
//	"thinking"          — clear_thinking_20251015 only
//	"all"               — both strategies
//
// The remaining fields tune thresholds; zero values keep the documented API
// defaults (trigger 100k input tokens, keep last 3 tool uses, etc.).
type ContextEditingConfig struct {
	Mode               string
	TriggerTokens      int64    // input_tokens threshold before edits fire
	KeepToolUses       int64    // most recent tool uses to preserve
	ClearAtLeastTokens int64    // minimum tokens a pass must clear
	ExcludeTools       []string // tool names whose results are never cleared
	ClearToolInputs    bool     // also clear tool inputs (default false)
	KeepThinkingTurns  int64    // thinking turns preserved by clear_thinking
}

// ParseContextEditing converts the endpoint-level context_editing config
// value into a provider config. Unrecognized values return nil (feature off)
// so a typo can never change request semantics silently.
func ParseContextEditing(v string) *ContextEditingConfig {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "off":
		return nil
	case "tool_results":
		return &ContextEditingConfig{Mode: "tool_results"}
	case "thinking":
		return &ContextEditingConfig{Mode: "thinking"}
	case "all":
		return &ContextEditingConfig{Mode: "all"}
	default:
		return nil
	}
}

// contextManagementPayload builds the JSON value for the context_management
// request field. Returns nil when the feature is off or the config carries
// no usable strategy, in which case the field must NOT be attached.
func contextManagementPayload(cfg *ContextEditingConfig) map[string]any {
	if cfg == nil {
		return nil
	}
	var edits []map[string]any
	if cfg.Mode == "tool_results" || cfg.Mode == "all" {
		e := map[string]any{"type": contextEditStrategyToolUses}
		if cfg.TriggerTokens > 0 {
			e["trigger"] = map[string]any{"type": "input_tokens", "value": cfg.TriggerTokens}
		}
		if cfg.KeepToolUses > 0 {
			e["keep"] = map[string]any{"type": "tool_uses", "value": cfg.KeepToolUses}
		}
		if cfg.ClearAtLeastTokens > 0 {
			e["clear_at_least"] = map[string]any{"type": "input_tokens", "value": cfg.ClearAtLeastTokens}
		}
		if cfg.ClearToolInputs {
			e["clear_tool_inputs"] = true
		}
		if len(cfg.ExcludeTools) > 0 {
			e["exclude_tools"] = append([]string(nil), cfg.ExcludeTools...)
		}
		edits = append(edits, e)
	}
	if cfg.Mode == "thinking" || cfg.Mode == "all" {
		e := map[string]any{"type": contextEditStrategyThinking}
		if cfg.KeepThinkingTurns > 0 {
			e["keep"] = map[string]any{"type": "thinking_turns", "value": cfg.KeepThinkingTurns}
		}
		edits = append(edits, e)
	}
	if len(edits) == 0 {
		return nil
	}
	return map[string]any{"edits": edits}
}

// contextAppliedEdit mirrors one entry of the response-side
// context_management.applied_edits array.
type contextAppliedEdit struct {
	Type                 string `json:"type"`
	ClearedToolUses      int64  `json:"cleared_tool_uses"`
	ClearedThinkingTurns int64  `json:"cleared_thinking_turns"`
	ClearedInputTokens   int64  `json:"cleared_input_tokens"`
}

// parseAppliedEdits extracts a human-readable summary of the server-side
// edits from the raw JSON of a Message (non-streaming) or a
// message_delta stream event (streaming carries the same block in
// context_management). The second return is false when nothing was edited
// or the payload does not parse — a missing block is the normal no-edit
// case, not an error.
func parseAppliedEdits(rawJSON []byte) (string, bool) {
	var probe struct {
		ContextManagement *struct {
			AppliedEdits []contextAppliedEdit `json:"applied_edits"`
		} `json:"context_management"`
	}
	if err := json.Unmarshal(rawJSON, &probe); err != nil {
		return "", false
	}
	if probe.ContextManagement == nil || len(probe.ContextManagement.AppliedEdits) == 0 {
		return "", false
	}
	var toolUses, thinkingTurns, inputTokens int64
	for _, e := range probe.ContextManagement.AppliedEdits {
		inputTokens += e.ClearedInputTokens
		switch e.Type {
		case contextEditStrategyToolUses:
			toolUses += e.ClearedToolUses
		case contextEditStrategyThinking:
			thinkingTurns += e.ClearedThinkingTurns
		}
	}
	var parts []string
	if toolUses > 0 {
		parts = append(parts, fmt.Sprintf("%d tool result(s)", toolUses))
	}
	if thinkingTurns > 0 {
		parts = append(parts, fmt.Sprintf("%d thinking turn(s)", thinkingTurns))
	}
	if len(parts) == 0 {
		return "", false
	}
	summary := "context editing cleared " + strings.Join(parts, ", ")
	if inputTokens > 0 {
		summary += fmt.Sprintf(" (~%d input tokens)", inputTokens)
	}
	return summary, true
}
