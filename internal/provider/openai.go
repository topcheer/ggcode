package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
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
	toolChoice      string // "", "auto", "required", "none"
	temperature     float64
	topP            float64
	name            string
	baseURL         string                    // endpoint URL, for logging
	transport       *headerInjectingTransport // kept for runtime header updates
}

// ModelName returns the current model name, implementing ModelNameProvider.
func (p *OpenAIProvider) ModelName() string { return p.model }

// CloneWithModel returns a shallow copy of this provider with a different model.
// Used by named subagents to run with a model override.
func (p *OpenAIProvider) CloneWithModel(model string) Provider {
	return &OpenAIProvider{
		client:          p.client,
		model:           model,
		maxTokens:       p.maxTokens,
		cap:             p.cap,
		reasoningEffort: p.reasoningEffort,
		toolChoice:      p.toolChoice,
		temperature:     p.temperature,
		topP:            p.topP,
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

// SetToolChoice sets the tool_choice parameter: "auto" (model decides),
// "required" (force tool use), "none" (disable tools), or "" (API default).
func (p *OpenAIProvider) SetToolChoice(choice string) {
	p.toolChoice = strings.ToLower(strings.TrimSpace(choice))
}

func (p *OpenAIProvider) ToolChoice() string { return p.toolChoice }

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

// applyToolChoice sets the tool_choice field on the request when tools are
// present and tool_choice is configured. Values: "auto", "required", "none".
func (p *OpenAIProvider) applyToolChoice(req *openai.ChatCompletionRequest) {
	if p.toolChoice == "" || len(req.Tools) == 0 {
		return
	}
	switch p.toolChoice {
	case "auto", "required", "none":
		req.ToolChoice = p.toolChoice
	}
}

// SetTemperature sets the sampling temperature. A value of 0 means "use provider
// default" (which is typically 1.0). Values between 0 and 2 are valid.
func (p *OpenAIProvider) SetTemperature(temp float64) { p.temperature = temp }
func (p *OpenAIProvider) Temperature() float64        { return p.temperature }

// SetTopP sets the nucleus sampling parameter. A value of 0 means "use provider
// default". Values between 0 and 1 are valid.
func (p *OpenAIProvider) SetTopP(topP float64) { p.topP = topP }
func (p *OpenAIProvider) TopP() float64        { return p.topP }

// applySampling sets temperature and top_p on the request when configured.
// Following the OpenAI API guidance, both can be set simultaneously (unlike
// Anthropic which recommends using only one).
func (p *OpenAIProvider) applySampling(req *openai.ChatCompletionRequest) {
	if p.temperature > 0 {
		req.Temperature = float32(p.temperature)
	}
	if p.topP > 0 {
		req.TopP = float32(p.topP)
	}
}

// effectiveMaxTokens returns the max output tokens to send on the next
// request: the adaptive cap when set (learned from rejections/truncations),
// otherwise the configured maxTokens. Mirrors anthropic.go's
// effectiveMaxTokens (#561-A: this was previously never consumed — requests
// went out without max_tokens and the backend's small default truncated
// them into a finish_reason=length continue-loop).
func (p *OpenAIProvider) effectiveMaxTokens() int {
	if p.cap != nil {
		if v := p.cap.Get(); v > 0 {
			return v
		}
	}
	return p.maxTokens
}

// applyMaxTokens sets req.MaxTokens from the effective limit. A zero value
// means "don't send" (use the model default).
func (p *OpenAIProvider) applyMaxTokens(req *openai.ChatCompletionRequest) {
	if v := p.effectiveMaxTokens(); v > 0 {
		req.MaxTokens = v
	}
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
		resp, err = p.client.CreateChatCompletion(ctx, req)
	}
	return resp, err
}

func (p *OpenAIProvider) createChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest, hasReasoningEffort bool) (*openai.ChatCompletionStream, error) {
	stream, err := p.client.CreateChatCompletionStream(ctx, req)
	if err != nil && hasReasoningEffort && retryWithoutReasoningEffort(err) {
		req.ReasoningEffort = ""
		stream, err = p.client.CreateChatCompletionStream(ctx, req)
	}
	return stream, err
}

// headerInjectingTransport wraps an http.RoundTripper to inject custom headers
// that mimic the claude-cli client identity, and captures rate-limit headers
// from responses for proactive quota monitoring.
type headerInjectingTransport struct {
	base       http.RoundTripper
	mu         sync.RWMutex
	headers    http.Header
	rateLimits *rateLimitTracker // nil if rate-limit capture is disabled
}

func (t *headerInjectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.RLock()
	for k, vals := range t.headers {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}
	t.mu.RUnlock()
	resp, err := t.base.RoundTrip(req)
	if err == nil && resp != nil && t.rateLimits != nil {
		t.rateLimits.Update(resp.Header)
	}
	return resp, err
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
	var origClient *http.Client
	if hc, ok := config.HTTPClient.(*http.Client); ok && hc != nil && hc.Transport != nil {
		baseTransport = hc.Transport
		origClient = hc
	}
	if baseTransport == nil {
		baseTransport = newProviderHTTPTransport()
	}
	transport := &headerInjectingTransport{
		base:       baseTransport,
		headers:    extraHeaders,
		rateLimits: newRateLimitTracker(),
	}
	// #561(G): preserve the original client's Timeout/Jar/CheckRedirect —
	// replacing the whole client dropped them (a 1ns-timeout caller config was
	// silently ignored). Shallow-copy and swap only the Transport.
	newClient := &http.Client{
		Transport: transport,
	}
	if origClient != nil {
		*newClient = *origClient
		newClient.Transport = transport
	}
	config.HTTPClient = newClient

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

// RateLimitInfo returns the latest rate-limit status parsed from response headers.
func (p *OpenAIProvider) RateLimitInfo() RateLimitInfo {
	if p.transport != nil && p.transport.rateLimits != nil {
		return p.transport.rateLimits.Snapshot()
	}
	return RateLimitInfo{RemainingRequests: -1, RemainingTokens: -1, LimitRequests: -1, LimitTokens: -1}
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
	p.applyToolChoice(&req)
	p.applySampling(&req)
	p.applyMaxTokens(&req)

	var resp openai.ChatCompletionResponse
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.createChatCompletion(ctx, req, hasReasoningEffort)
		return callErr
	}, providerRetryAttempts)
	if err != nil {
		if rejected, parsed := maxTokensRejection(err); rejected && p.cap != nil {
			p.cap.OnRejected(parsed)
		}
		debug.Log("openai", "Chat FATAL model=%s baseURL=%s: %T: %v", p.model, p.baseURL, err, err)
		return nil, fmt.Errorf("openai chat: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai chat: no choices in response")
	}

	choice := resp.Choices[0]
	// #455: non-streaming path must feed the adaptive cap on truncation —
	// anthropic/gemini and this file's own streaming path all report
	// finish_reason=length via OnTruncated; without it the cap never lowers
	// and requests stay truncated.
	if string(choice.FinishReason) == "length" && p.cap != nil {
		p.cap.OnTruncated()
	}
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
	p.applyToolChoice(&req)
	p.applySampling(&req)
	p.applyMaxTokens(&req)

	debug.Log("openai", "ChatStream START model=%s msgs=%d tools=%d", p.model, len(chatMsgs), len(req.Tools))

	ch := make(chan StreamEvent, 64)

	safego.Go("provider.openai.streamRead", func() {
		defer close(ch)

		var usage *TokenUsage
		var outputChars int
		var err error
		var truncated bool
		streamError := false // set when a non-retryable error was sent to ch

		for attempt := 0; attempt < providerRetryAttempts; attempt++ {
			if attempt > 0 {
				debug.Log("openai", "Stream retry attempt %d/%d model=%s baseURL=%s", attempt+1, providerRetryAttempts, p.model, p.baseURL)
			}

			// Reset per-attempt state to avoid leaking failed-attempt usage
			// into the next (successful) attempt. Same fix as gemini.go.
			usage = nil
			// #577(B): truncated must reset per attempt too. Attempt 1 could hit
			// finish_reason=length and then die to a retryable transport error
			// (emitted=false → retry); a fully-complete attempt 2 then shipped
			// Done{Truncated:true}, making the agent inject a needless
			// "continue" prompt on a complete response (up to 3 wasted calls).
			truncated = false

			// (Re-)establish the stream for each attempt
			var localStreamer *openai.ChatCompletionStream
			localStreamer, err = p.createChatCompletionStream(ctx, req, hasReasoningEffort)
			if err != nil {
				if rejected, parsed := maxTokensRejection(err); rejected && p.cap != nil {
					p.cap.OnRejected(parsed)
				}
				if isRetryableForContext(ctx, err) && attempt < providerRetryAttempts-1 {
					delay := retryDelay(err, attempt)
					debug.Log("openai", "CONNECT FAILED model=%s baseURL=%s attempt=%d/%d delay=%v: %T: %v", p.model, p.baseURL, attempt+1, providerRetryAttempts, delay, err, err)
					// Notify user about retry
					ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, providerRetryAttempts, delay)}
					if sleepErr := retrySleep(ctx, delay); sleepErr != nil {
						ch <- StreamEvent{Type: StreamEventError, Error: sleepErr}
						streamError = true
						return
					}
					// Retry the connection on the next attempt instead of
					// falling through to the CONNECT FATAL path. Without
					// this, the sleep above runs but the goroutine then
					// returns a fatal error — the retry loop is dead code
					// and every transient 502/503/504/429 kills the run.
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
					shouldFlush := normalEnd && !retry // #302: cancel no longer flushes half-made tool calls
					if !shouldFlush {
						return
					}
					for idx, tc := range toolCalls {
						if tc.Name == "" || tc.ID == "" {
							continue
						}
						// Validate arguments look like complete JSON.
						// If invalid, attempt JSON repair before skipping -
						// stream truncation and weak models frequently produce
						// nearly-valid JSON that can be salvaged.
						if len(tc.Arguments) > 0 && !json.Valid(tc.Arguments) {
							if repaired, ok := RepairJSON(tc.Arguments); ok {
								debug.Log("openai", "flush tool_call id=%s name=%s: JSON repaired %d→%d bytes", tc.ID, tc.Name, len(tc.Arguments), len(repaired))
								tc.Arguments = repaired
							} else {
								debug.Log("openai", "skip flush incomplete tool_call id=%s name=%s (invalid JSON args, repair failed)", tc.ID, tc.Name)
								continue
							}
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
						// #302: context cancellation must surface as an error event
						// (anthropic.go parity), NOT as a normal end — treating it as
						// "ended normally" finalized partial output as a complete
						// assistant message and repaired+flushed unfinished tool calls.
						if errors.Is(recvErr, context.Canceled) {
							debug.Log("openai", "stream cancelled: %v emitted=%v", recvErr, emitted)
							ch <- StreamEvent{Type: StreamEventError, Error: recvErr}
							streamError = true
							return
						}
						// Stream ended normally
						if errors.Is(recvErr, io.EOF) {
							debug.Log("openai", "Stream ended normally: %v reasoning_total=%d emitted=%v", recvErr, reasoningBuf.Len(), emitted)
							normalEnd = true
							return
						}
						debug.Log("openai", "STREAM ERROR model=%s baseURL=%s attempt=%d/%d emitted=%v reasoning=%d output=%d: %T: %v", p.model, p.baseURL, attempt+1, providerRetryAttempts, emitted, reasoningBuf.Len(), outputChars, recvErr, recvErr)
						// Retry if no content emitted yet and error is retryable
						if !emitted && isRetryableForContext(ctx, recvErr) && attempt < providerRetryAttempts-1 {
							delay := retryDelay(recvErr, attempt)
							ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, providerRetryAttempts, delay)}
							if sleepErr := retrySleep(ctx, delay); sleepErr != nil {
								ch <- StreamEvent{Type: StreamEventError, Error: sleepErr}
								// Mark the stream as errored so the tail does
								// not emit a usage-bearing Done after the
								// terminal Error (mirrors the connect-phase
								// branch above and anthropic.go).
								streamError = true
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

					// #561(E): vLLM/OpenRouter compat layers may number choices
					// starting at an index > 0. Prefer the choice with Index==0;
					// if no choice carries Index 0, fall back to the first one
					// so index>0 deltas are not silently dropped.
					choice := resp.Choices[0]
					for i := range resp.Choices {
						if resp.Choices[i].Index == 0 {
							choice = resp.Choices[i]
							break
						}
					}
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
						if debug.IsVerbose("openai") {
							debug.Log("openai", "finish_reason=%s tool_calls=%d", finishReason, len(toolCalls))
							for _, tc := range toolCalls {
								debug.Log("openai", "tool_call id=%s name=%s args=%s", tc.ID, tc.Name, string(tc.Arguments))
							}
						}
						for idx, tc := range toolCalls {
							// Attempt JSON repair for truncated/malformed args.
							// This is especially important when finish_reason is
							// "length" (max_tokens truncation) - the args may be
							// cut off mid-object.
							if len(tc.Arguments) > 0 && !json.Valid(tc.Arguments) {
								if repaired, ok := RepairJSON(tc.Arguments); ok {
									debug.Log("openai", "finish_reason tool_call id=%s name=%s: JSON repaired %d→%d bytes", tc.ID, tc.Name, len(tc.Arguments), len(repaired))
									tc.Arguments = repaired
								}
							}
							outputChars += len(tc.Name) + len(tc.Arguments)
							emitted = true
							ch <- StreamEvent{Type: StreamEventToolCallDone, Tool: *tc}
							delete(toolCalls, idx)
						}
						if isLengthFinishReason(finishReason) {
							// Output was truncated by max_tokens limit - this is NOT an
							// error. We keep the partial content already streamed.
							if p.cap != nil {
								p.cap.OnTruncated()
							}
							truncated = true
						} else if finishErr := finishReasonError(finishReason); finishErr != nil {
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
				ch <- StreamEvent{Type: StreamEventDone, Usage: usage, Truncated: truncated}
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

// mergeInjectedUserMessages folds text-only "user" messages that appear between
// an assistant tool_use message and the subsequent tool_result message into the
// tool_result message's content. Many detectors inject guidance as user messages
// during the tool execution loop. Strict OpenAI-compatible APIs (e.g. Kimi K3)
// reject requests where a tool_call is not immediately followed by its tool_result.
//
// Example transformation:
//
//	assistant: [tool_use(id=1)]
//	user:      [text: "guidance warning"]           ← injected by detector
//	user:      [tool_result(id=1), text: "result"]  ← actual tool results
//
// Becomes:
//
//	assistant: [tool_use(id=1)]
//	user:      [tool_result(id=1), text: "guidance warning\n\nresult"]
func mergeInjectedUserMessages(messages []Message) []Message {
	if len(messages) < 3 {
		return messages
	}

	// Detect if there are any problematic patterns before copying.
	hasProblem := false
	for i := 0; i < len(messages)-2; i++ {
		if messages[i].Role != "assistant" {
			continue
		}
		hasToolUse := false
		for _, b := range messages[i].Content {
			if b.Type == "tool_use" {
				hasToolUse = true
				break
			}
		}
		if !hasToolUse {
			continue
		}
		// Scan forward: are there 1+ text-only user messages followed by a tool_result?
		j := i + 1
		foundTextOnly := false
		for j < len(messages) && messages[j].Role == "user" && isTextOnly(messages[j].Content) {
			foundTextOnly = true
			j++
		}
		if foundTextOnly && j < len(messages) && messages[j].Role == "user" && hasToolResultBlock(messages[j].Content) {
			hasProblem = true
			break
		}
	}
	if !hasProblem {
		return messages
	}

	debug.Log("openai", "mergeInjectedUserMessages: folding text-only user messages into tool_result messages")

	result := make([]Message, 0, len(messages))
	i := 0
	for i < len(messages) {
		// Check if this is an assistant message with tool_use, followed by text-only user messages
		if messages[i].Role == "assistant" && hasToolUseBlock(messages[i].Content) &&
			i+1 < len(messages) && messages[i+1].Role == "user" && isTextOnly(messages[i+1].Content) {

			// Collect all consecutive text-only user messages between assistant and tool_result
			var guidanceTexts []string
			j := i + 1
			for j < len(messages) && messages[j].Role == "user" && isTextOnly(messages[j].Content) {
				for _, b := range messages[j].Content {
					if b.Type == "text" && b.Text != "" {
						guidanceTexts = append(guidanceTexts, b.Text)
					}
				}
				j++
			}

			// j now points to the tool_result message (or end)
			result = append(result, messages[i]) // assistant

			if j < len(messages) && messages[j].Role == "user" && hasToolResultBlock(messages[j].Content) {
				// Merge guidance texts into the tool_result message
				merged := messages[j]
				if len(guidanceTexts) > 0 {
					prefix := strings.Join(guidanceTexts, "\n\n") + "\n\n"
					merged.Content = prependToToolResultContent(merged.Content, prefix)
				}
				result = append(result, merged)
				i = j + 1
			} else {
				// No tool_result found - keep messages as-is
				result = append(result, messages[i+1])
				i = i + 2
			}
		} else {
			result = append(result, messages[i])
			i++
		}
	}
	return result
}

func hasToolUseBlock(blocks []ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

func hasToolResultBlock(blocks []ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

func isTextOnly(blocks []ContentBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type != "text" {
			return false
		}
	}
	return true
}

// prependToToolResultContent adds a text prefix to the first text block in a
// tool_result message, or inserts a new text block at the beginning if none exists.
func prependToToolResultContent(blocks []ContentBlock, prefix string) []ContentBlock {
	result := make([]ContentBlock, 0, len(blocks)+1)
	inserted := false
	for _, b := range blocks {
		if !inserted && b.Type == "text" {
			result = append(result, ContentBlock{Type: "text", Text: prefix + b.Text})
			inserted = true
		} else if !inserted && b.Type == "tool_result" {
			// Prepend guidance as a separate text block before tool results
			result = append(result, ContentBlock{Type: "text", Text: prefix})
			inserted = true
			result = append(result, b)
		} else {
			result = append(result, b)
		}
	}
	if !inserted {
		result = append(result, ContentBlock{Type: "text", Text: prefix})
	}
	return result
}

func (p *OpenAIProvider) convertMessages(messages []Message) []openai.ChatCompletionMessage {
	// Merge all system messages into one to avoid interspersed system messages
	// in the OpenAI messages array.
	messages = MergeSystemMessages(messages)

	// Fold injected text-only user messages between assistant tool_calls and
	// tool_result messages to satisfy strict OpenAI-compatible APIs (Kimi K3).
	messages = mergeInjectedUserMessages(messages)

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
				// #453/#474: mergeInjectedUserMessages prepends guidance TEXT
				// blocks into tool_result messages; the old loop emitted only
				// tool blocks and silently dropped the text. Emitting the text
				// as a separate user message BETWEEN the assistant tool_calls
				// and the tool messages violates the ordering contract (strict
				// backends 400) — instead prepend it to the FIRST tool
				// message's content, which every backend accepts.
				var guidanceText []string
				for _, b := range m.Content {
					if b.Type == "text" && b.Text != "" {
						guidanceText = append(guidanceText, b.Text)
					}
				}
				guidancePrefix := ""
				if len(guidanceText) > 0 {
					guidancePrefix = strings.Join(guidanceText, "\n") + "\n\n"
				}
				firstTool := true
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
							if firstTool && guidancePrefix != "" {
								// #474: guidance travels INSIDE the first
								// tool message — ordering contract intact.
								parts = append([]openai.ChatMessagePart{{
									Type: openai.ChatMessagePartTypeText,
									Text: guidancePrefix,
								}}, parts...)
							}
							result = append(result, openai.ChatCompletionMessage{
								Role:         openai.ChatMessageRoleTool,
								ToolCallID:   b.ToolID,
								MultiContent: parts,
							})
						} else {
							content := b.Output
							if firstTool && guidancePrefix != "" {
								// #474: guidance travels INSIDE the first
								// tool message — ordering contract intact.
								content = guidancePrefix + content
							}
							result = append(result, openai.ChatCompletionMessage{
								Role:       openai.ChatMessageRoleTool,
								Content:    content,
								ToolCallID: b.ToolID,
							})
						}
						firstTool = false
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
	result := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		params := t.Parameters
		// Validate the schema is well-formed JSON. MCP servers and plugins
		// may return invalid JSON schemas that crash the entire request
		// serialization (json.RawMessage.MarshalJSON panics on invalid content).
		// Fall back to an empty object schema instead of failing all 191 tools.
		if len(bytes.TrimSpace(params)) == 0 || !json.Valid(params) {
			debug.Log("openai", "WARNING: tool %q has invalid JSON schema (%d bytes), using empty object fallback", t.Name, len(params))
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		result = append(result, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
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
