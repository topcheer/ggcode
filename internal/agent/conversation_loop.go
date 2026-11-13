package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// runConversationLoop runs the agent conversation loop verbatim from
// RunStreamWithContent (#5543 knife-4): iteration preamble, chat turns,
// no-tool handling, tool batches, and the post-loop terminators. All
// defers stay in the caller so their LIFO order is untouched.
func (a *Agent) runConversationLoop(ctx context.Context, onEvent func(provider.StreamEvent), sid string, userPromptForStats string, toolDefs []provider.ToolDefinition, runStats *RunStats, st *loopState, turnCounters *chatTurnCounters) (err error) {
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
		if err := ctx.Err(); err != nil {
			return err
		}
		a.guidanceBudget.reset()

		if msg := a.sessionTimeout.check(); msg != "" {
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventSystem,
				Text: msg,
			})
			if a.sessionTimeout.shouldStop() {
				debug.Log("session-timeout", "wall-clock timeout exceeded, stopping agent loop")
				st.sessionTimedOut = true
				break
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
				return context.Canceled
			}
			continue
		}

		a.injectSubagentCompletions()
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

		if planHint := a.maybeSuggestPlan(i + 1); planHint != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: planHint}},
			})
			msgs = a.contextManager.Messages()
		}

		if staleReminder := a.maybeRemindStaleTodo(i + 1); staleReminder != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: staleReminder}},
			})
			msgs = a.contextManager.Messages()
		}

		if staleMsg := a.fileFreshness.maybeCheckStaleFiles(i + 1); staleMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: staleMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		if thermalMsg := a.toolThermal.maybeWarn(i); thermalMsg != "" {
			debug.Log("thermal-profile", "imbalanced tool usage detected at iteration %d: %s", i+1, a.toolThermal.categoryBreakdown())
			msgs = a.guidanceEmit(thermalMsg, msgs)
		}

		if ecMsg := a.errorCompound.maybeWarn(i + 1); ecMsg != "" {

			if a.injectGuidance(ecMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.errorCompound.markUndelivered()
			}
		}

		if csMsg := a.correctionSpiral.maybeWarn(i + 1); csMsg != "" {
			msgs = a.guidanceEmit(csMsg, msgs)
		}

		if vdMsg := a.verifyDebt.maybeWarn(i + 1); vdMsg != "" {
			msgs = a.guidanceEmit(vdMsg, msgs)
		}

		if epMsg := a.editPropagation.maybeWarn(i + 1); epMsg != "" {
			msgs = a.guidanceEmit(epMsg, msgs)
		}

		if a.claimsSupervision {
			if sgMsg := a.subgoalTrack.maybeWarn(i + 1); sgMsg != "" {
				debug.Log("agent", "Iteration %d: subgoal completion gap detected", i+1)

				if a.injectGuidance(sgMsg) {
					msgs = a.contextManager.Messages()
				} else {
					a.subgoalTrack.markUndelivered()
				}
			}
		}

		if a.claimsSupervision {
			if sdMsg := a.successDeclare.maybeWarn(i + 1); sdMsg != "" {
				debug.Log("agent", "Iteration %d: premature success declaration detected", i+1)

				if a.injectGuidance(sdMsg) {
					msgs = a.contextManager.Messages()
				} else {
					a.successDeclare.markUndelivered()
				}
			}
		}
		if cdMsg := a.criteriaDrift.maybeWarn(i + 1); cdMsg != "" {
			debug.Log("agent", "Iteration %d: success criteria drift detected", i+1)
			msgs = a.guidanceEmit(cdMsg, msgs)
		}

		if abMsg := a.attemptBrief.maybeBrief(i + 1); abMsg != "" {
			debug.Log("agent", "Iteration %d: injecting attempt brief", i+1)
			msgs = a.guidanceEmit(abMsg, msgs)
		}

		if scentMsg := a.infoScent.maybeWarn(i + 1); scentMsg != "" {
			msgs = a.guidanceEmit(scentMsg, msgs)
		}

		if qcMsg := a.queryConverge.maybeWarn(i + 1); qcMsg != "" {

			if a.injectGuidance(qcMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.queryConverge.markUndelivered()
			}
		}
		if bgOrphanMsg := a.maybeWarnBgOrphan(i + 1); bgOrphanMsg != "" {
			msgs = a.guidanceEmit(bgOrphanMsg, msgs)
		}

		if rrMsg := a.reasoningRedund.maybeWarn(i+1, a.maxIter); rrMsg != "" {
			debug.Log("reasoning-redund", "Iteration %d: reasoning redundancy detected -- consecutive text-only overthinking", i+1)

			if a.injectGuidance(rrMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.reasoningRedund.markUndelivered()
			}
		}

		if ipMsg := a.maybeWarnIterPressure(i + 1); ipMsg != "" {
			msgs = a.guidanceEmit(ipMsg, msgs)
		}

		if ptMsg := a.maybeWarnTermination(); ptMsg != "" {
			msgs = a.guidanceEmit(ptMsg, msgs)
		}

		if bsMsg := a.bareEditStreak.maybeWarn(i + 1); bsMsg != "" {

			if a.injectGuidance(bsMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.bareEditStreak.markUndelivered()
			}
		}

		if sfMsg := a.strategyFixation.check(); sfMsg != "" {
			msgs = a.guidanceEmit(sfMsg, msgs)
		}

		if erMsg := a.errorRush.check(); erMsg != "" {
			msgs = a.guidanceEmit(erMsg, msgs)
		}

		if afMsg := a.attentionFragment.analyze(); afMsg != "" {
			msgs = a.guidanceEmit(afMsg, msgs)
		}

		a.driftRecurrence.recordIteration(i + 1)

		if fcMsg := a.futileCycle.maybeWarn(i + 1); fcMsg != "" {

			if a.injectGuidance(fcMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.futileCycle.markUndelivered()
			}
		}

		if caMsg := a.constraintAmnesia.maybeWarn(i + 1); caMsg != "" {

			if a.injectGuidance(caMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.constraintAmnesia.markUndelivered()
			}
		}

		if a.delegationOrch != nil {

			if delegationOrphanGateEnabled {
				if delOrchMsg := a.delegationOrch.maybeWarnOrphanedDelegations(i + 1); delOrchMsg != "" {
					debug.Log("agent", "Iteration %d: delegation orphan gate injected guidance", i+1)
					msgs = a.guidanceEmit(delOrchMsg, msgs)
				}
			}
			if delegationSerialGateEnabled {
				if serialMsg := a.delegationOrch.maybeWarnSerialDelegation(); serialMsg != "" {
					debug.Log("agent", "Iteration %d: serial delegation gate injected guidance", i+1)
					msgs = a.guidanceEmit(serialMsg, msgs)
				}
			}
			if overDelMsg := a.delegationOrch.maybeWarnOverDelegation(); overDelMsg != "" {
				debug.Log("agent", "Iteration %d: over-delegation gate injected guidance", i+1)
				msgs = a.guidanceEmit(overDelMsg, msgs)
			}
		}

		if monorepoMsg := a.monorepoScoper.maybeWarnScopeSprawl(); monorepoMsg != "" {
			debug.Log("monorepo-scope", "package scope sprawl detected: %s", monorepoMsg)

			if a.injectGuidance(monorepoMsg) {
				msgs = a.contextManager.Messages()
			} else {
				a.monorepoScoper.markUndelivered()
			}
		}

		if a.maxIter >= 20 && !st.progressCheckInjected && i+1 >= a.maxIter*3/5 {
			st.progressCheckInjected = true
			debug.Log("agent", "Injecting mid-point progress checkpoint at iteration %d/%d", i+1, a.maxIter)

			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf(
					"Progress checkpoint: iteration %d/%d. Assess - on track? If not, switch strategy.",
					i+1, a.maxIter,
				)}},
			})
			msgs = a.contextManager.Messages()
		}

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
		a.syncContextManagerUsage(resp.Usage)
		a.emitUsage(resp.Usage)

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

		if cacheGuidance := a.cacheEffMonitor.record(resp.Usage); cacheGuidance != "" {

			a.crossDetectorConsensus.recordFiring("Cache Efficiency", i+1)
			a.injectGuidance(cacheGuidance)
			debug.Log("cache-efficiency", "injecting cache bust storm guidance at iteration %d", i+1)
		}

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
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: hedgingHint,
				}},
			})
		}

		if compHint := a.maybeWarnCompoundedUncertainty(); compHint != "" {
			debug.Log("agent", "Iteration %d: compounded trajectory uncertainty threshold crossed", i+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: compHint,
				}},
			})
		}

		a.recordSpiralTurn(textBuf)
		if spiralHint := a.maybeWarnSpiralHallucination(); spiralHint != "" {
			debug.Log("agent", "Iteration %d: spiral of hallucination pattern detected", i+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: spiralHint,
				}},
			})
		}

		a.strategistNoProgressCount = 0
		preExecuted := a.preExecuteReadOnlyTools(ctx, toolCalls)

		waitPreExecuted := a.preExecuteWaitTools(ctx, toolCalls)

		action, aerr, msgs := a.runToolBatch(ctx, onEvent, msgs, toolCalls, i, textBuf, reasoningChars, userPromptForStats, preExecuted, waitPreExecuted, runStats, st)
		if action != chatTurnContinue || aerr != nil {
			return aerr
		}

		if serialWarn := a.serialRead.endTurn(i + 1); serialWarn != "" {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: serialWarn,
				}},
			})
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

		if tcMsg, stop := a.toolCallBudget.check(); tcMsg != "" {
			debug.Log("tool-call-budget", "threshold crossed: calls=%d budget=%d stop=%v",
				a.toolCallBudget.totalCalls, a.toolCallBudget.effectiveBudget(), stop)
			if stop {

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

			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: tcMsg,
				}},
			})
			msgs = a.contextManager.Messages()
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
