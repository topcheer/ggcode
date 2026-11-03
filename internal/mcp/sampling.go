package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

type SamplingContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID     string            `json:"toolUseId,omitempty"`
	ResultContent []SamplingContent `json:"content,omitempty"`
	IsError       bool              `json:"isError,omitempty"`
}

type SamplingMessage struct {
	Role    string            `json:"role"`
	Content SamplingContent   `json:"content"`
	Blocks  []SamplingContent `json:"-"`
}

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
			m.Content = m.Blocks[0]
		}
		return nil
	}
	return json.Unmarshal(probe.Content, &m.Content)
}

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

type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	CostPriority         float64     `json:"costPriority,omitempty"`
	SpeedPriority        float64     `json:"speedPriority,omitempty"`
	IntelligencePriority float64     `json:"intelligencePriority,omitempty"`
}

type ModelHint struct {
	Name string `json:"name,omitempty"`
}

type SamplingTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`

	Annotations json.RawMessage `json:"annotations,omitempty"`
}

const (
	ToolChoiceAuto     = "auto"
	ToolChoiceRequired = "required"
	ToolChoiceNone     = "none"
)

type SamplingToolChoice struct {
	Mode string `json:"mode"`
}

type SamplingParams struct {
	Messages         []SamplingMessage   `json:"messages"`
	ModelPreferences ModelPreferences    `json:"modelPreferences,omitempty"`
	SystemPrompt     string              `json:"systemPrompt,omitempty"`
	IncludeContext   string              `json:"includeContext,omitempty"`
	MaxTokens        int                 `json:"maxTokens,omitempty"`
	Temperature      float64             `json:"temperature,omitempty"`
	StopSequences    []string            `json:"stopSequences,omitempty"`
	Tools            []SamplingTool      `json:"tools,omitempty"`
	ToolChoice       *SamplingToolChoice `json:"toolChoice,omitempty"`
}

type SamplingResult struct {
	Model      string            `json:"model"`
	StopReason string            `json:"stopReason"`
	Role       string            `json:"role"`
	Content    SamplingContent   `json:"content"`
	Blocks     []SamplingContent `json:"-"`
}

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

func (r *SamplingResult) UnmarshalJSON(data []byte) error {
	if isArrayJSON(data) {
		return nil
	}
	type alias SamplingResult
	return json.Unmarshal(data, (*alias)(r))
}

type SamplingHandler func(ctx context.Context, params SamplingParams) (*SamplingResult, error)

func ParseSamplingParams(raw json.RawMessage) (SamplingParams, error) {
	var p SamplingParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func ValidateSamplingParams(p SamplingParams) error {
	if p.ToolChoice != nil {
		if len(p.Tools) == 0 {
			return fmt.Errorf("toolChoice provided without tools")
		}
		switch p.ToolChoice.Mode {
		case ToolChoiceAuto, ToolChoiceRequired, ToolChoiceNone:
		case "":

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

	if len(pendingToolIDs) > 0 {
		ids := make([]string, 0, len(pendingToolIDs))
		for id := range pendingToolIDs {
			ids = append(ids, id)
		}
		return fmt.Errorf("tool result missing in request: unresolved toolUseIds %v", ids)
	}
	return nil
}

const MaxSamplingTokens = 4096

func EffectiveMaxTokens(requested int) int {
	if requested <= 0 || requested > MaxSamplingTokens {
		return MaxSamplingTokens
	}
	return requested
}
