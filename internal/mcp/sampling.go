package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// SamplingContent represents a content block in a sampling request/response.
// MCP 2025-11-25 (SEP-1577) adds "tool_use" (assistant turns) and
// "tool_result" (user turns) blocks; "audio" was already a legal data type.
type SamplingContent struct {
	Type string `json:"type"` // "text", "image", "audio", "tool_use", "tool_result"
	Text string `json:"text,omitempty"`
	// Image/audio fields follow MCP spec for media content.
	Data     string `json:"data,omitempty"`     // base64-encoded media data
	MIMEType string `json:"mimeType,omitempty"` // MIME type for image/audio
	// tool_use fields (assistant message blocks).
	ID    string          `json:"id,omitempty"`    // tool call id (e.g. "call_abc123")
	Name  string          `json:"name,omitempty"`  // tool name
	Input json.RawMessage `json:"input,omitempty"` // tool arguments object
	// tool_result fields (user message blocks).
	ToolUseID     string            `json:"toolUseId,omitempty"` // matches tool_use id
	ResultContent []SamplingContent `json:"content,omitempty"`   // tool output blocks
	IsError       bool              `json:"isError,omitempty"`   // execution failure flag
}

// SamplingMessage is a single message in a sampling request.
// Content holds the single-block form; Blocks holds the array form
// (mandatory for tool_result turns and multi-block assistant turns).
// Marshal/Unmarshal transparently pick the right wire shape.
type SamplingMessage struct {
	Role    string            `json:"role"` // "user" or "assistant"
	Content SamplingContent   `json:"content"`
	Blocks  []SamplingContent `json:"-"`
}

// MarshalJSON emits the array form when Blocks is populated (tool sampling
// turns), otherwise the historical single-object form.
func (m SamplingMessage) MarshalJSON() ([]byte, error) {
	if len(m.Blocks) == 0 {
		type alias SamplingMessage
		return json.Marshal(alias(m))
	}
	return json.Marshal(struct {
		Role    string            `json:"role"`
		Content []SamplingContent `json:"content"`
	}{m.Role, m.Blocks})
}

// UnmarshalJSON accepts both content shapes: a single object (pre-2025-11-25
// servers) and an array (tool_use / tool_result turns). The shape is decided
// from the content FIELD, since the message root itself is always an object.
func (m *SamplingMessage) UnmarshalJSON(data []byte) error {
	var probe struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	m.Role = probe.Role
	if len(probe.Content) == 0 {
		return nil
	}
	if isArrayJSON(probe.Content) {
		if err := json.Unmarshal(probe.Content, &m.Blocks); err != nil {
			return err
		}
		if len(m.Blocks) > 0 {
			m.Content = m.Blocks[0] // single-block view stays populated for consumers
		}
		return nil
	}
	return json.Unmarshal(probe.Content, &m.Content)
}

// isArrayJSON reports whether the (possibly whitespace-padded) JSON value is
// an array.
func isArrayJSON(data []byte) bool {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
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

// SamplingTool is a tool definition offered to the client's LLM in a
// tool-enabled sampling request (MCP 2025-11-25, SEP-1577). The SERVER
// supplies definitions and executes the resulting tool calls in follow-up
// sampling turns; the client only runs its LLM and relays tool_use blocks.
type SamplingTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Annotations is an optional hint object (e.g. title, readOnlyHint)
	// passed through opaquely per the MCP tool annotation schema.
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

// Tool-choice modes for tool-enabled sampling (SEP-1577).
const (
	ToolChoiceAuto     = "auto"     // model decides (default)
	ToolChoiceRequired = "required" // model MUST use at least one tool
	ToolChoiceNone     = "none"     // model MUST NOT use tools
)

// SamplingToolChoice controls the tool use ability of the model.
type SamplingToolChoice struct {
	Mode string `json:"mode"` // "auto" | "none" | "required"
}

// SamplingParams is the parameters for a sampling/createMessage request.
type SamplingParams struct {
	Messages         []SamplingMessage   `json:"messages"`
	ModelPreferences ModelPreferences    `json:"modelPreferences,omitempty"`
	SystemPrompt     string              `json:"systemPrompt,omitempty"`
	IncludeContext   string              `json:"includeContext,omitempty"`
	MaxTokens        int                 `json:"maxTokens,omitempty"`
	Temperature      float64             `json:"temperature,omitempty"`
	StopSequences    []string            `json:"stopSequences,omitempty"`
	Tools            []SamplingTool      `json:"tools,omitempty"`      // SEP-1577
	ToolChoice       *SamplingToolChoice `json:"toolChoice,omitempty"` // SEP-1577
}

// SamplingResult is the result returned to the server after sampling.
// Content holds the single-block form; Blocks holds the array form used for
// tool_use responses (stopReason "toolUse", SEP-1577).
type SamplingResult struct {
	Model      string            `json:"model"`
	StopReason string            `json:"stopReason"` // "end_turn", "stop_sequence", "max_tokens", "toolUse"
	Role       string            `json:"role"`       // always "assistant"
	Content    SamplingContent   `json:"content"`
	Blocks     []SamplingContent `json:"-"`
}

// MarshalJSON emits the array form when Blocks is populated (tool_use
// responses), otherwise the historical single-object form.
func (r SamplingResult) MarshalJSON() ([]byte, error) {
	if len(r.Blocks) == 0 {
		type alias SamplingResult
		return json.Marshal(alias(r))
	}
	return json.Marshal(struct {
		Model      string            `json:"model"`
		StopReason string            `json:"stopReason"`
		Role       string            `json:"role"`
		Content    []SamplingContent `json:"content"`
	}{r.Model, r.StopReason, r.Role, r.Blocks})
}

// UnmarshalJSON accepts both content shapes (single object and array).
func (r *SamplingResult) UnmarshalJSON(data []byte) error {
	if isArrayJSON(data) {
		return nil // an array is never valid at the result root; caller surfaces the error
	}
	type alias SamplingResult
	return json.Unmarshal(data, (*alias)(r))
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

// ValidateSamplingParams enforces the structural rules for tool-enabled
// sampling (MCP 2025-11-25, SEP-1577 + error-handling guidance):
//
//   - toolChoice requires a non-empty tools array, and its mode must be
//     "auto", "none", or "required".
//   - every tool needs a name and an inputSchema.
//   - a user message containing tool_result blocks must contain ONLY tool
//     results (mixing with text/image/audio is invalid - providers map tool
//     results to dedicated roles).
//   - every assistant message with tool_use blocks must be immediately
//     followed by a user message of tool_result blocks whose toolUseIds
//     cover every tool_use id (spec: "Tool result missing in request").
//
// Violations surface as JSON-RPC -32602 (invalid params).
func ValidateSamplingParams(p SamplingParams) error {
	if p.ToolChoice != nil {
		if len(p.Tools) == 0 {
			return fmt.Errorf("toolChoice provided without tools")
		}
		switch p.ToolChoice.Mode {
		case ToolChoiceAuto, ToolChoiceRequired, ToolChoiceNone:
		case "":
			// Spec examples always carry a mode; treat empty as auto
			// (documented default) rather than a hard failure.
		default:
			return fmt.Errorf("toolChoice mode %q is not one of auto/none/required", p.ToolChoice.Mode)
		}
	}
	seen := make(map[string]bool, len(p.Tools))
	for _, t := range p.Tools {
		if t.Name == "" {
			return fmt.Errorf("tool definition missing name")
		}
		if len(t.InputSchema) == 0 {
			return fmt.Errorf("tool %q missing inputSchema", t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("duplicate tool name %q", t.Name)
		}
		seen[t.Name] = true
	}

	// pendingToolIDs holds tool_use ids from the most recent assistant
	// message awaiting their tool_result counterparts.
	var pendingToolIDs map[string]bool
	for _, msg := range p.Messages {
		blocks := msg.Blocks
		if len(blocks) == 0 {
			blocks = []SamplingContent{msg.Content}
		}
		toolResults := 0
		var resultsSeen map[string]bool
		for _, b := range blocks {
			switch b.Type {
			case "tool_result":
				toolResults++
				if b.ToolUseID == "" {
					return fmt.Errorf("tool_result missing toolUseId")
				}
				if resultsSeen == nil {
					resultsSeen = make(map[string]bool)
				}
				resultsSeen[b.ToolUseID] = true
			case "tool_use":
				if b.ID == "" || b.Name == "" {
					return fmt.Errorf("tool_use block missing id or name")
				}
			}
		}
		isToolResultMessage := toolResults > 0
		if isToolResultMessage && toolResults != len(blocks) {
			return fmt.Errorf("tool results mixed with other content in a user message")
		}
		if msg.Role == "user" && isToolResultMessage {
			// Spec: tool_result ids must resolve the previous assistant
			// turn's tool_use ids exactly - "Tool result missing in request".
			if len(pendingToolIDs) == 0 {
				return fmt.Errorf("tool results without a preceding assistant tool_use turn")
			}
			for id := range pendingToolIDs {
				if !resultsSeen[id] {
					return fmt.Errorf("tool result missing in request: toolUseId %q", id)
				}
			}
			pendingToolIDs = nil
		}
		if msg.Role == "assistant" {
			pendingToolIDs = nil
			for _, b := range blocks {
				if b.Type == "tool_use" {
					if pendingToolIDs == nil {
						pendingToolIDs = make(map[string]bool)
					}
					pendingToolIDs[b.ID] = true
				}
			}
		}
	}
	// An assistant tool_use turn with no following tool_result turn is only
	// legal on the FINAL sampling result - inside a request message history
	// it leaves the conversation unbalanced, so reject it.
	if len(pendingToolIDs) > 0 {
		ids := make([]string, 0, len(pendingToolIDs))
		for id := range pendingToolIDs {
			ids = append(ids, id)
		}
		return fmt.Errorf("tool result missing in request: unresolved toolUseIds %v", ids)
	}
	return nil
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
