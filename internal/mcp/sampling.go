package mcp

import (
	"context"
	"encoding/json"
)

// SamplingContent represents a content block in a sampling request/response.
type SamplingContent struct {
	Type string `json:"type"` // "text" or "image"
	Text string `json:"text,omitempty"`
	// Image fields follow MCP spec for image content.
	Data     string `json:"data,omitempty"`     // base64-encoded image data
	MIMEType string `json:"mimeType,omitempty"` // MIME type for image
}

// SamplingMessage is a single message in a sampling request.
type SamplingMessage struct {
	Role    string          `json:"role"` // "user" or "assistant"
	Content SamplingContent `json:"content"`
}

// ModelPreferences hints at which model the server prefers.
type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	CostPriority         float64     `json:"costPriority,omitempty"`
	SpeedPriority        float64     `json:"speedPriority,omitempty"`
	IntelligencePriority float64     `json:"intelligencePriority,omitempty"`
}

// ModelHint is a hint about which model to use.
type ModelHint struct {
	Name string `json:"name,omitempty"`
}

// SamplingParams is the parameters for a sampling/createMessage request.
type SamplingParams struct {
	Messages         []SamplingMessage `json:"messages"`
	ModelPreferences ModelPreferences  `json:"modelPreferences,omitempty"`
	SystemPrompt     string            `json:"systemPrompt,omitempty"`
	IncludeContext   string            `json:"includeContext,omitempty"`
	MaxTokens        int               `json:"maxTokens,omitempty"`
	Temperature      float64           `json:"temperature,omitempty"`
	StopSequences    []string          `json:"stopSequences,omitempty"`
}

// SamplingResult is the result returned to the server after sampling.
type SamplingResult struct {
	Model      string          `json:"model"`
	StopReason string          `json:"stopReason"` // "end_turn", "stop_sequence", "max_tokens"
	Role       string          `json:"role"`       // always "assistant"
	Content    SamplingContent `json:"content"`
}

// SamplingHandler processes a sampling request from an MCP server.
// The handler should generate a completion using the agent's LLM provider
// and return the result. If sampling is not permitted (e.g., permission
// mode restrictions), return an error.
type SamplingHandler func(ctx context.Context, params SamplingParams) (*SamplingResult, error)

// ParseSamplingParams extracts sampling parameters from a JSON-RPC request.
func ParseSamplingParams(raw json.RawMessage) (SamplingParams, error) {
	var p SamplingParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

// MaxSamplingTokens is the default max tokens cap for sampling responses.
// This prevents runaway generation from consuming excessive token budget.
const MaxSamplingTokens = 4096

// EffectiveMaxTokens returns the max tokens to use for a sampling request,
// clamped to a reasonable ceiling. If the request specifies 0, uses the cap.
func EffectiveMaxTokens(requested int) int {
	if requested <= 0 || requested > MaxSamplingTokens {
		return MaxSamplingTokens
	}
	return requested
}
