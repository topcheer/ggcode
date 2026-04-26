package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"

	"github.com/sashabaranov/go-openai"
)

const (
	// claudeCLIVersion is the version we masquerade as.
	claudeCLIVersion = "2.1.92"
)

// OpenAIProvider implements Provider using the OpenAI-compatible API.
type OpenAIProvider struct {
	client    *openai.Client
	model     string
	maxTokens int
	cap       *adaptiveCap // optional; when non-nil, takes precedence over maxTokens
	name      string
	transport *headerInjectingTransport // kept for runtime header updates
}

// SetAdaptiveCap installs (or replaces) the adaptive max-output-tokens cap.
// Used by NewProvider to share learned state across reconstructions.
func (p *OpenAIProvider) SetAdaptiveCap(c *adaptiveCap) { p.cap = c }

// effectiveMaxTokens returns the value to send on the next request.
// Priority: adaptive cap > static maxTokens > 0 (omit).
func (p *OpenAIProvider) effectiveMaxTokens() int {
	if p.cap != nil {
		if v := p.cap.Get(); v > 0 {
			return v
		}
	}
	return p.maxTokens
}

// headerInjectingTransport wraps an http.RoundTripper to inject custom headers
// that mimic the claude-cli client identity.
type headerInjectingTransport struct {
	base    http.RoundTripper
	mu      sync.RWMutex
	headers http.Header
}

func (t *headerInjectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.RLock()
	for k, vals := range t.headers {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}
	t.mu.RUnlock()
	return t.base.RoundTrip(req)
}

// UpdateHeaders replaces the injected headers. Safe for concurrent use with RoundTrip.
func (t *headerInjectingTransport) UpdateHeaders(newHeaders http.Header) {
	t.mu.Lock()
	t.headers = newHeaders
	t.mu.Unlock()
}

// claudeCLIHeaders returns the set of HTTP headers that mimic the official
// claude-cli client, allowing compatible API providers to recognize the client.
func claudeCLIHeaders() http.Header {
	h := make(http.Header)
	h.Set("User-Agent", fmt.Sprintf("claude-cli/%s (individual, cli)", claudeCLIVersion))
	h.Set("x-app", "cli")
	h.Set("anthropic-version", "2023-06-01")
	return h
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(apiKey string, model string, maxTokens int) *OpenAIProvider {
	config := openai.DefaultConfig(apiKey)
	return NewOpenAIProviderWithConfig(config, model, maxTokens, "openai")
}

// NewOpenAIProviderWithBaseURL creates a new OpenAI provider with a custom base URL.
func NewOpenAIProviderWithBaseURL(apiKey string, model string, maxTokens int, baseURL string) *OpenAIProvider {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL
	return NewOpenAIProviderWithConfig(config, model, maxTokens, "openai")
}

func NewOpenAIProviderWithConfig(config openai.ClientConfig, model string, maxTokens int, name string) *OpenAIProvider {
	// Build identity headers from impersonation state or defaults.
	protocol := "openai"
	extraHeaders := BuildHeadersForProvider(protocol)
	var baseTransport http.RoundTripper
	if hc, ok := config.HTTPClient.(*http.Client); ok && hc != nil && hc.Transport != nil {
		baseTransport = hc.Transport
	}
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	transport := &headerInjectingTransport{
		base:    baseTransport,
		headers: extraHeaders,
	}
	config.HTTPClient = &http.Client{
		Transport: transport,
	}

	client := openai.NewClientWithConfig(config)
	debug.Log("provider", "OpenAIProvider created: model=%s maxTokens=%d name=%s headers=%v",
		model, maxTokens, name, extraHeaders)

	if strings.TrimSpace(name) == "" {
		name = "openai"
	}
	return &OpenAIProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
		name:      name,
		transport: transport,
	}
}

func (p *OpenAIProvider) Name() string {
	if strings.TrimSpace(p.name) == "" {
		return "openai"
	}
	return p.name
}

// UpdateRuntimeHeaders updates the injected headers at runtime.
func (p *OpenAIProvider) UpdateRuntimeHeaders(headers http.Header) {
	if p.transport != nil {
		p.transport.UpdateHeaders(headers)
	}
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
	if v := p.effectiveMaxTokens(); v > 0 {
		req.MaxCompletionTokens = v
	}
	if len(tools) > 0 {
		req.Tools = p.convertTools(tools)
	}
	dumpRequestJSON("openai", "Chat", req)

	var resp openai.ChatCompletionResponse
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.client.CreateChatCompletion(ctx, req)
		return callErr
	}, providerRetryAttempts)
	if err != nil {
		if rejected, parsed := maxTokensRejection(err); rejected {
			p.cap.OnRejected(parsed)
		}
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
	if v := p.effectiveMaxTokens(); v > 0 {
		req.MaxCompletionTokens = v
	}
	if len(tools) > 0 {
		req.Tools = p.convertTools(tools)
	}

	debug.Log("openai", "ChatStream START model=%s msgs=%d tools=%d", p.model, len(chatMsgs), len(req.Tools))
	dumpRequestJSON("openai", "ChatStream", req)
	if len(req.Tools) > 0 {
		if toolJSON, err := json.Marshal(req.Tools); err == nil {
			debug.Log("openai", "Tools: %s", string(toolJSON))
		}
	}

	ch := make(chan StreamEvent, 64)

	safego.Go("provider.openai.streamRead", func() {
		defer close(ch)

		var usage *TokenUsage
		var outputChars int

		for attempt := 0; attempt < providerRetryAttempts; attempt++ {
			if attempt > 0 {
				debug.Log("openai", "Stream retry attempt %d", attempt)
			}

			// (Re-)establish the stream for each attempt
			var localStreamer *openai.ChatCompletionStream
			err := retryWithBackoffCtx(ctx, func() error {
				var sErr error
				localStreamer, sErr = p.client.CreateChatCompletionStream(ctx, req)
				return sErr
			}, providerRetryAttempts)
			if err != nil {
				if rejected, parsed := maxTokensRejection(err); rejected {
					p.cap.OnRejected(parsed)
				}
				if isRetryable(err) && attempt < providerRetryAttempts-1 {
					if sleepErr := retrySleep(ctx, retryDelay(err, attempt)); sleepErr != nil {
						ch <- StreamEvent{Type: StreamEventError, Error: sleepErr}
						return
					}
					continue
				}
				debug.Log("openai", "ChatStream ERROR model=%s: %v", p.model, err)
				ch <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("openai stream: %w", err)}
				return
			}

			toolCalls := make(map[int]*ToolCallDelta)
			emitted := false
			retry := false
			normalEnd := false

			func() {
				defer localStreamer.Close()
				defer func() {
					// Flush any tool_calls that accumulated but never received
					// a chunk with a non-empty finish_reason. Some
					// OpenAI-compatible backends (LiteLLM, vLLM, ZAI compat,
					// some Azure deployments) terminate the SSE without ever
					// emitting finish_reason; without this flush the agent
					// silently drops the tool call and hangs in "thinking".
					// See locks.md S7. Only flush on a clean EOF — never on
					// retry (would double-execute) or hard error (broken
					// conversation, can't trust the partial args).
					if !normalEnd || retry {
						return
					}
					for idx, tc := range toolCalls {
						if tc.Name == "" && len(tc.Arguments) == 0 {
							continue
						}
						debug.Log("openai", "flush residual tool_call id=%s name=%s args=%s", tc.ID, tc.Name, string(tc.Arguments))
						outputChars += len(tc.Name) + len(tc.Arguments)
						emitted = true
						ch <- StreamEvent{Type: StreamEventToolCallDone, Tool: *tc}
						delete(toolCalls, idx)
					}
				}()
				for {
					resp, recvErr := localStreamer.Recv()
					if recvErr != nil {
						// Stream ended normally
						if errors.Is(recvErr, io.EOF) || recvErr == context.Canceled || recvErr == context.DeadlineExceeded {
							debug.Log("openai", "Stream ended normally: %v", recvErr)
							if errors.Is(recvErr, io.EOF) {
								normalEnd = true
							}
							return
						}
						debug.Log("openai", "Stream ERROR: %v", recvErr)
						// Retry if no content emitted yet and error is retryable
						if !emitted && isRetryable(recvErr) && attempt < providerRetryAttempts-1 {
							if sleepErr := retrySleep(ctx, retryDelay(recvErr, attempt)); sleepErr != nil {
								ch <- StreamEvent{Type: StreamEventError, Error: sleepErr}
								return
							}
							retry = true
							return
						}
						ch <- StreamEvent{Type: StreamEventError, Error: recvErr}
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
						emitted = true
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
							emitted = true
							ch <- StreamEvent{Type: StreamEventToolCallDone, Tool: *tc}
							delete(toolCalls, idx)
						}
						if finishErr := finishReasonError(finishReason); finishErr != nil {
							if isLengthFinishReason(finishReason) {
								p.cap.OnTruncated()
							}
							ch <- StreamEvent{Type: StreamEventError, Error: finishErr}
							return
						}
					}
				}
			}()

			if retry {
				continue
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
			return
		}
	})

	return ch, nil
}

func (p *OpenAIProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return estimateTokensForMessages(messages), nil
}

// estimateTokensForMessages counts all content fields (Text, Output, Input)
// and converts to an approximate token count.
func estimateTokensForMessages(messages []Message) int {
	totalChars := 0
	textChars := 0
	outputChars := 0
	inputChars := 0
	for _, msg := range messages {
		for _, block := range msg.Content {
			textChars += len(block.Text)
			outputChars += len(block.Output)
			inputChars += len(block.Input)
		}
	}
	totalChars = textChars + outputChars + inputChars
	tokens := estimateTokensFromChars(totalChars)
	debug.Log("provider", "estimateTokensForMessages: msgs=%d text_chars=%d output_chars=%d input_chars=%d total_chars=%d tokens=%d",
		len(messages), textChars, outputChars, inputChars, totalChars, tokens)
	return tokens
}

func estimateTokensFromChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	// Conservative estimate: ~3 chars/token on average (mixed ASCII/CJK/code).
	// errs on the side of overcounting to trigger compaction early enough.
	estimate := chars / 3
	if estimate < 1 {
		return 1
	}
	return estimate
}

func isLengthFinishReason(finishReason string) bool {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "length", "max_tokens", "max_output_tokens":
		return true
	}
	return false
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
						if len(b.Images) > 0 && !b.IsError {
							// Multimodal tool result: images + text
							var parts []openai.ChatMessagePart
							for _, img := range b.Images {
								parts = append(parts, openai.ChatMessagePart{
									Type: openai.ChatMessagePartTypeImageURL,
									ImageURL: &openai.ChatMessageImageURL{
										URL:    fmt.Sprintf("data:%s;base64,%s", img.MIME, img.Base64),
										Detail: openai.ImageURLDetailAuto,
									},
								})
							}
							if b.Output != "" {
								parts = append(parts, openai.ChatMessagePart{
									Type: openai.ChatMessagePartTypeText,
									Text: b.Output,
								})
							}
							result = append(result, openai.ChatCompletionMessage{
								Role:         openai.ChatMessageRoleTool,
								ToolCallID:   b.ToolID,
								MultiContent: parts,
							})
						} else {
							result = append(result, openai.ChatCompletionMessage{
								Role:       openai.ChatMessageRoleTool,
								Content:    b.Output,
								ToolCallID: b.ToolID,
							})
						}
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
					if len(b.Images) > 0 && !b.IsError {
						var parts []openai.ChatMessagePart
						for _, img := range b.Images {
							parts = append(parts, openai.ChatMessagePart{
								Type: openai.ChatMessagePartTypeImageURL,
								ImageURL: &openai.ChatMessageImageURL{
									URL:    fmt.Sprintf("data:%s;base64,%s", img.MIME, img.Base64),
									Detail: openai.ImageURLDetailAuto,
								},
							})
						}
						if b.Output != "" {
							parts = append(parts, openai.ChatMessagePart{
								Type: openai.ChatMessagePartTypeText,
								Text: b.Output,
							})
						}
						result = append(result, openai.ChatCompletionMessage{
							Role:         openai.ChatMessageRoleTool,
							ToolCallID:   b.ToolID,
							MultiContent: parts,
						})
					} else {
						result = append(result, openai.ChatCompletionMessage{
							Role:       openai.ChatMessageRoleTool,
							Content:    b.Output,
							ToolCallID: b.ToolID,
						})
					}
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
