package agent

import "strconv"

// Mindless Action Pattern Detector
//
// Research basis:
//   - Agentic Metacognition (arXiv 2509.19783, 2025): proposes a
//     metacognitive layer for agent self-awareness. Key insight: agents
//     that act without reflecting on results become trapped in
//     "autopilot loops" - firing tools mindlessly without integrating
//     feedback from prior steps.
//   - MetaCognition Patterns for AI Agent Self-Monitoring (Zylos AI,
//     2026): identifies "rapid-fire action without reasoning" as a
//     primary failure mode in non-deterministic agents.
//   - Cognitive Load Framework (Springer, 2026): overloaded agents
//     shed reasoning steps and fall back to stimulus-response patterns.
//
// Problem: AI coding agents sometimes fire 5+ tool calls in rapid
// succession with negligible reasoning text between them (< 50 chars
// per step). This "mindless action" pattern indicates the agent is
// operating in autopilot mode - not reflecting on results before
// acting, which leads to:
//
//  1. Acting on stale/unintegrated tool results
//  2. Missing important information in outputs
//  3. Cascading errors from unexamined failures
//  4. Wasted token budget on unconsidered actions
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - tool_overuse.go: detects redundant calls for already-known info.
//   - analysis_paralysis.go: detects too much exploration vs action.
//   - edit_oscillation.go: detects back-and-forth semantic changes.
//
// Gap: No detector monitors the reasoning-to-action ratio per step
// to catch the agent when it stops thinking between actions. This
// detector addresses the gap.
//
// Design:
//   - Tracks consecutive tool-call steps where assistant text before
//     the tool call was below a threshold (mindless threshold).
//   - When 4+ consecutive mindless steps are detected, inject guidance.
//   - Non-blocking advisory, max 2 warnings per run.
//   - Zero LLM cost - pure deterministic text length tracking.

const (
	// mindlessTextThreshold: if the reasoning text before a tool call is
	// shorter than this (in chars), the step is considered "mindless".
	mindlessTextThreshold = 50

	// mindlessStreakThreshold: number of consecutive mindless steps
	// before triggering a warning.
	mindlessStreakThreshold = 4

	// mindlessMaxWarnings: max warnings per run.
	mindlessMaxWarnings = 2
)

// mindlessActionState tracks consecutive mindless action steps.
type mindlessActionState struct {
	streak   int // current consecutive mindless steps
	warnings int // warnings issued this run
}

func newMindlessActionState() *mindlessActionState {
	return &mindlessActionState{}
}

func (s *mindlessActionState) reset() {
	s.streak = 0
	s.warnings = 0
}

// recordStep records whether the current step had meaningful
// reasoning before a tool call. Returns true if a warning should fire.
func (s *mindlessActionState) recordStep(reasoningLen int, hasToolCall bool) bool {
	if !hasToolCall {
		// Non-tool step resets the streak (agent is reflecting).
		s.streak = 0
		return false
	}

	if reasoningLen < mindlessTextThreshold {
		s.streak++
	} else {
		// Adequate reasoning breaks the streak.
		s.streak = 0
	}

	if s.streak >= mindlessStreakThreshold && s.warnings < mindlessMaxWarnings {
		s.warnings++
		return true
	}

	return false
}

// mindlessActionWarning returns the guidance text when the detector fires.
func mindlessActionWarning(streak int) string {
	return "[INFO-mindless-action] You have fired " + strconv.Itoa(streak) +
		" consecutive tool calls with minimal reasoning between them. " +
		"You may be operating in autopilot mode without adequately " +
		"reflecting on results. Pause and review the output of your " +
		"recent tool calls before proceeding. " +
		"Consider: Did the last action succeed? Are you acting on " +
		"verified information? Is each step moving toward the goal?"
}
