package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// loopState carries cross-section loop locals through the #5543 extractions.
type loopState struct {
	asyncVerifyStats          *RunStats
	consecutiveEmptyResponses int
	truncationContinues       int
	todoCheckCount            int
	detectorContinueBudget    int
	inlineToolCallNudges      int
	guidanceAtBudget          bool
	syncVerifyRetries         int
	deferredMemoryContent     string
	deferredMemoryFiles       []string
	deferredMemoryTarget      string
	msgs                      []provider.Message
	followUpMessages          []provider.Message
	progressCheckInjected     bool
	sessionBudgetStop         bool
	sessionTimedOut           bool
	transientCompactWarned    bool
}

// handleNoToolResponse processes the no-tool-calls path of one assistant
// turn: empty/blocked-response handling, the full text-only response
// inform family, and autopilot strategist turns. Verbatim body from
// RunStreamWithContent (#5543 knife-2); continue/return map to returned
// chatTurnAction signals, runStats capture goes through st.
func (a *Agent) handleNoToolResponse(ctx context.Context, resp *provider.ChatResponse, textBuf string, toolCalls []provider.ToolCallDelta, truncated bool, policyBlocked bool, reasoningChars int, onEvent func(provider.StreamEvent), iter int, runStats *RunStats, userPromptForStats string, st *loopState) (chatTurnAction, error) {
	if resp.Usage.OutputTokens == 0 && len(toolCalls) == 0 &&
		strings.TrimSpace(textBuf) == "" && !hasNonThinkingContent(resp.Message.Content) {

		if policyBlocked {
			debug.Log("agent", "Iteration %d: response fully blocked by provider policy, not retrying", iter+1)
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventSystem,
				Text: "[Response blocked by provider safety policy - not retrying. Try rephrasing the request.] ",
			})
			if len(resp.Message.Content) > 0 {
				a.contextManager.Add(resp.Message)
			}

			return chatTurnFatal, fmt.Errorf("agent: response blocked by provider safety filter (task did not complete)")
		}
		st.consecutiveEmptyResponses++
		debug.Log("agent", "Iteration %d: empty response detected (consecutive=%d, input_tokens=%d)",
			iter+1, st.consecutiveEmptyResponses, resp.Usage.InputTokens)
		if st.consecutiveEmptyResponses >= 3 {
			debug.Log("agent", "too many consecutive empty responses (%d), aborting", st.consecutiveEmptyResponses)

			onEvent(provider.StreamEvent{
				Type: provider.StreamEventText,
				Text: "[run aborted - the model returned 3 consecutive empty responses; the task did not complete]\n",
			})
			return chatTurnFatal, fmt.Errorf("agent: aborted after %d consecutive empty responses", st.consecutiveEmptyResponses)
		}

		a.contextManager.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "The previous response was empty. Please try again.",
			}},
		})
		return chatTurnContinue, nil
	}
	st.consecutiveEmptyResponses = 0

	if a.trajectoryHealth != nil {
		eCnt, rCnt := countToolTypes(toolCalls)
		assCnt := 0
		a.trajectoryHealth.recordIteration(eCnt, 0, len(toolCalls), rCnt, assCnt)
	}

	if len(toolCalls) == 0 {

		if truncated && !policyBlocked && st.truncationContinues < 3 {
			st.truncationContinues++
			debug.Log("agent", "Iteration %d: response truncated by output limit, auto-continuing (attempt %d/3)", iter+1, st.truncationContinues)
			a.contextManager.Add(resp.Message)
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventSystem,
				Text: "[Response was truncated by output length limit - continuing...] ",
			})

			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: "Your previous response was cut off by the output token limit. Continue from where you left off - do not repeat what you already wrote.",
				}},
			})
			return chatTurnContinue, nil
		}
		if truncated && policyBlocked {

			debug.Log("agent", "Iteration %d: response blocked by provider policy, skipping auto-continuation", iter+1)
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventSystem,
				Text: "[Response blocked by provider safety policy - partial output kept, not retrying.] ",
			})
		} else if truncated && !policyBlocked && st.truncationContinues >= 3 {

			debug.Log("agent", "Iteration %d: response truncated, continuation budget exhausted (3/3), keeping partial output", iter+1)
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventSystem,
				Text: "[Output truncated - auto-continuation budget exhausted (3/3); response may be incomplete.] ",
			})
		}

		assistantText := textBuf
		a.constraintViolation.recordReasoning(assistantText, iter+1)
		a.reasoningRedund.recordReasoning(assistantText, false)
		a.recordGiveupText(assistantText)

		if hasInlineToolCall(assistantText) && st.inlineToolCallNudges < 2 {
			st.inlineToolCallNudges++
			debug.Log("agent", "Iteration %d: inline tool call detected in text, nudging model (attempt %d/2)", iter+1, st.inlineToolCallNudges)
			a.contextManager.Add(resp.Message)

			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: "Use structured tool_use format, not inline text syntax for tool calls.",
				}},
			})
			return chatTurnContinue, nil
		}

		a.contextManager.Add(resp.Message)

		if fpMsg := a.falsePremise.checkFalsePremise(assistantText); fpMsg != "" {
			debug.Log("agent", "Iteration %d: false premise detected (ungrounded success claim)", iter+1)
			a.recordUncertainty("false_premise", weightFalsePremise)
			a.injectGuidance(fpMsg)
		}

		if rvMsg := a.reverify.checkStaleTrap(assistantText, iter+1); rvMsg != "" {
			debug.Log("agent", "Iteration %d: stale-observation reverify escalation triggered", iter+1)
			a.recordUncertainty("stale_obs_trapped", weightStaleObs)
			a.injectGuidance(rvMsg)
		}

		a.integrationCheckAndWarn(assistantText)

		if a.claimsSupervision {
			if psHint := a.prematureSuccess.checkSuccessClaim(assistantText); psHint != "" {
				debug.Log("agent", "Iteration %d: premature success claim detected (edits without verification)", iter+1)
				a.injectGuidance(psHint)

				a.recordUncertainty("unverified_success", weightUnverifiedSucc)
			}
		}

		if a.claimsSupervision {
			if pvHint := a.maybeWarnPhantomVerify(assistantText); pvHint != "" {
				debug.Log("agent", "Iteration %d: phantom verification detector found unverified category claims", iter+1)
				a.injectGuidance(pvHint)
			}
		}

		a.successDeclare.recordAssistantText(assistantText, iter)
		a.criteriaDrift.recordAssistantText(assistantText, iter)
		a.subgoalTrack.recordAssistantText(assistantText, iter)

		runStats.recordAssistantText(assistantText)

		detectorMsgsBefore := len(a.contextManager.Messages())
		if truncHint := a.truncClaim.maybeWarnTruncClaim(assistantText, iter); truncHint != "" {
			debug.Log("agent", "Iteration %d: truncated output completeness fallacy detected", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: truncHint,
				}},
			})
		}

		if circularHint := a.maybeWarnCircularReasoning(assistantText, iter); circularHint != "" {
			debug.Log("agent", "Iteration %d: circular reasoning detector triggered (%d instances)", iter+1, len(a.circularReasoning.instances))
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: circularHint,
				}},
			})
		}

		if contradictionHint := a.maybeWarnContradiction(assistantText, iter); contradictionHint != "" {
			debug.Log("agent", "Iteration %d: cross-turn contradiction detector triggered (%d contradictions)", iter+1, len(a.contradiction.contradictions))
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: contradictionHint,
				}},
			})
		}

		if scopeHint := a.maybeWarnScopeCreep(assistantText); scopeHint != "" {
			debug.Log("agent", "Iteration %d: scope creep detector detected unsolicited expansion", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: scopeHint,
				}},
			})
		}

		if abstrHint := a.maybeWarnPrematureAbstraction(assistantText); abstrHint != "" {
			debug.Log("agent", "Iteration %d: premature abstraction detector detected over-engineering", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: abstrHint,
				}},
			})
		}

		if capHint := a.maybeWarnCapabilityBoundary(assistantText); capHint != "" {
			debug.Log("agent", "Iteration %d: capability boundary detector detected stubborn persistence", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: capHint,
				}},
			})
		}

		if planHint := a.maybeWarnPlanAbandon(assistantText, runStats); planHint != "" {
			debug.Log("agent", "Iteration %d: plan abandonment detector triggered (declared %d steps)", iter+1, len(a.planAbandon.declaredSteps))
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: planHint,
				}},
			})
		}

		reproToolNames, reproToolInputs := extractToolNamesAndInputs(toolCalls)

		reproRan := false
		reproRunInput := ""
		for ti, tn := range reproToolNames {
			if reproducerRunToolNames[tn] {
				reproRan = true
				if ti < len(reproToolInputs) {
					reproRunInput = reproToolInputs[ti]
				}
				break
			}
		}
		a.reproducerLifecycle.observeText(iter+1, assistantText, reproRan, reproRunInput)
		a.reproducerLifecycle.observeToolCalls(iter+1, reproToolNames, reproToolInputs)
		if rlHint := a.reproducerLifecycle.checkIncomplete(iter + 1); rlHint != "" {
			debug.Log("agent", "Iteration %d: reproducer lifecycle detector triggered", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: rlHint,
				}},
			})
		}

		if ttHint := a.maybeWarnToolTargetMismatch(assistantText, toolCalls); ttHint != "" {
			debug.Log("agent", "Iteration %d: tool-target mismatch detector triggered", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: ttHint,
				}},
			})
		}

		if omHint := a.outcomeMisattrib.checkMisattribution(assistantText, iter+1); omHint != "" {
			debug.Log("agent", "Iteration %d: outcome misattribution detector triggered", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: omHint,
				}},
			})
		}

		if raHint := a.maybeWarnReasonAction(assistantText, toolCalls); raHint != "" {
			debug.Log("agent", "Iteration %d: reasoning-action alignment verifier triggered", iter+1)
			a.injectGuidance(raHint)
		}

		a.mindlessAction.recordStep(len(assistantText), false)

		if healthHint := a.maybeWarnTrajectoryHealth(); healthHint != "" {
			debug.Log("agent", "Iteration %d: trajectory health synthesizer detected composite degradation", iter+1)
			a.injectGuidance(healthHint)
		}

		if wasteHint := a.maybeWarnTokenWaste(); wasteHint != "" {
			debug.Log("agent", "Iteration %d: token waste budget exceeded 40%% threshold", iter+1)
			a.injectGuidance(wasteHint)
		}

		a.foresightCalib.recordPrediction(assistantText, toolCalls, iter+1)

		if epHint := a.foresightCalib.expectProtocolHint(toolCalls); epHint != "" {
			a.injectGuidance(epHint)
		}

		if gdHint := a.goalDriftCtx.checkDrift(iter + 1); gdHint != "" {
			debug.Log("agent", "Iteration %d: context-length goal drift detected", iter+1)
			a.injectGuidance(gdHint)
		}

		if len(a.contextManager.Messages()) > detectorMsgsBefore && st.detectorContinueBudget > 0 {
			st.detectorContinueBudget--
			debug.Log("agent", "Iteration %d: detector guidance injected; continuing run to consume it (#4650)", iter+1)
			return chatTurnContinue, nil
		}
		if injected, kind := a.injectPendingInterruptions(iter + 1); injected {
			a.recordIntervention(iter + 1)

			if a.maxIter > 0 && iter == a.maxIter-1 {
				st.guidanceAtBudget = true
			}

			if kind == InterruptionRetractStop {
				debug.Log("agent", "retraction-stop: aborting run after user withdrew the task")
				return chatTurnFatal, context.Canceled
			}
			return chatTurnContinue, nil
		}

		if !a.suppressAutopilotInjection && a.currentMode().IsCruiseMode() && a.hasAutopilotGoal() && a.autopilotStrategistCount < maxAutopilotStrategistCalls {
			a.strategistNoProgressCount++
			a.autopilotStrategistCount++

			if a.strategistNoProgressCount >= maxConsecutiveStrategistNoProgress {
				debug.Log("agent", "Iteration %d: autopilot force-terminate after %d consecutive no-progress rounds", iter+1, a.strategistNoProgressCount)
				preview := textBuf
				if len(preview) > 200 {

					preview = strings.ToValidUTF8(preview[:200], "")
				}
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Autopilot: agent idle for %d consecutive rounds - terminating to avoid deadlock. Last output: %s]", a.strategistNoProgressCount, preview)})
				a.ClearAutopilotGoal()

				if codeChangedInRun(runStats) {
					st.asyncVerifyStats = runStats
				}
				return chatTurnFatal, nil
			}
			debug.Log("agent", "Iteration %d: autopilot calling strategist (call #%d/%d, no-progress=%d/%d)", iter+1, a.autopilotStrategistCount, maxAutopilotStrategistCalls, a.strategistNoProgressCount, maxConsecutiveStrategistNoProgress)
			onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist #%d/%d: analyzing conversation and deciding next steps...] ", a.autopilotStrategistCount, maxAutopilotStrategistCalls)})
			result, sErr := a.runAutopilotStrategist(ctx, textBuf)
			if sErr != nil {
				debug.Log("agent", "autopilot strategist failed: %v", sErr)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist unavailable (%v) - autopilot stopping]", sErr)})

			} else if result.Complete {
				debug.Log("agent", "Iteration %d: strategist declared goal achieved", iter+1)
				summary := result.Guidance

				summary = stripStrategistCompleteMarker(summary)
				msg := "[Strategist: goal achieved - autopilot complete.]"
				if summary != "" {
					msg = fmt.Sprintf("[Strategist: goal achieved - autopilot complete. %s]", summary)
				}
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: msg})
				a.ClearAutopilotGoal()

				if codeChangedInRun(runStats) {
					st.asyncVerifyStats = runStats
				}
				return chatTurnFatal, nil
			} else if result.Guidance != "" {
				debug.Log("agent", "Iteration %d: strategist injecting guidance (%d chars)", iter+1, len(result.Guidance))
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: result.Guidance,
					}},
				})
				return chatTurnContinue, nil
			} else {

				debug.Log("agent", "Iteration %d: strategist returned empty guidance; continuing autonomously", iter+1)
				onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Strategist returned no guidance - continuing autonomously]"})
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: "Continue the task autonomously: check remaining work, run build/tests to verify, then finish and summarize what was completed.",
					}},
				})
				return chatTurnContinue, nil
			}
		} else if a.currentMode().IsCruiseMode() && a.hasAutopilotGoal() && !a.strategistBudgetAnnounced {

			a.strategistBudgetAnnounced = true
			debug.Log("agent", "Iteration %d: strategist budget exhausted (%d/%d), injecting one-time continuation guidance", iter+1, a.autopilotStrategistCount, maxAutopilotStrategistCalls)
			onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Strategist budget at limit (%d/%d) - continuing autonomously]", a.autopilotStrategistCount, maxAutopilotStrategistCalls)})
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: "Strategist budget exhausted. Continue remaining tasks autonomously - build, test, verify, then summarize.",
				}},
			})
			return chatTurnContinue, nil
		}

		if st.todoCheckCount < 2 {
			if reminder := a.checkIncompleteTodos(); reminder != "" {
				st.todoCheckCount++
				debug.Log("agent", "Iteration %d: incomplete todos detected, injecting reminder (%d/2)", iter+1, st.todoCheckCount)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: reminder,
					}},
				})
				return chatTurnContinue, nil
			}
		}

		if driftMsg := a.planDrift.checkPlanDrift(runStats, textBuf); driftMsg != "" {
			debug.Log("agent", "Iteration %d: plan drift detected, injecting reminder", iter+1)

			a.driftRecurrenceMarkWarn(runStats.Iterations)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: driftMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		a.constraintAudit.observe(userPromptForStats)
		if fulfillmentMsg := a.checkFulfillmentGate(userPromptForStats, runStats, textBuf); fulfillmentMsg != "" {
			debug.Log("agent", "Iteration %d: fulfillment gate detected gap, injecting reminder", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fulfillmentMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if wf := a.workflowEngineLazy(); wf != nil {
			if wfMsg := wf.outstandingMessageOnce(); wfMsg != "" {
				debug.Log("agent", "Iteration %d: workflow-spec audit injected", iter+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: wfMsg,
					}},
				})

				return chatTurnContinue, nil
			}
		}

		if auditMsg := a.constraintAudit.checkAndInject(); auditMsg != "" {
			debug.Log("agent", "Iteration %d: constraint audit injected", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: auditMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if fuMsg := a.constraintFollowup.checkAndInject(textBuf, a.constraintAudit); fuMsg != "" {
			debug.Log("agent", "Iteration %d: constraint followup injected (round %d)", iter+1, a.constraintFollowup.currentRound())
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fuMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		a.mu.Lock()
		gateMsg := finalTurnEvidenceGate(
			a.postEditVerify.sourceEditsThisRun,
			a.postEditVerify.lastSourceFileThisRun,
			a.postEditVerify.realBuildOrTestRunThisRun,
			a.postEditVerify.finalGateFiredThisRun,
			a.workingDir)
		if gateMsg != "" {
			a.postEditVerify.finalGateFiredThisRun = true
		}
		a.mu.Unlock()
		if gateMsg != "" {
			debug.Log("agent", "Iteration %d: final-turn evidence gate fired, demanding verification before stop", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: gateMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if retraceShouldCheck(a.postEditVerify, a.overseer.researchMode, a.retraceGate.firedThisRun) {
			a.retraceGate.firedThisRun = true
			if diff := uncommittedDiff(a.workingDir); strings.TrimSpace(diff) != "" {
				if msg := a.runRetraceVerification(ctx, userPromptForStats, diff); msg != "" {
					debug.Log("agent", "Iteration %d: retrace gate verdict non-aligned, injecting revision guidance", iter+1)
					a.contextManager.Add(provider.Message{
						Role: "user",
						Content: []provider.ContentBlock{{
							Type: "text",
							Text: msg,
						}},
					})
					return chatTurnContinue, nil
				}
			}
		}

		a.mu.Lock()
		researchMsg := researchReportGate(
			a.overseer.researchMode,
			a.overseer.searchCalls+a.overseer.fetchCalls,
			researchReportGateMinCalls,
			a.overseer.reportGateFired)
		if researchMsg != "" {
			a.overseer.reportGateFired = true
		}
		a.mu.Unlock()
		if researchMsg != "" {

			if draft := SynthesizeResearchReport("", a.overseer.findingsSnapshot(), a.researchPlanSubs); draft != "" {
				researchMsg = draft + "\n\n" + researchMsg
			}
			debug.Log("agent", "Iteration %d: research-report gate fired, demanding structured synthesis before stop", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: researchMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if a.claimsSupervision {
			if claimMsg := a.checkUnverifiedClaim(textBuf, runStats); claimMsg != "" {
				debug.Log("agent", "Iteration %d: unverified success claim detected, injecting reminder", iter+1)
				a.contextManager.Add(provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: claimMsg,
					}},
				})
				return chatTurnContinue, nil
			}
		}

		if companionMsg := a.companionGuard.checkCompanionFiles(runStats, a.WorkingDir()); companionMsg != "" {
			debug.Log("agent", "Iteration %d: companion file guard detected unedited test companions", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: companionMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if specGamingMsg := a.checkSpecGaming(runStats, userPromptForStats); specGamingMsg != "" {
			debug.Log("agent", "Iteration %d: specification gaming detected, injecting warning", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: specGamingMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		syncPassed := false
		if codeChangedInRun(runStats) && a.currentMode() != permission.PlanMode && ctx.Err() == nil {

			if shouldContinue, passed := a.syncVerifyAndGate(ctx, runStats, st.syncVerifyRetries); shouldContinue {
				st.syncVerifyRetries++
				debug.Log("agent", "Iteration %d: sync verify failed, auto-repairing (retry %d/%d)", iter+1, st.syncVerifyRetries, maxSyncVerifyRetries)
				return chatTurnContinue, nil
			} else if passed {
				syncPassed = true
			}
		}
		if syncPassed {
			debug.Log("agent", "sync verify passed, skipping async verify")
		} else {

			st.asyncVerifyStats = runStats
		}

		if complexityMsg := a.checkComplexityGate(runStats); complexityMsg != "" {
			debug.Log("agent", "Iteration %d: complexity gate detected quality issues, injecting advisory", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: complexityMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if impactMsg := a.checkCrossFileImpact(runStats); impactMsg != "" {
			debug.Log("agent", "Iteration %d: cross-file impact analysis detected potential breakage", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: impactMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if reconcileMsg := a.checkChangeReconcile(runStats); reconcileMsg != "" {
			debug.Log("agent", "Iteration %d: change reconciliation detected unexpected files", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: reconcileMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if evalMsg := a.checkAdversarialReviewGate(ctx, runStats, userPromptForStats); evalMsg != "" {
			debug.Log("agent", "Iteration %d: adversarial evaluator returned findings", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: evalMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if diffSummaryMsg := a.checkDiffSummaryGate(runStats); diffSummaryMsg != "" {
			debug.Log("agent", "Iteration %d: diff summary gate injected self-review", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: diffSummaryMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if goalMsg := a.checkGoalFinishGate(); goalMsg != "" {
			debug.Log("agent", "Iteration %d: goal finish gate found dangling in_progress leaves", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: goalMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if commitHintMsg := a.checkCommitHintGate(runStats); commitHintMsg != "" {
			debug.Log("agent", "Iteration %d: commit hint gate injected reminder", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: commitHintMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if docDriftMsg := a.checkDocDriftGate(runStats); docDriftMsg != "" {
			debug.Log("agent", "Iteration %d: doc-drift advisory injected", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: docDriftMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if draftPRMsg := a.checkDraftPRHintGate(runStats); draftPRMsg != "" {
			debug.Log("agent", "Iteration %d: draft-PR hint gate injected reminder", iter+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: draftPRMsg,
				}},
			})
			return chatTurnContinue, nil
		}

		if d := a.oversightTriage.digest(); d != "" {
			onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: d})
		}

		if d := a.autonomyDial.suggest(); d != "" {
			onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: d})
		}
		debug.Log("agent", "Iteration %d: no tool calls, returning", iter+1)
		return chatTurnFatal, nil
	}
	// unreachable by construction: the caller gates on len(toolCalls)==0,
	// which always enters the no-tool block above (whose last path returns).
	return chatTurnFatal, nil
}
