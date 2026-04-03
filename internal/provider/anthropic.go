package provider

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicProvider implements Provider using the Anthropic SDK.
type AnthropicProvider struct {
	client    anthropic.Client
	model     string
	maxTokens int
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(apiKey string, model string, maxTokens int) *AnthropicProvider {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
	}
}

// NewAnthropicProviderWithBaseURL creates a new Anthropic provider with a custom base URL.
func NewAnthropicProviderWithBaseURL(apiKey string, model string, maxTokens int, baseURL string) *AnthropicProvider {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	}
	client := anthropic.NewClient(opts...)
	return &AnthropicProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
	}
}

func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

func (p *AnthropicProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	params := p.buildParams(messages, tools)

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	msg := convertAnthropicResponse(resp.Content)
	usage := TokenUsage{
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
	}

	return &ChatResponse{
		Message: Message{Role: "assistant", Content: msg},
		Usage:   usage,
	}, nil
}

func (p *AnthropicProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	debug.Log("anthropic", "ChatStream START model=%s msgs=%d tools=%d", p.model, len(messages), len(tools))
	params := p.buildParams(messages, tools)

	stream := p.client.Messages.NewStreaming(ctx, params)

	ch := make(chan StreamEvent, 64)

	go func() {
		defer close(ch)
		debug.Log("anthropic", "Stream goroutine started")
		toolCalls := make(map[int]*ToolCallDelta)
		var inputTokens, outputTokens int

		for stream.Next() {
			event := stream.Current()

			switch event.Type {
			case "content_block_start":
				cb := event.ContentBlock
				if cb.Type == "tool_use" {
					idx := int(event.Index)
					tc := &ToolCallDelta{Index: idx, ID: cb.ID, Name: cb.Name}
					toolCalls[idx] = tc
					debug.Log("anthropic", "content_block_start tool_use id=%s name=%s idx=%d", cb.ID, cb.Name, idx)
				}

			case "content_block_delta":
				delta := event.Delta
				switch delta.Type {
				case "text_delta":
					debug.Log("anthropic", "chunk text=%q", delta.Text)
					ch <- StreamEvent{Type: StreamEventText, Text: delta.Text}
				case "input_json_delta":
					tc, ok := toolCalls[int(event.Index)]
					if !ok {
						tc = &ToolCallDelta{Index: int(event.Index)}
						toolCalls[int(event.Index)] = tc
					}
					tc.Arguments = append(tc.Arguments, delta.PartialJSON...)
				}

			case "content_block_stop":
				idx := int(event.Index)
				if tc, ok := toolCalls[idx]; ok {
					debug.Log("anthropic", "content_block_stop tool_call id=%s name=%s args=%s", tc.ID, tc.Name, string(tc.Arguments))
					ch <- StreamEvent{
						Type: StreamEventToolCallDone,
						Tool: *tc,
					}
					delete(toolCalls, idx)
				}

			case "message_delta":
				outputTokens = int(event.Usage.OutputTokens)

			case "message_start":
				inputTokens = int(event.Message.Usage.InputTokens)
			}
		}

		if err := stream.Err(); err != nil {
			debug.Log("anthropic", "Stream ERROR: %v", err)
			ch <- StreamEvent{Type: StreamEventError, Error: err}
			return
		}
		debug.Log("anthropic", "Stream completed input_tokens=%d output_tokens=%d", inputTokens, outputTokens)

		ch <- StreamEvent{
			Type: StreamEventDone,
			Usage: &TokenUsage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			},
		}
	}()

	return ch, nil
}

func (p *AnthropicProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	total := 0
	for _, msg := range messages {
		for _, block := range msg.Content {
			total += len(block.Text)
		}
	}
	return total / 4, nil
}

func (p *AnthropicProvider) buildParams(messages []Message, tools []ToolDefinition) anthropic.MessageNewParams {
	var msgParams []anthropic.MessageParam
	// Collect system messages to embed into first user message (zai Anthropic rejects 'system' role)
	var systemTexts []string
	for _, m := range messages {
		if m.Role == "system" {
			for _, b := range m.Content {
				if b.Type == "text" {
					systemTexts = append(systemTexts, b.Text)
				}
			}
			continue
		}
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				blocks = append(blocks, anthropic.NewTextBlock(b.Text))
			case "image":
				blocks = append(blocks, anthropic.NewImageBlockBase64(b.ImageMIME, b.ImageData))
			case "tool_use":
				blocks = append(blocks, anthropic.NewToolUseBlock(b.ToolID, b.Input, b.ToolName))
			case "tool_result":
				blocks = append(blocks, anthropic.NewToolResultBlock(b.ToolID, b.Output, b.IsError))
			}
		}
		param := anthropic.MessageParam{Role: anthropic.MessageParamRole(m.Role), Content: blocks}
		// Prepend system text into first user message
		if m.Role == "user" && len(systemTexts) > 0 {
			var sb strings.Builder
			for i, st := range systemTexts {
				if i > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(st)
			}
			systemTexts = nil
			newBlocks := make([]anthropic.ContentBlockParamUnion, 0, len(blocks)+1)
			newBlocks = append(newBlocks, anthropic.NewTextBlock("[System]\n"+sb.String()+"\n[End System]"))
			newBlocks = append(newBlocks, blocks...)
			param.Content = newBlocks
		}
		msgParams = append(msgParams, param)
	}
	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: int64(p.maxTokens),
		Messages:  msgParams,
	}

	if len(tools) > 0 {
		toolParams := make([]anthropic.ToolUnionParam, len(tools))
		for i, t := range tools {
			inputSchema := anthropic.ToolInputSchemaParam{
				Type: "object",
			}
			if json.Unmarshal(t.Parameters, &inputSchema) == nil {
				// populates Properties/Required/Type directly
			}
			desc := anthropic.String(t.Description)
			toolParams[i] = anthropic.ToolUnionParamOfTool(inputSchema, t.Name)
			if toolParams[i].OfTool != nil {
				toolParams[i].OfTool.Description = desc
			}
		}
		params.Tools = toolParams
	}

	return params
}

func convertAnthropicResponse(blocks []anthropic.ContentBlockUnion) []ContentBlock {
	result := make([]ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			result = append(result, TextBlock(b.Text))
		case "tool_use":
			result = append(result, ToolUseBlock(b.ID, b.Name, b.Input))
		}
	}
	return result
}
