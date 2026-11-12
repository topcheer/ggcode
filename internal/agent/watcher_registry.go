package agent

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #5521 PR2: watcher registry. The inform-only post-tool blocks that used
// to live inline in RunStreamWithContent become registered observers.
// Contract: registration order == original inline execution order; every
// watcher method is the verbatim body of its former inline block (debug
// logging included), so advisories fire at the same points with the same
// payloads (golden-transcript zero-diff acceptance).

// postToolWatcher observes one executed tool call and may return an
// advisory message to inject as a user-role context entry. Returning ""
// means no advisory. Watchers must touch only their own detector state
// (side-effect observers are allowed; cross-detector writes stay in the
// stream loop).
type postToolWatcher interface {
	ObservePostTool(tc provider.ToolCallDelta, result tool.Result, iter int) string
}

// funcWatcher adapts an Agent method to postToolWatcher.
type funcWatcher func(tc provider.ToolCallDelta, result tool.Result, iter int) string

func (f funcWatcher) ObservePostTool(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	return f(tc, result, iter)
}

// guardZoneWatchers returns the guard-zone family in original inline order:
// build idempotency, tainted-data influence (2), SLO error budget,
// exfiltration chain, serial-read/reasoning observation, edit coverage,
// self-model, tool effectiveness.
func (a *Agent) guardZoneWatchers() []postToolWatcher {
	return []postToolWatcher{
		funcWatcher(a.watchBuildIdempotency),
		funcWatcher(a.watchTaintInfluence),
		funcWatcher(a.watchPseudoDirectives),
		funcWatcher(a.watchErrorBudget),
		funcWatcher(a.watchExfilChain),
		funcWatcher(a.observeSerialRead),
		funcWatcher(a.watchEditCoverage),
		funcWatcher(a.observeSelfModel),
		funcWatcher(a.watchToolEffectiveness),
	}
}

// runGuardZoneWatchers executes the family exactly as the former inline
// blocks did: warn (when non-empty) -> debug log inside the watcher ->
// user-role context add -> message snapshot refresh.
func (a *Agent) runGuardZoneWatchers(tc provider.ToolCallDelta, result tool.Result, iter int, msgs []provider.Message) []provider.Message {
	for _, w := range a.guardZoneWatchers() {
		if msg := w.ObservePostTool(tc, result, iter); msg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: msg}},
			})
			msgs = a.contextManager.Messages()
		}
	}
	return msgs
}

// ---- watcher bodies (verbatim former inline blocks) -----------------------

func (a *Agent) watchBuildIdempotency(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	if idempWarn := a.buildIdempot.recordToolCall(tc.Name, tc.Arguments, iter); idempWarn != "" {
		debug.Log("agent", "Iteration %d: build idempotency violation detected", iter)
		return idempWarn
	}
	return ""
}

func (a *Agent) watchTaintInfluence(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	if taintWarn := a.taintInfluence.checkInfluence(tc.Name, string(tc.Arguments)); taintWarn != "" {
		debug.Log("agent", "Iteration %d: tainted data influence detected on %s", iter, tc.Name)
		return taintWarn
	}
	return ""
}

func (a *Agent) watchPseudoDirectives(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	// (no debug log in the original inline block - kept absent)
	return a.taintInfluence.checkPseudoDirectiveArgs(tc.Name, string(tc.Arguments))
}

func (a *Agent) watchErrorBudget(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	// #28 SLO error budget: sliding-window success-rate guard. Fast
	// burn warns once; exhausting the budget trips the breaker and
	// escalates to ask_user instead of letting the agent keep
	// burning retries (budget layer vs compounding_failure's hint layer).
	if a.errorBudget != nil {
		if budgetMsg := a.errorBudget.evaluate(!result.IsError); budgetMsg != "" {
			debug.Log("agent", "Iteration %d: error budget event", iter)
			return budgetMsg
		}
	}
	return ""
}

func (a *Agent) watchExfilChain(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	if exfilWarn := a.exfilChain.checkExfilChain(tc.Name, string(tc.Arguments)); exfilWarn != "" {
		debug.Log("agent", "Iteration %d: exfiltration chain detected on %s", iter, tc.Name)
		return exfilWarn
	}
	return ""
}

// observeSerialRead: pure state recording (no advisory ever).
func (a *Agent) observeSerialRead(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	a.serialRead.recordToolCall(tc.Name)
	a.reasoningRedund.recordReasoning("", true)
	return ""
}

func (a *Agent) watchEditCoverage(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	if covWarn := a.editCoverage.recordToolCall(tc.Name, string(tc.Arguments)); covWarn != "" {
		debug.Log("agent", "Iteration %d: verification coverage gap detected", iter)
		if cmd := extractCommandFromToolCall(tc.Arguments); cmd != "" {
			a.scopeNarrow.lastCoverageWarnedCmd = cmd
		}
		return covWarn
	}
	return ""
}

// observeSelfModel: self-model success/failure recording (no advisory).
func (a *Agent) observeSelfModel(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	if a.selfModel != nil {
		if result.IsError {
			a.selfModel.Record(SelfModelPath(a.WorkingDir()), tc.Name, false, result.Content)
		} else {
			a.selfModel.Record(SelfModelPath(a.WorkingDir()), tc.Name, true, "")
		}
	}
	return ""
}

func (a *Agent) watchToolEffectiveness(tc provider.ToolCallDelta, result tool.Result, iter int) string {
	if effGuidance := a.toolEff.recordCall(tc.Name, result.Content, result.IsError); effGuidance != "" {
		debug.Log("agent", "Iteration %d: tool effectiveness guidance for %s", iter, tc.Name)
		return effGuidance
	}
	return ""
}

// ---- mutation watcher (dedup cache invalidation) ---------------------------

// sourceMutationWatcher reports whether a completed tool call mutated the
// source tree (dedup-cache invalidation signal).
type sourceMutationWatcher interface {
	MutatedSource(tc provider.ToolCallDelta, result tool.Result) bool
}

// dedupMutationWatcher implements sourceMutationWatcher with the verbatim
// #5153/#3851 condition: non-speculative tool that either succeeded or
// wrote despite error.
type dedupMutationWatcher struct{}

func (dedupMutationWatcher) MutatedSource(tc provider.ToolCallDelta, result tool.Result) bool {
	// #5153: a tool that errored but still wrote (multi_file_edit
	// partial_success) mutated the source tree, so cached read-only results
	// must be dropped - same condition as the toolMemo invalidation at the
	// mutatesSourceTree site (#2991).
	return !speculativeSafeTools[tc.Name] && (!result.IsError || partialEditWroteDespiteError(tc.Name, result.Content, result.IsError))
}

// dedupInvalidation is the registered source-mutation signal consumed by
// the seenReadOnly invalidation branch in RunStreamWithContent.
var dedupInvalidation dedupMutationWatcher
