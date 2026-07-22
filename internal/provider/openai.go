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
	claudeCLIVersion = "2.1.209"
)

// OpenAIProvider implements Provider using the OpenAI-compatible API.
type OpenAIProvider struct {
	client          *openai.Client
	model           string
	maxTokens       int
	cap             *adaptiveCap // optional; when non-nil, takes precedence over maxTokens
	reasoningEffort string
	name            string
	baseURL         string                    // endpoint URL, for logging
	transport       *headerInjectingTransport // kept for runtime header updates
}

// CloneWithModel returns a shallow copy of this provider with a different model.
// Used by named subagents to run with a model override.
func (p *OpenAIProvider) CloneWithModel(model string) Provider {
	return &OpenAIProvider{
		client:          p.client,
		model:           model,
		maxTokens:       p.maxTokens,
		cap:             p.cap,
		reasoningEffort: p.reasoningEffort,
		name:            p.name,
		baseURL:         p.baseURL,
		transport:       p.transport,
	}
}

// SetAdaptiveCap installs (or replaces) the adaptive max-output-tokens cap.
// Used by NewProvider to share learned state across reconstructions.
func (p *OpenAIProvider) SetAdaptiveCap(c *adaptiveCap) { p.cap = c }

func (p *OpenAIProvider) SetReasoningEffort(effort string) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "", "low", "medium", "high":
		p.reasoningEffort = effort
	}
}

func (p *OpenAIProvider) ReasoningEffort() string { return p.reasoningEffort }

// probeChat sends a single chat request without retry, adaptive cap
// tracking, or token counting. Used by context window probing.
func (p *OpenAIProvider) probeChat(ctx context.Context, messages []Message) error {
	req := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: p.convertMessages(messages),
	}
	_, err := p.client.CreateChatCompletion(ctx, req)
	return err
}

func (p *OpenAIProvider) applyReasoningEffort(req *openai.ChatCompletionRequest) bool {
	if p.reasoningEffort == "" {
		return false
	}
	req.ReasoningEffort = p.reasoningEffort
	return true
}

func retryWithoutReasoningEffort(err error) bool {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Param != nil && strings.EqualFold(*apiErr.Param, "reasoning_effort") {
			return true
		}
		msg := strings.ToLower(apiErr.Message)
		return strings.Contains(msg, "reasoning_effort") || strings.Contains(msg, "reasoning effort")
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "reasoning_effort") || strings.Contains(msg, "reasoning effort")
}

func (p *OpenAIProvider) createChatCompletion(ctx context.Context, req openai.ChatCompletionRequest, hasReasoningEffort bool) (openai.ChatCompletionResponse, error) {
	resp, err := p.client.CreateChatCompletion(ctx, req)
	if err != nil && hasReasoningEffort && retryWithoutReasoningEffort(err) {
		req.ReasoningEffort = ""
		p.SetReasoningEffort("")
		resp, err = p.client.CreateChatCompletion(ctx, req)
	}
	return resp, err
}

func (p *OpenAIProvider) createChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest, hasReasoningEffort bool) (*openai.ChatCompletionStream, error) {
	stream, err := p.client.CreateChatCompletionStream(ctx, req)
	if err != nil && hasReasoningEffort && retryWithoutReasoningEffort(err) {
		req.ReasoningEffort = ""
		p.SetReasoningEffort("")
		stream, err = p.client.CreateChatCompletionStream(ctx, req)
	}
	return stream, err
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

// snapshotHeaders returns a copy of the current headers so callers can
// safely modify and re-set them.
func (t *headerInjectingTransport) snapshotHeaders() http.Header {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cp := make(http.Header, len(t.headers))
	for k, vs := range t.headers {
		cp[k] = append([]string(nil), vs...)
	}
	return cp
}

// claudeCLIHeaders returns the set of HTTP headers that mimic the official
// claude-cli client, allowing compatible API providers to recognize the client.
// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(apiKey string, model string, maxTokens int) *OpenAIProvider {
	config := openai.DefaultConfig(apiKey)
	return NewOpenAIProviderWithConfig(config, apiKey, model, maxTokens, "openai")
}

// NewOpenAIProviderWithBaseURL creates a new OpenAI provider with a custom base URL.
func NewOpenAIProviderWithBaseURL(apiKey string, model string, maxTokens int, baseURL string) *OpenAIProvider {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL
	return NewOpenAIProviderWithConfig(config, apiKey, model, maxTokens, "openai")
}

func NewOpenAIProviderWithConfig(config openai.ClientConfig, apiKey, model string, maxTokens int, name string) *OpenAIProvider {
	// Build identity headers from impersonation state or defaults.
	protocol := "openai"
	extraHeaders := BuildHeadersForProvider(protocol)
	for key, values := range vendorSpecificAuthHeaders(config.BaseURL, apiKey) {
		for _, value := range values {
			extraHeaders.Set(key, value)
		}
	}
	// OpenRouter-specific headers for attribution and ranking.
	if isOpenRouterEndpoint(config.BaseURL) {
		extraHeaders.Set("HTTP-Referer", "https://ggcode.dev")
		extraHeaders.Set("X-Title", "GGCode")
		extraHeaders.Set("X-OpenRouter-Title", "GGCode")
		extraHeaders.Set("X-OpenRouter-Categories", "cli-agent,programming-app")
	}
	var baseTransport http.RoundTripper
	if hc, ok := config.HTTPClient.(*http.Client); ok && hc != nil && hc.Transport != nil {
		baseTransport = hc.Transport
	}
	if baseTransport == nil {
		baseTransport = newProviderHTTPTransport()
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
		baseURL:   config.BaseURL,
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

// SetSessionID injects the session ID into outgoing requests via a custom
// HTTP header (GGCode-SessionID).
func (p *OpenAIProvider) SetSessionID(sessionID string) {
	if sessionID == "" || p.transport == nil {
		return
	}
	existing := p.transport.snapshotHeaders()
	existing.Set("GGCode-SessionID", sessionID)
	p.transport.UpdateHeaders(existing)
}

func (p *OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	chatMsgs := p.convertMessages(messages)
	req := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: chatMsgs,
	}
	hasReasoningEffort := p.applyReasoningEffort(&req)
	if len(tools) > 0 {
		req.Tools = p.convertTools(tools)
	}

	var resp openai.ChatCompletionResponse
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.createChatCompletion(ctx, req, hasReasoningEffort)
		return callErr
	}, providerRetryAttempts)
	if err != nil {
		if rejected, parsed := maxTokensRejection(err); rejected {
			p.cap.OnRejected(parsed)
		}
		debug.Log("openai", "Chat FATAL model=%s baseURL=%s: %T: %v", p.model, p.baseURL, err, err)
		return nil, fmt.Errorf("openai chat: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai chat: no choices in response")
	}

	choice := resp.Choices[0]
	content := p.convertResponseContent(choice.Message)

	usage := openAIUsage(resp.Usage)

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
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
	}
	hasReasoningEffort := p.applyReasoningEffort(&req)
	if len(tools) > 0 {
		req.Tools = p.convertTools(tools)
	}

	debug.Log("openai", "ChatStream START model=%s msgs=%d tools=%d", p.model, len(chatMsgs), len(req.Tools))

	ch := make(chan StreamEvent, 64)

	safego.Go("provider.openai.streamRead", func() {
		defer close(ch)

		var usage *TokenUsage
		var outputChars int
		var err error
		streamError := false // set when a non-retryable error was sent to ch

		for attempt := 0; attempt < providerRetryAttempts; attempt++ {
			if attempt > 0 {
				debug.Log("openai", "Stream retry attempt %d/%d model=%s baseURL=%s", attempt+1, providerRetryAttempts, p.model, p.baseURL)
			}

			// Reset per-attempt state to avoid leaking failed-attempt usage
			// into the next (successful) attempt. Same fix as gemini.go.
			usage = nil

			// (Re-)establish the stream for each attempt
			var localStreamer *openai.ChatCompletionStream
			localStreamer, err = p.createChatCompletionStream(ctx, req, hasReasoningEffort)
			if err != nil {
				if rejected, parsed := maxTokensRejection(err); rejected {
					p.cap.OnRejected(parsed)
				}
				if isRetryableForContext(ctx, err) && attempt < providerRetryAttempts-1 {
					delay := retryDelay(err, attempt)
					debug.Log("openai", "CONNECT FAILED model=%s baseURL=%s attempt=%d/%d delay=%v: %T: %v", p.model, p.baseURL, attempt+1, providerRetryAttempts, delay, err, err)
					// Notify user about retry
					ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, providerRetryAttempts, delay)}
					if sleepErr := retrySleep(ctx, delay); sleepErr != nil {
						ch <- StreamEvent{Type: StreamEventError, Error: sleepErr}
						return
					}
					continue
				}
				debug.Log("openai", "CONNECT FATAL model=%s baseURL=%s attempt=%d/%d: %T: %v", p.model, p.baseURL, attempt+1, providerRetryAttempts, err, err)
				ch <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("openai stream: %w", err)}
				return
			}

			toolCalls := make(map[int]*ToolCallDelta)
			var reasoningBuf strings.Builder
			emitted := false
			retry := false
			normalEnd := false
			cancelledCleanly := false

			func() {
				defer localStreamer.Close()
				defer func() {
					// Flush any tool_calls that accumulated but never received
					// a chunk with a non-empty finish_reason. Some
					// OpenAI-compatible backends (LiteLLM, vLLM, ZAI compat,
					// some Azure deployments) terminate the SSE without ever
					// emitting finish_reason; without this flush the agent
					// silently drops the tool call and hangs in "thinking".
					// Flush on clean EOF or context cancellation (the latter
					// may carry complete tool call data from a prior stream).
					// Never flush on retry (would double-execute) or hard
					// error (broken conversation, can't trust partial args).
					shouldFlush := (normalEnd || cancelledCleanly) && !retry
					if !shouldFlush {
						return
					}
					for idx, tc := range toolCalls {
						if tc.Name == "" || tc.ID == "" {
							continue
						}
						// Validate arguments look like complete JSON
						if len(tc.Arguments) > 0 && !json.Valid(tc.Arguments) {
							debug.Log("openai", "skip flush incomplete tool_call id=%s name=%s (invalid JSON args)", tc.ID, tc.Name)
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
						if errors.Is(recvErr, io.EOF) || errors.Is(recvErr, context.Canceled) {
							debug.Log("openai", "Stream ended normally: %v reasoning_total=%d emitted=%v", recvErr, reasoningBuf.Len(), emitted)
							if errors.Is(recvErr, io.EOF) {
								normalEnd = true
							}
							if errors.Is(recvErr, context.Canceled) {
								cancelledCleanly = true
							}
							return
						}
						debug.Log("openai", "STREAM ERROR model=%s baseURL=%s attempt=%d/%d emitted=%v reasoning=%d output=%d: %T: %v", p.model, p.baseURL, attempt+1, providerRetryAttempts, emitted, reasoningBuf.Len(), outputChars, recvErr, recvErr)
						// Retry if no content emitted yet and error is retryable
						if !emitted && isRetryableForContext(ctx, recvErr) && attempt < providerRetryAttempts-1 {
							delay := retryDelay(recvErr, attempt)
							ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, providerRetryAttempts, delay)}
							if sleepErr := retrySleep(ctx, delay); sleepErr != nil {
								ch <- StreamEvent{Type: StreamEventError, Error: sleepErr}
								return
							}
							retry = true
							return
						}
						ch <- StreamEvent{Type: StreamEventError, Error: recvErr}
						streamError = true
						return
					}

					if resp.Usage != nil {
						parsedUsage := openAIUsage(*resp.Usage)
						usage = &parsedUsage
					}

					if len(resp.Choices) == 0 {
						continue
					}

					choice := resp.Choices[0]
					delta := choice.Delta

					// Reasoning content (DeepSeek v4, etc.)
					if delta.ReasoningContent != "" {
						reasoningBuf.WriteString(delta.ReasoningContent)
						emitted = true
						ch <- StreamEvent{Type: StreamEventReasoning, Text: delta.ReasoningContent}
					}

					// Text content
					if delta.Content != "" {
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
							streamError = true
							return
						}
					}
				}
			}()

			if retry {
				continue
			}

			if !streamError {
				if usage == nil {
					inputTokens, err := p.CountTokens(ctx, messages)
					if err != nil {
						inputTokens = 0
					}
					usage = &TokenUsage{
						InputTokens:       inputTokens,
						OutputTokens:      estimateTokensFromChars(outputChars),
						PromptTokensTotal: inputTokens,
					}
				}
				ch <- StreamEvent{Type: StreamEventDone, Usage: usage}
			}
			return
		}
		// All retry attempts exhausted without success.
		ch <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("openai stream: %d retry attempts exhausted", providerRetryAttempts)}
	})

	return ch, nil
}

func openAIUsage(usage openai.Usage) TokenUsage {
	parsed := TokenUsage{
		InputTokens:       int(usage.PromptTokens),
		OutputTokens:      int(usage.CompletionTokens),
		PromptTokensTotal: int(usage.PromptTokens),
	}
	if usage.PromptTokensDetails != nil {
		parsed.CacheRead = usage.PromptTokensDetails.CachedTokens
	}
	return parsed
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
	// Merge all system messages into one to avoid interspersed system messages
	// in the OpenAI messages array.
	messages = MergeSystemMessages(messages)
	result := make([]openai.ChatCompletionMessage, 0, len(messages))
	for idx, m := range messages {
		if debug.IsVerbose("openai") {
			debug.Log("openai", "convertMessages[%d]: role=%s content_blocks=%d", idx, m.Role, len(m.Content))
			for ci, cb := range m.Content {
				debug.Log("openai", "  content[%d]: type=%s tool_id=%q", ci, cb.Type, cb.ToolID)
			}
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
			if debug.IsVerbose("openai") {
				debug.Log("openai", "convert user msg: content_blocks=%d", len(m.Content))
			}
			for i, b := range m.Content {
				if debug.IsVerbose("openai") {
					out := b.Output
					if len(out) > 100 {
						out = util.Truncate(out, 100)
					}
					debug.Log("openai", "  block[%d]: type=%s tool_id=%s output=%s", i, b.Type, b.ToolID, out)
				}
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
			var reasoningContent string
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
				// Collect reasoning content from any block that has it
				if b.ReasoningContent != "" {
					reasoningContent = b.ReasoningContent
				}
			}
			msg.ToolCalls = toolCalls
			// DeepSeek reasoning models require reasoning_content when tool_calls
			// are present in a message. If the previous model (e.g. GLM-5.1) did not
			// generate reasoning content, supply an empty string to avoid 400 errors
			// when switching to DeepSeek V4 mid-session.
			if reasoningContent != "" {
				msg.ReasoningContent = reasoningContent
			} else if len(toolCalls) > 0 {
				msg.ReasoningContent = ""
			}
			// DeepSeek V4 strictly requires assistant messages to have content or
			// tool_calls. If both are empty (e.g. from a previous model that produced
			// an empty response), skip the message entirely.
			if msg.Content == "" && len(msg.ToolCalls) == 0 {
				debug.Log("openai", "WARNING: skipping empty assistant message (idx=%d, content_blocks=%d)", idx, len(m.Content))
				continue
			}
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
