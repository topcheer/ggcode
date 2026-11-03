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
	"sync/atomic"
)

type GeminiProvider struct {
	client           *genai.Client
	model            string
	maxTokens        int
	cap              *adaptiveCap
	reasoningEffort  string
	toolChoice       string
	temperature      float64
	samplingOverride atomic.Pointer[SamplingOverride]
	serverTools      []ServerToolConfig
	topP             float64
	responseSchema   json.RawMessage
	transport        *headerInjectingTransport
	logprobs         bool
	policy           callPolicy
}

func (p *GeminiProvider) ModelName() string { return p.model }

func (p *GeminiProvider) CloneWithModel(model string) Provider {
	clone := &GeminiProvider{
		client:    p.client,
		model:     model,
		maxTokens: p.maxTokens,

		cap:             AdaptiveCapForModelSwap(p.cap, model, p.maxTokens),
		reasoningEffort: p.reasoningEffort,
		toolChoice:      p.toolChoice,
		temperature:     p.temperature,
		topP:            p.topP,
		transport:       p.transport,
		serverTools:     append([]ServerToolConfig(nil), p.serverTools...),
		policy:          p.policy,
		logprobs:        p.logprobs,

		responseSchema: append(json.RawMessage(nil), p.responseSchema...),
	}

	if ov := p.samplingOverride.Load(); ov != nil {
		clone.samplingOverride.Store(ov)
	}
	return clone
}

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

func (p *GeminiProvider) SetTemperature(temp float64) { p.temperature = temp }

func (p *GeminiProvider) SetSamplingOverride(o *SamplingOverride) { p.samplingOverride.Store(o) }

func (p *GeminiProvider) SamplingOverride() *SamplingOverride { return p.samplingOverride.Load() }
func (p *GeminiProvider) Temperature() float64                { return p.temperature }

func (p *GeminiProvider) SetTopP(topP float64) { p.topP = topP }
func (p *GeminiProvider) TopP() float64        { return p.topP }

func (p *GeminiProvider) SetToolChoice(choice string) {
	p.toolChoice = strings.ToLower(strings.TrimSpace(choice))
}

func (p *GeminiProvider) SetLogprobsRequest(on bool) { p.logprobs = on }

func (p *GeminiProvider) LogprobsRequested() bool { return p.logprobs }

func (p *GeminiProvider) applyLogprobs(config *genai.GenerateContentConfig) {
	if !p.logprobs {
		return
	}
	config.ResponseLogprobs = true
	n := int32(5)
	config.Logprobs = &n
}

func (p *GeminiProvider) ToolChoice() string { return p.toolChoice }

func (p *GeminiProvider) SetServerTools(tools []ServerToolConfig) {
	kept := make([]ServerToolConfig, 0, len(tools))
	for _, t := range tools {
		switch strings.ToLower(strings.TrimSpace(t.Type)) {
		case "google_search", "web_search", "url_context":
			kept = append(kept, t)
		default:
			debug.Log("gemini", "ignoring unsupported server tool declaration: %q", t.Type)
		}
	}
	if len(kept) > 0 {
		p.serverTools = kept
	}
}

func (p *GeminiProvider) hasBuiltinTools() bool { return len(p.serverTools) > 0 }

func (p *GeminiProvider) builtinGeminiTools() []*genai.Tool {
	var out []*genai.Tool
	for _, t := range p.serverTools {
		switch strings.ToLower(strings.TrimSpace(t.Type)) {
		case "google_search", "web_search":
			out = append(out, &genai.Tool{GoogleSearch: &genai.GoogleSearch{}})
		case "url_context":
			out = append(out, &genai.Tool{URLContext: &genai.URLContext{}})
		}
	}
	return out
}

func (p *GeminiProvider) SetAdaptiveCap(c *adaptiveCap) { p.cap = c }

func NewGeminiProvider(apiKey string, model string, maxTokens int) (*GeminiProvider, error) {
	return NewGeminiProviderWithBaseURL(apiKey, model, maxTokens, "")
}

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

func (p *GeminiProvider) SetSessionID(sessionID string) {
	if sessionID == "" || p.transport == nil {
		return
	}
	existing := p.transport.snapshotHeaders()
	existing.Set("GGCode-SessionID", sessionID)
	p.transport.UpdateHeaders(existing)
}

func (p *GeminiProvider) UpdateRuntimeHeaders(headers http.Header) {
	if p.transport != nil {
		if sid := p.transport.snapshotHeaders().Get("GGCode-SessionID"); sid != "" && headers.Get("GGCode-SessionID") == "" {
			merged := headers.Clone()
			merged.Set("GGCode-SessionID", sid)
			headers = merged
		}
		p.transport.UpdateHeaders(headers)
	}
}

func normalizeGeminiFinishReason(r genai.FinishReason) string {
	switch r {
	case genai.FinishReasonMaxTokens:
		return "max_tokens"
	case genai.FinishReasonStop:
		return "end_turn"
	case genai.FinishReasonSafety, genai.FinishReasonRecitation,
		genai.FinishReasonProhibitedContent, genai.FinishReasonSPII:
		return "refusal"
	case genai.FinishReasonUnspecified:
		return ""
	default:
		return strings.ToLower(string(r))
	}
}

func (p *GeminiProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {

	ctx, cancel := p.policy.withTimeout(ctx)
	defer cancel()
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
	p.applyLogprobs(config)
	p.applyResponseSchema(config)

	var resp *genai.GenerateContentResponse
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.client.Models.GenerateContent(ctx, p.model, contents, config)
		return callErr
	}, p.policy.attempts(), p.policy.timeoutRetryInPlace())
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

	if pf := resp.PromptFeedback; pf != nil && pf.BlockReason != genai.BlockedReasonUnspecified && len(resp.Candidates) == 0 {
		reason := string(pf.BlockReason)
		if pf.BlockReasonMessage != "" {
			reason = pf.BlockReasonMessage
		}
		return nil, fmt.Errorf("gemini chat: prompt blocked by input safety filter: %s", reason)
	}

	content, usage := p.convertResponse(resp)

	finishReason := ""
	if len(resp.Candidates) > 0 {
		finishReason = normalizeGeminiFinishReason(resp.Candidates[0].FinishReason)
	}
	var confidence *float64
	if p.logprobs && len(resp.Candidates) > 0 {
		avg := resp.Candidates[0].AvgLogprobs
		confidence = &avg
	}
	return &ChatResponse{
		Message:    Message{Role: "assistant", Content: content},
		Usage:      usage,
		StopReason: finishReason,
		Confidence: confidence,
	}, nil
}

func (p *GeminiProvider) sendEvent(ctx context.Context, ch chan<- StreamEvent, ev StreamEvent) bool {

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
	p.applyLogprobs(config)
	p.applyResponseSchema(config)

	ch := make(chan StreamEvent, 64)

	safego.Go("provider.gemini.streamRead", func() {
		defer close(ch)

		parentCtx := ctx
		var cancelAttempt context.CancelFunc
		defer func() { cancelAttempt() }()
		budget := newRetryBudget()
		grounding := newGeminiGroundingAccum()
		attempts := p.policy.attempts()
		for attempt := 0; attempt < attempts; attempt++ {
			if attempt > 0 {
				cancelAttempt()
			}
			ctx, cancelAttempt = p.policy.withTimeout(parentCtx)
			var usage TokenUsage
			var truncated bool
			var policyBlocked bool
			var avgConf *float64
			var outputChars int
			iter := p.client.Models.GenerateContentStream(ctx, p.model, contents, config)
			emitted := false
			retry := false
			for resp, err := range iter {
				if err != nil {
					if rejected, parsed := maxTokensRejection(err); rejected {
						p.cap.OnRejected(parsed)
					}
					if !emitted && isRetryableForContext(ctx, err) && attempt < attempts-1 {

						delay := retryDelay(err, attempt)
						if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, p.policy.attempts(), delay)}) {
							return
						}
						if isTimeoutClassError(err) && p.policy.timeoutRetryInPlace() {
							budget.unlimited = true
							attempts = retryAlwaysAttempts
						}
						if notice := TimeoutRetryNotice(err, attempt); notice != "" {
							if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventSystem, Text: notice}) {
								return
							}
						}
						if sleepErr := budget.sleep(ctx, delay); sleepErr != nil {

							if sleepErr == errRetryBudgetExhausted {
								sleepErr = fmt.Errorf("%w: %w", errRetryBudgetExhausted, err)
							}
							if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: sleepErr}) {
								return
							}
							return
						}
						retry = true
						break
					}
					if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: fmt.Errorf("gemini stream: %w", err)}) {
						return
					}
					return
				}

				if resp.UsageMetadata != nil {
					usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)

					usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount) + int(resp.UsageMetadata.ThoughtsTokenCount)
					usage.CacheRead = int(resp.UsageMetadata.CachedContentTokenCount)
					usage.PromptTokensTotal = int(resp.UsageMetadata.PromptTokenCount)
				}

				if len(resp.Candidates) > 0 {
					grounding.add(resp.Candidates[0].GroundingMetadata)
				}

				if p.logprobs && len(resp.Candidates) > 0 {
					avg := resp.Candidates[0].AvgLogprobs
					avgConf = &avg
				}

				var finishNotice string
				if len(resp.Candidates) > 0 {
					fr := resp.Candidates[0].FinishReason
					if fr == genai.FinishReasonMaxTokens {

						p.cap.OnTruncated()
						truncated = true
					} else if finishErr := geminiFinishReasonError(fr); finishErr != nil {

						debug.Log("provider", "gemini stream fatal finish reason: %v", finishErr)
						finishNotice = fmt.Sprintf("[Warning: %v] ", finishErr)
						truncated = true

						policyBlocked = true
					}
				}

				if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {

					if pf := resp.PromptFeedback; pf != nil && pf.BlockReason != genai.BlockedReasonUnspecified {
						reason := string(pf.BlockReason)
						if pf.BlockReasonMessage != "" {
							reason = pf.BlockReasonMessage
						}
						debug.Log("gemini", "stream prompt blocked by policy: %s", reason)
						if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Blocked by input safety filter: %s] ", reason)}) {
							return
						}
						policyBlocked = true
						truncated = true
					}

					if finishNotice != "" {
						if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventSystem, Text: finishNotice}) {
							return
						}
					}
					continue
				}

				for _, part := range resp.Candidates[0].Content.Parts {
					if part.Text != "" && !part.Thought {
						emitted = true
						outputChars += len(part.Text)
						if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventText, Text: part.Text}) {
							return
						}
					}
					if part.FunctionCall != nil {
						emitted = true
						args, _ := json.Marshal(part.FunctionCall.Args)
						outputChars += len(part.FunctionCall.Name) + len(args)
						id := part.FunctionCall.ID
						if id == "" {
							id = part.FunctionCall.Name
						}
						if !p.sendEvent(ctx, ch, StreamEvent{
							Type: StreamEventToolCallDone,
							Tool: ToolCallDelta{
								Index:            0,
								ID:               id,
								Name:             part.FunctionCall.Name,
								Arguments:        args,
								ThoughtSignature: part.ThoughtSignature,
							},
						}) {
							return
						}
					}
				}

				if finishNotice != "" {
					if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventSystem, Text: finishNotice}) {
						return
					}
				}
			}
			if retry {
				continue
			}

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
			if s := grounding.summary(); s != "" {
				if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventSystem, Text: s}) {
					return
				}
			}
			if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventDone, Usage: &usage, Truncated: truncated, PolicyBlocked: policyBlocked, Confidence: avgConf}) {
				return
			}
			return
		}

		if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: fmt.Errorf("gemini stream: %d retry attempts exhausted", p.policy.attempts())}) {
			return
		}
	})

	return ch, nil
}

func (p *GeminiProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return estimateTokensForMessages(messages), nil
}

func isGemini3Model(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "gemini-3")
}

func (p *GeminiProvider) applyReasoningEffort(config *genai.GenerateContentConfig) {
	if p.reasoningEffort == "" {
		return
	}
	maxTok := p.effectiveMaxTokens()

	if maxTok <= 1024 {
		if isGemini3Model(p.model) {
			config.ThinkingConfig = &genai.ThinkingConfig{
				ThinkingLevel: genai.ThinkingLevelMinimal,
			}
			return
		}

		config.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget: ptrToInt32(0),
		}
		return
	}

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
		config.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
		debug.Log("gemini", "thinking level=%s (effort=%s, gemini-3)", level, p.reasoningEffort)
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

	if mode == genai.FunctionCallingConfigModeAny && p.hasBuiltinTools() {
		mode = genai.FunctionCallingConfigModeAuto
	}
	config.ToolConfig = &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode: mode,
		},
	}
}

func ptrToInt32(v int32) *int32 { return &v }

func (p *GeminiProvider) effectiveMaxTokens() int {
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

func (p *GeminiProvider) applySamplingConfig(config *genai.GenerateContentConfig) {
	temp := p.temperature
	if o := p.samplingOverride.Load(); o != nil && o.Temperature > 0 {
		temp = o.Temperature
	}
	if temp > 0 {
		config.Temperature = ptrToFloat32(float32(temp))
	}
	if p.topP > 0 {
		config.TopP = ptrToFloat32(float32(p.topP))
	}

	if v := p.effectiveMaxTokens(); v > 0 {
		config.MaxOutputTokens = int32(v)
	}

	if o := p.samplingOverride.Load(); o != nil && len(o.StopSequences) > 0 {
		config.StopSequences = o.StopSequences
	}
}

func ptrToFloat32(v float32) *float32 { return &v }

func (p *GeminiProvider) convertMessages(messages []Message) ([]*genai.Content, *genai.Content) {

	messages = foldInjectedUserMessages(messages, appendToToolResultContent)
	var contents []*genai.Content
	var systemParts []*genai.Part
	toolNamesByID := make(map[string]string)

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

	toolList := p.builtinGeminiTools()

	functionDecls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
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
		functionDecls = append(functionDecls, fd)
	}

	if len(functionDecls) > 0 {
		toolList = append(toolList, &genai.Tool{
			FunctionDeclarations: functionDecls,
		})
	}
	return toolList
}

func (p *GeminiProvider) convertResponse(resp *genai.GenerateContentResponse) ([]ContentBlock, TokenUsage) {
	var blocks []ContentBlock
	var usage TokenUsage

	if resp.UsageMetadata != nil {
		usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)

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
				blk.ThinkingSignature = string(part.ThoughtSignature)
				blocks = append(blocks, blk)
			}
		}
	}

	if len(resp.Candidates) > 0 {
		if s := newGeminiGroundingAccum().add(resp.Candidates[0].GroundingMetadata).summary(); s != "" {
			blocks = append(blocks, TextBlock(s))
		}
	}

	return blocks, usage
}

type geminiGroundingAccum struct {
	queries []string
	sources []string
	seen    map[string]bool
}

func newGeminiGroundingAccum() *geminiGroundingAccum {
	return &geminiGroundingAccum{seen: make(map[string]bool)}
}

func (a *geminiGroundingAccum) add(gm *genai.GroundingMetadata) *geminiGroundingAccum {
	if gm == nil || a == nil {
		return a
	}
	if a.seen == nil {
		a.seen = make(map[string]bool)
	}
	for _, q := range gm.WebSearchQueries {
		if q != "" && !a.seen["q:"+q] {
			a.seen["q:"+q] = true
			a.queries = append(a.queries, q)
		}
	}
	for _, c := range gm.GroundingChunks {
		if c == nil || c.Web == nil || c.Web.URI == "" {
			continue
		}
		if a.seen["u:"+c.Web.URI] {
			continue
		}
		a.seen["u:"+c.Web.URI] = true
		if c.Web.Title != "" {
			a.sources = append(a.sources, fmt.Sprintf("  - %s (%s)", c.Web.Title, c.Web.URI))
		} else {
			a.sources = append(a.sources, "  - "+c.Web.URI)
		}
	}
	return a
}

func (a *geminiGroundingAccum) summary() string {
	if a == nil || (len(a.queries) == 0 && len(a.sources) == 0) {
		return ""
	}
	var b strings.Builder
	if len(a.queries) > 0 {
		b.WriteString("[grounding] search queries: ")
		b.WriteString(strings.Join(a.queries, ", "))
		b.WriteString("\n")
	}
	if len(a.sources) > 0 {
		b.WriteString("[grounding] sources:\n")
		b.WriteString(strings.Join(a.sources, "\n"))
	}
	return b.String()
}

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

func (p *GeminiProvider) probeChat(ctx context.Context, messages []Message) error {
	contents, _ := p.convertMessages(messages)
	_, err := p.client.Models.GenerateContent(ctx, p.model, contents, nil)
	return err
}
