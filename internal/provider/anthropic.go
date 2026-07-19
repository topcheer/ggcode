package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"net/http"
)

// AnthropicProvider implements Provider using the Anthropic SDK.
type AnthropicProvider struct {
	client     anthropic.Client
	model      string
	maxTokens  int
	cap        *adaptiveCap
	transport  *headerInjectingTransport // kept for runtime header updates
	calibrator *tokenCountCalibrator     // periodic real-API token calibration
}

// CloneWithModel returns a shallow copy of this provider with a different model.
func (p *AnthropicProvider) CloneWithModel(model string) Provider {
	return &AnthropicProvider{
		client:     p.client,
		model:      model,
		maxTokens:  p.maxTokens,
		cap:        p.cap,
		transport:  p.transport,
		calibrator: p.calibrator,
	}
}

// SetAdaptiveCap installs the adaptive max-output-tokens cap.
func (p *AnthropicProvider) SetAdaptiveCap(c *adaptiveCap) { p.cap = c }

// probeChat sends a single messages request without retry or adaptive
// cap tracking. Used by context window probing.
func (p *AnthropicProvider) probeChat(ctx context.Context, messages []Message) error {
	params := p.buildParams(messages, nil)
	_, err := p.client.Messages.New(ctx, params)
	return err
}

func (p *AnthropicProvider) effectiveMaxTokens() int {
	if p.cap != nil {
		if v := p.cap.Get(); v > 0 {
			return v
		}
	}
	return p.maxTokens
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
		base:    newProviderHTTPTransport(),
		headers: headers,
	}
	opts := anthropicProviderOptions(apiKey, baseURL)
	opts = append(opts, option.WithHTTPClient(&http.Client{Transport: transport}))
	client := anthropic.NewClient(opts...)
	debug.Log("provider", "AnthropicProvider created: model=%s maxTokens=%d baseURL=%s", model, maxTokens, baseURL)
	return &AnthropicProvider{
		client:     client,
		model:      model,
		maxTokens:  maxTokens,
		transport:  transport,
		calibrator: newTokenCountCalibrator(),
	}
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
	params := p.buildParams(messages, tools)

	var resp *anthropic.Message
	err := retryWithBackoffCtx(ctx, func() error {
		var callErr error
		resp, callErr = p.client.Messages.New(ctx, params)
		return callErr
	}, providerRetryAttempts)
	if err != nil {
		if rejected, parsed := maxTokensRejection(err); rejected {
			p.cap.OnRejected(parsed)
		}
		return nil, err
	}
	if string(resp.StopReason) == "max_tokens" {
		p.cap.OnTruncated()
	}

	msg := convertAnthropicResponse(resp.Content)
	usage := anthropicUsage(resp.Usage)

	return &ChatResponse{
		Message: Message{Role: "assistant", Content: msg},
		Usage:   usage,
	}, nil
}

func (p *AnthropicProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	debug.Log("anthropic", "ChatStream START model=%s msgs=%d tools=%d", p.model, len(messages), len(tools))
	params := p.buildParams(messages, tools)

	ch := make(chan StreamEvent, 64)

	safego.Go("provider.anthropic.streamRead", func() {
		defer close(ch)

		var usage *TokenUsage
		var outputChars int

		for attempt := 0; attempt < providerRetryAttempts; attempt++ {
			if attempt > 0 {
				debug.Log("anthropic", "Stream retry attempt %d", attempt)
			}

			toolCalls := make(map[int]*ToolCallDelta)
			var inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int
			emitted := false
			retry := false

			func() {
				stream := p.client.Messages.NewStreaming(ctx, params)
				defer func() {
					// The Anthropic SDK stream doesn't expose a Close method;
					// it drains automatically when the loop exits.
					_ = stream
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
							toolCalls[int(event.Index)] = &ToolCallDelta{
								Index: int(event.Index),
								Name:  "__redacted_thinking__", // sentinel
								ID:    cb.Data,                 // carries redacted data for echo-back
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
						if tc, ok := toolCalls[idx]; ok && tc.Name != "" {
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
						outputTokens = int(event.Usage.OutputTokens)
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
						// Check stop_reason for truncation / policy errors.
						if stopReason := string(event.Delta.StopReason); stopReason != "" {
							debug.Log("anthropic", "stop_reason=%s", stopReason)
							if stopErr := anthropicStopReasonError(stopReason); stopErr != nil {
								if stopReason == "max_tokens" {
									p.cap.OnTruncated()
								}
								ch <- StreamEvent{Type: StreamEventError, Error: stopErr}
								return
							}
						}

					case "message_start":
						inputTokens = int(event.Message.Usage.InputTokens)
						debug.Log("anthropic", "message_start usage: input_tokens=%d output_tokens=%d cache_read=%d cache_write=%d",
							event.Message.Usage.InputTokens, event.Message.Usage.OutputTokens,
							event.Message.Usage.CacheReadInputTokens, event.Message.Usage.CacheCreationInputTokens)
						cacheWriteTokens = int(event.Message.Usage.CacheCreationInputTokens)
						cacheReadTokens = int(event.Message.Usage.CacheReadInputTokens)
					}
				}

				if err := stream.Err(); err != nil {
					debug.Log("anthropic", "Stream ERROR: %v", err)
					if rejected, parsed := maxTokensRejection(err); rejected {
						p.cap.OnRejected(parsed)
					}
					// Retry if no content has been emitted yet and the error is retryable.
					if !emitted && isRetryableForContext(ctx, err) && attempt < providerRetryAttempts-1 {
						// Notify user about retry
						delay := retryDelay(err, attempt)
						ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, providerRetryAttempts, delay)}
						if sleepErr := retrySleep(ctx, delay); sleepErr != nil {
							ch <- StreamEvent{Type: StreamEventError, Error: sleepErr}
							return
						}
						retry = true
						return
					}
					ch <- StreamEvent{Type: StreamEventError, Error: err}
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

		// If the loop exited without a successful stream (all retries exhausted),
		// report the failure instead of sending a Done event with empty usage.
		if usage == nil && outputChars == 0 {
			ch <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("anthropic stream: %d retry attempts exhausted", providerRetryAttempts)}
			return
		}

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
		ch <- StreamEvent{Type: StreamEventDone, Usage: usage}
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
		realTokens, err := p.remoteCountTokens(calCtx, messages)
		if err != nil {
			debug.Log("provider-calibrator", "first calibration failed: %v", err)
			p.calibrator.disable()
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
		realTokens, err := p.remoteCountTokens(calCtx, messages)
		if err != nil {
			debug.Log("provider-calibrator", "async calibration failed: %v", err)
			return // transient errors don't disable
		}
		p.calibrator.applyResult(estimated, realTokens)
		debug.Log("provider-calibrator", "async calibration OK: estimated=%d real=%d ratio=%.3f", estimated, realTokens, p.calibrator.currentRatio())
	})
	return result, nil
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

func (p *AnthropicProvider) buildParams(messages []Message, tools []ToolDefinition) anthropic.MessageNewParams {
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
	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: int64(p.effectiveMaxTokens()),
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

	// Dump full request JSON for debugging protocol violations.
	// Covers both Chat() (e.g. summarization) and ChatStream() (normal flow).

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
