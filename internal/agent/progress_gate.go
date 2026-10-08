// progress_gate.go — ReflexGrad-style progress-gated mode switch (sa-138).
//
// Frontier basis: "ReflexGrad: Within-Episode Failure Recovery in LLM Agents
// via Progress-Gated Dual-Process Routing" (2025). The paper routes between a
// fast process (local patch attempts) and a slow process (causal replanning)
// using a single progress gate: sustained failure flips the agent out of
// patch mode into replan mode within the SAME episode.
//
// Gap verdict (sa-138): every existing failure detector in this package
// (capability_boundary, correction_spiral, error_strategy_loop,
// compounding_failure, convergence_lock) is ADVISORY — guidance text via
// guidanceEmit, subject to guidance-budget throttling, with zero behavioral
// change. The sole behavioral replan hook, errStreamInterruptedForReplan
// (agent.go), has no producer in the entire tree. So the agent can burn a
// whole run in patch-loop mode while detectors whisper advice it ignores.
//
// This gate is BEHAVIORAL, unlike its neighbors:
//   - On trigger (failure streak >= 3 spanning >= 2 distinct error
//     categories, with no interleaved success — any success resets the
//     streak), it FREEZES the remaining mutation-class tool calls in the
//     current batch (placeholder tool_result, read-only tools pass) so a
//     failing strategy cannot keep compounding writes.
//   - It then injects a structured REPLAN directive through a dedicated
//     channel (contextManager.Add, NOT guidanceEmit) so budget throttling
//     cannot swallow the mode switch.
//   - The next model turn starts from the replan directive: restate goal,
//     recheck constraints, choose a different approach.
//
// Anti-noise: fires at most 2 times per run; after firing, the streak resets
// and a 4-iteration refractory window prevents re-triggering while the new
// plan is still being tried. Zero LLM cost — pure classification reuse.
//
// Distinct from neighbors (do not merge):
//   - error_strategy_loop: SAME-category recurrence, advisory hint.
//   - capability_boundary: approach-pivot LANGUAGE detection, advisory.
//   - compounding_failure: rolling failure-rate, advisory.
//   - adaptive_effort: tool-type-driven effort tiers, not failure-driven.

package agent

import (
	"fmt"
	"sort"
	"strings"
)

const (
	progressGateStreakThreshold = 3 // consecutive failures needed
	progressGateMinCategories   = 2 // distinct error categories needed
	progressGateMaxFires        = 2 // per run
	progressGateRefractoryIters = 4 // iterations between fires
)

type progressGateState struct {
	failureStreak int
	streakCats    map[errCategory]int
	firedCount    int
	lastFiredIter int
	freezing      bool   // freeze remaining mutations in current batch
	pendingReplan string // replan directive to inject after the batch
}

func newProgressGateState() *progressGateState {
	return &progressGateState{
		streakCats:    make(map[errCategory]int),
		lastFiredIter: -progressGateRefractoryIters,
	}
}

func (s *progressGateState) reset() {
	s.failureStreak = 0
	s.streakCats = make(map[errCategory]int)
	s.firedCount = 0
	s.lastFiredIter = -progressGateRefractoryIters
	s.freezing = false
	s.pendingReplan = ""
}

// recordToolResult tracks the failure streak. Any successful tool result
// resets the streak: interleaved progress means the agent is NOT stuck in a
// patch loop, exactly like ReflexGrad's progress gate.
func (s *progressGateState) recordToolResult(resultContent string, isError bool) {
	cat, isErr := classifyErrResult(resultContent, isError)
	if !isErr {
		s.failureStreak = 0
		s.streakCats = make(map[errCategory]int)
		return
	}
	s.failureStreak++
	s.streakCats[cat]++
}

// shouldTrigger: streak >= 3 AND >= 2 distinct categories AND fire budget
// left AND outside the refractory window.
func (s *progressGateState) shouldTrigger(iter int) bool {
	if s.firedCount >= progressGateMaxFires {
		return false
	}
	if iter-s.lastFiredIter < progressGateRefractoryIters {
		return false
	}
	if s.failureStreak < progressGateStreakThreshold {
		return false
	}
	return len(s.streakCats) >= progressGateMinCategories
}

// trigger arms the freeze + replan directive and consumes the streak.
func (s *progressGateState) trigger(iter int) {
	s.firedCount++
	s.lastFiredIter = iter
	s.freezing = true
	s.pendingReplan = s.replanDirective()
	// Consume the streak so the directive gets a clean trial; the refractory
	// window guards against immediate re-arming while the new plan runs.
	s.failureStreak = 0
	s.streakCats = make(map[errCategory]int)
}

func (s *progressGateState) freezingMutations() bool {
	return s.freezing
}

// endBatch clears the freeze flag once the current batch is done — freezing
// applies ONLY to the remainder of the batch that was mid-flight when the
// gate fired, never to later batches (the replan directive governs those).
func (s *progressGateState) endBatch() {
	s.freezing = false
}

// consumePendingReplan returns and clears the queued replan directive.
func (s *progressGateState) consumePendingReplan() string {
	msg := s.pendingReplan
	s.pendingReplan = ""
	return msg
}

// freezePlaceholder is the tool_result a frozen mutation call receives. It is
// marked as an error result so the model unambiguously knows the write did
// NOT land.
func (s *progressGateState) freezePlaceholder(toolName string) string {
	return fmt.Sprintf(
		"[frozen by progress gate] %s was skipped: the run hit %d consecutive tool failures across %d+ error categories. "+
			"Remaining writes in this batch are frozen so the failing strategy cannot compound further. "+
			"Read-only tools still work. See the replan checkpoint message that follows.",
		toolName, progressGateStreakThreshold, progressGateMinCategories,
	)
}

// replanDirective is the structured slow-process prompt (ReflexGrad-style):
// stop patching, restate the goal, recheck constraints, pick a new approach.
// Injected through contextManager (dedicated channel), not guidanceEmit.
func (s *progressGateState) replanDirective() string {
	cats := make([]string, 0, len(s.streakCats))
	for c := range s.streakCats {
		cats = append(cats, string(c))
	}
	sort.Strings(cats)
	var sb strings.Builder
	sb.WriteString("[REPLAN CHECKPOINT — mode switch from local patching] ")
	sb.WriteString(fmt.Sprintf("The last %d+ tool calls failed across multiple error categories (%s) with no successful step in between. ", progressGateStreakThreshold, strings.Join(cats, ", ")))
	sb.WriteString("This pattern means the current APPROACH is wrong, not the individual edits. Do NOT continue patching. Instead: (1) restate the original task goal and its constraints in one sentence; (2) list what the failed attempts had in common; (3) choose a materially different approach (different tool, different order, verify assumptions first, or split the problem); (4) only then resume execution. Frozen writes in the previous batch were NOT applied — re-check file state before editing.")
	return sb.String()
}
