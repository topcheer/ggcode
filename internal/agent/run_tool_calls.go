package agent

import (
	"context"
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// #5543: loop-core extraction. The tool-execution half of the stream loop
// (everything after the no-tool-calls branch: hedging analysis, the
// per-tool-call idx loop, guard-zone watchers, staleness/verification
// wiring) becomes an explicit method; behavior unchanged (verbatim body,
// control flow via returned signals).

// toolCallsAction tells the stream loop what to do after the tool-execution
// zone completes.
type toolCallsAction int

const (
	toolCallsContinue toolCallsAction = iota // loop continue (skip post-zone work)
	toolCallsTerminal                        // stop the run; paired error is final
)

// runToolCallsZone executes the tool phase of one stream iteration
// (verbatim order): action-hedging detection over the announced calls, the
// per-call idx loop (approval, execution, result recording, background
// verify registration), and post-loop staleness notes. Terminal paths
// return toolCallsTerminal with the final error; the single early
// loop-continue returns toolCallsContinue.
func (a *Agent) runToolCallsZone(
	ctx context.Context,
	i int,
	resp *provider.ChatResponse,
	textBuf string,
	toolCalls []provider.ToolCallDelta,
	truncated bool,
	policyBlocked bool,
	reasoningChars int,
	runStats *RunStats,
	userPromptForStats string,
	msgs []provider.Message,
	onEvent func(provider.StreamEvent),
) (toolCallsAction, error) {
	debug.Log("agent", "Iteration %d: tool_calls=%d", i+1, len(toolCalls))
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
	var toolResults []provider.ContentBlock
	var followUpMessages []provider.Message

	preExecuted := a.preExecuteReadOnlyTools(ctx, toolCalls)

	waitPreExecuted := a.preExecuteWaitTools(ctx, toolCalls)

	batchConflictWarnings := detectBatchEditConflicts(toolCalls)

	if depWarn := a.recordToolCallBatch(toolCalls, i+1); depWarn != "" {
		debug.Log("agent", "Iteration %d: counterfactual dependency assumption detected", i+1)
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: depWarn}},
		})
		msgs = a.contextManager.Messages()
	}

	if len(toolCalls) > 1 {
		var batchInfos []couplingToolCall
		for _, tc := range toolCalls {
			batchInfos = append(batchInfos, couplingToolCall{name: tc.Name, args: tc.Arguments})
		}
		if couplingWarn := a.batchCoupling.checkBatchCoupling(batchInfos); couplingWarn != "" {
			debug.Log("agent", "Iteration %d: batch tool call coupling detected", i+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: couplingWarn}},
			})
			msgs = a.contextManager.Messages()
		}
	}

	if a.irrevGate != nil {
		for _, tc := range toolCalls {
			if warn := a.irrevGate.recordAction(tc.Name, string(tc.Arguments)); warn != "" {
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: warn}},
				})
			}
		}
	}

	if a.mergeGate != nil {
		mode := a.currentMode()
		for _, tc := range toolCalls {
			if req := a.mergeGate.checkCommand(mode, tc.Name, string(tc.Arguments)); req != "" {
				debug.Log("agent", "Iteration %d: merge gate requirement injected", i+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: req}},
				})
			}
		}
	}

	if hint := a.maybeToolflowSuggestion(a.runToolNames); hint != "" {
		debug.Log("agent", "injected toolflow next-step hint (%d chars)", len(hint))
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: hint}},
		})
	}

	a.cascade.beginBatch()

	for _, tc := range toolCalls {
		a.cascade.notePlanned(tc.Name)
	}

	if a.mindlessAction.recordStep(len(textBuf)+reasoningChars, true) {
		debug.Log("agent", "Iteration %d: mindless action detector triggered (streak=%d)", i+1, a.mindlessAction.streak)
		a.injectGuidance(mindlessActionWarning(a.mindlessAction.streak))
	}
	toolResults, followUpMessages, deferredMemoryContent, deferredMemoryFiles, deferredMemoryTarget, idxAction, idxErr := a.runToolCallIdxLoop(ctx, i, resp, textBuf, toolCalls, truncated, policyBlocked, runStats, userPromptForStats, msgs, onEvent, batchConflictWarnings, preExecuted, waitPreExecuted)
	if idxErr != nil {
		return idxAction, idxErr
	}
	if idxAction == toolCallsTerminal {
		// HaltAgent (#5520) and the in-batch cancel path return terminal
		// with a nil error: results were already landed in context inside
		// the idx loop and the run must unwind cleanly without any
		// post-zone work (verbatim `return nil` of the former inline
		// shape).
		return toolCallsTerminal, nil
	}
	if idxAction == toolCallsContinue && toolResults == nil {
		return toolCallsContinue, nil
	}
	if err := ctx.Err(); err != nil {

		if len(toolResults) > 0 {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: toolResults,
			})
		}
		return toolCallsTerminal, err
	}
	if len(toolResults) == 0 {
		return toolCallsContinue, nil
	}
	debug.Log("agent", "Adding tool results to contextManager: blocks=%d", len(toolResults))
	a.contextManager.Add(provider.Message{
		Role:    "user",
		Content: toolResults,
	})

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

	for _, msg := range followUpMessages {
		debug.Log("agent", "Injecting follow-up message from tool: role=%s", msg.Role)
		a.contextManager.Add(msg)
	}

	if deferredMemoryContent != "" {
		targetLabel := deferredMemoryTarget
		if targetLabel == "" {
			targetLabel = "the pending path"
		}
		a.contextManager.Add(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + deferredMemoryContent}},
		})
		a.contextManager.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: fmt.Sprintf("Additional project memory now applies to %s. Review that guidance first, then continue the task with the updated constraints.", targetLabel),
			}},
		})
		a.SetProjectMemoryFiles(deferredMemoryFiles)
		debug.Log("agent", "injected deferred path-scoped project memory for %s (%d files)", targetLabel, len(deferredMemoryFiles))
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
			return toolCallsTerminal, err
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
	return toolCallsContinue, nil
}
