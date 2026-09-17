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
	"sync/atomic"
)

// AnthropicProvider implements Provider using the Anthropic SDK.
type AnthropicProvider struct {
	client           anthropic.Client
	model            string
	maxTokens        int
	samplingOverride atomic.Pointer[SamplingOverride] // #2248: reader-side race-free override
	cap              *adaptiveCap
	transport        *headerInjectingTransport            // kept for runtime header updates
	calibrator       *tokenCountCalibrator                // periodic real-API token calibration
	files            *fileUploader                        // Anthropic Files API (large-image file_id referencing)
	reasoningEffort  string                               // "", "low", "medium", "high", "xhigh", "max" — maps to thinking budget
	toolChoice       string                               // "", "auto", "required", "none" — maps to Anthropic tool_choice
	temperature      float64                              // 0 = provider default
	topP             float64                              // 0 = provider default
	serverTools      []ServerToolConfig                   // Anthropic server-side tools (web_search/web_fetch), executed in-API
	memoryTool       bool                                 // Anthropic Memory Tool (memory_20250818): declared here, executed agent-side
	thinkingMode     string                               // "", "manual", "adaptive" — thinking carrier override ("" = auto-detect from model)
	contextEditing   atomic.Pointer[ContextEditingConfig] // server-side context editing (beta)
	strictTools      map[string]bool                      // strict tool use allowlist (empty = disabled)

	// Top-level effort carrier (output_config.effort, GA effort parameter).
	// Cache-aware per Anthropic's 2026 effort guidance: a top-level effort
	// change does not preserve cached prefixes, so the carrier is attached
	// only once a level stabilizes across consecutive requests — per-turn
	// adaptive-effort oscillation never touches it (see beginEffortTracking).
	effortCarrier      atomic.Bool // true until the endpoint rejects output_config
	lastCallEffort     string      // effort level observed on the previous request
	conversationEffort string      // effort level established for the cached prefix
}

// ModelName returns the current model name used by this provider.
func (p *AnthropicProvider) ModelName() string { return p.model }

// CloneWithModel returns a shallow copy of this provider with a different model.
func (p *AnthropicProvider) CloneWithModel(model string) Provider {
	clone := &AnthropicProvider{
		client:    p.client,
		model:     model,
		maxTokens: p.maxTokens,
		// #1603: re-key the adaptive cap for the NEW model - sharing the
		// parent's learned cap pointer mixed per-model state across the
		// registry's carefully-partitioned keys.
		cap:             AdaptiveCapForModelSwap(p.cap, model, p.maxTokens),
		transport:       p.transport,
		calibrator:      p.calibrator,
		reasoningEffort: p.reasoningEffort,
		toolChoice:      p.toolChoice,
		strictTools:     p.strictTools,
		temperature:     p.temperature,
		topP:            p.topP,
		serverTools:     p.serverTools,
		thinkingMode:    p.thinkingMode,
	}
	// Inherit the endpoint capability latch (an endpoint that rejected
	// output_config stays off), but reset the per-conversation stability
	// window: the clone re-learns effort stabilization for its own cache
	// prefix over its first two requests.
	clone.effortCarrier.Store(p.effortCarrier.Load())
	return clone
}

// SetReasoningEffort sets the reasoning effort. It maps to Anthropic's
// extended thinking budget_tokens parameter ("low" ~5K, "medium" ~16K,
// "high" ~32K) and, once the level stabilizes, to the top-level
// output_config.effort carrier ("xhigh"/"max" are carrier-only). Empty
// string disables both.
func (p *AnthropicProvider) SetReasoningEffort(effort string) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max":
		p.reasoningEffort = effort
	}
}

func (p *AnthropicProvider) ReasoningEffort() string { return p.reasoningEffort }

// SetThinkingMode overrides the extended-thinking carrier: "manual" forces
// budget_tokens, "adaptive" forces thinking:{type:"adaptive"} (Claude 4.6+/
// 5.x only — older models reject it with 400), "" (default) auto-detects
// per model. Opt in per endpoint via config (`thinking_mode: adaptive`).
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

// ThinkingMode returns the configured thinking-mode override ("" = auto).
func (p *AnthropicProvider) ThinkingMode() string { return p.thinkingMode }

// adaptiveThinkingModels matches Claude generations that support adaptive
// thinking (thinking:{type:"adaptive"}): 4.6+ and the 5.x families. The
// extended-thinking-only generation (Sonnet/Opus/Haiku 4.5 and earlier)
// rejects it with 400. Unknown future families fall back to manual mode;
// users can override per endpoint with thinking_mode: adaptive.
// https://platform.claude.com/docs/en/build-with-claude/extended-thinking
var adaptiveThinkingModels = regexp.MustCompile(`(?i)claude-(?:opus|sonnet|haiku)-4-[6-9]|claude-(?:opus|sonnet|haiku|fable|mythos)-[5-9]|claude-[5-9]`)

// adaptiveThinkingForModel reports whether the model natively supports
// adaptive thinking.
func adaptiveThinkingForModel(model string) bool {
	return adaptiveThinkingModels.MatchString(strings.ToLower(strings.TrimSpace(model)))
}

// useAdaptiveThinking selects the thinking carrier for this provider:
// explicit override wins, otherwise auto-detect from the model name.
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

// adaptiveEffort maps a reasoning effort level to the output_config.effort
// value used by adaptive-thinking models (their only depth control —
// budget_tokens is forbidden there). Returns "" for empty/unknown levels.
func adaptiveEffort(effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return effort
	default:
		return ""
	}
}

// anthropicBetaHeader returns the anthropic-beta header value to attach to
// tool-using manual-mode requests. budget_tokens thinking does not interleave
// with tool use unless this beta is set; the API ignores the header on models
// where it does not apply. Adaptive models interleave natively (no header).
// https://platform.claude.com/docs/en/build-with-claude/extended-thinking#interleaved-thinking
func (p *AnthropicProvider) anthropicBetaHeader(hasTools bool) string {
	if !hasTools || p.useAdaptiveThinking() {
		return ""
	}
	if p.thinkingBudgetForEffort(p.reasoningEffort) > 0 {
		return "interleaved-thinking-2025-05-14"
	}
	return ""
}

// betaHeaderOpts wraps the beta header into SDK request options for the
// per-call sites (Messages.New / Messages.NewStreaming).
func (p *AnthropicProvider) betaHeaderOpts(hasTools bool) []option.RequestOption {
	if h := p.anthropicBetaHeader(hasTools); h != "" {
		return []option.RequestOption{option.WithHeader("anthropic-beta", h)}
	}
	return nil
}

// SetMaxTokens implements provider.MaxTokensSetter (#1592-A).
func (p *AnthropicProvider) SetMaxTokens(n int) {
	if n > 0 {
		p.maxTokens = n
	}
}

// beginEffortTracking updates the cache-aware effort-carrier bookkeeping
// for this request. Returns whether buildParams should attach
// output_config.effort.
//
// Hysteresis (Anthropic effort guidance, 2026): hold the top-level effort
// constant within a conversation. A level is adopted as the carrier only
// after TWO consecutive requests at the same level — the adaptive-effort
// adapter sets a level before each call and restores the previous level
// right after, so its oscillation never stabilizes and never reaches the
// carrier (budget_tokens alone modulates per-turn thinking, which is
// cache-neutral). A user switch (/effort, config) persists across calls
// and re-establishes the carrier on the second call: one deliberate,
// one-time cache rewrite instead of a storm.
func (p *AnthropicProvider) beginEffortTracking() bool {
	if !p.effortCarrier.Load() {
		return false
	}
	effort := strings.ToLower(strings.TrimSpace(p.reasoningEffort))
	if effort == "" {
		// Effort off (or adaptive restored its previous level): reset the
		// stability window so oscillation can never establish a carrier.
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

// SetToolChoice sets the tool_choice parameter: "auto" (model decides),
// "required" (force tool use), "none" (disable tools), or "" (API default).
func (p *AnthropicProvider) SetToolChoice(choice string) {
	p.toolChoice = strings.ToLower(strings.TrimSpace(choice))
}

// SetStrictTools implements StrictToolsSetter: allowlisted tools are sent with
// Anthropic's structured-outputs `strict: true` (grammar-constrained tool
// input). Non-allowlisted tools keep today's serialization (ToolParam.Strict
// is omitzero, so nothing changes on the wire).
func (p *AnthropicProvider) SetStrictTools(allow map[string]bool) {
	p.strictTools = allow
}

func (p *AnthropicProvider) ToolChoice() string { return p.toolChoice }

// SetServerTools implements provider.ServerToolsSetter: declarative Anthropic
// server-side tools (web_search/web_fetch) executed inside the API. Results
// arrive in-band as server_tool_use/web_search_tool_result blocks and are
// echoed back verbatim on subsequent requests.
func (p *AnthropicProvider) SetServerTools(tools []ServerToolConfig) {
	p.serverTools = tools
}

// SetMemoryTool enables the Anthropic Memory Tool declaration
// (memory_20250818). Unlike server tools, memory is client-executed: the
// agent's handler (internal/agent/memory_tool.go) fulfills the model's
// tool_use calls against a local /memories store. Opt in per endpoint via
// config (`memory_tool: true`).
func (p *AnthropicProvider) SetMemoryTool(enabled bool) { p.memoryTool = enabled }

// MemoryToolEnabled reports whether requests carry the memory tool
// declaration; the agent probes this to install its client-side handler.
func (p *AnthropicProvider) MemoryToolEnabled() bool { return p.memoryTool }

// SetTemperature sets the sampling temperature. 0 means "use provider default".
func (p *AnthropicProvider) SetTemperature(temp float64) { p.temperature = temp }

// #2271 follow-up: StopSequenceSetter (Set/Get) is removed - the
// per-call stop sequences ride the sampling override exclusively
// since #2248/#2266 and no production caller set the field.

// SetSamplingOverride implements provider.SamplingOverrideSetter (#2248).
func (p *AnthropicProvider) SetSamplingOverride(o *SamplingOverride) { p.samplingOverride.Store(o) }

// SamplingOverride implements provider.SamplingOverrideSetter (#2248).
func (p *AnthropicProvider) SamplingOverride() *SamplingOverride { return p.samplingOverride.Load() }
func (p *AnthropicProvider) Temperature() float64                { return p.temperature }

// SetTopP sets the nucleus sampling parameter. 0 means "use provider default".
func (p *AnthropicProvider) SetTopP(topP float64) { p.topP = topP }
func (p *AnthropicProvider) TopP() float64        { return p.topP }

// SetAdaptiveCap installs the adaptive max-output-tokens cap.
func (p *AnthropicProvider) SetAdaptiveCap(c *adaptiveCap) { p.cap = c }

// probeChat sends a single messages request without retry or adaptive
// cap tracking. Used by context window probing.
func (p *AnthropicProvider) probeChat(ctx context.Context, messages []Message) error {
	params := p.buildParams(ctx, messages, nil)
	_, err := p.client.Messages.New(ctx, params)
	return err
}

func (p *AnthropicProvider) effectiveMaxTokens() int {
	if o := p.samplingOverride.Load(); o != nil && o.MaxTokens > 0 {
		return o.MaxTokens // #2248: active sampling window wins
	}
	if p.cap != nil {
		if v := p.cap.Get(); v > 0 {
			return v
		}
	}
	return p.maxTokens
}

// thinkingBudgetForEffort converts a reasoning effort level to an Anthropic
// thinking budget_tokens value. Returns 0 if thinking should be disabled.
// Anthropic API requires budget_tokens >= 1024 and max_tokens > budget_tokens.
func (p *AnthropicProvider) thinkingBudgetForEffort(effort string) int64 {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return 0
	}
	maxTok := p.effectiveMaxTokens()
	if maxTok <= 1024 {
		return 0 // not enough room for thinking
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

// isThinkingError reports whether an API error is a genuine "model does
// not support / misconfigured extended thinking" parameter error.
// #2115: this used to be a bare substring match on "thinking"/
// "budget_tokens" over the WHOLE message, so unrelated errors mentioning
// the words anywhere - upstream 5xx "error while streaming thinking
// blocks", a gateway error whose MODEL NAME contains "thinking", or
// "429 quota exceeded for budget_tokens plan" - were misjudged, stripped
// thinking, and retried; the retry often succeeded and the session silently
// ran without reasoning from then on. Now:
//   - when a status code is extractable (anthropic SDK apierror) it must
//     be a 4xx parameter class (400/404/422);
//   - the message must contain an ANCHORED phrase from real
//     thinking-rejection errors, not the bare word anywhere in the text.
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

// thinkingErrorAnchors are phrases from real API thinking-rejection
// errors (Anthropic direct and gateway-stringified variants).
var thinkingErrorAnchors = []string{
	"does not support thinking",
	"thinking is not supported",
	"thinking is not enabled",
	"thinking is not available",
	"does not support extended thinking",
	"budget_tokens must",
	"budget_tokens is",
	"budget_tokens: ", // gateway field-error shape: "budget_tokens: ..."
	"thinking parameter",
	"max_tokens must be greater than budget_tokens",
}

// effortErrorAnchors are phrases from real output_config/effort rejection
// errors (Anthropic direct and gateway-stringified variants).
var effortErrorAnchors = []string{
	"output_config", // unknown-parameter and field-error shapes
	"per-turn effort",
	"per-message effort",
	"does not support effort",
	"effort is not supported",
}

// isEffortError reports whether an API error is a genuine rejection of the
// top-level output_config effort carrier — e.g. an Anthropic-compatible
// gateway that predates the parameter. Mirrors isThinkingError: status must
// be a 4xx parameter class and the message must contain an anchored phrase.
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

// asStatusCode extracts an HTTP status code from a provider error when
// the concrete type exposes one (the anthropic SDK's apierror.Error does).
func asStatusCode(err error) (int, bool) {
	var sc interface{ StatusCode() int }
	if errors.As(err, &sc) {
		return sc.StatusCode(), true
	}
	return 0, false
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(apiKey string, model string, maxTokens int) *AnthropicProvider {
	return newAnthropicProvider(apiKey, model, maxTokens, "")
}

// NewAnthropicProviderWithBaseURL creates a new Anthropic provider with a custom base URL.
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
	// OpenRouter-specific headers for attribution and ranking.
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
		option.WithMaxRetries(0), // retry handled by our outer loop, not SDK
	}
	// Inject identity headers from impersonation state or protocol defaults.
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

// UpdateRuntimeHeaders updates the injected headers at runtime.
func (p *AnthropicProvider) UpdateRuntimeHeaders(headers http.Header) {
	if p.transport != nil {
		p.transport.UpdateHeaders(headers)
	}
}

// RateLimitInfo returns the latest rate-limit status parsed from response headers.
func (p *AnthropicProvider) RateLimitInfo() RateLimitInfo {
	if p.transport != nil && p.transport.rateLimits != nil {
		return p.transport.rateLimits.Snapshot()
	}
	return RateLimitInfo{RemainingRequests: -1, RemainingTokens: -1, LimitRequests: -1, LimitTokens: -1}
}

// SetSessionID injects the session ID into outgoing requests via a custom
// HTTP header (GGCode-SessionID).
func (p *AnthropicProvider) SetSessionID(sessionID string) {
	if sessionID == "" || p.transport == nil {
		return
	}
	existing := p.transport.snapshotHeaders()
	existing.Set("GGCode-SessionID", sessionID)
	p.transport.UpdateHeaders(existing)
}

func (p *AnthropicProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	debug.Log("anthropic", "Chat START model=%s msgs=%d tools=%d", p.model, len(messages), len(tools))
	p.beginEffortTracking()
	params := p.buildParams(ctx, messages, tools)
	callOpts := p.betaHeaderOpts(len(tools) > 0)

	var resp *anthropic.Message
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.client.Messages.New(ctx, params, callOpts...)
		resp, callErr = p.client.Messages.New(ctx, params, p.contextEditingOptions()...)
		return callErr
	}, providerRetryAttempts)
	// Retry once without extended thinking if the model rejects it
	// (manual budget_tokens and adaptive alike).
	if err != nil && (params.Thinking.OfEnabled != nil || params.Thinking.OfAdaptive != nil) && isThinkingError(err) {
		debug.Log("anthropic", "Chat: retrying without extended thinking (model rejected thinking parameters)")
		params.Thinking = anthropic.ThinkingConfigParamUnion{}
		callOpts = nil
		err = retryWithBackoffCtx(ctx, func() error {
			var callErr error
			resp, callErr = p.client.Messages.New(ctx, params, callOpts...)
			resp, callErr = p.client.Messages.New(ctx, params, p.contextEditingOptions()...)
			return callErr
		}, providerRetryAttempts)
	}
	// Retry once without the effort carrier if the endpoint rejects
	// output_config (Anthropic-compatible gateways predating the parameter).
	// The latch keeps effort semantics on budget_tokens for the session.
	if err != nil && isEffortError(err) && p.effortCarrier.CompareAndSwap(true, false) {
		debug.Log("anthropic", "Chat: retrying without output_config (endpoint rejected the effort carrier)")
		params.OutputConfig = anthropic.OutputConfigParam{}
		err = retryWithBackoffCtx(ctx, func() error {
			var callErr error
			resp, callErr = p.client.Messages.New(ctx, params, callOpts...)
			resp, callErr = p.client.Messages.New(ctx, params, p.contextEditingOptions()...)
			return callErr
		}, providerRetryAttempts)
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

	// Surface server-side context edits so long-session token savings are
	// observable (context_management.applied_edits, beta response block).
	if p.contextEditing.Load() != nil {
		if summary, ok := parseAppliedEdits([]byte(resp.RawJSON())); ok {
			debug.Log("anthropic", "Chat %s", summary)
		}
	}

	msg := convertAnthropicResponse(resp.Content)
	usage := anthropicUsage(resp.Usage)

	// #1484-C: relay the real stop reason (SDK field; nil on some
	// backends) so callers stop guessing from usage numbers.
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

		var usage *TokenUsage
		var outputChars int
		var truncated bool
		streamError := false       // set when a non-retryable error was sent to ch
		budget := newRetryBudget() // #722: cap cumulative retry backoff sleep per stream call

		for attempt := 0; attempt < providerRetryAttempts; attempt++ {
			if attempt > 0 {
				debug.Log("anthropic", "Stream retry attempt %d", attempt)
			}

			toolCalls := make(map[int]*ToolCallDelta)
			var inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int
			emitted := false
			retry := false
			// #577(C): reset truncated per attempt — attempt 1 could observe
			// stop_reason=max_tokens and then die to a retryable error; a
			// complete attempt 2 must not inherit Truncated=true. Mirrors the
			// gemini.go per-attempt declaration (#561-C) and openai.go (#577-B).
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
						cb := event.ContentBlock
						switch cb.Type {
						case "tool_use":
							idx := int(event.Index)
							tc := &ToolCallDelta{Index: idx, ID: cb.ID, Name: cb.Name}
							toolCalls[idx] = tc
							debug.Log("anthropic", "content_block_start tool_use id=%s name=%s idx=%d", cb.ID, cb.Name, idx)
						case "server_tool_use":
							// Anthropic server-side tool invocation (executed in-API).
							// Input arrives via input_json_delta like a client tool_use,
							// but the block must NOT be surfaced as a client tool call —
							// it is emitted verbatim at content_block_stop.
							idx := int(event.Index)
							toolCalls[idx] = &ToolCallDelta{Index: idx, ID: cb.ID, Name: cb.Name, ServerTool: true}
						case "web_search_tool_result", "web_fetch_tool_result":
							// Result blocks arrive complete (no deltas). Keep the raw
							// JSON verbatim for echo-back on the next request.
							emitted = true
							ch <- StreamEvent{
								Type:  StreamEventServerTool,
								Block: ContentBlock{Type: cb.Type, Raw: json.RawMessage(cb.RawJSON())},
							}
						case "thinking":
							debug.Log("anthropic", "content_block_start thinking idx=%d sig_len=%d", event.Index, len(cb.Signature))
							toolCalls[int(event.Index)] = &ToolCallDelta{
								Index: int(event.Index),
								ID:    cb.Signature, // carries signature for echo-back
							}
							// Emit reasoning event with signature so agent can store it
							emitted = true
							ch <- StreamEvent{Type: StreamEventReasoning, ThinkingSignature: cb.Signature}
						case "redacted_thinking":
							debug.Log("anthropic", "content_block_start redacted_thinking idx=%d data_len=%d", event.Index, len(cb.Data))
							// Register with empty Name (like the thinking branch)
							// so content_block_stop's `tc.Name != ""` check skips
							// it — redacted thinking is reasoning data, not a
							// tool call. Echo-back happens via the reasoning
							// event below (#224).
							toolCalls[int(event.Index)] = &ToolCallDelta{
								Index: int(event.Index),
								ID:    cb.Data, // carries redacted data for echo-back
							}
							// Emit reasoning event with redacted data for echo-back
							emitted = true
							ch <- StreamEvent{Type: StreamEventReasoning, Text: "__redacted_thinking__", ThinkingSignature: cb.Data}
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
						idx := int(event.Index)
						if tc, ok := toolCalls[idx]; ok && tc.ServerTool {
							debug.Log("anthropic", "content_block_stop server_tool_use id=%s name=%s", tc.ID, tc.Name)
							emitted = true
							ch <- StreamEvent{
								Type: StreamEventServerTool,
								Block: ContentBlock{
									Type: "server_tool_use",
									ID:   tc.ID,
									Raw:  serverToolUseRaw(tc.ID, tc.Name, tc.Arguments),
								},
							}
							delete(toolCalls, idx)
						} else if tc, ok := toolCalls[idx]; ok && tc.Name != "" {
							debug.Log("anthropic", "content_block_stop tool_call id=%s name=%s args=%s", tc.ID, tc.Name, string(tc.Arguments))
							outputChars += len(tc.Name) + len(tc.Arguments)
							emitted = true
							ch <- StreamEvent{
								Type: StreamEventToolCallDone,
								Tool: *tc,
							}
							delete(toolCalls, idx)
						}

					case "message_delta":
						// #2129: symmetric zero-guard with the input/cache tokens
						// below (#722/#1168): the SSE protocol allows MULTIPLE
						// message_delta events (final value in the last one) - a
						// gateway replaying/synthesizing a trailing delta with an
						// all-zero Usage zeroed the accumulated output count,
						// skewing cost/context stats low while input stayed
						// guarded.
						if event.Usage.OutputTokens > 0 {
							outputTokens = int(event.Usage.OutputTokens)
						}
						// message_delta in the Anthropic SSE protocol only carries
						// output_tokens reliably. input_tokens here is often 0 or
						// just the non-cached portion. Do NOT overwrite inputTokens
						// (and cache tokens) from message_start unless message_delta
						// actually provides a non-zero value.
						if event.Usage.InputTokens > 0 && inputTokens == 0 {
							inputTokens = int(event.Usage.InputTokens)
						}
						if event.Usage.CacheCreationInputTokens > 0 {
							cacheWriteTokens = int(event.Usage.CacheCreationInputTokens)
						}
						if event.Usage.CacheReadInputTokens > 0 {
							cacheReadTokens = int(event.Usage.CacheReadInputTokens)
						}
						// Surface server-side context edits applied by
						// context_management so the user sees what was cleared.
						if p.contextEditing.Load() != nil {
							if summary, ok := parseAppliedEdits([]byte(event.RawJSON())); ok {
								ch <- StreamEvent{Type: StreamEventSystem, Text: "[" + summary + "] "}
							}
						}
						// Check stop_reason for truncation / policy errors.
						if stopReason := string(event.Delta.StopReason); stopReason != "" {
							debug.Log("anthropic", "stop_reason=%s", stopReason)
							if stopReason == "max_tokens" {
								// Output was truncated — NOT an error. Keep partial content.
								p.cap.OnTruncated()
								truncated = true
							} else if stopErr := anthropicStopReasonError(stopReason); stopErr != nil {
								ch <- StreamEvent{Type: StreamEventError, Error: stopErr}
								streamError = true
								return
							}
						}

					case "message_start":
						// #722: same non-zero guard as message_delta above — an
						// out-of-order (protocol-violating) stream must not zero out
						// input tokens already counted.
						if event.Message.Usage.InputTokens > 0 && inputTokens == 0 {
							inputTokens = int(event.Message.Usage.InputTokens)
						}
						debug.Log("anthropic", "message_start usage: input_tokens=%d output_tokens=%d cache_read=%d cache_write=%d",
							event.Message.Usage.InputTokens, event.Message.Usage.OutputTokens,
							event.Message.Usage.CacheReadInputTokens, event.Message.Usage.CacheCreationInputTokens)
						// #1168: same non-zero guard for cache counters - a
						// duplicate or out-of-order message_start (retried or
						// resumed stream) carrying a zeroed Usage must not
						// wipe cache tokens already accumulated via
						// message_delta, matching the #722 inputTokens guard.
						if event.Message.Usage.CacheCreationInputTokens > 0 {
							cacheWriteTokens = int(event.Message.Usage.CacheCreationInputTokens)
						}
						if event.Message.Usage.CacheReadInputTokens > 0 {
							cacheReadTokens = int(event.Message.Usage.CacheReadInputTokens)
						}
					}
				}

				if err := stream.Err(); err != nil {
					debug.Log("anthropic", "Stream ERROR: %v", err)
					if rejected, parsed := maxTokensRejection(err); rejected {
						p.cap.OnRejected(parsed)
					}
					// Retry without extended thinking if the model rejects it
					// (manual budget_tokens and adaptive alike).
					if !emitted && (params.Thinking.OfEnabled != nil || params.Thinking.OfAdaptive != nil) && isThinkingError(err) && attempt < providerRetryAttempts-1 {
						debug.Log("anthropic", "Stream: retrying without extended thinking (model rejected thinking parameters)")
						// #2115: the downgrade must be VISIBLE - the user set a
						// reasoning effort and silently losing it for the rest of
						// the session looked like the model just being dumb.
						ch <- StreamEvent{Type: StreamEventSystem, Text: "[Model rejected extended thinking - retrying without it] "}
						params.Thinking = anthropic.ThinkingConfigParamUnion{}
						retry = true
						return
					}
					// Retry without the effort carrier if the endpoint rejects it.
					if !emitted && isEffortError(err) && p.effortCarrier.CompareAndSwap(true, false) {
						debug.Log("anthropic", "Stream: retrying without output_config (endpoint rejected the effort carrier)")
						ch <- StreamEvent{Type: StreamEventSystem, Text: "[Endpoint rejected output_config effort - retrying without it] "}
						params.OutputConfig = anthropic.OutputConfigParam{}
						retry = true
						return
					}
					// Retry if no content has been emitted yet and the error is retryable.
					if !emitted && isRetryableForContext(ctx, err) && attempt < providerRetryAttempts-1 {
						// Notify user about retry
						delay := retryDelay(err, attempt)
						ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, providerRetryAttempts, delay)}
						if sleepErr := budget.sleep(ctx, delay); sleepErr != nil {
							// #722: budget exhausted — stop retrying now; wrap with the
							// sentinel so the failover layer switches immediately.
							if sleepErr == errRetryBudgetExhausted {
								sleepErr = fmt.Errorf("%w: %w", errRetryBudgetExhausted, err)
							}
							ch <- StreamEvent{Type: StreamEventError, Error: sleepErr}
							streamError = true
							return
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

			// Stream completed successfully.
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

		// #602(R5): the "retries exhausted" guard that used to live here was
		// unreachable dead code — every iteration of the attempt loop above
		// ends in `continue` (retry), `return` (streamError sent), or the
		// unconditional usage assignment plus the single `break`, so the loop
		// can never exit with usage == nil. The defensive estimate fallback
		// below is kept for symmetry with openai.go/gemini.go.
		if usage == nil {
			// Fallback: estimate InputTokens from the messages themselves,
			// same as the OpenAI provider does. Without this, InputTokens=0
			// would cause RecordUsage to collapse the baseline to just
			// OutputTokens, making the TUI context usage display absurdly small.
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

	// Fast path: if calibrator is nil or disabled, return local estimate.
	if p.calibrator == nil {
		return estimated, nil
	}

	// Check if we should trigger a calibration.
	p.calibrator.mu.Lock()
	needCalibration := p.calibrator.shouldCalibrate()
	isFirst := p.calibrator.lastCalibrate.IsZero()
	p.calibrator.mu.Unlock()

	if !needCalibration {
		// Apply the learned ratio to the local estimate.
		return int(float64(estimated) * p.calibrator.currentRatio()), nil
	}

	// First calibration: synchronous with a longer timeout.
	if isFirst {
		calCtx, cancel := context.WithTimeout(ctx, calibrateFirstTimeout)
		defer cancel()
		// #1795 case 1: the guard sat AFTER the remote call - the skip
		// branch neither applied the result nor recorded a failure, so
		// lastCalibrate stayed zero, shouldCalibrate was ALWAYS true, and
		// every CountTokens made a synchronous 2s remote call (competing
		// with the main API for rate limit) whose precise value was then
		// DISCARDED for the local estimate. Guard first; when skipping,
		// mark the calibration as done so the cadence holds.
		// #1618-A: skip asymmetric samples - the local estimator counts
		// only text, but image/tool_result-attachment/thinking blocks all
		// land in the remote count. Feeding such a sample pinned the
		// ratio at the 3.0 clamp for the whole visual session.
		if messagesContainNonTextBlocks(messages) {
			debug.Log("provider-calibrator", "first calibration skipped: non-text blocks (image/thinking) make the sample asymmetric")
			p.calibrator.recordSkip()
			return estimated, nil
		}
		realTokens, err := p.remoteCountTokens(calCtx, messages)
		if err != nil {
			debug.Log("provider-calibrator", "first calibration failed: %v", err)
			// Transient failures (429/5xx/network) back off instead of retrying
			// a synchronous remote attempt on every call (#708); permanent
			// 404/403 errors disable inside remoteCountTokens.
			p.calibrator.recordFailure()
			return estimated, nil
		}
		p.calibrator.applyResult(estimated, realTokens)
		debug.Log("provider-calibrator", "first calibration OK: estimated=%d real=%d ratio=%.3f", estimated, realTokens, p.calibrator.currentRatio())
		return realTokens, nil
	}

	// Subsequent calibrations: async, non-blocking.
	// Return ratio-adjusted estimate immediately, update ratio in background.
	result := int(float64(estimated) * p.calibrator.currentRatio())
	safego.Go("provider.calibrateTokens", func() {
		calCtx, cancel := context.WithTimeout(context.Background(), calibrateAsyncTimeout)
		defer cancel()
		// #1795 case 1: same reordering as the first-calibration path -
		// guard BEFORE paying for the remote call.
		if messagesContainNonTextBlocks(messages) {
			debug.Log("provider-calibrator", "async calibration skipped: non-text blocks (image/thinking) make the sample asymmetric")
			p.calibrator.recordSkip()
			return
		}
		realTokens, err := p.remoteCountTokens(calCtx, messages)
		if err != nil {
			debug.Log("provider-calibrator", "async calibration failed: %v", err)
			p.calibrator.recordFailure() // transient errors back off, don't disable (#708)
			return
		}
		p.calibrator.applyResult(estimated, realTokens)
		debug.Log("provider-calibrator", "async calibration OK: estimated=%d real=%d ratio=%.3f", estimated, realTokens, p.calibrator.currentRatio())
	})
	return result, nil
}

// messagesContainNonTextBlocks reports whether any message carries blocks the
// local estimator does not count (image data, tool_result attachments,
// thinking/redacted_thinking) - such samples are asymmetric against the
// remote truth and must not feed the calibration ratio (#1618-A).
func messagesContainNonTextBlocks(messages []Message) bool {
	for _, m := range messages {
		for _, b := range m.Content {
			switch b.Type {
			// #1795 case 2: tool_result was filtered WHOLESALE, but the
			// local estimator already counts its Output text (symmetric) -
			// only its EMBEDDED IMAGES are asymmetric. Agent sessions
			// ALWAYS carry tool_result from the first tool call on, so the
			// async calibration never ran for any real agent session and
			// the ratio froze on the first pure-text sample. Images and
			// thinking blocks remain asymmetric (counted remotely,
			// invisible locally).
			case "image", "thinking", "redacted_thinking":
				return true
			}
		}
	}
	return false
}

// remoteCountTokens calls the Anthropic count_tokens API for accurate token counts.
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

// buildCountTokensParams converts internal messages to the Anthropic
// MessageCountTokensParams format, reusing the same block-conversion logic
// as buildParams but without tool definitions or max_tokens.
func (p *AnthropicProvider) buildCountTokensParams(messages []Message) anthropic.MessageCountTokensParams {
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
			case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result":
				// Verbatim echo-back of the server-tool exchange; silently skip
				// only on impossible re-parse failures (raw was captured from
				// the API itself).
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
				blocks = append(blocks, anthropic.NewToolUseBlock(b.ToolID, normalizeToolInputValue(b.Input), b.ToolName))
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
		// Prepend system blocks into first user message (same as buildParams).
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

// buildParams converts internal messages to MessageNewParams. ctx is used to
// resolve large images into Files API file_ids (the upload happens inline here,
// at most once per unique image, and is cancellable with the request).
func (p *AnthropicProvider) buildParams(ctx context.Context, messages []Message, tools []ToolDefinition) anthropic.MessageNewParams {
	var msgParams []anthropic.MessageParam
	// Collect system content blocks preserving cache hints so we can emit
	// separate Anthropic text blocks with selective cache_control breakpoints.
	// This follows "Don't Break the Cache" (arXiv:2601.06007): static system
	// prompt content gets its own cache breakpoint, so dynamic layers (ratchet
	// rules, playbook) changing between runs doesn't invalidate the cache for
	// the much larger static prefix.
	type sysBlock struct {
		text  string
		cache bool
	}
	var systemBlocks []sysBlock
	for _, m := range messages {
		if m.Role == "system" {
			for _, b := range m.Content {
				if b.Type == "text" && b.Text != "" {
					systemBlocks = append(systemBlocks, sysBlock{text: b.Text, cache: b.Cache})
				}
			}
			continue
		}
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result":
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
				blocks = append(blocks, anthropic.NewToolUseBlock(b.ToolID, normalizeToolInputValue(b.Input), b.ToolName))
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
				// Anthropic extended thinking: must echo back with signature
				if b.ThinkingSignature != "" {
					blocks = append(blocks, anthropic.NewThinkingBlock(b.ThinkingSignature, b.ReasoningContent))
				}
			case "redacted_thinking":
				// Anthropic redacted thinking: must echo back with data
				if b.ThinkingData != "" {
					blocks = append(blocks, anthropic.NewRedactedThinkingBlock(b.ThinkingData))
				}
			}
		}
		param := anthropic.MessageParam{Role: anthropic.MessageParamRole(m.Role), Content: blocks}
		// Prepend system blocks into first user message, emitting each as a
		// separate Anthropic text block with selective cache_control.
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
	// #786: systemBlocks are flushed when a user message is encountered; a
	// message list with no user role (assistant-prefill-first resume) used
	// to silently drop the entire system prompt. Flush any remainder into
	// params.System as a last resort so the prompt is never lost.
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

	// #2239: per-call stop sequences (MCP sampling contract) - the
	// response side already maps "stop_sequence" (#1484-C). #2246: this
	// used to sit INSIDE the temperature>0 guard, so the default config
	// (temperature 0 = provider default) silently dropped stop_sequences
	// - the #2239 symptom reincarnated on Anthropic only, while the
	// openai/gemini siblings were unconditional.
	// #2266: the #2248 snapshot switch wired anthropic's MaxTokens half
	// (effectiveMaxTokens) but left this block reading the field only -
	// an active sampling override's sequences never reached the request
	// body. Override wins (the provider.go contract), siblings merged.
	// #2271 follow-up: the p.stopSequences field had NO writer left
	// (SetStopSequences has been dead since the override switch) - the
	// override is now the sole source.
	if o := p.samplingOverride.Load(); o != nil && len(o.StopSequences) > 0 {
		params.StopSequences = o.StopSequences
	}
	// Apply temperature when set (0 means use provider default). The
	// active sampling override (#1592-A family) wins over the configured
	// default so an MCP server's temperature hint reaches the request.
	temp := p.temperature
	if o := p.samplingOverride.Load(); o != nil && o.Temperature > 0 {
		temp = o.Temperature
	}
	if temp > 0 {
		params.Temperature = param.NewOpt(temp)
	}
	// Apply top_p when set (0 means use provider default).
	if p.topP > 0 {
		params.TopP = param.NewOpt(p.topP)
	}

	// Extended thinking carrier (Anthropic 2026):
	//   - adaptive: thinking:{type:"adaptive"} + output_config.effort — the
	//     only mode that interleaves thinking between tool calls on Claude
	//     4.6+/5.x models (manual budget_tokens is deprecated on 4.6 and
	//     rejected on 4.7+). No budget_tokens: the API forbids combining
	//     them. Effort, not budget, is the depth control.
	//   - manual: budget_tokens from the effort level, required for the
	//     extended-thinking-only generation (Sonnet/Opus/Haiku 4.5 and
	//     earlier), composed with the effort carrier below.
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

	// Effort via the top-level output_config (GA effort parameter, 2026):
	// attach only the established conversation level (see beginEffortTracking)
	// so the request prefix stays constant across calls. Composed with
	// budget_tokens this is the documented best practice for models that
	// support effort alongside extended thinking: effort governs total token
	// volume, the budget caps explicit thinking. The per-message
	// output_config marker (cache-preserving mid-conversation switching) is
	// not expressible in SDK v1.68 typed params; when the SDK gains it, flip
	// re-establishments to the marker form.
	if p.effortCarrier.Load() && p.conversationEffort != "" && p.lastCallEffort == p.conversationEffort {
		params.OutputConfig = anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffort(p.conversationEffort),
		}
	}
	// Adaptive models have no budget_tokens: effort is their only depth
	// control, so it rides every request from the first call (subject to
	// the endpoint latch — an endpoint that rejected output_config keeps
	// running on the default level, mirroring the manual-mode degradation).
	if useAdaptive && params.OutputConfig.Effort == "" && p.effortCarrier.Load() {
		if effort != "" {
			params.OutputConfig = anthropic.OutputConfigParam{
				Effort: anthropic.OutputConfigEffort(effort),
			}
		}
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
				// Strict tool use (structured outputs): allowlisted tools get
				// grammar-constrained input decoding. Validation runs against
				// the raw schema (all top-level fields must be required);
				// the root "additionalProperties": false that Anthropic
				// requires goes through ExtraFields because
				// ToolInputSchemaParam's typed fields cannot carry it.
				// (Recursive schema injection is applied on the OpenAI path,
				// which serializes schemas as raw JSON.)
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
				// Add cache control breakpoint on the last tool definition so
				// Anthropic caches all tool schemas (which are large and static
				// across turns). Only the last item needs the breakpoint —
				// Anthropic caches everything from the start up to each
				// breakpoint.
				if i == len(tools)-1 {
					toolParams[i].OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
			}
		}
		params.Tools = toolParams
	}

	// Server-side tools (web_search/web_fetch): declared once, executed inside
	// Anthropic's infrastructure. Names are defaulted by the SDK ("web_search"
	// /"web_fetch"). Declarations are static, so a cache breakpoint on the
	// last one keeps the whole tool block cached.
	if len(p.serverTools) > 0 {
		for i, st := range p.serverTools {
			var u anthropic.ToolUnionParam
			switch st.Type {
			case "web_search_20250305":
				u = anthropic.ToolUnionParam{OfWebSearchTool20250305: &anthropic.WebSearchTool20250305Param{}}
			case "web_fetch_20250910":
				u = anthropic.ToolUnionParam{OfWebFetchTool20250910: &anthropic.WebFetchTool20250910Param{}}
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

	// Anthropic Memory Tool (memory_20250818): declared with no input schema
	// — the schema is fixed API-side. Execution happens agent-side (see
	// internal/agent/memory_tool.go). Place a cache breakpoint here only when
	// this is the trailing static declaration; otherwise the loop above
	// already put one on the last server tool.
	if p.memoryTool {
		u := anthropic.ToolUnionParam{OfMemoryTool20250818: &anthropic.MemoryTool20250818Param{}}
		if len(p.serverTools) == 0 {
			setToolUnionCacheControl(&u)
		}
		params.Tools = append(params.Tools, u)
	}

	// Apply tool_choice when set. Only sent when tools are present (API requirement).
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

	// Dump full request JSON for debugging protocol violations.
	// Covers both Chat() (e.g. summarization) and ChatStream() (normal flow).

	return params
}

// serverToolUseRaw rebuilds the verbatim JSON for a completed server_tool_use
// block from the streamed fields (content_block_stop carries no payload).
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

// serverToolBlockParam converts a stored verbatim server-tool block back into
// a request-side param union for echo-back. The API requires the full
// server_tool_use + result pair on subsequent requests; a dropped result makes
// it treat the call as deferred and re-run the search (wasted tokens).
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
	case "web_fetch_tool_result":
		var b anthropic.WebFetchToolResultBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		param := b.ToParam()
		return &anthropic.ContentBlockParamUnion{OfWebFetchToolResult: &param}, nil
	}
	return nil, fmt.Errorf("unknown server tool block type %q", probe.Type)
}

// setToolUnionCacheControl attaches a cache breakpoint to a server-tool union.
func setToolUnionCacheControl(u *anthropic.ToolUnionParam) {
	switch {
	case u.OfWebSearchTool20250305 != nil:
		u.OfWebSearchTool20250305.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case u.OfWebFetchTool20250910 != nil:
		u.OfWebFetchTool20250910.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case u.OfMemoryTool20250818 != nil:
		u.OfMemoryTool20250818.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
}

func convertAnthropicResponse(blocks []anthropic.ContentBlockUnion) []ContentBlock {
	result := make([]ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			result = append(result, TextBlock(b.Text))
		case "tool_use":
			result = append(result, ToolUseBlock(b.ID, b.Name, b.Input))
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
		case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result":
			// Anthropic server-side tool blocks: executed inside the API, never
			// surfaced as client tool calls. Keep the raw JSON verbatim so the
			// full exchange can be echoed back on subsequent requests.
			result = append(result, ContentBlock{Type: b.Type, Raw: json.RawMessage(b.RawJSON())})
		}
	}
	return result
}

// anthropicStopReasonError returns an error for stop reasons that indicate
// truncation or policy issues. Returns nil for normal completion reasons.
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

// SetContextEditing opts this endpoint into Anthropic server-side context
// management. nil disables the feature and strips the beta flag from the
// request header. The flag is toggled on the transport (the same injection
// point SetSessionID uses) so the value merges with any configured
// anthropic-beta header instead of clobbering it.
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

// toggleBetaToken adds or removes a comma-separated token from an
// anthropic-beta header value. Idempotent in both directions; preserves
// unrelated tokens (including their original order).
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

// contextEditingOptions returns the per-call request options attaching the
// context_management field. Nil when the feature is off, so the spread at
// the call sites is a no-op.
func (p *AnthropicProvider) contextEditingOptions() []option.RequestOption {
	payload := contextManagementPayload(p.contextEditing.Load())
	if payload == nil {
		return nil
	}
	return []option.RequestOption{option.WithJSONSet("context_management", payload)}
}
