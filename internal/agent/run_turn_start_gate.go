package agent

import (
	"context"
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// #5543: loop-core extraction. The turn-start gate of the stream loop
// (session budget stop, ctx cancellation, wall-clock timeout, ready
// pre-compaction, pending interruptions, subagent completions, auto
// compaction) becomes an explicit method; behavior unchanged (verbatim
// body, control flow via returned signals).

// turnGateAction tells the stream loop what to do after the turn-start
// gate completes.
type turnGateAction int

const (
	turnGateProceed  turnGateAction = iota // continue to detector guidance + chat turn
	turnGateContinue                       // next loop iteration (interruption injected)
	turnGateBreak                          // leave the loop (wall-clock timeout)
	turnGateTerminal                       // stop the run; paired error is final
)

// runTurnStartGate runs the per-iteration preamble (verbatim order). It
// arms sessionTimedOut before returning turnGateBreak, and the
// retraction-stop interruption returns turnGateTerminal with
// context.Canceled.
func (a *Agent) runTurnStartGate(
	ctx context.Context,
	i int,
	runStats *RunStats,
	sessionBudgetStop bool,
	transientCompactWarned *bool,
	st *noToolCallsState,
	onEvent func(provider.StreamEvent),
) (turnGateAction, error) {
	if sessionBudgetStop {

		runStats.finalize(nil)
		summary := runStats.Summary()
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nSession budget exhausted (token/cost/delegated/time limit reached). No further LLM calls this run. Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message or raising the session budget.", summary),
		})
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ErrSessionBudgetExhausted})
		debug.Log("session-token-budget", "winding down: no further LLM calls (budget stop gate, sentinel returned)")
		return turnGateTerminal, ErrSessionBudgetExhausted
	}
	runStats.Iterations = i + 1
	if err := ctx.Err(); err != nil {
		return turnGateTerminal, err
	}
	a.guidanceBudget.reset()

	if msg := a.sessionTimeout.check(); msg != "" {
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventSystem,
			Text: msg,
		})
		if a.sessionTimeout.shouldStop() {
			debug.Log("session-timeout", "wall-clock timeout exceeded, stopping agent loop")
			return turnGateBreak, nil
		}
		a.contextManager.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: msg,
			}},
		})
	}

	if a.consumeReadyPreCompact(onEvent) {
		runStats.recordCompaction()
	}
	if injected, kind := a.injectPendingInterruptions(i + 1); injected {
		a.recordIntervention(i + 1)

		if a.maxIter > 0 && i == a.maxIter-1 {
			st.guidanceAtBudget = true
		}

		if kind == InterruptionRetractStop {
			debug.Log("agent", "retraction-stop: aborting run after user withdrew the task")
			return turnGateTerminal, context.Canceled
		}
		return turnGateContinue, nil
	}

	a.injectSubagentCompletions()
	if err := a.maybeAutoCompact(ctx, onEvent, transientCompactWarned); err != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
		return turnGateTerminal, err
	}
	return turnGateProceed, nil
}
