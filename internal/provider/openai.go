package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
	"sync/atomic"
)

const (
	claudeCLIVersion = "2.1.209"
)

type OpenAIProvider struct {
	client           *openai.Client
	model            string
	maxTokens        int
	cap              *adaptiveCap
	reasoningEffort  string
	toolChoice       string
	strictTools      map[string]bool
	serviceTier      string
	temperature      float64
	samplingOverride atomic.Pointer[SamplingOverride]
	topP             float64
	responseSchema   json.RawMessage
	name             string
	baseURL          string
	transport        *headerInjectingTransport
	logprobs         bool
	policy           callPolicy
}

func (p *OpenAIProvider) ModelName() string { return p.model }

func (p *OpenAIProvider) CloneWithModel(model string) Provider {
	clone := &OpenAIProvider{
		client:    p.client,
		model:     model,
		maxTokens: p.maxTokens,

		cap:             AdaptiveCapForModelSwap(p.cap, model, p.maxTokens),
		policy:          p.policy,
		reasoningEffort: p.reasoningEffort,
		toolChoice:      p.toolChoice,
		strictTools:     p.strictTools,
		serviceTier:     p.serviceTier,
		temperature:     p.temperature,
		topP:            p.topP,
		name:            p.name,
		baseURL:         p.baseURL,
		transport:       p.transport,
		logprobs:        p.logprobs,

		responseSchema: append(json.RawMessage(nil), p.responseSchema...),
	}
	if ov := p.samplingOverride.Load(); ov != nil {
		clone.samplingOverride.Store(ov)
	}
	return clone
}

func (p *OpenAIProvider) SetAdaptiveCap(c *adaptiveCap) { p.cap = c }

func (p *OpenAIProvider) SetMaxTokens(n int) {
	if n > 0 {
		p.maxTokens = n
	}
}

func (p *OpenAIProvider) SetReasoningEffort(effort string) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "", "low", "medium", "high":
		p.reasoningEffort = effort
	}
}

func (p *OpenAIProvider) ReasoningEffort() string { return p.reasoningEffort }

func (p *OpenAIProvider) SetServiceTier(tier string) {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "":
		p.serviceTier = ""
	case "auto", "default", "flex", "priority", "fast", "scale":
		p.serviceTier = strings.ToLower(strings.TrimSpace(tier))
	}
}

func (p *OpenAIProvider) ServiceTier() string { return p.serviceTier }

func (p *OpenAIProvider) applyServiceTier(req *openai.ChatCompletionRequest) bool {
	if p.serviceTier == "" {
		return false
	}
	req.ServiceTier = openai.ServiceTier(p.serviceTier)
	return true
}

func (p *OpenAIProvider) SetToolChoice(choice string) {
	p.toolChoice = strings.ToLower(strings.TrimSpace(choice))
}

func (p *OpenAIProvider) ToolChoice() string { return p.toolChoice }

func (p *OpenAIProvider) SetStrictTools(allow map[string]bool) {
	p.strictTools = allow
}

func (p *OpenAIProvider) SetResponseSchema(schema json.RawMessage) {
	p.responseSchema = schema
}

func (p *OpenAIProvider) ResponseSchema() json.RawMessage { return p.responseSchema }

func (p *OpenAIProvider) applyResponseSchema(req *openai.ChatCompletionRequest) {
	if len(p.responseSchema) == 0 {
		return
	}
	req.ResponseFormat = &openai.ChatCompletionResponseFormat{
		JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
			Name:   "final_answer",
			Schema: p.responseSchema,
			Strict: true,
		},
	}
}

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

func (p *OpenAIProvider) applyToolChoice(req *openai.ChatCompletionRequest) {
	if p.toolChoice == "" || len(req.Tools) == 0 {
		return
	}
	switch p.toolChoice {
	case "auto", "required", "none":
		req.ToolChoice = p.toolChoice
	}
}

func (p *OpenAIProvider) SetTemperature(temp float64) { p.temperature = temp }

func (p *OpenAIProvider) SetLogprobsRequest(on bool) { p.logprobs = on }

func (p *OpenAIProvider) LogprobsRequested() bool { return p.logprobs }

func (p *OpenAIProvider) SetSamplingOverride(o *SamplingOverride) { p.samplingOverride.Store(o) }

func (p *OpenAIProvider) SamplingOverride() *SamplingOverride { return p.samplingOverride.Load() }
func (p *OpenAIProvider) Temperature() float64                { return p.temperature }

func (p *OpenAIProvider) SetTopP(topP float64) { p.topP = topP }
func (p *OpenAIProvider) TopP() float64        { return p.topP }

func (p *OpenAIProvider) applySampling(req *openai.ChatCompletionRequest) {
	temp := p.temperature
	if o := p.samplingOverride.Load(); o != nil && o.Temperature > 0 {
		temp = o.Temperature
	}
	if temp > 0 {
		req.Temperature = float32(temp)
	}
	if p.topP > 0 {
		req.TopP = float32(p.topP)
	}

	if o := p.samplingOverride.Load(); o != nil && len(o.StopSequences) > 0 {
		req.Stop = o.StopSequences
	}
}

func (p *OpenAIProvider) effectiveMaxTokens() int {
	if o := p.samplingOverride.Load(); o != nil && o.MaxTokens > 0 {
		return o.MaxTokens
	}
	if p.cap != nil {
		if v := p.cap.Get(); v > 0 {
			return v
		}
	}
	return p.maxTokens
}

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

func retryWithoutServiceTier(err error) bool {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Param != nil && strings.EqualFold(*apiErr.Param, "service_tier") {
			return true
		}
		msg := strings.ToLower(apiErr.Message)
		return strings.Contains(msg, "service_tier") || strings.Contains(msg, "service tier")
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "service_tier") || strings.Contains(msg, "service tier")
}

func (p *OpenAIProvider) createChatCompletion(ctx context.Context, req openai.ChatCompletionRequest, hasReasoningEffort bool) (openai.ChatCompletionResponse, error) {
	resp, err := p.client.CreateChatCompletion(ctx, req)
	if err != nil && hasReasoningEffort && retryWithoutReasoningEffort(err) {
		req.ReasoningEffort = ""
		resp, err = p.client.CreateChatCompletion(ctx, req)
	}
	if err != nil && req.ServiceTier != "" && retryWithoutServiceTier(err) {
		req.ServiceTier = ""
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
	if err != nil && req.ServiceTier != "" && retryWithoutServiceTier(err) {
		req.ServiceTier = ""
		stream, err = p.client.CreateChatCompletionStream(ctx, req)
	}
	return stream, err
}

type headerInjectingTransport struct {
	base           http.RoundTripper
	mu             sync.RWMutex
	headers        http.Header
	rateLimits     *rateLimitTracker
	promptCacheKey string
}

func (t *headerInjectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.RLock()
	for k, vals := range t.headers {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}
	cacheKey := t.promptCacheKey
	t.mu.RUnlock()
	if cacheKey != "" {
		injectPromptCacheKey(req, cacheKey)
	}
	resp, err := t.base.RoundTrip(req)
	if err == nil && resp != nil && t.rateLimits != nil {
		t.rateLimits.Update(resp.Header)
	}
	return resp, err
}

func (t *headerInjectingTransport) UpdateHeaders(newHeaders http.Header) {
	t.mu.Lock()
	t.headers = newHeaders
	t.mu.Unlock()
}

func (t *headerInjectingTransport) snapshotHeaders() http.Header {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cp := make(http.Header, len(t.headers))
	for k, vs := range t.headers {
		cp[k] = append([]string(nil), vs...)
	}
	return cp
}

func NewOpenAIProvider(apiKey string, model string, maxTokens int) *OpenAIProvider {
	config := openai.DefaultConfig(apiKey)
	return NewOpenAIProviderWithConfig(config, apiKey, model, maxTokens, "openai")
}

func NewOpenAIProviderWithBaseURL(apiKey string, model string, maxTokens int, baseURL string) *OpenAIProvider {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL
	return NewOpenAIProviderWithConfig(config, apiKey, model, maxTokens, "openai")
}

func NewOpenAIProviderWithConfig(config openai.ClientConfig, apiKey, model string, maxTokens int, name string) *OpenAIProvider {

	protocol := "openai"
	extraHeaders := BuildHeadersForProvider(protocol)
	for key, values := range vendorSpecificAuthHeaders(config.BaseURL, apiKey) {
		for _, value := range values {
			extraHeaders.Set(key, value)
		}
	}

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

func (p *OpenAIProvider) UpdateRuntimeHeaders(headers http.Header) {
	if p.transport != nil {
		if sid := p.transport.snapshotHeaders().Get("GGCode-SessionID"); sid != "" && headers.Get("GGCode-SessionID") == "" {
			merged := headers.Clone()
			merged.Set("GGCode-SessionID", sid)
			headers = merged
		}
		p.transport.UpdateHeaders(headers)
	}
}

func (p *OpenAIProvider) RateLimitInfo() RateLimitInfo {
	if p.transport != nil && p.transport.rateLimits != nil {
		return p.transport.rateLimits.Snapshot()
	}
	return RateLimitInfo{RemainingRequests: -1, RemainingTokens: -1, LimitRequests: -1, LimitTokens: -1}
}

func (p *OpenAIProvider) SetSessionID(sessionID string) {
	if sessionID == "" || p.transport == nil {
		return
	}
	existing := p.transport.snapshotHeaders()
	existing.Set("GGCode-SessionID", sessionID)
	p.transport.UpdateHeaders(existing)
}

func normalizeOpenAIFinishReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop":
		return "end_turn"
	case "length", "max_tokens", "max_output_tokens":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	case "":
		return ""
	default:

		return strings.ToLower(strings.TrimSpace(reason))
	}
}

func (p *OpenAIProvider) applyLogprobs(req *openai.ChatCompletionRequest) {
	if !p.logprobs {
		return
	}
	req.LogProbs = true
	req.TopLogProbs = 1
}

func meanLogprob(ps []float64) *float64 {
	if len(ps) == 0 {
		return nil
	}
	var sum float64
	for _, v := range ps {
		sum += v
	}
	avg := sum / float64(len(ps))
	return &avg
}

func (p *OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {

	ctx, cancel := p.policy.withTimeout(ctx)
	defer cancel()
	chatMsgs := p.convertMessages(messages)
	req := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: chatMsgs,
	}
	hasReasoningEffort := p.applyReasoningEffort(&req)
	p.applyServiceTier(&req)
	if len(tools) > 0 {
		req.Tools = p.convertTools(tools)
	}
	p.applyToolChoice(&req)
	p.applyResponseSchema(&req)
	p.applySampling(&req)
	p.applyMaxTokens(&req)
	p.applyLogprobs(&req)

	var resp openai.ChatCompletionResponse
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.createChatCompletion(ctx, req, hasReasoningEffort)
		return callErr
	}, p.policy.attempts(), p.policy.timeoutRetryInPlace())
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

	if isLengthFinishReason(string(choice.FinishReason)) && p.cap != nil {
		p.cap.OnTruncated()
	}
	content := p.convertResponseContent(choice.Message)

	usage := openAIUsage(resp.Usage)

	outputChars := len(choice.Message.Content)
	for _, tc := range choice.Message.ToolCalls {
		outputChars += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		inputTokens, err := p.CountTokens(ctx, messages)
		if err != nil {
			inputTokens = 0
		}
		usage = TokenUsage{
			InputTokens:       inputTokens,
			OutputTokens:      estimateTokensFromChars(outputChars),
			PromptTokensTotal: inputTokens,
		}
	} else if usage.OutputTokens == 0 && outputChars > 0 {

		usage.OutputTokens = estimateTokensFromChars(outputChars)
	} else if usage.InputTokens == 0 {

		if it, err := p.CountTokens(ctx, messages); err == nil {
			usage.InputTokens = it
			usage.PromptTokensTotal = it
		}
	}

	var confidence *float64
	if p.logprobs && choice.LogProbs != nil {
		lps := make([]float64, 0, len(choice.LogProbs.Content))
		for _, t := range choice.LogProbs.Content {
			lps = append(lps, t.LogProb)
		}
		confidence = meanLogprob(lps)
	}
	return &ChatResponse{
		Message:    Message{Role: "assistant", Content: content},
		Usage:      usage,
		StopReason: normalizeOpenAIFinishReason(string(choice.FinishReason)),
		Confidence: confidence,
	}, nil
}

func (p *OpenAIProvider) sendEvent(ctx context.Context, ch chan<- StreamEvent, ev StreamEvent) bool {

	select {
	case ch <- ev:
		return true
	default:
	}
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
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
	p.applyServiceTier(&req)
	if len(tools) > 0 {
		req.Tools = p.convertTools(tools)
	}
	p.applyToolChoice(&req)
	p.applyResponseSchema(&req)
	p.applySampling(&req)
	p.applyMaxTokens(&req)
	p.applyLogprobs(&req)

	debug.Log("openai", "ChatStream START model=%s msgs=%d tools=%d", p.model, len(chatMsgs), len(req.Tools))

	ch := make(chan StreamEvent, 64)

	safego.Go("provider.openai.streamRead", func() {
		defer close(ch)

		parentCtx := ctx
		var cancelAttempt context.CancelFunc
		defer func() { cancelAttempt() }()

		var usage *TokenUsage
		var outputChars int
		var err error
		var truncated bool
		var logprobSum float64
		var logprobN int
		streamError := false
		budget := newRetryBudget()

		attempts := p.policy.attempts()
		for attempt := 0; attempt < attempts; attempt++ {
			if attempt > 0 {
				cancelAttempt()
				debug.Log("openai", "Stream retry attempt %d/%d model=%s baseURL=%s", attempt+1, p.policy.attempts(), p.model, p.baseURL)
			}

			ctx, cancelAttempt = p.policy.withTimeout(parentCtx)

			usage = nil
			logprobSum, logprobN = 0, 0

			truncated = false

			var localStreamer *openai.ChatCompletionStream
			localStreamer, err = p.createChatCompletionStream(ctx, req, hasReasoningEffort)
			if err != nil {
				if rejected, parsed := maxTokensRejection(err); rejected && p.cap != nil {
					p.cap.OnRejected(parsed)
				}
				if isRetryableForContext(ctx, err) && attempt < attempts-1 {
					debug.Log("openai", "CONNECT FAILED model=%s baseURL=%s attempt=%d/%d delay=%v: %T: %v", p.model, p.baseURL, attempt+1, p.policy.attempts(), retryDelay(err, attempt), err, err)
					slept, sErr := p.sleepBeforeRetry(ctx, ch, budget, err, attempt)
					if !slept {
						if sErr != nil {
							if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: sErr}) {
								return
							}
							streamError = true
						}
						return
					}
					if isTimeoutClassError(err) && p.policy.timeoutRetryInPlace() {
						attempts = retryAlwaysAttempts
					}

					continue
				}
				debug.Log("openai", "CONNECT FATAL model=%s baseURL=%s attempt=%d/%d: %T: %v", p.model, p.baseURL, attempt+1, p.policy.attempts(), err, err)
				if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: fmt.Errorf("openai stream: %w", err)}) {
					return
				}
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

					flushChars, flushEmitted, _ := p.flushResidualToolCalls(ctx, ch, toolCalls, normalEnd, retry)
					outputChars += flushChars
					if flushEmitted {
						emitted = true
					}
				}()
				for {
					resp, recvErr := localStreamer.Recv()
					if recvErr != nil {

						if errors.Is(recvErr, context.Canceled) {
							debug.Log("openai", "stream cancelled: %v emitted=%v", recvErr, emitted)
							if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: recvErr}) {
								return
							}
							streamError = true
							return
						}

						if errors.Is(recvErr, io.EOF) {
							debug.Log("openai", "Stream ended normally: %v reasoning_total=%d emitted=%v", recvErr, reasoningBuf.Len(), emitted)
							normalEnd = true
							return
						}
						debug.Log("openai", "STREAM ERROR model=%s baseURL=%s attempt=%d/%d emitted=%v reasoning=%d output=%d: %T: %v", p.model, p.baseURL, attempt+1, p.policy.attempts(), emitted, reasoningBuf.Len(), outputChars, recvErr, recvErr)

						if !emitted && isRetryableForContext(ctx, recvErr) && attempt < attempts-1 {
							slept, sErr := p.sleepBeforeRetry(ctx, ch, budget, recvErr, attempt)
							if isTimeoutClassError(recvErr) && p.policy.timeoutRetryInPlace() {
								attempts = retryAlwaysAttempts
							}
							if !slept {
								if sErr != nil {
									if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: sErr}) {
										return
									}

									streamError = true
								}
								return
							}
							retry = true
							return
						}
						if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: recvErr}) {
							return
						}
						streamError = true
						return
					}

					if resp.Usage != nil {

						usage = applyOpenAIUsage(usage, *resp.Usage)
					}

					if len(resp.Choices) == 0 {
						continue
					}

					choice := resp.Choices[0]
					for i := range resp.Choices {
						if resp.Choices[i].Index == 0 {
							choice = resp.Choices[i]
							break
						}
					}
					delta := choice.Delta

					if delta.ReasoningContent != "" {
						reasoningBuf.WriteString(delta.ReasoningContent)
						emitted = true
						if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventReasoning, Text: delta.ReasoningContent}) {
							return
						}
					}

					if delta.Content != "" {
						outputChars += len(delta.Content)
						emitted = true
						if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventText, Text: delta.Content}) {
							return
						}
					}

					if p.logprobs && choice.Logprobs != nil {
						for _, t := range choice.Logprobs.Content {
							logprobSum += t.Logprob
							logprobN++
						}
					}

					for _, tc := range delta.ToolCalls {
						idx := -1
						if tc.Index != nil {
							idx = int(*tc.Index)
						} else if len(delta.ToolCalls) == 1 {

							idx = 0

							idx = resolveNoIndexToolCallSlot(toolCalls, tc.ID)
						} else if tc.ID != "" {

							idx = resolveNoIndexToolCallSlot(toolCalls, tc.ID)
						} else {

							debug.Log("openai", "dropping tool-call delta without index (multi-call delta, id=%s name=%s)", tc.ID, tc.Function.Name)
							continue
						}
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

					finishReason := string(choice.FinishReason)
					if finishReason != "" {
						if debug.IsVerbose("openai") {
							debug.Log("openai", "finish_reason=%s tool_calls=%d", finishReason, len(toolCalls))
							for _, tc := range toolCalls {
								debug.Log("openai", "tool_call id=%s name=%s args=%s", tc.ID, tc.Name, string(tc.Arguments))
							}
						}

						for _, idx := range slices.Sorted(maps.Keys(toolCalls)) {
							tc := toolCalls[idx]

							if len(tc.Arguments) > 0 && !json.Valid(tc.Arguments) {
								if repaired, ok := RepairJSON(tc.Arguments); ok {
									debug.Log("openai", "finish_reason tool_call id=%s name=%s: JSON repaired %d→%d bytes", tc.ID, tc.Name, len(tc.Arguments), len(repaired))
									tc.Arguments = repaired
								}
							}
							outputChars += len(tc.Name) + len(tc.Arguments)
							emitted = true
							if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventToolCallDone, Tool: *tc}) {
								return
							}
							delete(toolCalls, idx)
						}
						if isLengthFinishReason(finishReason) {

							if p.cap != nil {
								p.cap.OnTruncated()
							}
							truncated = true
						} else if finishErr := finishReasonError(finishReason); finishErr != nil {
							if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: finishErr}) {
								return
							}
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
				} else {
					if usage.OutputTokens == 0 && outputChars > 0 {

						usage.OutputTokens = estimateTokensFromChars(outputChars)
					}
					if usage.InputTokens == 0 {

						if it, err := p.CountTokens(ctx, messages); err == nil {
							usage.InputTokens = it
							usage.PromptTokensTotal = it
						}
					}
				}
				var confidence *float64
				if logprobN > 0 {
					avg := logprobSum / float64(logprobN)
					confidence = &avg
				}
				if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventDone, Usage: usage, Truncated: truncated, Confidence: confidence}) {
					return
				}
			}
			return
		}

		if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: fmt.Errorf("openai stream: %d retry attempts exhausted", p.policy.attempts())}) {
			return
		}
	})

	return ch, nil
}

func resolveNoIndexToolCallSlot(toolCalls map[int]*ToolCallDelta, id string) int {
	idx := 0
	if id == "" {
		return idx
	}
	for i, ex := range toolCalls {
		if ex != nil && ex.ID == id {
			return i
		}
	}
	if ex0 := toolCalls[0]; ex0 != nil && ex0.ID != "" && ex0.ID != id {
		return nextToolCallSlot(toolCalls)
	}
	return idx
}

func nextToolCallSlot(toolCalls map[int]*ToolCallDelta) int {
	max := -1
	for k := range toolCalls {
		if k > max {
			max = k
		}
	}
	return max + 1
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

func applyOpenAIUsage(cur *TokenUsage, u openai.Usage) *TokenUsage {
	parsed := openAIUsage(u)
	if parsed.InputTokens == 0 && parsed.OutputTokens == 0 && parsed.CacheRead == 0 {
		if cur == nil {
			return nil
		}
		return cur
	}
	if cur == nil {
		return &parsed
	}
	if parsed.InputTokens > 0 {
		cur.InputTokens = parsed.InputTokens
		cur.PromptTokensTotal = parsed.PromptTokensTotal
	}
	if parsed.OutputTokens > 0 {
		cur.OutputTokens = parsed.OutputTokens
	}
	if parsed.CacheRead > 0 {
		cur.CacheRead = parsed.CacheRead
	}
	return cur
}

func (p *OpenAIProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return estimateTokensForMessages(messages), nil
}

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

func mergeInjectedUserMessages(messages []Message) []Message {
	return foldInjectedUserMessages(messages, prependToToolResultContent)
}

func foldInjectedUserMessages(messages []Message, fold func([]ContentBlock, string) []ContentBlock) []Message {
	if len(messages) < 3 {
		return messages
	}

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

	debug.Log("provider", "mergeInjectedUserMessages: folding text-only user messages into tool_result messages")

	result := make([]Message, 0, len(messages))
	i := 0
	for i < len(messages) {

		if messages[i].Role == "assistant" && hasToolUseBlock(messages[i].Content) &&
			i+1 < len(messages) && messages[i+1].Role == "user" && isTextOnly(messages[i+1].Content) {

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

			result = append(result, messages[i])

			if j < len(messages) && messages[j].Role == "user" && hasToolResultBlock(messages[j].Content) {

				merged := messages[j]
				if len(guidanceTexts) > 0 {
					prefix := strings.Join(guidanceTexts, "\n\n") + "\n\n"
					merged.Content = fold(merged.Content, prefix)
				}
				result = append(result, merged)
				i = j + 1
			} else {

				for k := i + 1; k < j; k++ {
					result = append(result, messages[k])
				}
				i = j
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

func prependToToolResultContent(blocks []ContentBlock, prefix string) []ContentBlock {
	result := make([]ContentBlock, 0, len(blocks)+1)
	inserted := false
	for _, b := range blocks {
		if !inserted && b.Type == "text" {
			result = append(result, ContentBlock{Type: "text", Text: prefix + b.Text})
			inserted = true
		} else if !inserted && b.Type == "tool_result" {

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

	messages = MergeSystemMessages(messages)

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
				result = appendToolResultMessages(result, m.Content, guidancePrefix)
				break
			}

			hasImage := false
			for _, b := range m.Content {
				if b.Type == "image" {
					hasImage = true
					break
				}
			}
			if hasImage {
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

				if b.ReasoningContent != "" {
					reasoningContent = b.ReasoningContent
				}
			}
			msg.ToolCalls = toolCalls

			if reasoningContent != "" {
				msg.ReasoningContent = reasoningContent
			} else if len(toolCalls) > 0 {
				msg.ReasoningContent = ""
			}

			if msg.Content == "" && len(msg.ToolCalls) == 0 {
				debug.Log("openai", "WARNING: skipping empty assistant message (idx=%d, content_blocks=%d)", idx, len(m.Content))
				continue
			}
			result = append(result, msg)
		case "tool":

			result = appendToolResultMessages(result, m.Content, "")
		}
	}
	return result
}

func appendToolResultMessages(dst []openai.ChatCompletionMessage, blocks []ContentBlock, guidancePrefix string) []openai.ChatCompletionMessage {
	first := true
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		output := b.Output
		if first && guidancePrefix != "" {
			output = guidancePrefix + output
		}
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
			if output != "" {
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeText,
					Text: output,
				})
			}
			dst = append(dst, openai.ChatCompletionMessage{
				Role:         openai.ChatMessageRoleTool,
				ToolCallID:   b.ToolID,
				MultiContent: parts,
			})
		} else {
			dst = append(dst, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    output,
				ToolCallID: b.ToolID,
			})
		}
		first = false
	}
	return dst
}

func (p *OpenAIProvider) convertTools(tools []ToolDefinition) []openai.Tool {
	result := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		params := t.Parameters

		if len(bytes.TrimSpace(params)) == 0 || !json.Valid(params) {
			debug.Log("openai", "WARNING: tool %q has invalid JSON schema (%d bytes), using empty object fallback", t.Name, len(params))
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}

		if moonshotSchemaGateActive(p.baseURL, p.model) {
			params = normalizeMoonshotSchema(params)
		}

		strict := t.Strict || p.strictTools[t.Name]
		if strict {
			prepared, ok := PrepareStrictToolSchema(t.Name, params)
			if ok {
				params = prepared
			} else {
				strict = false
			}
		}
		result = append(result, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Strict:      strict,
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
