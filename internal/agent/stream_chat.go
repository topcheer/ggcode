package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
)

// streamChatResponse consumes one provider chat stream and assembles the
// assistant turn (text/thinking/tool-call blocks, usage, stall detection,
// confidence and rate-limit advisories, stream-stop hooks).
// Extracted verbatim from agent.go (#5521 PR3 seam; behavior unchanged).
func (a *Agent) streamChatResponse(ctx context.Context, msgs []provider.Message, toolDefs []provider.ToolDefinition, onEvent func(provider.StreamEvent)) (*provider.ChatResponse, string, []provider.ToolCallDelta, bool, bool, int, error) {

	if onEvent == nil {
		onEvent = func(provider.StreamEvent) {}
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	rawStream, err := a.provider.ChatStream(streamCtx, msgs, toolDefs)
	if err != nil {
		debug.Log("agent", "ChatStream error: %v", err)
		return nil, "", nil, false, false, 0, fmt.Errorf("chat error: %w", err)
	}
	stream := streamWithStallDetection(rawStream, streamStallThreshold)
	var (
		textBuf          strings.Builder
		assistantTextBuf strings.Builder
		content          []provider.ContentBlock
		toolCalls        []provider.ToolCallDelta
		usage            provider.TokenUsage
		truncated        bool
		policyBlocked    bool
	)
	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		s := textBuf.String()

		if strings.TrimSpace(s) != "" {
			content = append(content, provider.TextBlock(s))
		}
		textBuf.Reset()
	}
	var thinkingAcc thinkingAccumulator
	turnReasoningChars := 0

	turnMetrics := newLLMTurnMetrics()
	for event := range stream {
		switch event.Type {
		case provider.StreamEventText:
			turnMetrics.markText(event.Text)
			onEvent(event)
			textBuf.WriteString(event.Text)
			assistantTextBuf.WriteString(event.Text)
		case provider.StreamEventReasoning:
			turnMetrics.markReasoning(event.Text)

			onEvent(event)
			if event.Text != "" {
				turnReasoningChars += len(event.Text)
				thinkingAcc.onText(event.Text)
			}

			if event.ThinkingSignature != "" {
				thinkingAcc.onSignature(event.ThinkingSignature)
			}
		case provider.StreamEventToolCallChunk:

			turnMetrics.markToolChunk()
			onEvent(event)
		case provider.StreamEventToolCallDone:
			turnMetrics.closeThinkWindow()
			flushText()
			onEvent(event)
			toolCalls = append(toolCalls, event.Tool)
			blk := provider.ToolUseBlock(event.Tool.ID, event.Tool.Name, event.Tool.Arguments)
			blk.ThinkingSignature = string(event.Tool.ThoughtSignature)
			blk.CallerRaw = event.Tool.Caller
			content = append(content, blk)
		case provider.StreamEventServerTool:

			flushText()
			onEvent(event)
			content = append(content, event.Block)
		case provider.StreamEventDone:
			if event.Usage != nil {
				usage = *event.Usage
			}
			truncated = event.Truncated
			policyBlocked = event.PolicyBlocked

			if event.Confidence != nil {
				conf := *event.Confidence
				debug.Log("agent", "turn confidence: avg token logprob %.3f", conf)
				// Feed the signal into the trajectory-confidence state so it
				// participates in dual-evidence intervention (#uncertainty-loop P1)
				// instead of staying a debug-only orphan.
				if a.confidence != nil {
					c := conf
					a.confidence.lastLogprob = &c
				}
				if conf < -2.5 {
					onEvent(provider.StreamEvent{
						Type: provider.StreamEventSystem,
						Text: fmt.Sprintf("[confidence] low model confidence this turn (avg token logprob %.2f) - consider reviewing the output.", conf),
					})
				}
			}

			ev := turnMetrics.emit(usage)

			stampMetricModel(a.provider, &ev)
			a.emitMetric(ev)
			onEvent(event)

			if rl, ok := a.provider.(provider.RateLimitProvider); ok {
				info := rl.RateLimitInfo()
				if info.IsCritical() && !info.IsEmpty() {
					debug.Log("agent", "rate limit warning: req %d/%d (%.0f%%), tok %d/%d (%.0f%%)",
						info.RemainingRequests, info.LimitRequests, info.RequestFractionRemaining()*100,
						info.RemainingTokens, info.LimitTokens, info.TokenFractionRemaining()*100)

					a.mu.Lock()
					firstCritical := !a.rlCriticalWarned
					a.rlCriticalWarned = true
					a.mu.Unlock()
					if firstCritical {
						onEvent(provider.StreamEvent{
							Type: provider.StreamEventSystem,
							Text: fmt.Sprintf("[rate limit] Quota nearly exhausted (requests %d/%d, tokens %d/%d remaining). Expect 429s soon - consider wrapping up or switching endpoint.",
								info.RemainingRequests, info.LimitRequests, info.RemainingTokens, info.LimitTokens),
						})
					}
				} else if !info.IsEmpty() {

					a.mu.Lock()
					a.rlCriticalWarned = false
					a.mu.Unlock()
				}
			}

			a.mu.RLock()
			streamHookCfg := a.hookConfig
			streamWorkDir := a.workingDir
			a.mu.RUnlock()
			hooks.RunStreamStopHooks(streamHookCfg, hooks.HookEnv{
				Event:      hooks.EventOnStreamStop,
				Workspace:  streamWorkDir,
				WorkingDir: streamWorkDir,
				StopReason: "completed",
			})
		case provider.StreamEventSystem:

			onEvent(event)
		case provider.StreamEventError:
			debug.Log("agent", "ChatStream event error: %v", event.Error)
			return nil, assistantTextBuf.String(), nil, false, false, turnReasoningChars, fmt.Errorf("chat error: %w", event.Error)
		}
	}
	flushText()

	respMsg := provider.Message{
		Role:    "assistant",
		Content: content,
	}

	if blocks := thinkingAcc.accumulated(); len(blocks) > 0 {
		respMsg.Content = append(blocks, respMsg.Content...)
	}
	return &provider.ChatResponse{
		Message: respMsg,
		Usage:   usage,
	}, assistantTextBuf.String(), toolCalls, truncated, policyBlocked, turnReasoningChars, nil
}
