package agent

// Extracted from RunStreamWithContent's iteration loop: the per-iteration
// advisory detector chain. Each detector is a deterministic, zero-LLM-cost
// heuristic that inspects accumulated agent state at the top of every loop
// iteration and injects a guidance message when its problem pattern fires.
//
// The call order below is load-bearing: several detectors consume per-run
// quotas or set one-shot flags when their message is actually delivered
// (see the #681 markUndelivered rollbacks), so reorder only with care.
//
// i is the zero-based loop index; msgs is the current message slice. The
// returned slice must be assigned back by the caller because context-manager
// additions refresh it.

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// runAdvisoryDetectors runs the full advisory detector chain for iteration
// i (zero-based) and returns the possibly refreshed message slice.
func (a *Agent) runAdvisoryDetectors(i int, msgs []provider.Message) []provider.Message {
	// Agent-side planning: inject a plan suggestion or reminder early in
	// the conversation when the request was detected as complex. This is
	// a deterministic, zero-LLM-cost approach inspired by Devin's Planner
	// and Claude Code's auto-todo behavior.
	if planHint := a.maybeSuggestPlan(i + 1); planHint != "" {
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: planHint}},
		})
		msgs = a.contextManager.Messages()
	}
	// Mid-run stale todo detection: if the agent created a todo list but
	// hasn't updated it for several iterations while there are still
	// incomplete items, inject a one-time reminder to sync the plan.
	if staleReminder := a.maybeRemindStaleTodo(i + 1); staleReminder != "" {
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: staleReminder}},
		})
		msgs = a.contextManager.Messages()
	}
	// Task re-anchoring: prevent context collapse on long repair chains.
	// File freshness sentinel: proactively detect externally modified files
	// (IDE save, formatter, git pull, another agent). Injects a notification
	// BEFORE the agent uses stale content, not reactively at edit time.
	if staleMsg := a.fileFreshness.maybeCheckStaleFiles(i + 1); staleMsg != "" {
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: staleMsg}},
		})
		msgs = a.contextManager.Messages()
	}
	// Tool thermal profile: detect imbalanced tool-call distribution
	// (e.g., 90% reads with no edits = agent is spinning). Zero-LLM-cost
	// heuristic based on cross-tool category analysis.
	if thermalMsg := a.toolThermal.maybeWarn(i); thermalMsg != "" {
		debug.Log("thermal-profile", "imbalanced tool usage detected at iteration %d: %s", i+1, a.toolThermal.categoryBreakdown())
		msgs = a.guidanceEmit(thermalMsg, msgs)
	}
	// Error compounding risk: compute geometric compounding probability
	// and warn when accumulated errors make the trajectory unreliable.
	if ecMsg := a.errorCompound.maybeWarn(i + 1); ecMsg != "" {
		// #681: maybeWarn consumed the per-run quota ("at most 2 per run")
		// by returning; only a delivered message may keep it. Suppressed
		// fires are rolled back so the quota is not burned with zero
		// guidance delivered.
		if a.injectGuidance(ecMsg) {
			msgs = a.contextManager.Messages()
		} else {
			a.errorCompound.markUndelivered()
		}
	}
	// Correction spiral: detect error severity escalation across fix attempts.
	// Warns when each correction introduces a worse error (feedback control instability).
	if csMsg := a.correctionSpiral.maybeWarn(i + 1); csMsg != "" {
		msgs = a.guidanceEmit(csMsg, msgs)
	}
	// Verification debt: warn when source edits accumulate without a
	// successful build. Prevents last-mile failure from compounding
	// unverified changes (arXiv:2602.16666).
	if vdMsg := a.verifyDebt.maybeWarn(i + 1); vdMsg != "" {
		msgs = a.guidanceEmit(vdMsg, msgs)
	}
	// Cross-file edit propagation risk: warn when many DISTINCT files
	// are edited without verification. Cross-file dependency chains
	// create error propagation paths (MAST taxonomy, Cemri et al. 2025).
	if epMsg := a.editPropagation.maybeWarn(i + 1); epMsg != "" {
		msgs = a.guidanceEmit(epMsg, msgs)
	}
	// Premature success declaration: if the agent claimed completion in a
	// prior iteration but has since continued making tool calls, flag the
	// metacognitive calibration gap.
	// #1499 case A: subgoal tracking is a lexical heuristic in the
	// claims-supervision family - unconditioned, it interfered with
	// every user by default while its sibling (success_declare) is
	// opt-in. Same gate.
	if a.claimsSupervision {
		if sgMsg := a.subgoalTrack.maybeWarn(i + 1); sgMsg != "" {
			debug.Log("agent", "Iteration %d: subgoal completion gap detected", i+1)
			msgs = a.guidanceEmit(sgMsg, msgs)
		}
	}
	// Success-declaration calibration detector is gated behind
	// claimsSupervision (default off): lexical success-phrase heuristics on
	// intermediate states inject noise current models don't need.
	if a.claimsSupervision {
		if sdMsg := a.successDeclare.maybeWarn(i + 1); sdMsg != "" {
			debug.Log("agent", "Iteration %d: premature success declaration detected", i+1)
			msgs = a.guidanceEmit(sdMsg, msgs)
		}
	}
	if cdMsg := a.criteriaDrift.maybeWarn(i + 1); cdMsg != "" {
		debug.Log("agent", "Iteration %d: success criteria drift detected", i+1)
		msgs = a.guidanceEmit(cdMsg, msgs)
	}
	// Attempt brief: compact summary of failed approaches to prevent
	// repeating the same dead-end strategy.
	if abMsg := a.attemptBrief.maybeBrief(i + 1); abMsg != "" {
		debug.Log("agent", "Iteration %d: injecting attempt brief", i+1)
		msgs = a.guidanceEmit(abMsg, msgs)
	}
	// Wasted exploration detection: nudge the agent when previous
	// search results containing file paths were never acted upon.
	// Information scent decay detection: nudge when consecutive
	// exploration calls yield diminishing novel information.
	if scentMsg := a.infoScent.maybeWarn(i + 1); scentMsg != "" {
		msgs = a.guidanceEmit(scentMsg, msgs)
	}
	// Orphaned background command detection: nudge the agent to check
	// output of background commands (start_command) that haven't been
	// read for several iterations.
	// Query convergence failure: detect repeated similar search queries
	// across iterations without progressing to code action.
	if qcMsg := a.queryConverge.maybeWarn(i + 1); qcMsg != "" {
		msgs = a.guidanceEmit(qcMsg, msgs)
	}
	if bgOrphanMsg := a.maybeWarnBgOrphan(i + 1); bgOrphanMsg != "" {
		msgs = a.guidanceEmit(bgOrphanMsg, msgs)
	}
	// Reasoning redundancy detection: consecutive text-only iterations with
	// near-duplicate content indicate overthinking (arXiv:2503.16419).
	// Nudge the agent to stop deliberating and act.
	if rrMsg := a.reasoningRedund.maybeWarn(i+1, a.maxIter); rrMsg != "" {
		debug.Log("reasoning-redund", "Iteration %d: reasoning redundancy detected -- consecutive text-only overthinking", i+1)
		msgs = a.guidanceEmit(rrMsg, msgs)
	}
	// Iteration pressure degradation: detect verify/edit ratio drop
	// near the iteration budget limit (metacognitive monitoring).
	if ipMsg := a.maybeWarnIterPressure(i + 1); ipMsg != "" {
		msgs = a.guidanceEmit(ipMsg, msgs)
	}
	// Unverified mutation streak: detect consecutive edits without any
	// verification (build/test/run) to encourage tight feedback loops.
	if bsMsg := a.bareEditStreak.maybeWarn(i + 1); bsMsg != "" {
		msgs = a.guidanceEmit(bsMsg, msgs)
	}
	// Verification coverage gap: handled in tool-execution loop below.
	// Strategy fixation: detect when the agent has edited the same file
	// multiple times with intervening failed verifications, suggesting an
	// approach-level failure (PARC arXiv:2512.03549).
	if sfMsg := a.strategyFixation.check(); sfMsg != "" {
		msgs = a.guidanceEmit(sfMsg, msgs)
	}
	// Error rush: detect panic coding -- blind-fixing after consecutive
	// errors without diagnostic reads in between (Agentic Overconfidence,
	// arXiv 2026; AgentDiet, FSE 2026).
	if erMsg := a.errorRush.check(); erMsg != "" {
		msgs = a.guidanceEmit(erMsg, msgs)
	}
	// Attention fragmentation: detect rapid directory context-switching
	// that creates extraneous cognitive load (CLT for LLM agents,
	// arXiv:2506.06843). High switch density means the model is thrashing
	// between unrelated concerns instead of maintaining coherent focus.
	if afMsg := a.attentionFragment.analyze(); afMsg != "" {
		msgs = a.guidanceEmit(afMsg, msgs)
	}
	// Drift-recurrence iteration bookkeeping: check()'s post-warning
	// window (driftRecurrencePostWarnWindow) compares against the current
	// iteration — without this call currentIteration stayed 0 and the
	// window guard was permanently false, letting stale warnings from
	// dozens of iterations ago fire on a normal edit rhythm (#377).
	a.driftRecurrence.recordIteration(i + 1)
	// Futile cycle: detect when the agent re-reads the same set of files
	// that it explored earlier without making any edits in between.
	if fcMsg := a.futileCycle.maybeWarn(i + 1); fcMsg != "" {
		msgs = a.guidanceEmit(fcMsg, msgs)
	}
	// Constraint amnesia: remind the agent of user-specified constraints
	// that may have scrolled out of effective attention after many iterations.
	// Catastrophic forgetting in token space (Letta/MemGPT 2025).
	if caMsg := a.constraintAmnesia.maybeWarn(i + 1); caMsg != "" {
		msgs = a.guidanceEmit(caMsg, msgs)
	}
	// Diagnostic-action disconnect detection: when the agent has received
	// diagnostic content (errors, undefined symbols) but subsequent actions
	// don't address it, inject guidance to refocus on the known issue.
	// Delegation orchestration intelligence: detect orphaned delegations
	// (spawned agents whose results were never consumed), serial delegation
	// anti-pattern (should batch parallelizable tasks), and over-delegation
	// (excessive delegation ratio). Zero-LLM-cost deterministic heuristics.
	if a.delegationOrch != nil {
		// Gate activation per #345/#348 decision: only the over-delegation
		// gate is active. The orphan gate's ID matching never fired in
		// production (tool-call ID vs agent/task ID namespaces are
		// disjoint, so legitimate consumption never cleared orphan timers
		// and the gate false-positived); the serial gate was not part of
		// the activation decision. Both detection paths are now fixed and
		// kept dormant behind flags for re-enablement after validation.
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
	// Monorepo scope sprawl detection: if the agent is editing across many
	// packages in a monorepo without apparent cross-package intent, inject
	// a one-time hint to confirm scope and consider package-scoped ops.
	if monorepoMsg := a.monorepoScoper.maybeWarnScopeSprawl(); monorepoMsg != "" {
		debug.Log("monorepo-scope", "package scope sprawl detected: %s", monorepoMsg)
		// #681: one-shot hint — if the per-turn budget suppresses it, the
		// one-time chance is restored so it retries on a later iteration
		// instead of the detector going dark for the rest of the run.
		if a.injectGuidance(monorepoMsg) {
			msgs = a.contextManager.Messages()
		} else {
			a.monorepoScoper.markUndelivered()
		}
	}

	return msgs
}
