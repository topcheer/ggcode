package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	anthropicContextManagementBeta = "context-management-2025-06-27"
	contextEditStrategyToolUses    = "clear_tool_uses_20250919"
	contextEditStrategyThinking    = "clear_thinking_20251015"
)

type ContextEditingConfig struct {
	Mode               string
	TriggerTokens      int64
	KeepToolUses       int64
	ClearAtLeastTokens int64
	ExcludeTools       []string
	ClearToolInputs    bool
	KeepThinkingTurns  int64
}

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

type contextAppliedEdit struct {
	Type                 string `json:"type"`
	ClearedToolUses      int64  `json:"cleared_tool_uses"`
	ClearedThinkingTurns int64  `json:"cleared_thinking_turns"`
	ClearedInputTokens   int64  `json:"cleared_input_tokens"`
}

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
