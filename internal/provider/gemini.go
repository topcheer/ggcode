package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

// GeminiProvider implements Provider using the Google Generative AI API.
type GeminiProvider struct {
	client    *genai.Client
	model     string
	maxTokens int
}

// NewGeminiProvider creates a new Gemini provider.
func NewGeminiProvider(apiKey string, model string, maxTokens int) (*GeminiProvider, error) {
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	return &GeminiProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
	}, nil
}

func (p *GeminiProvider) Name() string {
	return "gemini"
}

func (p *GeminiProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	contents, systemInstruction := p.convertMessages(messages)

	config := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
	}
	if p.maxTokens > 0 {
		config.MaxOutputTokens = int32(p.maxTokens)
	}
	if len(tools) > 0 {
		config.Tools = p.convertTools(tools)
	}

	var resp *genai.GenerateContentResponse
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.client.Models.GenerateContent(ctx, p.model, contents, config)
		return callErr
	}, providerRetryAttempts)
	if err != nil {
		return nil, fmt.Errorf("gemini chat: %w", err)
	}

	content, usage := p.convertResponse(resp)
	return &ChatResponse{
		Message: Message{Role: "assistant", Content: content},
		Usage:   usage,
	}, nil
}

func (p *GeminiProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	contents, systemInstruction := p.convertMessages(messages)

	config := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
	}
	if p.maxTokens > 0 {
		config.MaxOutputTokens = int32(p.maxTokens)
	}
	if len(tools) > 0 {
		config.Tools = p.convertTools(tools)
	}

	ch := make(chan StreamEvent, 64)

	go func() {
		defer close(ch)

		var usage TokenUsage
		for attempt := 0; attempt < providerRetryAttempts; attempt++ {
			iter := p.client.Models.GenerateContentStream(ctx, p.model, contents, config)
			emitted := false
			retry := false
			for resp, err := range iter {
				if err != nil {
					if !emitted && isRetryable(err) && attempt < providerRetryAttempts-1 {
						if sleepErr := retrySleep(ctx, retryDelay(err, attempt)); sleepErr != nil {
							ch <- StreamEvent{Type: StreamEventError, Error: sleepErr}
							return
						}
						retry = true
						break
					}
					ch <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("gemini stream: %w", err)}
					return
				}

				// Extract usage metadata
				if resp.UsageMetadata != nil {
					usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
					usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
				}

				if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
					continue
				}

				for _, part := range resp.Candidates[0].Content.Parts {
					if part.Text != "" {
						emitted = true
						ch <- StreamEvent{Type: StreamEventText, Text: part.Text}
					}
					if part.FunctionCall != nil {
						emitted = true
						args, _ := json.Marshal(part.FunctionCall.Args)
						id := part.FunctionCall.ID
						if id == "" {
							id = part.FunctionCall.Name
						}
						ch <- StreamEvent{
							Type: StreamEventToolCallDone,
							Tool: ToolCallDelta{
								Index:     0,
								ID:        id,
								Name:      part.FunctionCall.Name,
								Arguments: args,
							},
						}
					}
				}
			}
			if retry {
				continue
			}
			ch <- StreamEvent{Type: StreamEventDone, Usage: &usage}
			return
		}
	}()

	return ch, nil
}

func (p *GeminiProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	total := 0
	for _, msg := range messages {
		for _, block := range msg.Content {
			total += len(block.Text)
		}
	}
	return total / 4, nil
}

func (p *GeminiProvider) convertMessages(messages []Message) ([]*genai.Content, *genai.Content) {
	var contents []*genai.Content
	var systemParts []*genai.Part

	for _, m := range messages {
		if m.Role == "system" {
			for _, b := range m.Content {
				if b.Type == "text" {
					systemParts = append(systemParts, &genai.Part{Text: b.Text})
				}
			}
			continue
		}

		role := m.Role
		if role == "assistant" {
			role = "model"
		}

		var parts []*genai.Part
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				parts = append(parts, &genai.Part{Text: b.Text})
			case "image":
				// Gemini uses InlineData for inline images
				parts = append(parts, &genai.Part{
					InlineData: &genai.Blob{
						MIMEType: b.ImageMIME,
						Data:     []byte(b.ImageData),
					},
				})
			case "tool_use":
				var args map[string]any
				if b.Input != nil {
					json.Unmarshal(b.Input, &args)
				}
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   b.ToolID,
						Name: b.ToolName,
						Args: args,
					},
				})
			case "tool_result":
				parts = append(parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						ID:   b.ToolID,
						Name: b.ToolName,
						Response: map[string]any{
							"output": b.Output,
						},
					},
				})
			}
		}

		contents = append(contents, &genai.Content{
			Role:  role,
			Parts: parts,
		})
	}

	var systemInstruction *genai.Content
	if len(systemParts) > 0 {
		systemInstruction = &genai.Content{
			Role:  "user",
			Parts: systemParts,
		}
	}

	return contents, systemInstruction
}

func (p *GeminiProvider) convertTools(tools []ToolDefinition) []*genai.Tool {
	functionDecls := make([]*genai.FunctionDeclaration, len(tools))
	for i, t := range tools {
		fd := &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
		}
		if len(t.Parameters) > 0 {
			schema := &genai.Schema{}
			if json.Unmarshal(t.Parameters, schema) == nil {
				fd.Parameters = schema
			}
		}
		functionDecls[i] = fd
	}

	return []*genai.Tool{
		{
			FunctionDeclarations: functionDecls,
		},
	}
}

func (p *GeminiProvider) convertResponse(resp *genai.GenerateContentResponse) ([]ContentBlock, TokenUsage) {
	var blocks []ContentBlock
	var usage TokenUsage

	if resp.UsageMetadata != nil {
		usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
		usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
	}

	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.Text != "" {
				blocks = append(blocks, TextBlock(part.Text))
			}
			if part.FunctionCall != nil {
				args, _ := json.Marshal(part.FunctionCall.Args)
				id := part.FunctionCall.ID
				if id == "" {
					id = part.FunctionCall.Name
				}
				blocks = append(blocks, ToolUseBlock(id, part.FunctionCall.Name, args))
			}
		}
	}

	return blocks, usage
}
