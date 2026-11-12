package agent

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// #5543: loop-core extraction. The per-iteration detector-guidance chain
// of the stream loop (plan suggestion, stale todo/file reminders, the
// maybeWarn* detector battery, delegation-orchestration warnings, and the
// mid-point progress checkpoint) becomes an explicit method; behavior
// unchanged (verbatim body, order preserved).

// runDetectorGuidance runs the iteration-prefix guidance detectors
// (verbatim order). It never exits the loop early: detectors only emit
// guidance into the context, refreshing msgs, and the mid-point progress
// checkpoint arms progressCheckInjected once.
func (a *Agent) runDetectorGuidance(i int, msgs []provider.Message, progressCheckInjected bool) ([]provider.Message, bool) {
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

	if a.maxIter >= 20 && !progressCheckInjected && i+1 >= a.maxIter*3/5 {
		progressCheckInjected = true
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
	return msgs, progressCheckInjected
}
