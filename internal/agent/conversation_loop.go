package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// runConversationLoop runs the agent conversation loop verbatim from
// RunStreamWithContent (#5543 knife-4): iteration preamble, chat turns,
// no-tool handling, tool batches, and the post-loop terminators. All
// defers stay in the caller so their LIFO order is untouched.
func (a *Agent) runConversationLoop(ctx context.Context, onEvent func(provider.StreamEvent), sid string, userPromptForStats string, toolDefs []provider.ToolDefinition, runStats *RunStats, st *loopState, turnCounters *chatTurnCounters) (err error) {
	// #5635 P2: reset the status-face counters so they always describe
	// the live (or most recently finished) run.
	a.runtimeStats.beginRun()

	// #5842: let mid-tool interruption checkpoints acknowledge interjections
	// through this run's stream while a tool is still executing.
	a.setMidToolStreamEmitter(onEvent)
	defer a.setMidToolStreamEmitter(nil)

	for i := 0; a.maxIter <= 0 || i < a.maxIter; i++ {

		if st.sessionBudgetStop {

			runStats.finalize(nil)
			summary := runStats.Summary()
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventText,
				Text: fmt.Sprintf("\nSession budget exhausted (token/cost/delegated/time limit reached). No further LLM calls this run. Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message or raising the session budget.", summary),
			})
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ErrSessionBudgetExhausted})
			debug.Log("session-token-budget", "winding down: no further LLM calls (budget stop gate, sentinel returned)")
			return ErrSessionBudgetExhausted
		}
		runStats.Iterations = i + 1
		a.runtimeStats.setIteration(i + 1)
		if err := ctx.Err(); err != nil {
			return err
		}
		a.guidanceBudget.reset()

		// Wall-clock budget: the 80%/95% "wrap up" nudges are no longer injected
		// into the conversation; only reaching the limit ends the run (the
		// user-facing summary is emitted after the loop).
		if a.sessionTimeout.shouldStop() {
			debug.Log("session-timeout", "wall-clock timeout exceeded, stopping agent loop")
			st.sessionTimedOut = true
			break
		}

		if a.consumeReadyPreCompact(onEvent) {
			runStats.recordCompaction()
		}
		if injected, kind := a.injectPendingInterruptions(i + 1); injected {

			if a.maxIter > 0 && i == a.maxIter-1 {
				st.guidanceAtBudget = true
			}

			if kind == InterruptionRetractStop {
				debug.Log("agent", "retraction-stop: aborting run after user withdrew the task")
				return context.Canceled
			}
			continue
		}

		a.injectSubagentCompletions()
		a.injectMailboxMessages()
		if err := a.maybeAutoCompact(ctx, onEvent, &st.transientCompactWarned); err != nil {
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
			return err
		}
		a.ensurePromptSendable()
		msgs := a.contextManager.Messages()
		runStats.recordContextUsage(a.contextManager.TokenCount())
		if runStats.ContextWindow == 0 {
			runStats.ContextWindow = a.contextManager.ContextWindow()
		}
		if debug.IsVerbose("agent") {
			debug.Log("agent", "Iteration %d/%d: contextManager messages=%d tokens=%d threshold=%d usage_ratio=%.3f maxTokens=%d",
				i+1, a.maxIter, len(msgs), a.contextManager.TokenCount(), a.contextManager.AutoCompactThreshold(), a.contextManager.UsageRatio(), a.contextManager.ContextWindow())
		}

		a.driftRecurrence.recordIteration(i + 1)

		effortApplied, effortPrev := a.applyAdaptiveEffort()

		cascadeSwapped := a.applyModelCascade()

		activeToolDefs := a.toolSearch.activeDefs(toolDefs)
		if a.serverToolSearch {

			markServerDeferred(activeToolDefs)
		}
		if cm, ok := a.contextManager.(interface{ SetToolDefinitionOverhead(int) }); ok {
			cm.SetToolDefinitionOverhead(estimateToolDefinitionOverhead(activeToolDefs))
		}

		msgs = a.contextManager.Messages()

		resp, textBuf, toolCalls, truncated, policyBlocked, reasoningChars, err := a.chatTurn(ctx, msgs, activeToolDefs, onEvent, effortApplied, effortPrev, cascadeSwapped)
		if err != nil {
			action, finalErr := a.handleChatTurnError(ctx, err, textBuf, onEvent, turnCounters, runStats)
			if action == chatTurnContinue {
				continue
			}
			return finalErr
		}

		turnCounters.reactiveCompactRetries = 0
		turnCounters.agentLLMRetries = 0

		a.maybeSetAutopilotGoalFromLLMOutput(textBuf)

		runStats.recordTokens(resp.Usage.InputTokens, resp.Usage.OutputTokens)
		a.runtimeStats.addTokens(int64(resp.Usage.InputTokens), int64(resp.Usage.OutputTokens))
		a.syncContextManagerUsage(resp.Usage)
		a.emitUsage(resp.Usage)
		// #5812: ctxstats sampler - buffer provider cache usage and flush one
		// prefix-drift record per model response (marker-gated, no-op by default).
		if cm, ok := a.contextManager.(*ctxpkg.Manager); ok {
			cm.RecordProviderUsage(resp.Usage.CacheRead, resp.Usage.CacheWrite, resp.Usage.InputTokens)
			cm.RecordTurn("")
		}

		if msg, stop := a.RecordSessionTokenUsage(int64(resp.Usage.InputTokens), int64(resp.Usage.OutputTokens)); msg != "" {
			if a.noteUsageBudgetCrossing(i+1, msg, stop, "Session Token Budget", "session-token-budget",
				"[Session token budget fully consumed - winding down. Summarize the state so the user can resume with a fresh budget.] ", onEvent) {
				st.sessionBudgetStop = true
			}
		}

		if msg, stop := a.RecordSessionCostUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheRead, resp.Usage.CacheWrite); msg != "" {
			if a.noteUsageBudgetCrossing(i+1, msg, stop, "Session Cost Limit", "session-cost-limit",
				"[Session spend limit fully consumed - winding down. Summarize the state so the user can resume with a fresh budget.] ", onEvent) {
				st.sessionBudgetStop = true
			}
		}

		if msg, stop := a.SessionTokenBudgetAlert(); msg != "" {
			if a.noteUsageBudgetCrossing(i+1, msg, stop, "Session Token Budget", "session-token-budget",
				"[Session token budget fully consumed (including sub-agent spend) - winding down. Summarize the state so the user can resume with a fresh budget.] ", onEvent) {
				st.sessionBudgetStop = true
			}
		}

		if msg, stop := a.RecordSessionTimeUsage(); msg != "" {
			if a.noteUsageBudgetCrossing(i+1, msg, stop, "Session Time Budget", "session-time-budget",
				"[Session time budget fully elapsed - winding down. Summarize the state so the user can resume with a fresh budget.] ", onEvent) {
				st.sessionBudgetStop = true
			}
		}

		a.cacheEffMonitor.record(resp.Usage)

		if len(toolCalls) == 0 || (resp.Usage.OutputTokens == 0 && strings.TrimSpace(textBuf) == "" && !hasNonThinkingContent(resp.Message.Content)) {
			// #5543 knife-2: the empty-response / no-tool-calls path lives in
			// handleNoToolResponse; chatTurnFatal carries the former function-
			// level return (non-nil aborts, nil ends the run cleanly).
			if action, aerr := a.handleNoToolResponse(ctx, resp, textBuf, toolCalls, truncated, policyBlocked, reasoningChars, onEvent, i, runStats, userPromptForStats, st); action != chatTurnContinue || aerr != nil {
				return aerr
			}
			continue
		}
		debug.Log("agent", "Iteration %d: tool_calls=%d", i+1, len(toolCalls))
		// #5549 audit: the empty-counter reset used to live in the main loop
		// body, so ANY non-empty iteration - including productive tool turns -
		// reset the consecutive count. The knife-2 move left the reset inside
		// handleNoToolResponse, which tool turns never reach; interleaved
		// empty/tool turns then accumulated toward the abort-at-3 threshold
		// without actually being consecutive. Restore the reset here.
		st.consecutiveEmptyResponses = 0
		a.contextManager.Add(resp.Message)

		hedgingHasMutation := false
		for _, tc := range toolCalls {
			if isMutationTool(tc.Name) {
				hedgingHasMutation = true
				break
			}
		}
		if hedgingHint := a.maybeWarnActionHedging(textBuf, hedgingHasMutation); hedgingHint != "" {
			debug.Log("agent", "Iteration %d: action hedging detector detected verbalized uncertainty during mutation", i+1)
			a.recordUncertainty("hedging", weightHedging)
		}

		a.recordSpiralTurn(textBuf)
		a.strategistNoProgressCount = 0
		preExecuted := a.preExecuteReadOnlyTools(ctx, toolCalls)

		waitPreExecuted := a.preExecuteWaitTools(ctx, toolCalls)

		action, aerr, msgs := a.runToolBatch(ctx, onEvent, msgs, toolCalls, i, textBuf, reasoningChars, userPromptForStats, preExecuted, waitPreExecuted, runStats, st)
		if action != chatTurnContinue || aerr != nil {
			return aerr
		}

		if len(toolCalls) > 0 {

			speculateOK := true
			if a.contextManager != nil {
				if threshold := a.contextManager.AutoCompactThreshold(); threshold > 0 {
					fillRatio := float64(a.contextManager.TokenCount()) / float64(threshold)
					if fillRatio >= contextFillCritical {
						speculateOK = false
						debug.Log("speculate", "skipping speculation: context fill %.0f%%", fillRatio*100)
					}
				}
			}
			if speculateOK {
				lastTC := toolCalls[len(toolCalls)-1]
				a.speculator.speculate(ctx, a.tools, lastTC.Name, lastTC.Arguments)
			}
		}

		for _, msg := range st.followUpMessages {
			debug.Log("agent", "Injecting follow-up message from tool: role=%s", msg.Role)
			a.contextManager.Add(msg)
		}

		if st.deferredMemoryContent != "" {
			targetLabel := st.deferredMemoryTarget
			if targetLabel == "" {
				targetLabel = "the pending path"
			}
			a.contextManager.Add(provider.Message{
				Role:    "system",
				Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + st.deferredMemoryContent}},
			})
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf("Additional project memory now applies to %s. Review that guidance first, then continue the task with the updated constraints.", targetLabel),
				}},
			})
			a.SetProjectMemoryFiles(st.deferredMemoryFiles)
			debug.Log("agent", "injected deferred path-scoped project memory for %s (%d files)", targetLabel, len(st.deferredMemoryFiles))
		}

		// Only an explicit budget (config tool_call_budget / spawn max_tool_calls)
		// can end the run here; no default cap, no steering message is injected.
		if a.toolCallBudget.check() {
			runStats.finalize(nil)
			summary := runStats.Summary()
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventText,
				Text: fmt.Sprintf("\nTool call budget exhausted (%d calls). Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message.", a.toolCallBudget.totalCalls, summary),
			})
			err := fmt.Errorf("tool call budget (%d calls) exhausted", a.toolCallBudget.totalCalls)
			onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
			return err
		}
	}

	if st.sessionTimedOut {
		runStats.finalize(nil)
		summary := runStats.Summary()
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nSession wall-clock timeout reached (limit: %s). Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message.", a.sessionTimeout.runTimeout.Round(time.Second), summary),
		})
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ErrSessionTimeout})
		return ErrSessionTimeout
	}

	if st.sessionBudgetStop {
		runStats.finalize(nil)
		summary := runStats.Summary()
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nSession budget exhausted (token/cost/delegated/time limit reached). No further LLM calls this run. Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message or raising the session budget.", summary),
		})
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ErrSessionBudgetExhausted})
		debug.Log("session-token-budget", "budget stop armed on final iteration; sentinel surfaced post-loop (#4421)")
		return ErrSessionBudgetExhausted
	}

	if st.guidanceAtBudget {
		runStats.finalize(nil)
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nMid-run guidance arrived on the final iteration and was saved to context, but this run had no budget left to act on it. It will be consumed first on the next run. Summary: %s.", runStats.Summary()),
		})
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: ErrGuidanceAtBudget})
		return ErrGuidanceAtBudget
	}
	if a.maxIter > 0 {

		runStats.finalize(nil)
		summary := runStats.Summary()
		debug.Log("agent", "RunStreamWithContent END: max iterations reached (%s)", summary)
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\nReached maximum iterations (%d). Summary: %s.\nYour task may be partially complete - review the changes above. You can continue by sending a follow-up message.", a.maxIter, summary),
		})
		err := fmt.Errorf("max iterations (%d) reached", a.maxIter)
		onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
		return err
	}
	return nil
}
