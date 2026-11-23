package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

// runToolBatch executes one iteration's tool-call batch verbatim from
// RunStreamWithContent (#5543 knife-3): dedup/memo prechecks, execution with
// adaptive timeout, the inform families (post-tool watchers consume via the
// registry), result assembly and context injection. Loop-level `continue`
// keeps its in-loop meaning; function-level returns become chatTurnFatal
// signals (nil = clean stop such as session handover). msgs flows in and
// back out.
func (a *Agent) runToolBatch(ctx context.Context, onEvent func(provider.StreamEvent), msgs []provider.Message, toolCalls []provider.ToolCallDelta, iter int, textBuf string, reasoningChars int, userPromptForStats string, preExecuted map[int]preExecutedResult, waitPreExecuted map[int]preExecutedResult, runStats *RunStats, st *loopState) (chatTurnAction, error, []provider.Message) {
	var toolResults []provider.ContentBlock
	// #5635 P2: inflight face - clear the main-agent current-tool marker
	// when the batch ends (any exit path, hence the defer).
	defer a.currentTool.clear()
	batchConflictWarnings := detectBatchEditConflicts(toolCalls)
	seenReadOnly := make(map[dedupKey]int)

	a.cascade.beginBatch()

	for _, tc := range toolCalls {
		a.cascade.notePlanned(tc.Name)
	}

	_ = a.mindlessAction.recordStep(len(textBuf)+reasoningChars, true)
	for idx, tc := range toolCalls {
		if err := ctx.Err(); err != nil {

			a.fillCancelledToolResults(toolCalls[idx:], &toolResults)
			return chatTurnFatal, err, msgs
		}
		if a.takeMidToolStop() {

			debug.Log("agent", "Iteration %d: mid-tool retraction-stop: aborting remaining %d tool call(s)", iter+1, len(toolCalls)-idx)
			a.fillCancelledToolResults(toolCalls[idx:], &toolResults)
			a.flushStagedMidToolInterruptions(iter + 1)
			return chatTurnFatal, context.Canceled, msgs
		}
		// #5635 P2: inflight face - mark this call as the main agent's
		// current tool while it executes.
		a.currentTool.begin(tc.Name, string(tc.Arguments))
		if mc, mf, mt := a.pendingProjectMemoryForTool(tc); len(mf) > 0 && strings.TrimSpace(mc) != "" {
			if st.deferredMemoryContent == "" {
				st.deferredMemoryContent = mc
				st.deferredMemoryFiles = mf
				st.deferredMemoryTarget = mt
			}
		}

		dedupK := dedupKey{tool: tc.Name, args: string(tc.Arguments)}
		if speculativeSafeTools[tc.Name] {
			if firstIdx, ok := seenReadOnly[dedupK]; ok && firstIdx < len(toolResults) {
				dedupContent := toolResults[firstIdx].Text
				debug.Log("agent", "in-turn dedup: %s already executed in this response, reusing result", tc.Name)
				toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, dedupContent, false))
				onEvent(provider.StreamEvent{
					Type:    provider.StreamEventToolResult,
					Tool:    tc,
					Result:  dedupContent,
					IsError: false,
				})
				continue
			}
		}

		askAdvisory := ""
		if tc.Name == "ask_user" {
			if blockMsg, advMsg := a.checkAskQualityGate(string(tc.Arguments)); blockMsg != "" {
				toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, blockMsg, true))
				onEvent(provider.StreamEvent{
					Type:    provider.StreamEventToolResult,
					Tool:    tc,
					Result:  blockMsg,
					IsError: true,
				})
				continue
			} else if advMsg != "" {
				askAdvisory = advMsg
			}
		}

		if redactWarn := checkRedactedInWrite(tc.Name, string(tc.Arguments)); redactWarn != "" {
			toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, redactWarn, true))
			onEvent(provider.StreamEvent{
				Type:    provider.StreamEventToolResult,
				Tool:    tc,
				Result:  redactWarn,
				IsError: true,
			})
			continue
		}

		runStats.recordToolCall(tc.Name)
		a.toolCallBudget.record()
		extractPathsFromToolCall(tc.Name, tc.Arguments, runStats)
		var result tool.Result
		memoHit := false
		if memoResult, hit := a.toolMemo.get(tc.Name, tc.Arguments); hit && a.speculativeHitAllowed(ctx, tc) {
			result = memoResult
			memoHit = true

			if result.Content != "" && !result.IsError {
				result.Content = fmt.Sprintf("[cached - %s returned identical content since your last call]\n%s", tc.Name, result.Content)
			}
			debug.Log("memoize", "memo hit for %s (saved tool execution)", tc.Name)
		} else if cachedResult, hit := a.speculator.getCached(tc.Name, tc.Arguments); hit && a.speculativeHitAllowed(ctx, tc) {

			result = cachedResult
			debug.Log("speculate", "speculative cache hit for %s (saved tool execution)", tc.Name)
		} else if pre, ok := preExecuted[idx]; ok {

			result = a.usePreExecutedWithPermission(ctx, tc, pre)
		} else if pre, ok := waitPreExecuted[idx]; ok {

			result = a.usePreExecutedWithPermission(ctx, tc, pre)
		} else if cmdCached, hit := a.checkCommandCache(tc.Name, tc.Arguments); hit && a.speculativeHitAllowed(ctx, tc) {

			result = cmdCached
		} else {
			execStart := time.Now()
			result = a.executeToolWithPermissionGate(ctx, tc, onEvent)
			if isWaitClassTool(tc.Name) {
				runStats.addWaitTime(time.Since(execStart))
			}

			a.storeCommandResult(tc.Name, tc.Arguments, result)

			if adv := a.checkoutAdvisory(tc.Name, tc.Arguments); adv != "" {
				result.Content += adv
			}
			a.recordEffectAttempt(tc.Name, tc.Arguments, result)
		}

		a.exfilChain.recordSensitiveSource(tc.Name, string(tc.Arguments), result.Content)
		result.Content = redactSecrets(tc.Name, result.Content)

		a.speculator.recordObservation(tc.Name)

		if tc.Name == "todo_write" && !result.IsError {
			a.plannerMarkTodoCreated()

		}

		if tc.Name == "ask_user" && !result.IsError {
			a.markAskAskedQG(string(tc.Arguments))
		}

		a.termGuard.recordToolStep(result.IsError)
		wroteDespiteError := partialEditWroteDespiteError(tc.Name, result.Content, result.IsError)
		if mutatesSourceTree(tc.Name) && (!result.IsError || wroteDespiteError) {
			a.speculator.invalidateCache()

			if gitWholeTreeTools[tc.Name] {
				a.toolMemo.invalidateAll()
				debug.Log("agent", "whole-tree git operation %s: invalidated all caches", tc.Name)
			} else {

				a.toolMemo.invalidateTTLBased()
			}

			a.commandCache.invalidate()

			for _, p := range extractCreateFilePaths(tc.Name, tc.Arguments) {
				if a.turnImpact != nil {
					a.turnImpact.recordFileEdit(tc.Name, resolveImpactPath(a.WorkingDir(), p))
				}
			}

			if a.codeIndex != nil {
				a.codeIndex.MarkDirty(extractEditedPaths(tc))
			}
		}

		if tc.Name == "run_command" || tc.Name == "start_command" {
			if cmd, _ := parseRunCommandArgs(tc.Arguments); shellMutatesSources(cmd) {
				a.speculator.invalidateCache()
				a.toolMemo.invalidateTTLBased()
				a.commandCache.invalidate()

				a.getUserEditObserver().RestampBaseline()
				if a.turnImpact != nil {
					a.turnImpact.recordShellMutation(cmd, shellDestructive(cmd))
				}
				// Ring-noise control: keep only the command head; the full string can
				// embed multi-KB payloads and floods the ring.
				debug.Log("agent", "shell source mutation %q (failed cmd included): invalidated command/speculator/memo caches", truncateString(cmd, 120))
			}
		}

		if speculativeSafeTools[tc.Name] && !result.IsError && !memoHit {
			a.toolMemo.put(tc.Name, tc.Arguments, result)
		}

		// #5244: apply_patch (OpenAI Responses-native) edits source too,
		// but #3006/#737 pin sourceMutatingTools at nine - extensions
		// ride mutatesSourceTree() semantics instead of the canonical
		// map (#5249: helper shared with the other tracker gates).
		if !result.IsError && mutatingSourceEdit(tc.Name) {

			for _, p := range extractTrackedEditPaths(tc.Name, tc.Arguments) {

				if a.tokenWasteBudget != nil {
					a.tokenWasteBudget.markFileEdited(p)
				}

				if tc.Name == "undo_edit" && !result.IsError {

					a.getUserEditObserver().NoteAgentUndo(p)
				}

			}
		}

		if !result.IsError && giveupRollbackTools[tc.Name] {
			a.recordGiveupRollback()
		}

		a.falsePremise.recordToolResult(tc.Name, result.Content, result.IsError)
		a.integrationRecordToolResult(tc.Name, result.Content)

		if warn, ok := batchConflictWarnings[idx]; ok {
			a.appendGuidance(&result, warn)
		}
		if result.IsError {
			debug.Log("agent", "tool result ERROR: tool=%s output=%s", tc.Name, util.Truncate(result.Content, 200))
		}

		a.capBoundary.recordToolResult(result.IsError)

		a.oversightTriage.record(tc)

		a.autonomyDial.record(result.IsError, result.Content)

		a.mu.Lock()
		a.lastExecutedTool = tc.Name
		a.mu.Unlock()

		if !result.IsError {
			if tc.Name == "git_commit" {
				if h := extractNewCommitHash(result.Content); h != "" {
				}
			}
		}

		msgs = a.runGuardZoneWatchers(tc, result, iter+1, msgs)

		if result.IsError {
			a.usageHints.recordFailure(tc.Name, result.Content)
		} else {
			a.usageHints.recordSuccess(tc.Name)
		}

		a.outcomeMisattrib.recordToolCallForOM(tc.Name)

		a.outcomeMisattrib.recordResult(tc.Name, result.Content, result.IsError, iter+1)

		a.reverify.recordToolResult(tc.Name, string(tc.Arguments), result.Content, result.IsError, iter+1)

		if selfModMsg := a.checkSelfModification(tc.Name, tc.Arguments); selfModMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: selfModMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		if tc.Name == "exit_plan_mode" {
			a.planDrift.capturePlan(extractPlanFromArgs(tc.Arguments))
		}

		if result.IsError {
			runStats.recordToolError(tc.Name, result.Content)
		} else if userWriteTools[tc.Name] {

			for _, p := range extractUserEditPaths(tc.Name, tc.Arguments) {
				a.getUserEditObserver().NoteAgentWrite(p)
			}
		}

		a.planDrift.adherence.noteToolCall(a.planDrift.items, runStats)
		if tc.Name == "run_command" || tc.Name == "start_command" {
			cmd := extractCommandFromToolCall(tc.Arguments)
			if cmd != "" {

				if tc.Name == "run_command" && !result.IsError {
					runStats.recordSuccessfulCommand(cmd)
				}
			}
		}

		if a.effortAdapter != nil {
			a.effortAdapter.recordToolResultErr(tc.Name, result.IsError, result.Content)
		}

		a.cascade.noteError(result.IsError)

		if tc.Name == "run_command" && a.testFails != nil {

			a.testFails.record(extractCommandFromToolCall(tc.Arguments), result.Content)
		}

		driftRecPaths := extractFileHints(tc.Name, tc.Arguments)
		if written := extractWrittenPaths(result.Content); len(written) > 0 {
			driftRecPaths = written
		}
		if len(driftRecPaths) == 0 {
			driftRecPaths = []string{extractFileHint(tc.Name, tc.Arguments)}
		}
		for _, fh := range driftRecPaths {
			a.driftRecurrenceRecord(tc.Name, fh, string(tc.Arguments), !result.IsError)
		}

		if written := extractWrittenPaths(result.Content); len(written) > 0 {
			for _, p := range written {
				a.lastGoodCheckpointRecordEdit(tc.Name, p)
			}
		} else if !result.IsError {
			a.lastGoodCheckpointRecordEdit(tc.Name, extractFileHint(tc.Name, tc.Arguments))
		}

		a.causalAttribution.recordEdit(tc.Name, extractFileHint(tc.Name, tc.Arguments), iter)
		a.confidence.recordResult(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments))

		if tc.Name == "run_command" || tc.Name == "bash" || tc.Name == "powershell" || tc.Name == "start_command" || tc.Name == "wait_command" || tc.Name == "read_command_output" {
			if result.IsError || looksLikeFailure(result.Content) {
				// Track the suspect edit for the end-of-run failure attribution
				// (playbook_failures.go); the in-loop "[causal-attribution]"
				// guidance injection was removed as noise.
				a.causalAttribution.attributeFailureCmd(result.Content, causalCmdForGate(tc, result.Content), result.IsError)
			}
		}
		editPaths := extractEditedPaths(tc)
		if result.IsError {
			if written := extractWrittenPaths(result.Content); len(written) > 0 {
				editPaths = written
			} else {
				editPaths = nil
			}
		}
		if isEditTool(tc.Name) && len(editPaths) > 0 {

			a.constraintAmnesia.noteEdit(iter + 1)

		}

		a.maybeResetVerifyOnCommand(tc.Name, tc.Arguments, result.IsError)

		if tc.Name == "start_command" && !result.IsError {
			a.bgVerifyJobs.register(psExtractJobID(result.Content), extractCommandFromArgs(tc.Arguments))
		}
		if tc.Name == "wait_command" || tc.Name == "read_command_output" {

			if cmd, ok := a.bgVerifyJobs.peek(bgVerifyExtractJobID(tc.Arguments)); ok && isVerificationCommand(cmd) {

				if terminal, _ := psTerminalVerifyOutcome(psParseJobStatus(result.Content)); terminal {
					a.bgVerifyJobs.remove(bgVerifyExtractJobID(tc.Arguments))

				}
			}
		}

		var psArgs map[string]interface{}
		if len(tc.Arguments) > 0 {
			_ = json.Unmarshal(tc.Arguments, &psArgs)
		}

		a.phantomVerify.recordToolCall(tc.Name, string(tc.Arguments), result.IsError)

		if mutatingSourceEdit(tc.Name) && !result.IsError { // #5249: apply_patch must count as a write

			a.phantomVerify.invalidateEdits()
		}

		if mutatingSourceEdit(tc.Name) || tc.Name == "run_command" { // #5249: apply_patch counts as a success-claim edit
			a.successDeclare.recordToolCall()
		}

		// State-only: feeds the end-of-turn evidence gate. The post-edit
		// "[Verification reminder]" and "[Test coverage gap]" hints (and the
		// "[Convergence alert]" detector) were removed as noise.
		if !result.IsError {
			a.noteSourceEdit(tc.Name, tc.Arguments)
		}

		if len(result.FollowUpMessages) > 0 {
			st.followUpMessages = append(st.followUpMessages, result.FollowUpMessages...)
		}

		if result.SuggestedWorkingDir != "" && !result.IsError {
			// Companion provenance probes git (bounded, up to 3 subprocesses);
			// the guard is confined to the run-loop goroutine, so run it before
			// taking a.mu to avoid stalling other Agent methods on git I/O.
			if a.companionGuard != nil {
				a.companionGuard.refreshProvenance(result.SuggestedWorkingDir)
			}
			a.mu.Lock()
			oldDir := a.workingDir
			a.workingDir = result.SuggestedWorkingDir

			if a.ruleStore != nil {
				a.resetRuleStoreLocked()
			}
			a.mu.Unlock()
			debug.Log("agent", "working dir changed: %s -> %s (suggested by %s)", oldDir, result.SuggestedWorkingDir, tc.Name)
		}

		result.Content = guardPromptInjection(tc.Name, tc.Arguments, result.Content)

		a.taintInfluence.recordIfTainted(tc.Name, result.Content)

		if staleMsg := a.checkPushReviewStaleness(tc.Name, tc.Arguments, result.Content); staleMsg != "" {
			result.Content += "\n\n" + staleMsg
		}

		if branchMsg := a.checkSharedTreeBranchGuard(tc.Name, tc.Arguments, result.Content); branchMsg != "" {
			result.Content += "\n\n" + branchMsg
		}

		if !result.IsError {
			a.recordSpiralVerification(tc.Name, result.Content)
		}

		if compressed := compressRepetitiveLines(result.Content); len(compressed) < len(result.Content) {
			debug.Log("compress", "repetitive-line compression: tool=%s %d→%d bytes", tc.Name, len(result.Content), len(compressed))
			result.Content = compressed
		}

		if !result.IsError {
			threshold := a.contextManager.AutoCompactThreshold()
			if threshold > 0 {
				fillRatio := float64(a.contextManager.TokenCount()) / float64(threshold)
				guardedOut, guardRep := guardToolOutputReport(result.Content, fillRatio)
				if guardRep.Truncated && len(guardedOut) < len(result.Content) {
					debug.Log("agent", "tool output guarded: tool=%s tokens=%d threshold=%d fill=%.0f%% %d→%d bytes", tc.Name, a.contextManager.TokenCount(), threshold, fillRatio*100, len(result.Content), len(guardedOut))
					guarded := guardedOut

					if spillPath := a.outputOffload.spill(tc.Name, result.Content); spillPath != "" {
						guarded += spillNotice(spillPath, len(result.Content))
						debug.Log("agent", "tool output offloaded: tool=%s path=%s originalLen=%d", tc.Name, spillPath, len(result.Content))
					}
					result.Content = withTruncationAdvisoryGuard(guarded, tc.Name, len(result.Content), guardRep.Limit)
					a.truncClaim.recordTruncation(tc.Name, iter)

				}
			}
		}

		measuredLen := len(result.Content)

		if askAdvisory != "" && tc.Name == "ask_user" && !result.IsError {
			result.Content = result.Content + "\n\n" + askAdvisory
		}

		llmContent := spotlightUntrustedOutput(tc.Name, result.Content)
		if len(result.Images) > 0 && a.SupportsVision() {
			imgs := make([]provider.ContentImage, len(result.Images))
			for iter, ri := range result.Images {
				imgs[iter] = provider.ContentImage{MIME: ri.MIME, Base64: ri.Base64}
			}
			toolResults = append(toolResults, provider.ToolResultWithImages(tc.ID, tc.Name, llmContent, imgs, result.IsError))
		} else {
			toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, llmContent, result.IsError))
		}
		onEvent(provider.StreamEvent{
			Type:    provider.StreamEventToolResult,
			Tool:    tc,
			Result:  result.Content,
			IsError: result.IsError,
		})

		if speculativeSafeTools[tc.Name] && !result.IsError {
			seenReadOnly[dedupK] = len(toolResults) - 1
		} else if dedupInvalidation.MutatedSource(tc, result) {
			// #5153/#3851: source mutation (success or wrote-despite-error,
			// non-speculative) drops cached read-only results - same
			// condition as the toolMemo invalidation (#2991).
			if len(seenReadOnly) > 0 {
				debug.Log("agent", "in-turn dedup invalidated: %s mutated state, %d cached read-only results dropped", tc.Name, len(seenReadOnly))
				seenReadOnly = make(map[dedupKey]int)
			}
		}
		var pathsRead []string
		if tc.Name == "read_file" || tc.Name == "multi_file_read" {
			pathsRead = extractReadFilePaths(tc.Name, tc.Arguments)
		}
		isRedundant := false
		if a.tokenWasteBudget != nil {

			a.tokenWasteBudget.recordToolResultLen(tc.Name, result.Content, measuredLen, result.IsError, isRedundant, pathsRead)
		}
		if err := ctx.Err(); err != nil {

			a.fillCancelledToolResults(toolCalls[idx+1:], &toolResults)

			if len(toolCalls[idx+1:]) == 0 && len(toolResults) > 0 {
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: toolResults,
				})
			}
			return chatTurnFatal, err, msgs
		}

		// #5520: a successful tool result carrying HaltAgent (restart) ends
		// the run right here — any further turn in this process is wasted
		// work, and the tool has already armed whatever durable handover
		// it needs (resume nudge). Remaining calls in this batch are
		// cancelled-fill so the provider transcript stays balanced, the
		// collected results land in context for the resumed session, and
		// the run unwinds cleanly.
		if result.HaltAgent && !result.IsError {
			debug.Log("agent", "halt-agent: %s requested immediate loop stop; cancelling %d remaining tool calls in batch", tc.Name, len(toolCalls)-idx-1)
			a.fillCancelledToolResults(toolCalls[idx+1:], &toolResults)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: toolResults,
			})
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventSystem,
				Text: fmt.Sprintf("Agent loop stopped: %s requested immediate termination (session handover in progress, e.g. pending restart).", tc.Name),
			})
			return chatTurnFatal, nil, msgs
		}
	}
	if err := ctx.Err(); err != nil {

		if len(toolResults) > 0 {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: toolResults,
			})
		}
		return chatTurnFatal, err, msgs
	}
	if len(toolResults) == 0 {
		return chatTurnContinue, nil, msgs
	}
	debug.Log("agent", "Adding tool results to contextManager: blocks=%d", len(toolResults))
	a.contextManager.Add(provider.Message{
		Role:    "user",
		Content: toolResults,
	})
	return chatTurnContinue, nil, msgs
}
