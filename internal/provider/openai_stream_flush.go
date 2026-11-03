package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/topcheer/ggcode/internal/debug"
)

func (p *OpenAIProvider) flushResidualToolCalls(ctx context.Context, ch chan<- StreamEvent, toolCalls map[int]*ToolCallDelta, normalEnd, retry bool) (chars int, emittedAny bool, ok bool) {
	shouldFlush := normalEnd && !retry
	if !shouldFlush {
		return 0, false, true
	}

	for _, idx := range slices.Sorted(maps.Keys(toolCalls)) {
		tc := toolCalls[idx]
		if tc.Name == "" || tc.ID == "" {
			continue
		}

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

func (p *OpenAIProvider) sleepBeforeRetry(ctx context.Context, ch chan<- StreamEvent, budget *retryBudget, cause error, attempt int) (bool, error) {

	if isTimeoutClassError(cause) {
		budget.unlimited = true
	}
	delay := retryDelay(cause, attempt)
	if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, p.policy.attempts(), delay)}) {
		return false, nil
	}
	if notice := TimeoutRetryNotice(cause, attempt); notice != "" {
		if !p.sendEvent(ctx, ch, StreamEvent{Type: StreamEventSystem, Text: notice}) {
			return false, nil
		}
	}
	if sleepErr := budget.sleep(ctx, delay); sleepErr != nil {

		if sleepErr == errRetryBudgetExhausted {
			sleepErr = fmt.Errorf("%w: %w", errRetryBudgetExhausted, cause)
		}
		return false, sleepErr
	}
	return true, nil
}
