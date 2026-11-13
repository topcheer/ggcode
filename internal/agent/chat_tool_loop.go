package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	batchConflictWarnings := detectBatchEditConflicts(toolCalls)
	seenReadOnly := make(map[dedupKey]int)

	if depWarn := a.recordToolCallBatch(toolCalls, iter+1); depWarn != "" {
		debug.Log("agent", "Iteration %d: counterfactual dependency assumption detected", iter+1)
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
			debug.Log("agent", "Iteration %d: batch tool call coupling detected", iter+1)
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
				debug.Log("agent", "Iteration %d: merge gate requirement injected", iter+1)
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
		debug.Log("agent", "Iteration %d: mindless action detector triggered (streak=%d)", iter+1, a.mindlessAction.streak)
		a.injectGuidance(mindlessActionWarning(a.mindlessAction.streak))
	}
	for idx, tc := range toolCalls {
		if err := ctx.Err(); err != nil {

			a.fillCancelledToolResults(toolCalls[idx:], &toolResults)
			return chatTurnFatal, err, msgs
		}
		var undoBlindHint string
		if ub := a.undoBlind.recordToolCall(tc.Name, tc.Arguments); ub != "" {
			debug.Log("agent", "Iteration %d: undo-blind mutation detected (pre-execution)", iter+1)
			undoBlindHint = ub
		}

		a.orphanFile.recordPreExec(tc.Name, string(tc.Arguments), a.workingDir)
		var loopGuidance string
		if guidance := a.loopDetectionInjection(tc); guidance != "" {
			loopGuidance = guidance
		}
		var searchParamHint string
		if hint := a.searchParamGuard.checkParamQuality(tc.Name, tc.Arguments); hint != "" {
			searchParamHint = hint
		}

		if revGuidance := a.reversibility.checkPreAction(tc.Name, string(tc.Arguments)); revGuidance != "" {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: revGuidance,
				}},
			})
			msgs = a.contextManager.Messages()
		}

		a.reversibility.recordSafetySignal(tc.Name, string(tc.Arguments))
		var redundancyHint string
		if hint := a.toolRedundancy.recordCall(tc.Name, tc.Arguments); hint != "" {
			redundancyHint = hint
		}
		var equivHint string
		rawFp := fingerprintToolCall(tc.Name, tc.Arguments)
		if hint := a.toolEquivDetect.recordCall(tc.Name, tc.Arguments, rawFp); hint != "" {
			equivHint = hint
		}

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

		a.runToolNames = append(a.runToolNames, tc.Name)

		runStats.recordToolCall(tc.Name)
		a.toolCallBudget.record()
		a.toolThermal.recordToolCall(tc.Name)
		a.iterPressure.recordToolCall(tc.Name, iter+1)
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
			result = a.executeToolWithPermission(ctx, tc)
			if isWaitClassTool(tc.Name) {
				runStats.addWaitTime(time.Since(execStart))
			}

			a.storeCommandResult(tc.Name, tc.Arguments, result)

			if adv := a.checkoutAdvisory(tc.Name, tc.Arguments); adv != "" {
				result.Content += adv
			}
			if hint := a.recordEffectAttempt(tc.Name, tc.Arguments, result); hint != "" {
				result.Content += hint
			}
		}

		a.exfilChain.recordSensitiveSource(tc.Name, string(tc.Arguments), result.Content)
		result.Content = redactSecrets(tc.Name, result.Content)

		a.overseer.recordFinding(tc.Name, string(tc.Arguments), result.Content)

		a.speculator.recordObservation(tc.Name)

		if tc.Name == "todo_write" && !result.IsError {
			a.plannerMarkTodoCreated()

			todoCount := parseTodoCount(tc.Arguments)
			a.recordTodoStalenessUpdate(iter+1, todoCount)

			if hint := a.checkTodoDrop(tc.Arguments); hint != "" {
				a.appendGuidance(&result, hint)
			}
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
				a.unreadEdit.recordCreated(p)
				a.tunnelVision.recordFile(p)
				a.fileFreshness.recordWrite(p)
				a.readHash.recordWriteHash(p)
				if a.turnImpact != nil {
					a.turnImpact.recordFileEdit(tc.Name, resolveImpactPath(a.WorkingDir(), p))
				}
			}

			a.recurringErrorRecordEdit()

			if a.codeIndex != nil {
				a.codeIndex.MarkDirty(extractEditedPaths(tc))
			}
		}

		if tc.Name == "run_command" || tc.Name == "start_command" {
			if cmd, _ := parseRunCommandArgs(tc.Arguments); shellMutatesSources(cmd) {
				a.speculator.invalidateCache()
				a.toolMemo.invalidateTTLBased()
				a.commandCache.invalidate()

				a.redundantReverify.recordShellSourceMutation()

				a.getUserEditObserver().RestampBaseline()
				if a.turnImpact != nil {
					a.turnImpact.recordShellMutation(cmd, shellDestructive(cmd))
				}
				debug.Log("agent", "shell source mutation %q (failed cmd included): invalidated command/speculator/memo caches", cmd)
			}
		}

		if speculativeSafeTools[tc.Name] && !result.IsError && !memoHit {
			a.toolMemo.put(tc.Name, tc.Arguments, result)
		}

		if (tc.Name == "read_file" || tc.Name == "multi_file_read") && !result.IsError {

			readPathsLen := len(extractReadFilePaths(tc.Name, tc.Arguments))

			hasWindow := readArgsHaveWindow(tc.Arguments)
			for pi, p := range extractReadFilePaths(tc.Name, tc.Arguments) {
				a.unreadEdit.recordReadWindow(p, hasWindow)
				a.tunnelVision.recordFile(p)
				a.editFailRecovery.recordRead(p)
				a.fileFreshness.recordRead(p)
				a.readHash.recordReadHash(p)
				if hint := a.redundantRead.checkRedundantRead(p, hasWindow); hint != "" {
					a.appendGuidance(&result, hint)
				}
				if pi == readPathsLen-1 {
					if hint := a.patchExhaust.recordRead(p); hint != "" {
						a.appendGuidance(&result, hint)
					}
				}
			}
		}

		if searchResultTools[tc.Name] && !result.IsError {
			for _, p := range extractSearchResultPaths(tc.Name, result.Content) {
				a.tunnelVision.recordSearched(p)
			}
		}

		// #5244: apply_patch (OpenAI Responses-native) edits source too,
		// but #3006/#737 pin sourceMutatingTools at nine - extensions
		// ride mutatesSourceTree() semantics instead of the canonical
		// map (#5249: helper shared with the other tracker gates).
		if !result.IsError && mutatingSourceEdit(tc.Name) {

			a.prematureRefactorRecordEdit(tc.Name, tc.Arguments)
			for _, p := range extractTrackedEditPaths(tc.Name, tc.Arguments) {
				a.editFailRecovery.recordEditSuccess(p)

				if hint := a.readHash.validateContentAtEdit(p, extractOldTextLen(tc.Name, tc.Arguments)); hint != "" {
					a.appendGuidance(&result, hint)
				}
				a.fileFreshness.recordWrite(p)
				a.readHash.recordWriteHash(p)
				a.redundantRead.recordWrite(p)

				a.unreadEdit.recordWrite(p)
				if a.tokenWasteBudget != nil {
					a.tokenWasteBudget.markFileEdited(p)
				}
				a.patchExhaust.recordEdit(p)

				a.convergenceRecordEdit(tc.Name)

				a.diminishingRecordEdit(tc.Name, tc.Arguments)

				if a.claimsSupervision {
					if ocHint := a.overcorrectionRecordEdit(tc.Name, tc.Arguments); ocHint != "" {
						a.appendGuidance(&result, ocHint)
					}
				}

				a.fixCascade.recordEdit()

				a.errRegression.recordEdit()
				if hint := a.unreadEdit.checkUnreadEdit(p); hint != "" {
					a.appendGuidance(&result, hint)
				}

				if hint := a.unreadEdit.checkStaleRead(p); hint != "" {
					a.appendGuidance(&result, hint)
				}

				if hint := a.expiredRead.recordEdit(p); hint != "" {
					a.appendGuidance(&result, hint)
				}

				if tc.Name == "undo_edit" && !result.IsError {
					a.expiredRead.recordUndo(p)

					a.getUserEditObserver().NoteAgentUndo(p)
				}

				if hint := a.checkExportGuard(p); hint != "" {
					a.appendGuidance(&result, hint)
				}

				if hint := a.checkHubPackage(p); hint != "" {
					a.appendGuidance(&result, hint)
				}

				if hint := a.artifactGuard.checkGeneratedArtifact(p); hint != "" {
					a.appendGuidance(&result, hint)
				}

				if hint := a.checkBranchGuard(); hint != "" {
					a.appendGuidance(&result, hint)
				}
			}
		}

		if result.IsError && mutatingSourceEdit(tc.Name) { // #5249: error path too - apply_patch failures must reach editFailRecovery
			a.convergenceRecordEditError()
			for _, p := range extractTrackedEditPaths(tc.Name, tc.Arguments) {
				if hint := a.editFailRecovery.recordEditFailure(p); hint != "" {
					a.appendGuidance(&result, hint)
				}
			}
		}

		a.overcorrectionRecordError(tc.Name, result.Content, result.IsError)

		if !result.IsError && giveupRollbackTools[tc.Name] {
			a.recordGiveupRollback()
		}

		a.falsePremise.recordToolResult(tc.Name, result.Content, result.IsError)
		a.integrationRecordToolResult(tc.Name, result.Content)
		result.Content = a.injectRulesIntoResult(tc.Name, tc.Arguments, result.Content)

		if warn, ok := batchConflictWarnings[idx]; ok {
			a.appendGuidance(&result, warn)
		}
		if result.IsError {
			debug.Log("agent", "tool result ERROR: tool=%s output=%s", tc.Name, util.Truncate(result.Content, 200))
		}

		if !isEditTool(tc.Name) {
			a.overcorrection.recordNonEditStep()
		}

		a.capBoundary.recordToolResult(result.IsError)

		if argSizeHint := a.checkArgSizeGuard(tc.Name, tc.Arguments); argSizeHint != "" {
			a.appendGuidance(&result, argSizeHint)
		}

		if seqHint := a.toolSequence.record(tc, iter+1); seqHint != "" {
			a.appendGuidance(&result, seqHint)
		}

		if ocHint := a.obsContract.checkArgs(tc.Name, tc.Arguments, iter+1); ocHint != "" {
			a.appendGuidance(&result, ocHint)
		}
		a.obsContract.recordResult(tc.Name, result.Content, iter+1)

		a.oversightTriage.record(tc)

		a.autonomyDial.record(result.IsError, result.Content)

		a.mu.Lock()
		a.lastExecutedTool = tc.Name
		a.mu.Unlock()
		if ivHint := a.interventionLedger.hint(tc.Name); ivHint != "" {
			a.appendGuidance(&result, ivHint)
		}

		a.recordBgToolCall(tc.Name, tc.Arguments, result.Content, iter+1)

		if !result.IsError {
			if annihilWarn := a.actionAnnihil.recordToolCall(tc.Name, tc.Arguments, iter+1); annihilWarn != "" {
				debug.Log("agent", "Iteration %d: action annihilation detected", iter+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: annihilWarn}},
				})
				msgs = a.contextManager.Messages()
			}

			if tc.Name == "git_commit" {
				if h := extractNewCommitHash(result.Content); h != "" {
					a.actionAnnihil.recordCommitHash(iter+1, h)
				}
			}
		}

		fragWarn := ""
		if !result.IsError || mutatingToolNamesFrag[tc.Name] {
			fragWarn = a.exploreFrag.recordToolCall(tc.Name, tc.Arguments, iter+1)
		}
		if fragWarn != "" {
			debug.Log("agent", "Iteration %d: exploration fragmentation detected", iter+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fragWarn}},
			})
			msgs = a.contextManager.Messages()
		}

		if !result.IsError {
			if orphanWarn := a.orphanFile.recordToolCall(tc.Name, string(tc.Arguments), iter+1); orphanWarn != "" {
				debug.Log("agent", "Iteration %d: orphaned file detected", iter+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: orphanWarn}},
				})
				msgs = a.contextManager.Messages()
			}
		}

		msgs = a.runGuardZoneWatchers(tc, result, iter+1, msgs)

		if result.IsError {
			a.usageHints.recordFailure(tc.Name, result.Content)
		} else {
			a.usageHints.recordSuccess(tc.Name)
		}

		if rvHint := a.redundantReverify.recordToolCall(tc.Name, string(tc.Arguments), iter+1, result.IsError); rvHint != "" {
			debug.Log("agent", "Iteration %d: redundant re-verification detector triggered", iter+1)
			a.injectGuidance(rvHint)
		}

		a.outcomeMisattrib.recordToolCallForOM(tc.Name)

		a.outcomeMisattrib.recordResult(tc.Name, result.Content, result.IsError, iter+1)

		a.reverify.recordToolResult(tc.Name, string(tc.Arguments), result.Content, result.IsError, iter+1)

		a.goalDriftCtx.recordToolCall(tc.Name, string(tc.Arguments))

		if fcHint := a.foresightCalib.checkCalibration(tc.Name, result.Content, result.IsError, iter+1); fcHint != "" {
			debug.Log("agent", "Iteration %d: foresight calibration detector triggered", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fcHint,
				}},
			})
		}

		if cvMsg := a.constraintViolation.checkToolCall(tc.Name, parseToolArgs(tc.Arguments), iter+1); cvMsg != "" {
			debug.Log("agent", "Iteration %d: constraint violation detector triggered", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: cvMsg,
				}},
			})
		}

		a.errStrategyLoop.recordResult(result.Content, result.IsError)

		if seMsg := a.strategyExhaustion.recordToolCall(tc.Name, result.IsError, result.Content, iter+1); seMsg != "" {
			debug.Log("agent", "Iteration %d: strategy exhaustion detector triggered", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: seMsg,
				}},
			})
		}

		a.solutionFixation.recordToolCall(tc.Name, string(tc.Arguments), result.IsError)

		a.pivotDecision.recordToolCall(tc.Name, string(tc.Arguments), result.IsError, result.Content)

		if !result.IsError {
			a.redundantReverify.recordEdit(tc.Name)
		}
		if fixationHint := a.solutionFixation.checkAndWarn(); fixationHint != "" {
			debug.Log("agent", "Iteration %d: solution fixation detector triggered", iter+1)
			a.injectGuidance(fixationHint)
		}
		if pivotHint := a.pivotDecision.checkAndWarn(); pivotHint != "" {
			debug.Log("agent", "Iteration %d: pivot-decision detector triggered", iter+1)
			a.injectGuidance(pivotHint)
		}

		if strategyHint := a.errStrategyLoop.checkAndWarn(); strategyHint != "" {
			debug.Log("agent", "Iteration %d: error strategy loop detector triggered", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: strategyHint,
				}},
			})

			if recallHint := a.maybeRecallExperienceOnFailure(userPromptForStats, result.Content); recallHint != "" {
				debug.Log("agent", "injected decision-time experience recall (%d chars)", len(recallHint))
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: recallHint}},
				})
			}
		}

		if selfModMsg := a.checkSelfModification(tc.Name, tc.Arguments); selfModMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: selfModMsg}},
			})
			msgs = a.contextManager.Messages()
		}

		a.infoScent.recordExploration(tc.Name, string(tc.Arguments), result.Content, iter+1)

		a.queryConverge.recordToolCall(tc.Name, string(tc.Arguments), iter+1)

		if tc.Name == "exit_plan_mode" {
			a.planDrift.capturePlan(extractPlanFromArgs(tc.Arguments))
		}

		if a.delegationOrch != nil {
			if delegationToolNames[tc.Name] {
				taskSum := extractDelegationTaskSummary(tc.Name, tc.Arguments)
				a.delegationOrch.recordDelegationCall(tc.ID, tc.Name, taskSum, result.Content, iter+1)
			} else if delegationResultTools[tc.Name] && !result.IsError {

				a.delegationOrch.recordResultCheck(tc.Name, tc.Arguments, result.Content, iter+1)
			}
			a.delegationOrch.recordToolCallCount()
		}

		if result.IsError {
			runStats.recordToolError(tc.Name, result.Content)
		} else if userWriteTools[tc.Name] {

			for _, p := range extractUserEditPaths(tc.Name, tc.Arguments) {
				a.getUserEditObserver().NoteAgentWrite(p)
			}
		}

		if result.IsError {
			rKey := extractErrorResourceKey(tc.Name, tc.Arguments)
			a.silentError.recordToolError(tc.Name, rKey, result.Content, iter+1)
		} else {
			rKey := extractErrorResourceKey(tc.Name, tc.Arguments)
			if silentMsg := a.silentError.recordToolAction(tc.Name, rKey); silentMsg != "" {
				a.appendGuidance(&result, silentMsg)
			}
		}

		a.planDrift.adherence.noteToolCall(a.planDrift.items, runStats)
		if tc.Name == "run_command" || tc.Name == "start_command" {
			cmd := extractCommandFromToolCall(tc.Arguments)
			if cmd != "" {

				if tc.Name == "run_command" {
					if narrowMsg := a.scopeNarrow.recordVerificationCommand(tc.Name, cmd, result.Content, result.IsError); narrowMsg != "" {
						a.contextManager.Add(provider.Message{
							Role:    "user",
							Content: []provider.ContentBlock{{Type: "text", Text: narrowMsg}},
						})
						msgs = a.contextManager.Messages()
					}
				}

				if tc.Name == "run_command" && !result.IsError {
					runStats.recordSuccessfulCommand(cmd)
				}
			}
		}

		if a.effortAdapter != nil {
			a.effortAdapter.recordToolResultErr(tc.Name, result.IsError, result.Content)
		}

		a.cascade.noteError(result.IsError)

		if a.strategyStagnation.recordAttemptDetail(tc.Name, string(tc.Arguments), !result.IsError, result.Content) {
			debug.Log("agent", "Iteration %d: strategy stagnation detected (tool=%s)", iter+1, tc.Name)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: strategyStagnationWarning(tc.Name, extractStagnationTarget(tc.Name, string(tc.Arguments)), stagnationFailureThreshold),
				}},
			})
			msgs = a.contextManager.Messages()
		}

		if result.IsError {
			if catGuidance := a.errorClassifier.classifyToolError(tc.Name, result.Content); catGuidance.Name != "" {
				g := fmt.Sprintf("[Error guidance: %s] %s", catGuidance.Name, catGuidance.Guidance)
				a.appendGuidance(&result, g)
			}
		}

		if tc.Name == "run_command" && a.testFails != nil {

			a.testFails.record(extractCommandFromToolCall(tc.Arguments), result.Content)
		}

		if result.IsError {
			if fallbackHint := a.toolFallbackCheck(tc.Name, result.Content); fallbackHint != "" {
				a.appendGuidance(&result, fallbackHint)
			}
		}

		if nativeHint := a.shellNativeHint.maybeShellNativeHint(tc.Name, tc.Arguments); nativeHint != "" {
			a.appendGuidance(&result, nativeHint)
		}

		if errorGuidance := a.errorStreakCheck(result.IsError, tc.Name); errorGuidance != "" {
			a.appendGuidance(&result, errorGuidance)
		}

		a.compoundingFailure.recordResult(tc.Name, result.IsError)
		if compoundingGuidance := a.compoundingFailure.check(); compoundingGuidance != "" {
			a.appendGuidance(&result, compoundingGuidance)
		}

		if claimGuidance := a.claimVerify.check(tc.Name, result.Content, result.IsError, extractCommandFromToolCall(tc.Arguments)); claimGuidance != "" {
			a.appendGuidance(&result, claimGuidance)
		}

		if modeGuard := a.permDenyStreak.record(a.currentMode(), result); modeGuard != "" {
			a.appendGuidance(&result, modeGuard)
		}

		if modeGuidance := a.failureMode.recordResult(tc.Name, result.IsError, result.Content); modeGuidance != "" {

			a.crossDetectorConsensus.recordFiring("Failure Mode", iter+1)
			a.appendGuidance(&result, modeGuidance)
		}

		if result.IsError {
			if cascadeGuidance := a.errorCascade.recordError(tc.Name, result.Content); cascadeGuidance != "" {

				a.crossDetectorConsensus.recordFiring("Error Cascade", iter+1)
				a.appendGuidance(&result, cascadeGuidance)
			}
		}

		if propGuidance := a.errorPropagate.recordResult(tc.Name, result.Content, result.IsError); propGuidance != "" {

			a.crossDetectorConsensus.recordFiring("Error Propagation", iter+1)
			a.appendGuidance(&result, propGuidance)
		}

		if !result.IsError {
			driftPaths := extractFileHints(tc.Name, tc.Arguments)
			if written := extractWrittenPaths(result.Content); len(written) > 0 {
				driftPaths = written
			}
			for _, fh := range driftPaths {
				a.scopeDriftRecord(tc.Name, fh)
			}
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

		if !result.IsError {
			monoPaths := extractFileHints(tc.Name, tc.Arguments)
			if written := extractWrittenPaths(result.Content); len(written) > 0 {
				monoPaths = written
			}
			for _, fh := range monoPaths {
				a.monorepoScoper.recordEdit(fh)
			}
		}
		if scopeGuidance := a.scopeDriftCheck(); scopeGuidance != "" {

			a.driftRecurrenceMarkWarn(runStats.Iterations)
			a.appendGuidance(&result, scopeGuidance)
		}

		if recurrenceGuidance := a.driftRecurrenceCheck(); recurrenceGuidance != "" {
			a.appendGuidance(&result, recurrenceGuidance)
		}

		if overseerGuidance := a.overseerCheck(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments), result.Content, runStats.Iterations); overseerGuidance != "" {
			a.appendGuidance(&result, overseerGuidance)
		}

		if llmOverseerGuidance := a.llmOverseerCheck(runStats.Iterations); llmOverseerGuidance != "" {
			a.appendGuidance(&result, llmOverseerGuidance)
		}

		if repetitionGuidance := a.repetitionCheckEdit(tc.Name, tc.Arguments, result.IsError); repetitionGuidance != "" {
			a.appendGuidance(&result, repetitionGuidance)
		}

		if tc.Name == "read_file" || tc.Name == "multi_file_read" {
			if readGuidance := a.repetitionCheckRead(extractFileHint(tc.Name, tc.Arguments)); readGuidance != "" {
				a.appendGuidance(&result, readGuidance)
			}
		}

		a.causalAttribution.recordEdit(tc.Name, extractFileHint(tc.Name, tc.Arguments), iter)
		a.confidence.recordResult(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments))

		if tc.Name == "run_command" || tc.Name == "bash" || tc.Name == "powershell" || tc.Name == "start_command" || tc.Name == "wait_command" || tc.Name == "read_command_output" {

			if expGuidance := a.attrExperiment.observeCommand(tc.Name, causalCmdForGate(tc, result.Content), result.IsError, result.Content); expGuidance != "" {
				a.appendGuidance(&result, expGuidance)
			}
			if result.IsError || looksLikeFailure(result.Content) {

				if causalHint := a.causalAttribution.attributeFailureCmd(result.Content, causalCmdForGate(tc, result.Content), result.IsError); causalHint != "" {
					a.appendGuidance(&result, causalHint)

					if armGuidance := a.attrExperiment.arm(a.causalAttribution.lastSuspect, causalCmdForGate(tc, result.Content)); armGuidance != "" {
						a.appendGuidance(&result, armGuidance)
					}
				}
			}
		}
		if confidenceGuidance := a.confidence.maybeIntervene(); confidenceGuidance != "" {
			a.appendGuidance(&result, confidenceGuidance)
		}

		a.verifDebt.recordToolCall(tc.Name, string(tc.Arguments))

		a.prematureCommit.recordExploration(tc.Name, extractFileHints(tc.Name, tc.Arguments))
		if debtGuidance := a.verifDebt.maybeWarn(); debtGuidance != "" {
			a.appendGuidance(&result, debtGuidance)
		}

		a.editAbandon.recordToolCall(tc.Name, string(tc.Arguments))
		if abandonGuidance := a.editAbandon.maybeWarn(); abandonGuidance != "" {
			a.appendGuidance(&result, abandonGuidance)
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
			a.fileChurn.recordEdit(editPaths)

			a.constraintAmnesia.noteEdit(iter + 1)
			for _, p := range editPaths {
				a.tunnelVision.recordFile(p)
			}

			pcMsg := a.prematureCommit.checkFirstEdit(editPaths)
			if pcMsg != "" {
				a.appendGuidance(&result, pcMsg)
			}
			if cg := func() string {
				if !shouldRunDetector(detectorTierRoutine, iter+1) {
					return ""
				}
				return a.fileChurn.check()
			}(); cg != "" {
				a.appendGuidance(&result, cg)
			}

			a.editOscillation.recordEdit(tc.Name, tc.Arguments, iter+1)
			if om := func() string {
				if !shouldRunDetector(detectorTierRoutine, iter+1) {
					return ""
				}
				return a.editOscillation.check()
			}(); om != "" {
				a.appendGuidance(&result, om)
			}
		}

		if tv := func() string {
			if !shouldRunDetector(detectorTierRoutine, iter+1) {
				return ""
			}
			return a.tunnelVision.check(runStats.Iterations)
		}(); tv != "" {
			a.appendGuidance(&result, tv)
		}

		a.maybeResetVerifyOnCommand(tc.Name, tc.Arguments, result.IsError)

		if verifyDebtSpawnClearingTool(tc.Name) && !result.IsError && isVerificationCommand(extractCommandFromArgs(tc.Arguments)) {
			a.verifyDebt.recordVerifyCommand(extractCommandFromArgs(tc.Arguments), result.IsError)
		}

		if tc.Name == "start_command" && !result.IsError {
			a.bgVerifyJobs.register(psExtractJobID(result.Content), extractCommandFromArgs(tc.Arguments))
		}
		if tc.Name == "wait_command" || tc.Name == "read_command_output" {

			if cmd, ok := a.bgVerifyJobs.peek(bgVerifyExtractJobID(tc.Arguments)); ok && isVerificationCommand(cmd) {

				if terminal, passed := psTerminalVerifyOutcome(psParseJobStatus(result.Content)); terminal {
					a.bgVerifyJobs.remove(bgVerifyExtractJobID(tc.Arguments))

					a.strategyFixation.recordVerification(tc.Name, result.Content, !passed)
					if passed {
						a.verifyDebt.recordVerifyCommand(cmd, false)
						a.editPropagation.recordGreenBuild()

						if isStrictVerifyCommand(cmd) {
							a.fileChurn.recordVerifySuccess(cmd)
						}
					}
				}
			}
		}

		a.prematureRefactorRecordVerifyForTool(tc.Arguments)

		if verifyDebtSpawnClearingTool(tc.Name) && !result.IsError {
			if cmd := extractCommandFromArgs(tc.Arguments); cmd != "" && isVerifyCommand(cmd) {
				a.editPropagation.recordGreenBuild()

				if isStrictVerifyCommand(cmd) {
					a.fileChurn.recordVerifySuccess(cmd)
				}
			}
		}

		a.convergenceRecordVerify(tc.Name, tc.Arguments, result.IsError)

		if recurringGuidance := a.recurringErrorCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); recurringGuidance != "" {
			a.appendGuidance(&result, recurringGuidance)
		}

		if stalledGuidance := a.stalledConvergenceCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); stalledGuidance != "" {
			a.appendGuidance(&result, stalledGuidance)
		}
		if regressionGuidance := a.errorRegressionCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); regressionGuidance != "" {
			a.appendGuidance(&result, regressionGuidance)
		}

		a.errorCompound.recordStep(a.errorCompound.recordResult(tc.Name, result.IsError, iter+1))

		if result.IsError {
			if cat, file := classifyToolError(tc.Name, result.Content); cat != "" {
				a.fixAmnesia.recordErrorObserved(cat, file)
			}
		}
		if csIsEditTool(tc.Name) || tc.Name == "write_file" || tc.Name == "multi_edit_file" {
			fp := extractFilePathFromArgs(tc.Name, tc.Arguments)
			if result.IsError {

				if cat, file := classifyToolError(tc.Name, result.Content); cat != "" {
					if file == "" {
						file = fp
					}
					a.fixAmnesia.recordErrorObserved(cat, file)
				}
			} else {

				a.fixAmnesia.recordFileEdited(fp)
			}

			if !result.IsError && fp != "" && a.fixAmnesia.hasFixedPatternsInOtherFiles(fp) {
				if b, rerr := os.ReadFile(fp); rerr == nil {
					if faGuidance := a.fixAmnesia.checkContentAgainstFixed(extractFilePathFromError(result.Content), fp, string(b)); faGuidance != "" {
						a.appendGuidance(&result, faGuidance)
					}
				}
			}
		}
		var psArgs map[string]interface{}
		if len(tc.Arguments) > 0 {
			_ = json.Unmarshal(tc.Arguments, &psArgs)
		}
		if csIsEditTool(tc.Name) {
			a.correctionSpiral.recordEdit(iter + 1)
		} else if (tc.Name == "run_command" || tc.Name == "bash" || tc.Name == "powershell") && psIsVerifyCommand(sfCommandArg(psArgs)) {

			a.correctionSpiral.recordVerifyResult(tc.Name, result.Content, result.IsError, iter+1)
		} else if tc.Name == "wait_command" || tc.Name == "read_command_output" || tc.Name == "task_output" {

			jobID, _ := psArgs["job_id"].(string)
			if jobID == "" {
				jobID, _ = psArgs["task_id"].(string)
			}
			if jobID != "" && a.prematureSuccess.psJobIsVerify(jobID) {
				if terminal, _ := psTerminalVerifyOutcome(psParseJobStatus(result.Content)); terminal {
					a.correctionSpiral.recordVerifyResult(tc.Name, result.Content, result.IsError, iter+1)
				}
			}

			if jobID != "" {
				if cmd, ok := a.prematureSuccess.psJobCommand(jobID); ok && cmd != "" {
					if terminal, passed := psTerminalVerifyOutcome(psParseJobStatus(result.Content)); terminal && !passed {
						a.buildIdempot.markBuildFailed(cmd)
					}
				}
			}
		}

		a.bareEditStreak.recordToolCall(tc.Name, string(tc.Arguments))

		a.prematureSuccess.recordToolCall(tc.Name, psArgs, result.IsError, result.Content)

		a.phantomVerify.recordToolCall(tc.Name, string(tc.Arguments), result.IsError)

		sfIsVerify := strategyFixationIsVerification(tc.Name)
		if sfIsVerify && (tc.Name == "run_command" || tc.Name == "bash" || tc.Name == "powershell") {
			sfIsVerify = psIsVerifyCommand(sfCommandArg(psArgs))
		}

		if strategyFixationIsMutation(tc.Name) {

			for _, fp := range sfExtractMutationPaths(tc.Arguments) {
				a.strategyFixation.recordEdit(fp)
			}
		} else if sfIsVerify {
			a.strategyFixation.recordVerification(tc.Name, result.Content, result.IsError)
		}

		a.errorRush.recordToolCall(tc.Name, result.Content, result.IsError)
		var afArgs map[string]interface{}
		if len(tc.Arguments) > 0 {
			_ = json.Unmarshal(tc.Arguments, &afArgs)
		}
		a.attentionFragment.recordToolCall(tc.Name, afArgs)

		if a.recklessExec != nil {
			argsStr := string(tc.Arguments)
			if recklessIsExplorationTool(tc.Name) {
				a.recklessExec.recordReadTool(tc.Name, argsStr)
			} else if recklessIsEditTool(tc.Name) {
				a.recklessExec.iteration = iter + 1
				if a.recklessExec.recordEditTool(tc.Name, argsStr) {
					msg := recklessWarning(a.recklessExec.unexplored)
					a.contextManager.Add(provider.Message{
						Role:    "user",
						Content: []provider.ContentBlock{{Type: "text", Text: msg}},
					})
				}
			}
		}

		if a.irrevGate != nil {
			a.irrevGate.recordOutcome(tc.Name, result.IsError)
		}
		if a.mergeGate != nil {
			a.mergeGate.RecordPreflightResult(tc.Name, result.Content)
		}

		if mutatingSourceEdit(tc.Name) && !result.IsError { // #5249: apply_patch must count as a write
			a.futileCycle.recordWrite()

			a.phantomVerify.invalidateEdits()
		} else if !result.IsError {

			if readPaths := extractFilePathsFromArgs(tc.Arguments, tc.Name); len(readPaths) > 0 {

				for _, p := range readPaths {
					a.futileCycle.recordRead(p)

					a.expiredRead.recordRead(p)
					a.wtInvalidation.recordRead(p)
				}

				if hint := a.expiredRead.checkPostEditReread(readPaths[0]); hint != "" {
					a.appendGuidance(&result, hint)
				}
			}

			a.searchInvalidation.recordSearchResult(tc.Name, result.Content)
		}

		if (isWTMutatingTool(tc.Name) ||
			((tc.Name == "run_command" || tc.Name == "start_command" || tc.Name == "bash" || tc.Name == "powershell") && runCommandMutatesTree(string(tc.Arguments)))) && !result.IsError {
			if wtMsg := a.wtInvalidation.checkMutation(tc.Name, string(tc.Arguments)); wtMsg != "" {
				debug.Log("agent", "Iteration %d: working-tree invalidation detector triggered", iter+1)
				a.appendGuidance(&result, wtMsg)
			}
		}
		if mutatingSourceEdit(tc.Name) && !result.IsError { // #5249: apply_patch must feed edit trackers
			a.verifyDebt.recordSourceEdit()
			a.stalledConvergence.recordEdit()

			if editPath := extractFilePathFromArgs(tc.Name, tc.Arguments); editPath != "" {
				if siMsg := a.searchInvalidation.checkEditInvalidation(editPath); siMsg != "" {
					a.appendGuidance(&result, siMsg)
				}
			}
			a.editPropagation.recordEdit(tc.Name, string(tc.Arguments))
		}

		if mutatingSourceEdit(tc.Name) || tc.Name == "run_command" { // #5249: apply_patch counts as a success-claim edit
			a.successDeclare.recordToolCall()
		}
		a.subgoalTrack.recordToolCall(tc.Name, string(tc.Arguments))

		a.attemptBrief.recordOutcome(tc.Name, extractToolTarget(tc.Name, string(tc.Arguments)), !result.IsError, iter, result.Content)

		if trMsg := a.toolResultRedundancy.recordToolCall(tc.Name, tc.Arguments, result.Content, iter+1); trMsg != "" {
			a.appendGuidance(&result, trMsg)
		}
		if cascadeGuidance := a.fixCascadeCheckCommand(tc.Name, tc.Arguments, result.IsError, result.Content); cascadeGuidance != "" {

			a.crossDetectorConsensus.recordFiring("Fix Cascade", iter+1)
			a.appendGuidance(&result, cascadeGuidance)
		}

		if !result.IsError {
			if verifyHint := a.postEditVerifyHint(tc.Name, tc.Arguments); verifyHint != "" {
				a.appendGuidance(&result, verifyHint)
			}
		}

		if convergenceGuidance := a.convergenceCheck(); convergenceGuidance != "" {
			a.crossDetectorConsensus.recordFiring("Convergence Lock", iter+1)
			a.appendGuidance(&result, convergenceGuidance)
		}

		if diminishingGuidance := a.diminishingCheck(); diminishingGuidance != "" {
			a.appendGuidance(&result, diminishingGuidance)
		}

		if refactorGuidance := a.prematureRefactorCheck(); refactorGuidance != "" {
			a.appendGuidance(&result, refactorGuidance)
		}

		if consensusGuidance := a.crossDetectorConsensus.checkOnly(); consensusGuidance != "" {
			a.appendGuidance(&result, consensusGuidance)
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

					a.errorPropagate.recordGuardedTruncation(tc.Name, result.Content)
				}
			}
		}

		measuredLen := len(result.Content)

		if askAdvisory != "" && tc.Name == "ask_user" && !result.IsError {
			result.Content = result.Content + "\n\n" + askAdvisory
		}
		a.applyToolResultGuidance(&result, loopGuidance, searchParamHint, redundancyHint, equivHint, undoBlindHint)

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
		isRedundant := redundancyHint != ""
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
