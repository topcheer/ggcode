package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"

	"github.com/sashabaranov/go-openai"
)

// OpenAIProvider implements Provider using the OpenAI-compatible API.
type OpenAIProvider struct {
	client    *openai.Client
	model     string
	maxTokens int
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(apiKey string, model string, maxTokens int) *OpenAIProvider {
	config := openai.DefaultConfig(apiKey)
	client := openai.NewClientWithConfig(config)
	return &OpenAIProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
	}
}

// NewOpenAIProviderWithBaseURL creates a new OpenAI provider with a custom base URL.
func NewOpenAIProviderWithBaseURL(apiKey string, model string, maxTokens int, baseURL string) *OpenAIProvider {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL
	client := openai.NewClientWithConfig(config)
	return &OpenAIProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
	}
}

func (p *OpenAIProvider) Name() string {
	return "openai"
}

func (p *OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	chatMsgs := p.convertMessages(messages)
	req := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: chatMsgs,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
	}
	if p.maxTokens > 0 {
		req.MaxCompletionTokens = p.maxTokens
	}
	if len(tools) > 0 {
		req.Tools = p.convertTools(tools)
	}

	var resp openai.ChatCompletionResponse
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.client.CreateChatCompletion(ctx, req)
		return callErr
	}, providerRetryAttempts)
	if err != nil {
		return nil, fmt.Errorf("openai chat: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai chat: no choices in response")
	}

	choice := resp.Choices[0]
	content := p.convertResponseContent(choice.Message)

	usage := TokenUsage{}
	if resp.Usage.PromptTokens != 0 || resp.Usage.CompletionTokens != 0 {
		usage.InputTokens = int(resp.Usage.PromptTokens)
		usage.OutputTokens = int(resp.Usage.CompletionTokens)
	}

	return &ChatResponse{
		Message: Message{Role: "assistant", Content: content},
		Usage:   usage,
	}, nil
}

func (p *OpenAIProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	chatMsgs := p.convertMessages(messages)
	req := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: chatMsgs,
	}
	if p.maxTokens > 0 {
		req.MaxCompletionTokens = p.maxTokens
	}
	if len(tools) > 0 {
		req.Tools = p.convertTools(tools)
	}

	debug.Log("openai", "ChatStream START model=%s msgs=%d tools=%d", p.model, len(chatMsgs), len(req.Tools))
	if msgJSON, err := json.Marshal(chatMsgs); err == nil {
		debug.Log("openai", "Messages: %s", string(msgJSON))
	}
	if len(req.Tools) > 0 {
		if toolJSON, err := json.Marshal(req.Tools); err == nil {
			debug.Log("openai", "Tools: %s", string(toolJSON))
		}
	}

	var streamer *openai.ChatCompletionStream
	err := retryWithBackoffCtx(ctx, func() error {
		var sErr error
		streamer, sErr = p.client.CreateChatCompletionStream(ctx, req)
		return sErr
	}, providerRetryAttempts)
	if err != nil {
		debug.Log("openai", "ChatStream ERROR model=%s: %v", p.model, err)
		var apiErr *openai.APIError
		if errors.As(err, &apiErr) {
			debug.Log("openai", "API error: status=%d code=%s message=%s", apiErr.HTTPStatusCode, apiErr.Code, apiErr.Message)
		}
		if len(req.Tools) > 0 {
			if toolJSON, err := json.Marshal(req.Tools); err == nil {
				t := string(toolJSON)
				if len(t) > 500 {
					t = t[:500] + "..."
				}
				debug.Log("openai", "Request had %d tools, first tool JSON: %s", len(req.Tools), t)
			}
		}
		return nil, fmt.Errorf("openai stream: %w", err)
	}

	ch := make(chan StreamEvent, 64)

	go func() {
		defer close(ch)
		defer streamer.Close()
		debug.Log("openai", "Stream goroutine started")

		toolCalls := make(map[int]*ToolCallDelta)
		var usage *TokenUsage
		var outputChars int

		for {
			resp, err := streamer.Recv()
			if err != nil {
				// Stream ended
				if errors.Is(err, io.EOF) || err == context.Canceled || err == context.DeadlineExceeded {
					debug.Log("openai", "Stream ended normally: %v", err)
					break
				}
				debug.Log("openai", "Stream ERROR: %v", err)
				ch <- StreamEvent{Type: StreamEventError, Error: err}
				return
			}

			// Check for usage in final chunk (empty choices)
			if resp.Usage != nil && (resp.Usage.PromptTokens != 0 || resp.Usage.CompletionTokens != 0) && len(resp.Choices) == 0 {
				usage = &TokenUsage{
					InputTokens:  int(resp.Usage.PromptTokens),
					OutputTokens: int(resp.Usage.CompletionTokens),
				}
				continue
			}

			if len(resp.Choices) == 0 {
				continue
			}

			choice := resp.Choices[0]
			delta := choice.Delta

			// Text content
			if delta.Content != "" {
				debug.Log("openai", "chunk text=%q", delta.Content)
				outputChars += len(delta.Content)
				ch <- StreamEvent{Type: StreamEventText, Text: delta.Content}
			}

			// Tool call deltas
			for _, tc := range delta.ToolCalls {
				if tc.Index == nil {
					continue
				}
				idx := int(*tc.Index)
				existing, ok := toolCalls[idx]
				if !ok {
					existing = &ToolCallDelta{Index: idx}
					toolCalls[idx] = existing
				}
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					existing.Arguments = append(existing.Arguments, tc.Function.Arguments...)
				}
			}

			// Check for finish reason to emit completed tool calls
			finishReason := string(choice.FinishReason)
			if finishReason != "" {
				debug.Log("openai", "finish_reason=%s tool_calls=%d", finishReason, len(toolCalls))
				for idx, tc := range toolCalls {
					debug.Log("openai", "tool_call id=%s name=%s args=%s", tc.ID, tc.Name, string(tc.Arguments))
					outputChars += len(tc.Name) + len(tc.Arguments)
					ch <- StreamEvent{Type: StreamEventToolCallDone, Tool: *tc}
					delete(toolCalls, idx)
				}
				if finishErr := finishReasonError(finishReason); finishErr != nil {
					ch <- StreamEvent{Type: StreamEventError, Error: finishErr}
					return
				}
			}
		}

		if usage == nil {
			inputTokens, err := p.CountTokens(ctx, messages)
			if err != nil {
				inputTokens = 0
			}
			usage = &TokenUsage{
				InputTokens:  inputTokens,
				OutputTokens: estimateTokensFromChars(outputChars),
			}
		}
		ch <- StreamEvent{Type: StreamEventDone, Usage: usage}
	}()

	return ch, nil
}

func (p *OpenAIProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	total := 0
	for _, msg := range messages {
		for _, block := range msg.Content {
			total += len(block.Text)
		}
	}
	return total / 4, nil
}

func estimateTokensFromChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	estimate := chars / 4
	if estimate < 1 {
		return 1
	}
	return estimate
}

func finishReasonError(finishReason string) error {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "", "stop", "tool_calls", "function_call":
		return nil
	case "model_context_window_exceeded", "context_window_exceeded":
		return fmt.Errorf("prompt too long: model context window exceeded")
	case "length":
		return fmt.Errorf("openai stream ended with finish_reason=length")
	case "sensitive":
		return fmt.Errorf("openai stream ended with finish_reason=sensitive")
	case "network_error":
		return fmt.Errorf("openai stream ended with finish_reason=network_error")
	case "content_filter":
		return fmt.Errorf("openai stream ended with finish_reason=content_filter")
	default:
		return fmt.Errorf("openai stream ended with finish_reason=%s", finishReason)
	}
}

func (p *OpenAIProvider) convertMessages(messages []Message) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, 0, len(messages))
	for idx, m := range messages {
		debug.Log("openai", "convertMessages[%d]: role=%s content_blocks=%d", idx, m.Role, len(m.Content))
		for ci, cb := range m.Content {
			debug.Log("openai", "  content[%d]: type=%s tool_id=%q", ci, cb.Type, cb.ToolID)
		}
		switch m.Role {
		case "system":
			// Collect text blocks for system messages
			var text string
			for _, b := range m.Content {
				if b.Type == "text" {
					text += b.Text
				}
			}
			result = append(result, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: text,
			})
		case "user":
			// Check for tool_result blocks (agent stores tool results as role="user")
			hasToolResult := false
			debug.Log("openai", "convert user msg: content_blocks=%d", len(m.Content))
			for i, b := range m.Content {
				out := b.Output
				if len(out) > 100 {
					out = util.Truncate(out, 100)
				}
				debug.Log("openai", "  block[%d]: type=%s tool_id=%s output=%s", i, b.Type, b.ToolID, out)
				if b.Type == "tool_result" {
					hasToolResult = true
					break
				}
			}
			if hasToolResult {
				// Convert tool_result blocks to OpenAI tool messages
				for _, b := range m.Content {
					if b.Type == "tool_result" {
						result = append(result, openai.ChatCompletionMessage{
							Role:       openai.ChatMessageRoleTool,
							Content:    b.Output,
							ToolCallID: b.ToolID,
						})
					}
				}
				break
			}
			// Check if any content block is an image
			hasImage := false
			for _, b := range m.Content {
				if b.Type == "image" {
					hasImage = true
					break
				}
			}
			if hasImage {
				// Multi-part content with images
				var parts []openai.ChatMessagePart
				for _, b := range m.Content {
					switch b.Type {
					case "text":
						parts = append(parts, openai.ChatMessagePart{
							Type: openai.ChatMessagePartTypeText,
							Text: b.Text,
						})
					case "image":
						parts = append(parts, openai.ChatMessagePart{
							Type: openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{
								URL:    fmt.Sprintf("data:%s;base64,%s", b.ImageMIME, b.ImageData),
								Detail: openai.ImageURLDetailAuto,
							},
						})
					}
				}
				result = append(result, openai.ChatCompletionMessage{
					Role:         openai.ChatMessageRoleUser,
					MultiContent: parts,
				})
			} else {
				var text string
				for _, b := range m.Content {
					if b.Type == "text" {
						text += b.Text
					}
				}
				if text == "" {
					debug.Log("openai", "WARNING: skipping empty user message (idx=%d, content_blocks=%d)", idx, len(m.Content))
				} else {
					result = append(result, openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleUser,
						Content: text,
					})
				}
			}
		case "assistant":
			msg := openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: "",
			}
			var toolCalls []openai.ToolCall
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					msg.Content += b.Text
				case "tool_use":
					toolCalls = append(toolCalls, openai.ToolCall{
						ID:   b.ToolID,
						Type: openai.ToolTypeFunction,
						Function: openai.FunctionCall{
							Name:      b.ToolName,
							Arguments: normalizeToolInputJSONString(b.Input),
						},
					})
				}
			}
			msg.ToolCalls = toolCalls
			result = append(result, msg)
		case "tool":
			// Tool results - each tool_result block becomes a separate message
			for _, b := range m.Content {
				if b.Type == "tool_result" {
					result = append(result, openai.ChatCompletionMessage{
						Role:       openai.ChatMessageRoleTool,
						Content:    b.Output,
						ToolCallID: b.ToolID,
					})
				}
			}
		}
	}
	return result
}

func (p *OpenAIProvider) convertTools(tools []ToolDefinition) []openai.Tool {
	result := make([]openai.Tool, len(tools))
	for i, t := range tools {
		result[i] = openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}
	return result
}

func (p *OpenAIProvider) convertResponseContent(msg openai.ChatCompletionMessage) []ContentBlock {
	var result []ContentBlock
	if msg.Content != "" {
		result = append(result, TextBlock(msg.Content))
	}
	for _, tc := range msg.ToolCalls {
		result = append(result, ToolUseBlock(tc.ID, tc.Function.Name, json.RawMessage(tc.Function.Arguments)))
	}
	return result
}
