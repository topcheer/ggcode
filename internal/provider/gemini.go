package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"

	"google.golang.org/genai"
)

// GeminiProvider implements Provider using the Google Generative AI API.
type GeminiProvider struct {
	client          *genai.Client
	model           string
	maxTokens       int
	cap             *adaptiveCap
	reasoningEffort string                    // "", "low", "medium", "high" — maps to Gemini ThinkingConfig
	toolChoice      string                    // "", "auto", "required", "none" — maps to Gemini FunctionCallingConfig
	temperature     float64                   // 0 = provider default
	topP            float64                   // 0 = provider default
	transport       *headerInjectingTransport // kept for runtime header updates
}

// ModelName returns the current model name used by this provider.
func (p *GeminiProvider) ModelName() string { return p.model }

// CloneWithModel returns a shallow copy of this provider with a different model.
func (p *GeminiProvider) CloneWithModel(model string) Provider {
	return &GeminiProvider{
		client:    p.client,
		model:     model,
		maxTokens: p.maxTokens,
		// #1603: re-key the adaptive cap for the NEW model - sharing the
		// parent's learned cap pointer mixed per-model state across the
		// registry's carefully-partitioned keys.
		cap:             AdaptiveCapForModelSwap(p.cap, model, p.maxTokens),
		reasoningEffort: p.reasoningEffort,
		toolChoice:      p.toolChoice,
		temperature:     p.temperature,
		topP:            p.topP,
		transport:       p.transport,
	}
}

// SetReasoningEffort sets the reasoning effort, which maps to Gemini's
// ThinkingConfig.ThinkingBudget parameter. Effort levels: "low" (~25% of
// max tokens), "medium" (~50%), "high" (~75%). Empty string disables
// explicit thinking budget (uses model default behavior).
// SetMaxTokens implements provider.MaxTokensSetter (#1592-A).
func (p *GeminiProvider) SetMaxTokens(n int) {
	if n > 0 {
		p.maxTokens = n
	}
}

func (p *GeminiProvider) SetReasoningEffort(effort string) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "", "low", "medium", "high":
		p.reasoningEffort = effort
	}
}

func (p *GeminiProvider) ReasoningEffort() string { return p.reasoningEffort }

// SetTemperature sets the sampling temperature. 0 means "use provider default".
func (p *GeminiProvider) SetTemperature(temp float64) { p.temperature = temp }
func (p *GeminiProvider) Temperature() float64        { return p.temperature }

// SetTopP sets the nucleus sampling parameter. 0 means "use provider default".
func (p *GeminiProvider) SetTopP(topP float64) { p.topP = topP }
func (p *GeminiProvider) TopP() float64        { return p.topP }

// SetToolChoice sets the tool_choice parameter: "auto" (model decides),
// "required" (force at least one tool call), "none" (disable tools), or ""
// (API default). These map to Gemini's FunctionCallingConfig.Mode values
// AUTO, ANY, and NONE respectively.
func (p *GeminiProvider) SetToolChoice(choice string) {
	p.toolChoice = strings.ToLower(strings.TrimSpace(choice))
}

func (p *GeminiProvider) ToolChoice() string { return p.toolChoice }

// SetAdaptiveCap installs the adaptive max-output-tokens cap.
func (p *GeminiProvider) SetAdaptiveCap(c *adaptiveCap) { p.cap = c }

// NewGeminiProvider creates a new Gemini provider.
func NewGeminiProvider(apiKey string, model string, maxTokens int) (*GeminiProvider, error) {
	return NewGeminiProviderWithBaseURL(apiKey, model, maxTokens, "")
}

// NewGeminiProviderWithBaseURL creates a new Gemini provider with a custom base URL.
func NewGeminiProviderWithBaseURL(apiKey string, model string, maxTokens int, baseURL string) (*GeminiProvider, error) {
	headers := BuildHeadersForProvider("gemini")
	transport := &headerInjectingTransport{
		base:    newProviderHTTPTransport(),
		headers: headers,
	}
	clientConfig := &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
		HTTPClient: &http.Client{
			Transport: transport,
		},
	}
	if trimmed := strings.TrimSpace(baseURL); trimmed != "" {
		clientConfig.HTTPOptions.BaseURL = trimmed
	}

	client, err := genai.NewClient(context.Background(), clientConfig)
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	debug.Log("provider", "GeminiProvider created: model=%s maxTokens=%d baseURL=%s", model, maxTokens, baseURL)
	return &GeminiProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
		transport: transport,
	}, nil
}

func (p *GeminiProvider) Name() string {
	return "gemini"
}

// SetSessionID injects the session ID into outgoing requests via a custom
// HTTP header (GGCode-SessionID).
func (p *GeminiProvider) SetSessionID(sessionID string) {
	if sessionID == "" || p.transport == nil {
		return
	}
	existing := p.transport.snapshotHeaders()
	existing.Set("GGCode-SessionID", sessionID)
	p.transport.UpdateHeaders(existing)
}

// UpdateRuntimeHeaders updates the injected headers at runtime.
func (p *GeminiProvider) UpdateRuntimeHeaders(headers http.Header) {
	if p.transport != nil {
		p.transport.UpdateHeaders(headers)
	}
}

func (p *GeminiProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	contents, systemInstruction := p.convertMessages(messages)

	config := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
	}
	if len(tools) > 0 {
		config.Tools = p.convertTools(tools)
	}
	p.applyReasoningEffort(config)
	p.applySamplingConfig(config)
	p.applyToolChoice(config, tools)

	var resp *genai.GenerateContentResponse
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.client.Models.GenerateContent(ctx, p.model, contents, config)
		return callErr
	}, providerRetryAttempts)
	if err != nil {
		if rejected, parsed := maxTokensRejection(err); rejected {
			p.cap.OnRejected(parsed)
		}
		debug.Log("gemini", "Chat FATAL model=%s: %T: %v", p.model, err, err)
		return nil, fmt.Errorf("gemini chat: %w", err)
	}
	if len(resp.Candidates) > 0 && resp.Candidates[0].FinishReason == genai.FinishReasonMaxTokens {
		p.cap.OnTruncated()
	}
	// #1301: prompt-level safety blocks arrive with no candidates at all -
	// surface them as an error instead of returning an empty reply.
	if pf := resp.PromptFeedback; pf != nil && pf.BlockReason != genai.BlockedReasonUnspecified && len(resp.Candidates) == 0 {
		reason := string(pf.BlockReason)
		if pf.BlockReasonMessage != "" {
			reason = pf.BlockReasonMessage
		}
		return nil, fmt.Errorf("gemini chat: prompt blocked by input safety filter: %s", reason)
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
	if len(tools) > 0 {
		config.Tools = p.convertTools(tools)
	}
	p.applyReasoningEffort(config)
	p.applySamplingConfig(config)
	p.applyToolChoice(config, tools)

	ch := make(chan StreamEvent, 64)

	safego.Go("provider.gemini.streamRead", func() {
		defer close(ch)

		budget := newRetryBudget() // #722: cap cumulative retry backoff sleep per stream call
		for attempt := 0; attempt < providerRetryAttempts; attempt++ {
			var usage TokenUsage // reset per attempt to avoid leaking failed-attempt usage
			var truncated bool
			var policyBlocked bool
			var outputChars int // #561(C): for usage fallback when UsageMetadata is missing
			iter := p.client.Models.GenerateContentStream(ctx, p.model, contents, config)
			emitted := false
			retry := false
			for resp, err := range iter {
				if err != nil {
					if rejected, parsed := maxTokensRejection(err); rejected {
						p.cap.OnRejected(parsed)
					}
					if !emitted && isRetryableForContext(ctx, err) && attempt < providerRetryAttempts-1 {
						// Notify user about retry
						delay := retryDelay(err, attempt)
						ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, providerRetryAttempts, delay)}
						if sleepErr := budget.sleep(ctx, delay); sleepErr != nil {
							// #722: budget exhausted — stop retrying now; wrap with the
							// sentinel so failover switches immediately.
							if sleepErr == errRetryBudgetExhausted {
								sleepErr = fmt.Errorf("%w: %w", errRetryBudgetExhausted, err)
							}
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
					// Gemini bills thinking tokens at output rates (#225).
					usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount) + int(resp.UsageMetadata.ThoughtsTokenCount)
					usage.CacheRead = int(resp.UsageMetadata.CachedContentTokenCount)
					usage.PromptTokensTotal = int(resp.UsageMetadata.PromptTokenCount)
				}

				// Check finish reason for truncation / policy errors (#232).
				// This must run BEFORE the no-content skip below: fully blocked
				// responses (e.g. RECITATION with no partial text) have nil
				// Content but still carry the fatal finish reason — skipping
				// first would drop the Truncated/PolicyBlocked flags (#266).
				var finishNotice string
				if len(resp.Candidates) > 0 {
					fr := resp.Candidates[0].FinishReason
					if fr == genai.FinishReasonMaxTokens {
						// Output was truncated — NOT an error. Keep partial content.
						p.cap.OnTruncated()
						truncated = true
					} else if finishErr := geminiFinishReasonError(fr); finishErr != nil {
						// Fatal finish reason, but any text already streamed is
						// preserved. Surface the reason as a system notice, mark the
						// response truncated, and close the stream normally (Done)
						// instead of erroring out so partial content reaches history.
						debug.Log("provider", "gemini stream fatal finish reason: %v", finishErr)
						finishNotice = fmt.Sprintf("[Warning: %v] ", finishErr)
						truncated = true
						// These finish reasons are provider policy filters, not
						// output-length truncation: auto-continuation would resend
						// the full context just to hit the same filter again (#266).
						policyBlocked = true
					}
				}

				if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
					// #1301: input-side safety blocks carry NO candidates and
					// NO finishReason - the reason lives in the top-level
					// promptFeedback.blockReason. Without this check a blocked
					// prompt streamed an empty "success" reply (Done with
					// PolicyBlocked=false) and the agent spun on nothing.
					if pf := resp.PromptFeedback; pf != nil && pf.BlockReason != genai.BlockedReasonUnspecified {
						reason := string(pf.BlockReason)
						if pf.BlockReasonMessage != "" {
							reason = pf.BlockReasonMessage
						}
						debug.Log("gemini", "stream prompt blocked by policy: %s", reason)
						ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Blocked by input safety filter: %s] ", reason)}
						policyBlocked = true
						truncated = true
					}
					// Nothing to stream from this chunk; emit the finish notice
					// (if any) so fully blocked responses still surface the reason.
					if finishNotice != "" {
						ch <- StreamEvent{Type: StreamEventSystem, Text: finishNotice}
					}
					continue
				}

				for _, part := range resp.Candidates[0].Content.Parts {
					if part.Text != "" && !part.Thought {
						emitted = true
						outputChars += len(part.Text)
						ch <- StreamEvent{Type: StreamEventText, Text: part.Text}
					}
					if part.FunctionCall != nil {
						emitted = true
						args, _ := json.Marshal(part.FunctionCall.Args)
						outputChars += len(part.FunctionCall.Name) + len(args)
						id := part.FunctionCall.ID
						if id == "" {
							id = part.FunctionCall.Name
						}
						ch <- StreamEvent{
							Type: StreamEventToolCallDone,
							Tool: ToolCallDelta{
								Index:            0,
								ID:               id,
								Name:             part.FunctionCall.Name,
								Arguments:        args,
								ThoughtSignature: part.ThoughtSignature, // #1610-A
							},
						}
					}
				}

				// Emit the finish notice after the chunk's parts so partial text
				// is streamed before the warning (#232 ordering).
				if finishNotice != "" {
					ch <- StreamEvent{Type: StreamEventSystem, Text: finishNotice}
				}
			}
			if retry {
				continue
			}
			// #561(C): when the stream never carried UsageMetadata, don't emit
			// all-zero usage (it zeroes context-budget accounting and disables
			// compaction). Fall back to CountTokens + char estimation, mirroring
			// openai.go and anthropic.go.
			if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheRead == 0 {
				inputTokens, err := p.CountTokens(ctx, messages)
				if err != nil {
					inputTokens = 0
				}
				usage = TokenUsage{
					InputTokens:       inputTokens,
					OutputTokens:      estimateTokensFromChars(outputChars),
					PromptTokensTotal: inputTokens,
				}
			}
			ch <- StreamEvent{Type: StreamEventDone, Usage: &usage, Truncated: truncated, PolicyBlocked: policyBlocked}
			return
		}
		// All retry attempts exhausted without success.
		ch <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("gemini stream: %d retry attempts exhausted", providerRetryAttempts)}
	})

	return ch, nil
}

func (p *GeminiProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return estimateTokensForMessages(messages), nil
}

// applyReasoningEffort maps the reasoning effort level to Gemini's
// ThinkingConfig. Gemini 2.5+ models support a thinking budget expressed in
// tokens. We convert effort levels to approximate token budgets as a fraction
// of maxTokens, similar to the Anthropic provider's budget_tokens mapping.
//
//   - low:    ~25% of maxTokens (min 512)
//   - medium: ~50% of maxTokens
//   - high:   ~75% of maxTokens
//
// An empty effort string leaves ThinkingConfig unset (model default).
// The budget is clamped to [512, maxTokens-1] to satisfy API constraints.
// isGemini3Model reports whether the model is a gemini-3 family member,
// which only accepts thinkingLevel (#1610-B).
func isGemini3Model(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "gemini-3")
}

func (p *GeminiProvider) applyReasoningEffort(config *genai.GenerateContentConfig) {
	if p.reasoningEffort == "" {
		return
	}
	maxTok := p.maxTokens
	if p.cap != nil {
		if v := p.cap.Get(); v > 0 {
			maxTok = v
		}
	}
	// #1610-B: gemini-3 models REJECT thinkingBudget and only accept
	// thinkingLevel - routing them through the budget path 400'd every
	// request once an effort was set (user or adaptive). Level-based for
	// gemini-3*, budget-based for everything else; the <=512 disable also
	// uses level (budget 0 is likewise rejected).
	if isGemini3Model(p.model) {
		var level genai.ThinkingLevel
		switch p.reasoningEffort {
		case "low":
			level = genai.ThinkingLevelLow
		case "medium":
			level = genai.ThinkingLevelMedium
		case "high":
			level = genai.ThinkingLevelHigh
		default:
			return
		}
		if maxTok <= 512 {
			level = genai.ThinkingLevelMinimal
		}
		config.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
		debug.Log("gemini", "thinking level=%s (effort=%s, gemini-3)", level, p.reasoningEffort)
		return
	}
	if maxTok <= 512 {
		// Not enough room for meaningful thinking — set budget to 0
		// which disables thinking on Gemini (non-3 models).
		config.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget: ptrToInt32(0),
		}
		return
	}
	var budget int32
	switch p.reasoningEffort {
	case "low":
		budget = int32(maxTok) / 4
	case "medium":
		budget = int32(maxTok) / 2
	case "high":
		budget = int32(maxTok) * 3 / 4
	default:
		return
	}
	if budget < 512 {
		budget = 512
	}
	if budget >= int32(maxTok) {
		budget = int32(maxTok) - 1
	}
	config.ThinkingConfig = &genai.ThinkingConfig{
		ThinkingBudget: &budget,
	}
	debug.Log("gemini", "thinking budget=%d (effort=%s maxTok=%d)", budget, p.reasoningEffort, maxTok)
}

// applyToolChoice maps the tool_choice setting to Gemini's
// FunctionCallingConfig.Mode. Only applied when tools are present and
// toolChoice is non-empty. Values map as:
//   - "auto"     → AUTO (model decides whether to call a function)
//   - "required" → ANY  (model must call one of the provided functions)
//   - "none"     → NONE (model must not call any function)
func (p *GeminiProvider) applyToolChoice(config *genai.GenerateContentConfig, tools []ToolDefinition) {
	if p.toolChoice == "" || len(tools) == 0 {
		return
	}
	var mode genai.FunctionCallingConfigMode
	switch p.toolChoice {
	case "auto":
		mode = genai.FunctionCallingConfigModeAuto
	case "required":
		mode = genai.FunctionCallingConfigModeAny
	case "none":
		mode = genai.FunctionCallingConfigModeNone
	default:
		return
	}
	config.ToolConfig = &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode: mode,
		},
	}
}

// ptrToInt32 returns a pointer to the given int32 value.
func ptrToInt32(v int32) *int32 { return &v }

// applySamplingConfig injects temperature and top_p into the Gemini config
// when they are set (non-zero). Both map directly to genai fields.
func (p *GeminiProvider) applySamplingConfig(config *genai.GenerateContentConfig) {
	if p.temperature > 0 {
		config.Temperature = ptrToFloat32(float32(p.temperature))
	}
	if p.topP > 0 {
		config.TopP = ptrToFloat32(float32(p.topP))
	}
}

func ptrToFloat32(v float32) *float32 { return &v }

func (p *GeminiProvider) convertMessages(messages []Message) ([]*genai.Content, *genai.Content) {
	var contents []*genai.Content
	var systemParts []*genai.Part
	toolNamesByID := make(map[string]string)
	// sigsByID carries tool-call thought signatures into the responses (#1610-A).
	sigsByID := make(map[string][]byte)

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
				// #780: ImageData is a base64 STRING (provider contract, same as
				// openai.go/anthropic.go); Blob.Data is raw bytes and encoding/json
				// re-encodes []byte on marshal, so []byte(b.ImageData)
				// double-encoded every vision payload.
				imgBytes, decErr := base64.StdEncoding.DecodeString(b.ImageData)
				if decErr != nil {
					imgBytes = []byte(b.ImageData)
				}
				parts = append(parts, &genai.Part{
					InlineData: &genai.Blob{
						MIMEType: b.ImageMIME,
						Data:     imgBytes,
					},
				})
			case "tool_use":
				if b.ToolID != "" && b.ToolName != "" {
					toolNamesByID[b.ToolID] = b.ToolName
				}
				if b.ThinkingSignature != "" {
					sigsByID[b.ToolID] = []byte(b.ThinkingSignature)
				}
				args, _ := normalizeToolInputValue(b.Input).(map[string]any)
				if args == nil {
					args = map[string]any{
						"value": normalizeToolInputValue(b.Input),
					}
				}
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   b.ToolID,
						Name: b.ToolName,
						Args: args,
					},
				})
			case "tool_result":
				name := strings.TrimSpace(b.ToolName)
				if name == "" {
					name = toolNamesByID[b.ToolID]
				}
				if name == "" {
					name = "_ggcode_unknown_tool"
				}
				frPart := &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						ID:   b.ToolID,
						Name: name,
						Response: map[string]any{
							"output": b.Output,
						},
					},
				}
				// #1610-A: gemini-3 with thinking on REQUIRES the thought
				// signature echoed with the function response - omitting it
				// 400s every agentic multi-turn.
				if sig, ok := sigsByID[b.ToolID]; ok {
					frPart.ThoughtSignature = sig
				}
				parts = append(parts, frPart)
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
		// Gemini bills thinking tokens at output rates (#225).
		usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount) + int(resp.UsageMetadata.ThoughtsTokenCount)
		usage.CacheRead = int(resp.UsageMetadata.CachedContentTokenCount)
		usage.PromptTokensTotal = int(resp.UsageMetadata.PromptTokenCount)
	}

	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.Text != "" && !part.Thought {
				blocks = append(blocks, TextBlock(part.Text))
			}
			if part.FunctionCall != nil {
				args, _ := json.Marshal(part.FunctionCall.Args)
				id := part.FunctionCall.ID
				if id == "" {
					id = part.FunctionCall.Name
				}
				blk := ToolUseBlock(id, part.FunctionCall.Name, args)
				blk.ThinkingSignature = string(part.ThoughtSignature) // #1610-A
				blocks = append(blocks, blk)
			}
		}
	}

	return blocks, usage
}

// geminiFinishReasonError returns an error for finish reasons that indicate
// truncation or policy issues. Returns nil for normal completion.
func geminiFinishReasonError(reason genai.FinishReason) error {
	switch reason {
	case "", genai.FinishReasonStop, genai.FinishReasonUnspecified:
		return nil
	case genai.FinishReasonMaxTokens:
		return fmt.Errorf("gemini stream ended with FinishReason=MAX_TOKENS (output truncated)")
	case genai.FinishReasonSafety:
		return fmt.Errorf("gemini stream ended with FinishReason=SAFETY (content filtered)")
	case genai.FinishReasonRecitation:
		return fmt.Errorf("gemini stream ended with FinishReason=RECITATION (cited content blocked)")
	case genai.FinishReasonProhibitedContent:
		return fmt.Errorf("gemini stream ended with FinishReason=PROHIBITED_CONTENT")
	case genai.FinishReasonBlocklist:
		return fmt.Errorf("gemini stream ended with FinishReason=BLOCKLIST")
	case genai.FinishReasonMalformedFunctionCall:
		return fmt.Errorf("gemini stream ended with FinishReason=MALFORMED_FUNCTION_CALL")
	default:
		return fmt.Errorf("gemini stream ended with FinishReason=%s", reason)
	}
}

// probeModelsAPI queries the Gemini models endpoint for the model's
// inputTokenLimit, which is the context window size.
func (p *GeminiProvider) probeModelsAPI(ctx context.Context, model string) int {
	modelInfo, err := p.client.Models.Get(ctx, model, nil)
	if err != nil {
		debug.Log("probe", "gemini models API error: %v", err)
		return 0
	}
	if modelInfo.InputTokenLimit > 0 {
		debug.Log("probe", "gemini models API: inputTokenLimit=%d for %s", modelInfo.InputTokenLimit, model)
		return int(modelInfo.InputTokenLimit)
	}
	debug.Log("probe", "gemini models API: InputTokenLimit is 0 for %s", model)
	return 0
}

// probeChat sends a single generate-content request without retry or
// adaptive cap tracking. Used by context window probing.
func (p *GeminiProvider) probeChat(ctx context.Context, messages []Message) error {
	contents, _ := p.convertMessages(messages)
	_, err := p.client.Models.GenerateContent(ctx, p.model, contents, nil)
	return err
}
