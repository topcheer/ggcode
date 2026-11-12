package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// #5543: chat-turn retry-chain extraction. The former inline closure + err
// handling chain in RunStreamWithContent becomes explicit methods; behavior
// unchanged (verbatim bodies, control flow via returned signals).

// chatTurnCounters carries the cross-iteration retry counters (loop-local
// in the former inline shape; passed by pointer).
type chatTurnCounters struct {
	rateLimitRetries       int
	agentLLMRetries        int
	reactiveCompactRetries int
}

// chatTurnAction tells the stream loop what to do after error handling.
type chatTurnAction int

const (
	chatTurnContinue chatTurnAction = iota // loop continue (retry armed)
	chatTurnFatal                          // return the paired error
)

// chatTurn performs one chat-stream call with the effort/cascade restore
// defer (former 11-line inline closure, flattened; zero logic change).
func (a *Agent) chatTurn(ctx context.Context, msgs []provider.Message, activeToolDefs []provider.ToolDefinition, onEvent func(provider.StreamEvent), effortApplied string, effortPrev string, cascadeSwapped bool) (*provider.ChatResponse, string, []provider.ToolCallDelta, bool, bool, int, error) {
	defer func() {
		if effortApplied != "" {
			a.restoreEffort(effortPrev)
		}
		if cascadeSwapped {
			a.restoreModelCascade()
		}
	}()
	return a.streamChatResponse(ctx, a.ensureMessagesSendable(msgs), activeToolDefs, onEvent)
}

// handleChatTurnError runs the full chat-error chain (verbatim order):
// replan interrupt, reactive compaction, rate-limited sawtooth backoff,
// transient-LLM retry, then partial-text preservation + cancel/friendly
// classification. Retry paths reset/advance the shared counters and return
// chatTurnContinue; everything terminal returns chatTurnFatal with the
// final error.
func (a *Agent) handleChatTurnError(ctx context.Context, err error, textBuf string, onEvent func(provider.StreamEvent), c *chatTurnCounters, runStats *RunStats) (chatTurnAction, error) {
	a.cascade.resetBatches()
	if errors.Is(err, errStreamInterruptedForReplan) {
		c.reactiveCompactRetries = 0
		c.agentLLMRetries = 0
		return chatTurnContinue, nil
	}
	if a.tryReactiveCompact(ctx, onEvent, err, &c.reactiveCompactRetries) {
		runStats.recordCompaction()
		return chatTurnContinue, nil
	}

	// Rate-limited calls get a sawtooth exponential backoff (1s doubling to
	// the cap, cycling) that is independent of the short-backoff counter AND
	// of the English-keyword retryable gate below: GLM window-limit errors
	// arrive in Chinese and would never reach a branch nested inside that
	// gate. ClassifyLLMError already recognizes them. Retry in place until
	// success or cancel, and tell the user how to switch provider/endpoint
	// meanwhile.
	if isRateLimitedLLMError(err) {
		capInterval, maxAttempts := rateLimitRetryPolicy()
		if maxAttempts > 0 && c.rateLimitRetries >= maxAttempts {
			debug.Log("agent", "rate-limit retry budget exhausted (%d/%d): %v", c.rateLimitRetries, maxAttempts, err)
		} else {
			c.rateLimitRetries++
			delay := rateLimitBackoffDelay(c.rateLimitRetries, capInterval)
			if c.rateLimitRetries == 1 {
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem,
					Text: fmt.Sprintf("[Rate limited by the provider - auto-retrying with exponential backoff (1s doubling to %s, then cycling; Ctrl+C to stop). "+
						"This may be a short throttle or a plan window quota that resets later. "+
						"To continue immediately, switch to another provider/endpoint (see /model or your provider config).]", capInterval)})
			} else {
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem,
					Text: fmt.Sprintf("[Still rate limited - retry %d in %s...]", c.rateLimitRetries, delay)})
			}
			debug.Log("agent", "rate limited (attempt %d/%d cycle-pos backoff %v, cap %v): %v",
				c.rateLimitRetries, maxAttempts, delay, capInterval, err)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ctx.Err()})
				return chatTurnFatal, ctx.Err()
			}
			return chatTurnContinue, nil
		}
	}

	if isAgentRetryableLLMError(err) && c.agentLLMRetries < agentLLMRetryLimit() {

		c.agentLLMRetries++

		multiplier := 2
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "rate_limit") || strings.Contains(errStr, "too many") || strings.Contains(errStr, "overloaded") {
			multiplier = 5
		}
		delay := time.Duration(c.agentLLMRetries*multiplier) * time.Second
		debug.Log("agent", "transient LLM error (attempt %d/%d), retrying in %v: %v",
			c.agentLLMRetries, agentLLMRetryLimit(), delay, err)
		onEvent(provider.StreamEvent{Type: provider.StreamEventSystem,
			Text: fmt.Sprintf("[Retrying LLM call (%d/%d) after %v...] ",
				c.agentLLMRetries, agentLLMRetryLimit(), delay)})
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ctx.Err()})
			return chatTurnFatal, ctx.Err()
		}
		return chatTurnContinue, nil
	}

	if strings.TrimSpace(textBuf) != "" {
		a.contextManager.Add(provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{provider.TextBlock(textBuf)},
		})
	}

	if errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)) {

		cancelErr := ctx.Err()
		if cancelErr == nil {
			cancelErr = err
		}
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: cancelErr})
		return chatTurnFatal, cancelErr
	}

	friendlyErr := provider.WrapFriendly(err)
	onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: friendlyErr})
	return chatTurnFatal, friendlyErr
}

// noteUsageBudgetCrossing is the shared shape of the four session-budget
// crossings (token/cost/delegated-token/time): consensus firing, guidance
// injection, debug line, and the stop event. Returns whether the stop flag
// should be set for this iteration.
func (a *Agent) noteUsageBudgetCrossing(iter int, msg string, stop bool, detector, debugCat, stopText string, onEvent func(provider.StreamEvent)) bool {
	a.crossDetectorConsensus.recordFiring(detector, iter)
	a.injectGuidance(msg)
	debug.Log(debugCat, "threshold crossed at iteration %d stop=%v", iter, stop)
	if stop {
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventSystem,
			Text: stopText,
		})
		return true
	}
	return false
}
