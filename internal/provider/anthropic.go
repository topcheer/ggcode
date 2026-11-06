package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type AnthropicProvider struct {
	client           anthropic.Client
	model            string
	maxTokens        int
	samplingOverride atomic.Pointer[SamplingOverride]
	cap              *adaptiveCap
	transport        *headerInjectingTransport
	calibrator       *tokenCountCalibrator
	files            *fileUploader
	reasoningEffort  string
	toolChoice       string
	temperature      float64
	topP             float64
	serverTools      []ServerToolConfig
	memoryTool       bool
	thinkingMode     string
	contextEditing   atomic.Pointer[ContextEditingConfig]
	strictTools      map[string]bool
	toolSearchBeta   bool

	ptcMu               sync.Mutex
	ptcContainerID      string
	ptcContainerExpires time.Time

	effortCarrier       atomic.Bool
	lastCallEffort      string
	conversationEffort  string
	adaptiveEffortLatch string
	policy              callPolicy
}

func (p *AnthropicProvider) ModelName() string { return p.model }

func (p *AnthropicProvider) CloneWithModel(model string) Provider {
	clone := &AnthropicProvider{
		client:    p.client,
		model:     model,
		maxTokens: p.maxTokens,

		cap:             AdaptiveCapForModelSwap(p.cap, model, p.maxTokens),
		transport:       p.transport,
		calibrator:      p.calibrator,
		reasoningEffort: p.reasoningEffort,
		toolChoice:      p.toolChoice,
		strictTools:     p.strictTools,
		temperature:     p.temperature,
		topP:            p.topP,
		serverTools:     p.serverTools,
		memoryTool:      p.memoryTool,
		toolSearchBeta:  p.toolSearchBeta,
		thinkingMode:    p.thinkingMode,
		policy:          p.policy,
	}

	clone.files = p.files

	if ce := p.contextEditing.Load(); ce != nil {
		clone.contextEditing.Store(ce)
	}

	if ov := p.samplingOverride.Load(); ov != nil {
		clone.samplingOverride.Store(ov)
	}

	clone.effortCarrier.Store(p.effortCarrier.Load())
	return clone
}

func (p *AnthropicProvider) SetReasoningEffort(effort string) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max":
		p.reasoningEffort = effort
	}
}

func (p *AnthropicProvider) ReasoningEffort() string { return p.reasoningEffort }

func (p *AnthropicProvider) SetThinkingMode(mode string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "manual":
		p.thinkingMode = "manual"
	case "adaptive":
		p.thinkingMode = "adaptive"
	default:
		p.thinkingMode = ""
	}
}

func (p *AnthropicProvider) ThinkingMode() string { return p.thinkingMode }

var adaptiveThinkingModels = regexp.MustCompile(`(?i)claude-(?:opus|sonnet|haiku)-4-[6-9]|claude-(?:opus|sonnet|haiku|fable|mythos)-[5-9]|claude-[5-9]`)

func adaptiveThinkingForModel(model string) bool {
	return adaptiveThinkingModels.MatchString(strings.ToLower(strings.TrimSpace(model)))
}

func (p *AnthropicProvider) useAdaptiveThinking() bool {
	switch p.thinkingMode {
	case "manual":
		return false
	case "adaptive":
		return true
	default:
		return adaptiveThinkingForModel(p.model)
	}
}

func adaptiveEffort(effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return effort
	default:
		return ""
	}
}

func (p *AnthropicProvider) anthropicBetaHeader(hasTools bool) string {
	if !hasTools || p.useAdaptiveThinking() {
		return ""
	}
	if p.thinkingBudgetForEffort(p.reasoningEffort) > 0 {
		return "interleaved-thinking-2025-05-14"
	}
	return ""
}

func (p *AnthropicProvider) betaHeaderValue(hasTools bool) string {
	var tokens []string
	if h := p.anthropicBetaHeader(hasTools); h != "" {
		tokens = append(tokens, h)
	}
	if p.ptcCodeExecutionEnabled() {
		tokens = append(tokens, ptcBetaHeader)
	}
	if p.toolSearchBeta {
		tokens = append(tokens, advancedToolUseBeta)
	}
	return strings.Join(tokens, ",")
}

func (p *AnthropicProvider) betaHeaderOpts(hasTools bool) []option.RequestOption {
	if h := p.betaHeaderValue(hasTools); h != "" {
		return []option.RequestOption{option.WithHeader("anthropic-beta", h)}
	}
	return nil
}

func (p *AnthropicProvider) SetMaxTokens(n int) {
	if n > 0 {
		p.maxTokens = n
	}
}

func (p *AnthropicProvider) beginEffortTracking() bool {
	if !p.effortCarrier.Load() {
		return false
	}
	effort := strings.ToLower(strings.TrimSpace(p.reasoningEffort))
	if effort == "" {

		p.lastCallEffort = ""
		return false
	}
	if p.lastCallEffort != effort {
		p.lastCallEffort = effort
		return false
	}
	if p.conversationEffort != effort {
		debug.Log("anthropic", "effort carrier established: %s (top-level output_config changes restart the prompt cache)", effort)
		p.conversationEffort = effort
	}
	return true
}

func (p *AnthropicProvider) SetToolChoice(choice string) {
	p.toolChoice = strings.ToLower(strings.TrimSpace(choice))
}

func (p *AnthropicProvider) SetStrictTools(allow map[string]bool) {
	p.strictTools = allow
}

func (p *AnthropicProvider) ToolChoice() string { return p.toolChoice }

func (p *AnthropicProvider) SetServerTools(tools []ServerToolConfig) {
	p.serverTools = tools
	p.toolSearchBeta = false
	for _, t := range tools {
		if t.Type == "tool_search_tool_regex" || t.Type == "tool_search_tool_bm25" {
			p.toolSearchBeta = true
		}
	}
}

func (p *AnthropicProvider) ServerToolSearchActive() bool { return p.toolSearchBeta }

const advancedToolUseBeta = "advanced-tool-use-2025-11-20"

func (p *AnthropicProvider) SetMemoryTool(enabled bool) { p.memoryTool = enabled }

func (p *AnthropicProvider) MemoryToolEnabled() bool { return p.memoryTool }

func (p *AnthropicProvider) ptcCodeExecutionEnabled() bool {
	for _, st := range p.serverTools {
		if st.Type == "code_execution_20260120" {
			return true
		}
	}
	return false
}

func (p *AnthropicProvider) storePTCContainer(id string, expires time.Time) {
	p.ptcMu.Lock()
	defer p.ptcMu.Unlock()
	p.ptcContainerID = id
	p.ptcContainerExpires = expires
}

func (p *AnthropicProvider) freshPTCContainer() (string, bool) {
	p.ptcMu.Lock()
	defer p.ptcMu.Unlock()
	if p.ptcContainerID == "" {
		return "", false
	}
	if !p.ptcContainerExpires.IsZero() && time.Now().After(p.ptcContainerExpires.Add(-30*time.Second)) {
		debug.Log("anthropic", "PTC container %s expired, requesting a fresh one", p.ptcContainerID)
		return "", false
	}
	return p.ptcContainerID, true
}

const ptcBetaHeader = "programmatic-tool-calling-2026-01-20"

func (p *AnthropicProvider) SetTemperature(temp float64) { p.temperature = temp }

func (p *AnthropicProvider) SetSamplingOverride(o *SamplingOverride) { p.samplingOverride.Store(o) }

func (p *AnthropicProvider) SamplingOverride() *SamplingOverride { return p.samplingOverride.Load() }
func (p *AnthropicProvider) Temperature() float64                { return p.temperature }

func (p *AnthropicProvider) SetTopP(topP float64) { p.topP = topP }
func (p *AnthropicProvider) TopP() float64        { return p.topP }

func (p *AnthropicProvider) SetAdaptiveCap(c *adaptiveCap) { p.cap = c }

func (p *AnthropicProvider) probeChat(ctx context.Context, messages []Message) error {
	params := p.buildParams(ctx, messages, nil)
	_, err := p.client.Messages.New(ctx, params, p.betaHeaderOpts(false)...)
	return err
}

func (p *AnthropicProvider) effectiveMaxTokens() int {
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

func (p *AnthropicProvider) thinkingBudgetForEffort(effort string) int64 {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return 0
	}
	maxTok := p.effectiveMaxTokens()
	if maxTok <= 1024 {
		return 0
	}
	var budget int64
	var ceiling int64
	switch effort {
	case "low":
		budget = int64(maxTok) / 4
		ceiling = 5000
	case "medium":
		budget = int64(maxTok) * 2 / 5
		ceiling = 16000
	case "high":
		budget = int64(maxTok) / 2
		ceiling = 32000
	default:
		return 0
	}
	if budget > ceiling {
		budget = ceiling
	}
	if budget < 1024 {
		budget = 1024
	}
	if budget >= int64(maxTok) {
		budget = int64(maxTok) - 1
	}
	return budget
}

func isThinkingError(err error) bool {
	if err == nil {
		return false
	}
	if sc, ok := asStatusCode(err); ok {
		if sc != 400 && sc != 404 && sc != 422 {
			return false
		}
	}
	msg := strings.ToLower(err.Error())
	for _, anchor := range thinkingErrorAnchors {
		if strings.Contains(msg, anchor) {
			return true
		}
	}
	return false
}

var thinkingErrorAnchors = []string{
	"does not support thinking",
	"thinking is not supported",
	"thinking is not enabled",
	"thinking is not available",
	"does not support extended thinking",
	"budget_tokens must",
	"budget_tokens is",
	"budget_tokens: ",
	"thinking parameter",
	"max_tokens must be greater than budget_tokens",
}

var effortErrorAnchors = []string{
	"output_config",
	"per-turn effort",
	"per-message effort",
	"does not support effort",
	"effort is not supported",
}

func isEffortError(err error) bool {
	if err == nil {
		return false
	}
	if sc, ok := asStatusCode(err); ok {
		if sc != 400 && sc != 404 && sc != 422 {
			return false
		}
	}
	msg := strings.ToLower(err.Error())
	for _, anchor := range effortErrorAnchors {
		if strings.Contains(msg, anchor) {
			return true
		}
	}
	return false
}

func asStatusCode(err error) (int, bool) {
	var sc interface{ StatusCode() int }
	if errors.As(err, &sc) {
		return sc.StatusCode(), true
	}
	return 0, false
}

func NewAnthropicProvider(apiKey string, model string, maxTokens int) *AnthropicProvider {
	return newAnthropicProvider(apiKey, model, maxTokens, "")
}

func NewAnthropicProviderWithBaseURL(apiKey string, model string, maxTokens int, baseURL string) *AnthropicProvider {
	return newAnthropicProvider(apiKey, model, maxTokens, baseURL)
}

func newAnthropicProvider(apiKey, model string, maxTokens int, baseURL string) *AnthropicProvider {
	headers := BuildHeadersForProvider("anthropic")
	for key, values := range vendorSpecificAuthHeaders(baseURL, apiKey) {
		for _, value := range values {
			headers.Set(key, value)
		}
	}

	if isOpenRouterEndpoint(baseURL) {
		headers.Set("HTTP-Referer", "https://ggcode.dev")
		headers.Set("X-Title", "GGCode")
		headers.Set("X-OpenRouter-Title", "GGCode")
		headers.Set("X-OpenRouter-Categories", "cli-agent,programming-app")
	}
	transport := &headerInjectingTransport{
		base:       newProviderHTTPTransport(),
		headers:    headers,
		rateLimits: newRateLimitTracker(),
	}
	opts := anthropicProviderOptions(apiKey, baseURL)
	opts = append(opts, option.WithHTTPClient(&http.Client{Transport: transport}))
	client := anthropic.NewClient(opts...)
	debug.Log("provider", "AnthropicProvider created: model=%s maxTokens=%d baseURL=%s", model, maxTokens, baseURL)
	p := &AnthropicProvider{
		client:     client,
		model:      model,
		maxTokens:  maxTokens,
		transport:  transport,
		calibrator: newTokenCountCalibrator(),
	}
	p.files = newFileUploader(&p.client, baseURL)
	return p
}

func anthropicProviderOptions(apiKey, baseURL string) []option.RequestOption {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
	}

	headers := BuildHeadersForProvider("anthropic")
	for key, values := range vendorSpecificAuthHeaders(baseURL, apiKey) {
		for _, value := range values {
			opts = append(opts, option.WithHeader(key, value))
		}
	}
	for k, vals := range headers {
		for _, v := range vals {
			opts = append(opts, option.WithHeader(k, v))
		}
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return opts
}

func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

func (p *AnthropicProvider) UpdateRuntimeHeaders(headers http.Header) {
	if p.transport != nil {
		if sid := p.transport.snapshotHeaders().Get("GGCode-SessionID"); sid != "" && headers.Get("GGCode-SessionID") == "" {
			merged := headers.Clone()
			merged.Set("GGCode-SessionID", sid)
			headers = merged
		}
		p.transport.UpdateHeaders(headers)
	}
}

func (p *AnthropicProvider) RateLimitInfo() RateLimitInfo {
	if p.transport != nil && p.transport.rateLimits != nil {
		return p.transport.rateLimits.Snapshot()
	}
	return RateLimitInfo{RemainingRequests: -1, RemainingTokens: -1, LimitRequests: -1, LimitTokens: -1}
}

func (p *AnthropicProvider) SetSessionID(sessionID string) {
	if sessionID == "" || p.transport == nil {
		return
	}
	existing := p.transport.snapshotHeaders()
	existing.Set("GGCode-SessionID", sessionID)
	p.transport.UpdateHeaders(existing)
}

func (p *AnthropicProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {

	ctx, cancel := p.policy.withTimeout(ctx)
	defer cancel()
	debug.Log("anthropic", "Chat START model=%s msgs=%d tools=%d", p.model, len(messages), len(tools))
	p.beginEffortTracking()
	params := p.buildParams(ctx, messages, tools)
	callOpts := p.betaHeaderOpts(len(tools) > 0)

	var resp *anthropic.Message
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.client.Messages.New(ctx, params, append(callOpts, p.contextEditingOptions()...)...)
		return callErr
	}, p.policy.attempts(), p.policy.timeoutRetryInPlace())

	if err != nil && params.Thinking.OfEnabled != nil && isThinkingError(err) {
		debug.Log("anthropic", "Chat: retrying without extended thinking (model rejected thinking parameters)")
		params.Thinking = anthropic.ThinkingConfigParamUnion{}
		err = retryWithBackoffCtx(ctx, func() error {
			var callErr error
			resp, callErr = p.client.Messages.New(ctx, params, append(callOpts, p.contextEditingOptions()...)...)
			return callErr
		}, p.policy.attempts(), p.policy.timeoutRetryInPlace())
	}

	if err != nil && isEffortError(err) && p.effortCarrier.CompareAndSwap(true, false) {
		debug.Log("anthropic", "Chat: retrying without output_config (endpoint rejected the effort carrier)")
		params.OutputConfig = anthropic.OutputConfigParam{}
		err = retryWithBackoffCtx(ctx, func() error {
			var callErr error
			resp, callErr = p.client.Messages.New(ctx, params, append(callOpts, p.contextEditingOptions()...)...)
			return callErr
		}, p.policy.attempts(), p.policy.timeoutRetryInPlace())
	}
	if err != nil {
		if rejected, parsed := maxTokensRejection(err); rejected {
			p.cap.OnRejected(parsed)
		}
		debug.Log("anthropic", "Chat FATAL model=%s: %T: %v", p.model, err, err)
		return nil, err
	}
	if string(resp.StopReason) == "max_tokens" {
		p.cap.OnTruncated()
	}

	if resp.Container.ID != "" {
		p.storePTCContainer(resp.Container.ID, resp.Container.ExpiresAt)
	}

	if p.contextEditing.Load() != nil {
		if summary, ok := parseAppliedEdits([]byte(resp.RawJSON())); ok {
			debug.Log("anthropic", "Chat %s", summary)
		}
	}

	msg := convertAnthropicResponse(resp.Content)
	usage := anthropicUsage(resp.Usage)

	stopReason := ""
	if resp.StopReason != "" {
		stopReason = string(resp.StopReason)
	}

	return &ChatResponse{
		Message:    Message{Role: "assistant", Content: msg},
		Usage:      usage,
		StopReason: stopReason,
	}, nil
}

func (p *AnthropicProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	debug.Log("anthropic", "ChatStream START model=%s msgs=%d tools=%d", p.model, len(messages), len(tools))
	p.beginEffortTracking()
	params := p.buildParams(ctx, messages, tools)
	callOpts := p.betaHeaderOpts(len(tools) > 0)

	ch := make(chan StreamEvent, 64)

	safego.Go("provider.anthropic.streamRead", func() {
		defer close(ch)

		parentCtx := ctx
		var cancelAttempt context.CancelFunc
		defer func() { cancelAttempt() }()

		var usage *TokenUsage
		var outputChars int
		var truncated bool
		streamError := false
		budget := newRetryBudget()

		attempts := p.policy.attempts()
		for attempt := 0; attempt < attempts; attempt++ {
			if attempt > 0 {
				cancelAttempt()
				debug.Log("anthropic", "Stream retry attempt %d", attempt)
			}

			ctx, cancelAttempt = p.policy.withTimeout(parentCtx)

			toolCalls := make(map[int]*ToolCallDelta)
			var inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int
			emitted := false
			retry := false

			truncated = false

			func() {
				stream := p.client.Messages.NewStreaming(ctx, params, append(callOpts, p.contextEditingOptions()...)...)
				defer func() {
					_ = stream.Close()
				}()

				for stream.Next() {
					event := stream.Current()

					switch event.Type {
					case "content_block_start":
						if handleContentBlockStart(ch, toolCalls, event) {
							emitted = true
						}
					case "content_block_delta":
						delta := event.Delta
						switch delta.Type {
						case "text_delta":
							outputChars += len(delta.Text)
							emitted = true
							ch <- StreamEvent{Type: StreamEventText, Text: delta.Text}
						case "input_json_delta":
							tc, ok := toolCalls[int(event.Index)]
							if !ok {
								tc = &ToolCallDelta{Index: int(event.Index)}
								toolCalls[int(event.Index)] = tc
							}
							tc.Arguments = append(tc.Arguments, delta.PartialJSON...)
						case "thinking_delta":
							emitted = true
							ch <- StreamEvent{Type: StreamEventReasoning, Text: delta.Thinking}
						}

					case "content_block_stop":
						dc, de := handleContentBlockStop(ch, toolCalls, int(event.Index))
						outputChars += dc
						if de {
							emitted = true
						}
					case "message_delta":

						if event.Usage.OutputTokens > 0 {
							outputTokens = int(event.Usage.OutputTokens)
						}

						if event.Usage.InputTokens > 0 && inputTokens == 0 {
							inputTokens = int(event.Usage.InputTokens)
						}
						if event.Usage.CacheCreationInputTokens > 0 {
							cacheWriteTokens = int(event.Usage.CacheCreationInputTokens)
						}
						if event.Usage.CacheReadInputTokens > 0 {
							cacheReadTokens = int(event.Usage.CacheReadInputTokens)
						}

						if p.contextEditing.Load() != nil {
							if summary, ok := parseAppliedEdits([]byte(event.RawJSON())); ok {
								ch <- StreamEvent{Type: StreamEventSystem, Text: "[" + summary + "] "}
							}
						}

						if stopReason := string(event.Delta.StopReason); stopReason != "" {
							debug.Log("anthropic", "stop_reason=%s", stopReason)
							if stopReason == "max_tokens" {

								p.cap.OnTruncated()
								truncated = true
							} else if stopErr := anthropicStopReasonError(stopReason); stopErr != nil {
								ch <- StreamEvent{Type: StreamEventError, Error: stopErr}
								streamError = true
								return
							}
						}

					case "message_start":

						if event.Message.Usage.InputTokens > 0 && inputTokens == 0 {
							inputTokens = int(event.Message.Usage.InputTokens)
						}
						debug.Log("anthropic", "message_start usage: input_tokens=%d output_tokens=%d cache_read=%d cache_write=%d",
							event.Message.Usage.InputTokens, event.Message.Usage.OutputTokens,
							event.Message.Usage.CacheReadInputTokens, event.Message.Usage.CacheCreationInputTokens)

						if event.Message.Usage.CacheCreationInputTokens > 0 {
							cacheWriteTokens = int(event.Message.Usage.CacheCreationInputTokens)
						}
						if event.Message.Usage.CacheReadInputTokens > 0 {
							cacheReadTokens = int(event.Message.Usage.CacheReadInputTokens)
						}

						if event.Message.Container.ID != "" {
							p.storePTCContainer(event.Message.Container.ID, event.Message.Container.ExpiresAt)
							debug.Log("anthropic", "PTC container issued: %s", event.Message.Container.ID)
						}
					}
				}

				if err := stream.Err(); err != nil {
					debug.Log("anthropic", "Stream ERROR: %v", err)
					if rejected, parsed := maxTokensRejection(err); rejected {
						p.cap.OnRejected(parsed)
					}

					if !emitted && params.Thinking.OfEnabled != nil && isThinkingError(err) && attempt < attempts-1 {
						debug.Log("anthropic", "Stream: retrying without extended thinking (model rejected thinking parameters)")

						ch <- StreamEvent{Type: StreamEventSystem, Text: "[Model rejected extended thinking - retrying without it] "}
						params.Thinking = anthropic.ThinkingConfigParamUnion{}
						retry = true
						return
					}

					if !emitted && isEffortError(err) && attempt < attempts-1 && p.effortCarrier.CompareAndSwap(true, false) {
						debug.Log("anthropic", "Stream: retrying without output_config (endpoint rejected the effort carrier)")
						ch <- StreamEvent{Type: StreamEventSystem, Text: "[Endpoint rejected output_config effort - retrying without it] "}
						params.OutputConfig = anthropic.OutputConfigParam{}
						retry = true
						return
					}

					if !emitted && isRetryableForContext(ctx, err) && attempt < attempts-1 {
						slept, sErr := p.sleepBeforeRetry(ctx, ch, budget, err, attempt)
						if !slept {
							if sErr != nil {
								ch <- StreamEvent{Type: StreamEventError, Error: sErr}
								streamError = true
							}
							return
						}
						if isTimeoutClassError(err) && p.policy.timeoutRetryInPlace() {
							attempts = retryAlwaysAttempts
						}
						retry = true
						return
					}
					ch <- StreamEvent{Type: StreamEventError, Error: err}
					streamError = true
					return
				}
			}()

			if retry {
				continue
			}

			usage = &TokenUsage{
				InputTokens:       inputTokens,
				OutputTokens:      outputTokens,
				CacheRead:         cacheReadTokens,
				CacheWrite:        cacheWriteTokens,
				PromptTokensTotal: inputTokens + cacheReadTokens + cacheWriteTokens,
			}
			debug.Log("anthropic", "Stream completed input_tokens=%d output_tokens=%d cache_read=%d cache_write=%d", usage.InputTokens, usage.OutputTokens, usage.CacheRead, usage.CacheWrite)
			break
		}

		if usage == nil {

			inputTokens, estErr := p.CountTokens(ctx, messages)
			if estErr != nil {
				inputTokens = 0
			}
			usage = &TokenUsage{
				InputTokens:       inputTokens,
				OutputTokens:      estimateTokensFromChars(outputChars),
				PromptTokensTotal: inputTokens,
			}
		}
		if !streamError {
			ch <- StreamEvent{Type: StreamEventDone, Usage: usage, Truncated: truncated}
		}
	})

	return ch, nil
}

func anthropicUsage(usage anthropic.Usage) TokenUsage {
	return TokenUsage{
		InputTokens:       int(usage.InputTokens),
		OutputTokens:      int(usage.OutputTokens),
		CacheRead:         int(usage.CacheReadInputTokens),
		CacheWrite:        int(usage.CacheCreationInputTokens),
		PromptTokensTotal: int(usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens),
	}
}

func (p *AnthropicProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	estimated := estimateTokensForMessages(messages)

	if p.calibrator == nil {
		return estimated, nil
	}

	p.calibrator.mu.Lock()
	needCalibration := p.calibrator.shouldCalibrate()
	isFirst := p.calibrator.lastCalibrate.IsZero()
	p.calibrator.mu.Unlock()

	if !needCalibration {

		return int(float64(estimated) * p.calibrator.currentRatio()), nil
	}

	if isFirst {
		calCtx, cancel := context.WithTimeout(ctx, calibrateFirstTimeout)
		defer cancel()

		if messagesContainNonTextBlocks(messages) {
			debug.Log("provider-calibrator", "first calibration skipped: non-text blocks (image/thinking) make the sample asymmetric")
			p.calibrator.recordSkip()
			return estimated, nil
		}
		realTokens, err := p.remoteCountTokens(calCtx, messages)
		if err != nil {
			debug.Log("provider-calibrator", "first calibration failed: %v", err)

			p.calibrator.recordFailure()
			return estimated, nil
		}
		p.calibrator.applyResult(estimated, realTokens)
		debug.Log("provider-calibrator", "first calibration OK: estimated=%d real=%d ratio=%.3f", estimated, realTokens, p.calibrator.currentRatio())
		return realTokens, nil
	}

	result := int(float64(estimated) * p.calibrator.currentRatio())
	safego.Go("provider.calibrateTokens", func() {
		calCtx, cancel := context.WithTimeout(context.Background(), calibrateAsyncTimeout)
		defer cancel()

		if messagesContainNonTextBlocks(messages) {
			debug.Log("provider-calibrator", "async calibration skipped: non-text blocks (image/thinking) make the sample asymmetric")
			p.calibrator.recordSkip()
			return
		}
		realTokens, err := p.remoteCountTokens(calCtx, messages)
		if err != nil {
			debug.Log("provider-calibrator", "async calibration failed: %v", err)
			p.calibrator.recordFailure()
			return
		}
		p.calibrator.applyResult(estimated, realTokens)
		debug.Log("provider-calibrator", "async calibration OK: estimated=%d real=%d ratio=%.3f", estimated, realTokens, p.calibrator.currentRatio())
	})
	return result, nil
}

func messagesContainNonTextBlocks(messages []Message) bool {
	for _, m := range messages {
		for _, b := range m.Content {
			switch b.Type {

			case "image", "thinking", "redacted_thinking":
				return true
			}
		}
	}
	return false
}

func (p *AnthropicProvider) remoteCountTokens(ctx context.Context, messages []Message) (int, error) {
	params := p.buildCountTokensParams(messages)
	resp, err := p.client.Messages.CountTokens(ctx, params)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "404") || strings.Contains(errStr, "403") ||
			strings.Contains(errStr, "not found") {
			p.calibrator.disable()
		}
		return 0, err
	}
	return int(resp.InputTokens), nil
}

func appendToToolResultContent(blocks []ContentBlock, prefix string) []ContentBlock {
	lastToolResult := -1
	for i, b := range blocks {
		if b.Type == "tool_result" {
			lastToolResult = i
		}
	}
	if lastToolResult < 0 {
		return append(blocks, ContentBlock{Type: "text", Text: prefix})
	}
	result := make([]ContentBlock, 0, len(blocks)+1)
	result = append(result, blocks[:lastToolResult+1]...)
	result = append(result, ContentBlock{Type: "text", Text: prefix})
	result = append(result, blocks[lastToolResult+1:]...)
	return result
}

func (p *AnthropicProvider) buildCountTokensParams(messages []Message) anthropic.MessageCountTokensParams {

	messages = foldInjectedUserMessages(messages, appendToToolResultContent)
	var msgParams []anthropic.MessageParam
	type sysBlock struct {
		text string
	}
	var systemBlocks []sysBlock
	for _, m := range messages {
		if m.Role == "system" {
			for _, b := range m.Content {
				if b.Type == "text" && b.Text != "" {
					systemBlocks = append(systemBlocks, sysBlock{text: b.Text})
				}
			}
			continue
		}
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result", "tool_search_tool_result", "code_execution_tool_result":

				if pb, err := serverToolBlockParam(b.Raw); err == nil {
					blocks = append(blocks, *pb)
				} else {
					debug.Log("anthropic", "server tool block echo skipped: %v", err)
				}
			case "text":
				blocks = append(blocks, anthropic.NewTextBlock(b.Text))
			case "image":
				blocks = append(blocks, anthropic.NewImageBlockBase64(b.ImageMIME, b.ImageData))
			case "tool_use":
				blocks = append(blocks, toolUseBlockParam(b.ToolID, normalizeToolInputValue(b.Input), b.ToolName, b.CallerRaw))
			case "tool_result":
				if len(b.Images) > 0 && !b.IsError {
					var content []anthropic.ToolResultBlockParamContentUnion
					for _, img := range b.Images {
						content = append(content, anthropic.ToolResultBlockParamContentUnion{
							OfImage: &anthropic.ImageBlockParam{
								Source: anthropic.ImageBlockParamSourceUnion{
									OfBase64: &anthropic.Base64ImageSourceParam{
										Data:      img.Base64,
										MediaType: anthropic.Base64ImageSourceMediaType(img.MIME),
									},
								},
							},
						})
					}
					if b.Output != "" {
						content = append(content, anthropic.ToolResultBlockParamContentUnion{
							OfText: &anthropic.TextBlockParam{Text: b.Output},
						})
					}
					blocks = append(blocks, anthropic.ContentBlockParamUnion{
						OfToolResult: &anthropic.ToolResultBlockParam{
							ToolUseID: b.ToolID,
							Content:   content,
						},
					})
				} else {
					blocks = append(blocks, anthropic.NewToolResultBlock(b.ToolID, b.Output, b.IsError))
				}
			case "thinking":
				if b.ThinkingSignature != "" {
					blocks = append(blocks, anthropic.NewThinkingBlock(b.ThinkingSignature, b.ReasoningContent))
				}
			case "redacted_thinking":
				if b.ThinkingData != "" {
					blocks = append(blocks, anthropic.NewRedactedThinkingBlock(b.ThinkingData))
				}
			}
		}
		param := anthropic.MessageParam{Role: anthropic.MessageParamRole(m.Role), Content: blocks}

		if m.Role == "user" && len(systemBlocks) > 0 {
			newBlocks := make([]anthropic.ContentBlockParamUnion, 0, len(blocks)+len(systemBlocks))
			for i, sb := range systemBlocks {
				var text string
				if i == 0 {
					text = "[System]\n" + sb.text
				} else {
					text = sb.text
				}
				if i == len(systemBlocks)-1 {
					text += "\n[End System]"
				}
				block := anthropic.NewTextBlock(text)
				newBlocks = append(newBlocks, block)
			}
			systemBlocks = nil
			newBlocks = append(newBlocks, blocks...)
			param.Content = newBlocks
		}
		msgParams = append(msgParams, param)
	}
	return anthropic.MessageCountTokensParams{
		Model:    p.model,
		Messages: msgParams,
	}
}

func (p *AnthropicProvider) buildParams(ctx context.Context, messages []Message, tools []ToolDefinition) anthropic.MessageNewParams {

	messages = foldInjectedUserMessages(messages, appendToToolResultContent)
	var msgParams []anthropic.MessageParam
	type sysBlock struct {
		text  string
		cache bool
	}
	var systemBlocks []sysBlock

	programmaticTools := map[string]bool{}
	for _, m := range messages {
		if m.Role == "system" {
			for _, b := range m.Content {
				if b.Type == "text" && b.Text != "" {
					systemBlocks = append(systemBlocks, sysBlock{text: b.Text, cache: b.Cache})
				}
			}
			continue
		}
		if m.Role == "assistant" {
			for _, b := range m.Content {
				if b.Type == "tool_use" && isProgrammaticCaller(b.CallerRaw) {
					programmaticTools[b.ToolID] = true
				}
			}
		}
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result", "tool_search_tool_result", "code_execution_tool_result":
				if pb, err := serverToolBlockParam(b.Raw); err == nil {
					blocks = append(blocks, *pb)
				} else {
					debug.Log("anthropic", "server tool block echo skipped: %v", err)
				}
			case "text":
				blocks = append(blocks, anthropic.NewTextBlock(b.Text))
			case "image":
				blocks = append(blocks, p.imageContentBlock(ctx, b.ImageMIME, b.ImageData))
			case "tool_use":

				blocks = append(blocks, toolUseBlockParam(b.ToolID, normalizeToolInputValue(b.Input), b.ToolName, b.CallerRaw))
			case "tool_result":
				if len(b.Images) > 0 && !b.IsError {
					content := make([]anthropic.ToolResultBlockParamContentUnion, 0, len(b.Images)+1)
					for _, img := range b.Images {
						var src anthropic.ImageBlockParamSourceUnion
						if fsrc, ok := p.filesImageSource(ctx, img.MIME, img.Base64); ok {
							src = fsrc
						} else {
							src = anthropic.ImageBlockParamSourceUnion{
								OfBase64: &anthropic.Base64ImageSourceParam{
									Data:      img.Base64,
									MediaType: anthropic.Base64ImageSourceMediaType(img.MIME),
								},
							}
						}
						content = append(content, anthropic.ToolResultBlockParamContentUnion{
							OfImage: &anthropic.ImageBlockParam{Source: src},
						})
					}
					if b.Output != "" {
						content = append(content, anthropic.ToolResultBlockParamContentUnion{
							OfText: &anthropic.TextBlockParam{Text: b.Output},
						})
					}
					blocks = append(blocks, anthropic.ContentBlockParamUnion{
						OfToolResult: &anthropic.ToolResultBlockParam{
							ToolUseID: b.ToolID,
							Content:   content,
						},
					})
				} else {
					blocks = append(blocks, anthropic.NewToolResultBlock(b.ToolID, b.Output, b.IsError))
				}
			case "thinking":

				if b.ThinkingSignature != "" {
					blocks = append(blocks, anthropic.NewThinkingBlock(b.ThinkingSignature, b.ReasoningContent))
				}
			case "redacted_thinking":

				if b.ThinkingData != "" {
					blocks = append(blocks, anthropic.NewRedactedThinkingBlock(b.ThinkingData))
				}
			}
		}

		toolResultOnly := false
		if m.Role == "user" {
			for _, b := range m.Content {
				if b.Type == "tool_result" && programmaticTools[b.ToolID] {
					toolResultOnly = true
					break
				}
			}
		}
		if toolResultOnly && len(blocks) != countToolResultBlocks(blocks) {
			filtered := make([]anthropic.ContentBlockParamUnion, 0, len(blocks))
			for _, blk := range blocks {
				if blk.OfToolResult != nil {
					filtered = append(filtered, blk)
				}
			}
			debug.Log("anthropic", "PTC: stripped %d non-tool_result block(s) from programmatic tool_result message", len(blocks)-len(filtered))
			blocks = filtered
		}
		param := anthropic.MessageParam{Role: anthropic.MessageParamRole(m.Role), Content: blocks}

		if !toolResultOnly && m.Role == "user" && len(systemBlocks) > 0 {
			newBlocks := make([]anthropic.ContentBlockParamUnion, 0, len(blocks)+len(systemBlocks))
			for i, sb := range systemBlocks {
				var text string
				if i == 0 {
					text = "[System]\n" + sb.text
				} else {
					text = sb.text
				}
				if i == len(systemBlocks)-1 {
					text += "\n[End System]"
				}
				block := anthropic.NewTextBlock(text)
				if block.OfText != nil && sb.cache {
					block.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				newBlocks = append(newBlocks, block)
			}
			systemBlocks = nil
			newBlocks = append(newBlocks, blocks...)
			param.Content = newBlocks
		}
		msgParams = append(msgParams, param)
	}

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: int64(p.effectiveMaxTokens()),
		Messages:  msgParams,
	}
	if len(systemBlocks) > 0 {
		for _, sb := range systemBlocks {
			block := anthropic.TextBlockParam{Text: sb.text}
			if sb.cache {
				block.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
			params.System = append(params.System, block)
		}
	}

	if hasPendingProgrammaticToolCalls(messages) {
		if id, ok := p.freshPTCContainer(); ok {
			debug.Log("anthropic", "PTC: reusing container %s for continuation", id)
			params.Container = anthropic.MessageCreateParamsContainerUnion{
				OfContainers: &anthropic.ContainerParams{ID: param.NewOpt(id)},
			}
		} else {
			debug.Log("anthropic", "PTC: pending programmatic tool calls but no live container; letting the API provision one")
		}
	}

	if o := p.samplingOverride.Load(); o != nil && len(o.StopSequences) > 0 {
		params.StopSequences = o.StopSequences
	}

	temp := p.temperature
	if o := p.samplingOverride.Load(); o != nil && o.Temperature > 0 {
		temp = o.Temperature
	}
	if temp > 0 {
		params.Temperature = param.NewOpt(temp)
	}

	if p.topP > 0 {
		params.TopP = param.NewOpt(p.topP)
	}

	effort := adaptiveEffort(p.reasoningEffort)
	budget := p.thinkingBudgetForEffort(p.reasoningEffort)
	useAdaptive := p.useAdaptiveThinking()
	switch {
	case useAdaptive && (budget > 0 || effort != ""):
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		}
	case !useAdaptive && budget > 0:
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
	}

	if p.effortCarrier.Load() && p.conversationEffort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffort(p.conversationEffort),
		}
	}

	if useAdaptive && params.OutputConfig.Effort == "" && p.effortCarrier.Load() {
		if p.adaptiveEffortLatch == "" && effort != "" {
			p.adaptiveEffortLatch = effort
		}
		if p.adaptiveEffortLatch != "" {
			params.OutputConfig = anthropic.OutputConfigParam{
				Effort: anthropic.OutputConfigEffort(p.adaptiveEffortLatch),
			}
		}
	}

	if len(tools) > 0 {
		toolParams := make([]anthropic.ToolUnionParam, len(tools))
		lastCacheable := -1
		for i, t := range tools {
			inputSchema := anthropic.ToolInputSchemaParam{
				Type: "object",
			}
			if json.Unmarshal(t.Parameters, &inputSchema) == nil {

			}
			desc := anthropic.String(t.Description)
			toolParams[i] = anthropic.ToolUnionParamOfTool(inputSchema, t.Name)
			if toolParams[i].OfTool != nil {
				toolParams[i].OfTool.Description = desc

				if p.strictTools[t.Name] {
					if _, ok := PrepareStrictToolSchema(t.Name, t.Parameters); ok {
						toolParams[i].OfTool.Strict = param.NewOpt(true)
						extra := toolParams[i].OfTool.InputSchema.ExtraFields
						if extra == nil {
							extra = make(map[string]any, 1)
						}
						extra["additionalProperties"] = false
						toolParams[i].OfTool.InputSchema.ExtraFields = extra
					}
				}

				if t.DeferLoading {
					toolParams[i].OfTool.DeferLoading = param.NewOpt(true)
				} else {
					lastCacheable = i
				}

				if p.ptcCodeExecutionEnabled() {
					if len(t.AllowedCallers) > 0 {
						toolParams[i].OfTool.AllowedCallers = t.AllowedCallers
					} else {
						toolParams[i].OfTool.AllowedCallers = []string{"direct", "code_execution_20260120"}
					}
				} else if len(t.AllowedCallers) > 0 {
					toolParams[i].OfTool.AllowedCallers = t.AllowedCallers
				}
			}
		}

		if lastCacheable >= 0 && toolParams[lastCacheable].OfTool != nil {
			toolParams[lastCacheable].OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		params.Tools = toolParams
	}

	if len(p.serverTools) > 0 {
		for i, st := range p.serverTools {
			var u anthropic.ToolUnionParam
			switch st.Type {
			case "web_search_20250305":
				u = anthropic.ToolUnionParam{OfWebSearchTool20250305: &anthropic.WebSearchTool20250305Param{}}
			case "web_fetch_20250910":
				u = anthropic.ToolUnionParam{OfWebFetchTool20250910: &anthropic.WebFetchTool20250910Param{}}
			case "tool_search_tool_regex":

				u = anthropic.ToolUnionParam{OfToolSearchToolRegex20251119: &anthropic.ToolSearchToolRegex20251119Param{
					Type: anthropic.ToolSearchToolRegex20251119TypeToolSearchToolRegex20251119,
				}}
			case "tool_search_tool_bm25":
				u = anthropic.ToolUnionParam{OfToolSearchToolBm25_20251119: &anthropic.ToolSearchToolBm25_20251119Param{
					Type: anthropic.ToolSearchToolBm25_20251119TypeToolSearchToolBm25_20251119,
				}}
			case "code_execution_20260120":

				u = anthropic.ToolUnionParam{OfCodeExecutionTool20260120: &anthropic.CodeExecutionTool20260120Param{}}
			default:
				debug.Log("anthropic", "unknown server tool type %q ignored", st.Type)
				continue
			}
			if i == len(p.serverTools)-1 {
				setToolUnionCacheControl(&u)
			}
			params.Tools = append(params.Tools, u)
		}
	}

	if p.memoryTool {
		u := anthropic.ToolUnionParam{OfMemoryTool20250818: &anthropic.MemoryTool20250818Param{}}
		if len(p.serverTools) == 0 {
			setToolUnionCacheControl(&u)
		}
		params.Tools = append(params.Tools, u)
	}

	if len(tools) > 0 {
		switch p.toolChoice {
		case "auto":
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		case "required":
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{},
			}
		case "none":
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfNone: &anthropic.ToolChoiceNoneParam{},
			}
		}
	}

	return params
}

func serverToolUseRaw(id, name string, input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	raw, err := json.Marshal(struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}{Type: "server_tool_use", ID: id, Name: name, Input: input})
	if err != nil {
		return nil
	}
	return raw
}

func serverToolBlockParam(raw json.RawMessage) (*anthropic.ContentBlockParamUnion, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case "server_tool_use":
		var b anthropic.ServerToolUseBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		param := b.ToParam()
		return &anthropic.ContentBlockParamUnion{OfServerToolUse: &param}, nil
	case "web_search_tool_result":
		var b anthropic.WebSearchToolResultBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		param := b.ToParam()
		return &anthropic.ContentBlockParamUnion{OfWebSearchToolResult: &param}, nil
	case "tool_search_tool_result":
		var ts anthropic.ToolSearchToolResultBlockParam
		if err := json.Unmarshal(raw, &ts); err != nil {
			return nil, err
		}
		return &anthropic.ContentBlockParamUnion{OfToolSearchToolResult: &ts}, nil
	case "web_fetch_tool_result":
		var b anthropic.WebFetchToolResultBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		param := b.ToParam()
		return &anthropic.ContentBlockParamUnion{OfWebFetchToolResult: &param}, nil
	case "code_execution_tool_result":
		var b anthropic.CodeExecutionToolResultBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		param := b.ToParam()
		return &anthropic.ContentBlockParamUnion{OfCodeExecutionToolResult: &param}, nil
	}
	return nil, fmt.Errorf("unknown server tool block type %q", probe.Type)
}

func setToolUnionCacheControl(u *anthropic.ToolUnionParam) {
	switch {
	case u.OfWebSearchTool20250305 != nil:
		u.OfWebSearchTool20250305.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case u.OfWebFetchTool20250910 != nil:
		u.OfWebFetchTool20250910.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case u.OfMemoryTool20250818 != nil:
		u.OfMemoryTool20250818.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case u.OfToolSearchToolRegex20251119 != nil:
		u.OfToolSearchToolRegex20251119.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case u.OfToolSearchToolBm25_20251119 != nil:
		u.OfToolSearchToolBm25_20251119.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case u.OfCodeExecutionTool20260120 != nil:
		u.OfCodeExecutionTool20260120.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
}

func convertAnthropicResponse(blocks []anthropic.ContentBlockUnion) []ContentBlock {
	result := make([]ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			result = append(result, TextBlock(b.Text))
		case "tool_use":
			tub := ToolUseBlock(b.ID, b.Name, b.Input)
			tu := b.AsToolUse()
			if tu.JSON.Caller.Valid() {
				tub.CallerRaw = callerRawOf(tu.Caller)
			}
			result = append(result, tub)
		case "thinking":
			tb := b.AsThinking()
			result = append(result, ContentBlock{
				Type:              "thinking",
				ReasoningContent:  tb.Thinking,
				ThinkingSignature: tb.Signature,
			})
		case "redacted_thinking":
			rb := b.AsRedactedThinking()
			result = append(result, ContentBlock{
				Type:         "redacted_thinking",
				ThinkingData: rb.Data,
			})
		case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result", "tool_search_tool_result", "code_execution_tool_result":

			result = append(result, ContentBlock{Type: b.Type, Raw: json.RawMessage(b.RawJSON())})
		}
	}
	return result
}

func anthropicStopReasonError(reason string) error {
	switch reason {
	case "end_turn", "tool_use", "stop_sequence", "pause_turn":
		return nil
	case "max_tokens":
		return fmt.Errorf("anthropic stream ended with stop_reason=max_tokens (output truncated)")
	case "refusal":
		return fmt.Errorf("anthropic stream ended with stop_reason=refusal (content filtered)")
	default:
		return fmt.Errorf("anthropic stream ended with stop_reason=%s", reason)
	}
}

func (p *AnthropicProvider) SetContextEditing(cfg *ContextEditingConfig) {
	p.contextEditing.Store(cfg)
	if p.transport == nil {
		return
	}
	hdrs := p.transport.snapshotHeaders()
	beta := toggleBetaToken(hdrs.Get("anthropic-beta"), anthropicContextManagementBeta, cfg != nil)
	if beta != "" {
		hdrs.Set("anthropic-beta", beta)
	} else {
		hdrs.Del("anthropic-beta")
	}
	p.transport.UpdateHeaders(hdrs)
}

func toggleBetaToken(header, token string, add bool) string {
	var kept []string
	for _, part := range strings.Split(header, ",") {
		if t := strings.TrimSpace(part); t != "" && t != token {
			kept = append(kept, t)
		}
	}
	if add {
		kept = append(kept, token)
	}
	return strings.Join(kept, ",")
}

func (p *AnthropicProvider) contextEditingOptions() []option.RequestOption {
	payload := contextManagementPayload(p.contextEditing.Load())
	if payload == nil {
		return nil
	}
	return []option.RequestOption{option.WithJSONSet("context_management", payload)}
}

func callerRawOf(caller anthropic.ToolUseBlockCallerUnion) json.RawMessage {
	raw := caller.RawJSON()
	if raw == "" || raw == "null" {
		return nil
	}
	return json.RawMessage(raw)
}

func isProgrammaticCaller(callerRaw json.RawMessage) bool {
	if len(callerRaw) == 0 {
		return false
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(callerRaw, &probe); err != nil {
		return false
	}
	return strings.HasPrefix(probe.Type, "code_execution")
}

func toolUseBlockParam(id string, input any, name string, callerRaw json.RawMessage) anthropic.ContentBlockParamUnion {
	if len(callerRaw) == 0 {
		return anthropic.NewToolUseBlock(id, input, name)
	}
	p := anthropic.ToolUseBlockParam{ID: id, Input: input, Name: name}
	var c struct {
		Type   string `json:"type"`
		ToolID string `json:"tool_id"`
	}
	if err := json.Unmarshal(callerRaw, &c); err == nil {
		switch c.Type {
		case "direct":
			p.Caller = anthropic.ToolUseBlockParamCallerUnion{OfDirect: &anthropic.DirectCallerParam{}}
		case "code_execution_20250825":
			if c.ToolID != "" {
				p.Caller = anthropic.ToolUseBlockParamCallerUnion{OfCodeExecution20250825: &anthropic.ServerToolCallerParam{ToolID: c.ToolID}}
			}
		case "code_execution_20260120":
			if c.ToolID != "" {
				p.Caller = anthropic.ToolUseBlockParamCallerUnion{OfCodeExecution20260120: &anthropic.ServerToolCaller20260120Param{ToolID: c.ToolID}}
			}
		}
	}
	return anthropic.ContentBlockParamUnion{OfToolUse: &p}
}

func hasPendingProgrammaticToolCalls(messages []Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		switch m.Role {
		case "assistant":
			for _, b := range m.Content {
				if b.Type == "tool_use" && isProgrammaticCaller(b.CallerRaw) {
					return true
				}
			}
			return false
		case "user":
			for _, b := range m.Content {
				if b.Type == "tool_result" {
					return false
				}
			}
		}
	}
	return false
}

func countToolResultBlocks(blocks []anthropic.ContentBlockParamUnion) int {
	n := 0
	for _, blk := range blocks {
		if blk.OfToolResult != nil {
			n++
		}
	}
	return n
}
