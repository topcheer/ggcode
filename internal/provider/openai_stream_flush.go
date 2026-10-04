package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

// flushResidualToolCalls emits tool_calls that accumulated but never saw a
// chunk with a non-empty finish_reason (#302-era LiteLLM/vLLM/ZAI compat
// flush). Extracted verbatim from ChatStream's deferred flush — behavior
// identical: skip unnamed/id-less calls, repair nearly-valid JSON args, never
// flush on retry (double-execute) or hard error (untrustworthy partial args).
// Returns the character count added and whether anything was emitted; ok=false
// means the consumer context died mid-flush.
func (p *OpenAIProvider) flushResidualToolCalls(ctx context.Context, ch chan<- StreamEvent, toolCalls map[int]*ToolCallDelta, normalEnd, retry bool) (chars int, emittedAny bool, ok bool) {
	shouldFlush := normalEnd && !retry // #302: cancel no longer flushes half-made tool calls
	if !shouldFlush {
		return 0, false, true
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
		chars += len(tc.Name) + len(tc.Arguments)
		emittedAny = true
		if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventToolCallDone, Tool: *tc}) {
			return chars, emittedAny, false
		}
		delete(toolCalls, idx)
	}
	return chars, emittedAny, true
}

// sleepBeforeRetry emits the "[Retry n/m...]" system event and sleeps under
// the shared retry budget (#722). Extracted verbatim from the two duplicated
// retry blocks in ChatStream (connect-phase and stream-phase). Returns
// (slept, terminalErr): terminalErr non-nil means the budget is exhausted and
// the caller must emit Error{terminalErr}, set streamError, and return;
// slept=false with nil err means the consumer context died mid-emit, return.
func (p *OpenAIProvider) sleepBeforeRetry(ctx context.Context, ch chan<- StreamEvent, budget *retryBudget, cause error, attempt int) (bool, error) {
	delay := retryDelay(cause, attempt)
	if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, p.policy.attempts(), delay)}) {
		return false, nil
	}
	if sleepErr := budget.sleep(ctx, delay); sleepErr != nil {
		// #722: budget exhausted — stop retrying now; wrap with the
		// sentinel so the failover layer switches immediately.
		if sleepErr == errRetryBudgetExhausted {
			sleepErr = fmt.Errorf("%w: %w", errRetryBudgetExhausted, cause)
		}
		return false, sleepErr
	}
	return true, nil
}
