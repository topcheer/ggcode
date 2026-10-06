package agent

// model_cascade.go -- Turn-tier model routing (RouteLLM-style cascade, r485).
//
// Research basis:
//   - RouteLLM (LMSYS, arXiv:2406.18665): route each query to the cheapest
//     model that can handle it; agent-framework engineering of this pattern
//     (e.g. the Agent Patterns Catalog entry) documents >2x cost reduction
//     at fixed quality.
//   - FrugalGPT-style LLM cascades (arXiv:2305.05176) and the 2025-2026
//     "cheap model for exploration, strong model for synthesis" guidance
//     from frontier labs' agent engineering write-ups.
//
// Gap this closes: config `aux_model` (model_routing.go) already routes
// TASK-tier auxiliary calls (compaction, strategist, health) to a cheaper
// model, but the primary conversation loop always runs the main model -
// even during long purely-exploratory stretches (read/grep/glob turns)
// where the frontier model's reasoning budget is wasted. This file adds
// TURN-tier routing: after `cascadeMinLowBatches` consecutive tool batches
// that were entirely read-only and error-free, the NEXT turn is classified
// exploratory and its LLM request is routed to the aux model provider. Any
// mutation tool, any error, or any stream failure resets the streak and
// reverts to the main model immediately.
//
// Dormancy: fully inert unless aux_model is configured (SetAuxModel) - the
// same opt-in surface as task-tier routing, no new config keys.
//
// Provider swap safety: the swap wraps only the streamChatResponse call,
// restored in the same panic-safe closure defer as adaptive effort (#1817);
// Agent state, context manager, and tool registry are untouched. Usage for
// a cascaded turn is recorded under the aux provider - consistent with the
// existing task-tier aux-call accounting.

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// cascadeMinLowBatches is how many consecutive all-read-only, error-free
// tool batches qualify the next turn as exploratory. 2 keeps the first
// exploratory-looking turn on the main model (cheap insurance against
// single-batch misreads) while still catching real scan stretches.
const cascadeMinLowBatches = 2

// modelCascadeState tracks the recent tool trajectory for turn-tier
// routing. Zero value is fully dormant.
type modelCascadeState struct {
	consecLowBatches int // consecutive all-readonly error-free sealed batches
	batchTools       []string
	batchHasErr      bool
	batchOpen        bool
}

// beginBatch seals any unsealed batch (defensive: run may `continue` past
// the tool loop) and opens a fresh one.
func (s *modelCascadeState) beginBatch() {
	if s.batchOpen {
		s.sealBatch()
	}
	s.batchTools = s.batchTools[:0]
	s.batchHasErr = false
	s.batchOpen = true
}

// notePlanned records a tool call that is about to execute. Planning-time
// recording covers both sequential and parallel execution paths uniformly.
func (s *modelCascadeState) notePlanned(name string) {
	if !s.batchOpen {
		return
	}
	s.batchTools = append(s.batchTools, name)
}

// noteError flags the open batch as errored. Called from the sequential
// result loop; a parallel-path error that bypasses that loop leaves the
// batch unflagged, which at worst grants one extra cascaded turn before
// the trajectory resets (bounded, non-corrupting).
func (s *modelCascadeState) noteError(isError bool) {
	if !s.batchOpen || !isError {
		return
	}
	s.batchHasErr = true
}

// sealBatch classifies the closed batch: all read-only and error-free
// extends the streak; anything else (mutation tool, error, empty batch)
// either resets or (empty) keeps state - a text-only turn carries no
// evidence either way.
func (s *modelCascadeState) sealBatch() {
	s.batchOpen = false
	if len(s.batchTools) == 0 {
		return
	}
	if s.batchHasErr {
		s.consecLowBatches = 0
		return
	}
	for _, n := range s.batchTools {
		if !effortReadOnlyTools[n] {
			s.consecLowBatches = 0
			return
		}
	}
	s.consecLowBatches++
}

// resetBatches drops all trajectory evidence (stream failure, interrupt).
func (s *modelCascadeState) resetBatches() {
	s.consecLowBatches = 0
	s.batchTools = nil
	s.batchHasErr = false
	s.batchOpen = false
}

// shouldCascade reports whether the next turn qualifies as exploratory.
func (s *modelCascadeState) shouldCascade() bool {
	return s.consecLowBatches >= cascadeMinLowBatches
}

// applyModelCascade swaps a.provider to the cached aux provider when the
// trajectory qualifies as exploratory and turn-tier routing is armed.
// Returns true when the main provider was parked (caller must restore).
func (a *Agent) applyModelCascade() bool {
	if a.auxResolved == nil {
		return false // aux_model unset: turn-tier routing disabled
	}
	if !a.cascade.shouldCascade() {
		return false
	}
	aux := a.auxProviderFor() // build-failure degrades to main provider internally
	if aux == nil || aux == a.provider {
		return false
	}
	a.cascadeSavedProvider = a.provider
	a.provider = aux
	debug.Log("model-cascade", "exploratory turn routed to aux model %q (streak=%d)",
		a.auxModelName, a.cascade.consecLowBatches)
	return true
}

// restoreModelCascade returns the parked main provider after a cascaded
// turn completes (normal return or panic unwind - callers restore inside
// the #1817 closure defer).
func (a *Agent) restoreModelCascade() {
	if a.cascadeSavedProvider == nil {
		return
	}
	a.provider = a.cascadeSavedProvider
	a.cascadeSavedProvider = nil
	debug.Log("model-cascade", "restored main model provider")
}

// cascadeTurnProvider is the provider a NEW agent-level request would use;
// exposed for tests to assert swap/restore behavior without racing the
// loop.
func (a *Agent) cascadeTurnProvider() provider.Provider { return a.provider }
