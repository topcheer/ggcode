package agent

// Tool Call Storm Detector
//
// Research basis:
//   - "Scaling Test-time Compute for LLM Agents" (arXiv:2506.12928, Jun 2025):
//     Systematically explored test-time scaling for agents and identified
//     "agentic overthinking" vs "blind execution" failure modes. When agents
//     execute rapid consecutive tool calls without interleaved reasoning
//     ("blind burst mode"), task quality degrades because each tool call
//     is not informed by reflection on prior results.
//   - "ReVeal: Self-Evolving Code Agents via Reliable Self-Verification"
//     (arXiv:2506.11442, Jun 2025): highlights the "verification-generation
//     asymmetry" where agents generate rapidly but verify slowly. Widening
//     this asymmetry (acting without reflection) is a leading quality risk.
//   - MiRA Subgoal-driven Framework (arXiv:2603.19685, 2026): shows that
//     long-horizon agents "lose track as new information arrives, lacking
//     a clear and adaptive path" when they execute without pausing to
//     consolidate findings into intermediate subgoals.
//
// Problem: AI coding agents sometimes enter a "tool storm" -- firing many
// diverse tool calls across consecutive iterations with very little
// reasoning text between them. Each result piles into context without being
// synthesized, leading to:
//   1. Context bloat (tool results accumulate without synthesis)
//   2. Shallow exploitation (results not used to inform next action)
//   3. Higher error rates (actions not grounded in prior observations)
//   4. Token waste (results loaded but never acted upon)
//
// Example failure pattern:
//   Iter 1: read_file(a) -- reasoning: "Let me read this file." (28 chars)
//   Iter 2: grep(pattern) -- reasoning: "Now search for X." (18 chars)
//   Iter 3: read_file(b)  -- reasoning: "Read another." (13 chars)
//   Iter 4: glob(**/*.go) -- reasoning: "List files." (11 chars)
//   → 4 diverse tool calls, 70 total reasoning chars, no synthesis.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - repetition_tracker.go: detects same-tool repeated calls (e.g., 5x
//     grep). Does NOT cover diverse tools fired in rapid succession.
//   - convergence_lock.go: detects stalled progress (repeated actions
//     without forward motion). Storm is the opposite -- too MUCH action.
//   - tool_sequence.go: detects specific cross-iteration anti-patterns
//     (e.g., full-read then targeted re-read). Does NOT track the
//     reasoning-deficit pattern across diverse tool sequences.
//   - bare_edit_streak.go: tracks consecutive edits without verification,
//     but only for edit tools, not exploration bursts.
//   - wasted_explore.go: tracks individual search results never acted on,
//     not the burst pattern of low-reasoning tool calls.
//
// Gap: No detector identifies the pattern where the agent fires many
// diverse tools across consecutive iterations while producing minimal
// reasoning text -- the "blind burst mode" from the test-time scaling
// literature. This detector fills that gap.
//
// Design:
//   - After each iteration, records whether a tool was called and the
//     reasoning text length for that turn.
//   - Maintains a sliding window of the last N iterations.
//   - Triggers when: all N iterations had tool calls AND the average
//     reasoning text length is below the "thin reasoning" threshold AND
//     at least ceil(N*0.6) distinct tools were used (diversity signals
//     non-deliberate exploration rather than focused repetition).
//   - Injects guidance to pause and synthesize findings before continuing.
//   - Zero LLM cost -- pure deterministic state tracking.
//   - Fires at most 2 times per run (advisory, non-blocking).

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// stormWindowSize: number of consecutive iterations to examine for
	// storm pattern detection. A small window catches bursts quickly
	// without false-positiving on legitimate 1-2 call sequences.
	stormWindowSize = 4

	// stormMinReasoningChars: average reasoning text length per iteration
	// below which we consider reasoning "thin." Calibrated so a single
	// short sentence like "Let me check." (15 chars) counts as thin, but
	// a few lines of analysis (150+ chars) does not.
	stormMinReasoningAvg = 80

	// stormMinDiversityRatio: fraction of the window that must use
	// distinct tools. 0.6 means at least ceil(4*0.6)=3 distinct tools in
	// a 4-iteration window. This distinguishes diverse-tool storms (our
	// target) from same-tool repetition (already covered by
	// repetition_tracker).
	stormMinDiversityRatio = 0.6

	// stormMaxInjections: cap warnings per run to avoid context flooding.
	stormMaxInjections = 2

	// stormMinReasoningAbs: if total reasoning across the window is below
	// this absolute threshold, trigger even with some diversity. Catches
	// near-zero reasoning bursts.
	stormMinReasoningAbs = 120
)

// toolStormState tracks consecutive tool-call iterations and their
// associated reasoning text to detect "blind burst mode."
type toolStormState struct {
	mu sync.Mutex

	// window holds the last stormWindowSize iterations' data.
	// Index 0 is the oldest entry in the window.
	window []stormIterEntry

	// injectionCount tracks how many storm warnings fired this run.
	injectionCount int

	// lastWarnedIter prevents re-warning on consecutive iterations for
	// the same burst. Set to the iteration number when last warned.
	lastWarnedIter int

	// pendingReasoning is the reasoning text for the current iteration,
	// captured before tool calls are processed. Reset after evaluation.
	pendingReasoning string
}

// stormIterEntry records one iteration's tool activity.
type stormIterEntry struct {
	Iter          int    // 1-based iteration number
	HasToolCall   bool   // whether any tool was called in this iteration
	ToolName      string // first tool name called (for diversity check)
	ReasoningLen  int    // length of reasoning text in this iteration
	ReasoningText string // the reasoning text (truncated for diagnostics)
}

func newToolStormState() *toolStormState {
	return &toolStormState{
		window:         make([]stormIterEntry, 0, stormWindowSize+1),
		lastWarnedIter: -1,
	}
}

// reset clears all state for a new run.
func (s *toolStormState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.window = s.window[:0]
	s.injectionCount = 0
	s.lastWarnedIter = -1
	s.pendingReasoning = ""
}

// recordReasoning captures the assistant's reasoning/text for the current
// iteration. Called before tool execution in the agent loop.
func (s *toolStormState) recordReasoning(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingReasoning = strings.TrimSpace(text)
}

// stormWaitingTools are tools that represent legitimate waiting (sleep, CI
// polling, background command checks). These should NOT count toward the
// storm window -- they break the "rapid-fire diverse tools" pattern by
// definition (the agent is waiting, not storming).
var stormWaitingTools = map[string]bool{
	"sleep":               true,
	"ci_status":           true,
	"wait_command":        true,
	"wait_agent":          true,
	"read_command_output": true,
	"list_commands":       true,
	"list_agents":         true,
	"teammate_results":    true,
	"task_output":         true,
}

// recordToolCall registers a tool call for the current iteration and
// appends the entry to the sliding window. Called after each tool
// execution. The reasoning text captured via recordReasoning is paired
// with this entry.
func (s *toolStormState) recordToolCall(toolName string, iter int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Waiting/blocking tools break any active storm window.
	if stormWaitingTools[toolName] {
		s.window = s.window[:0]
		return
	}

	reasoning := s.pendingReasoning

	entry := stormIterEntry{
		Iter:          iter,
		HasToolCall:   true,
		ToolName:      toolName,
		ReasoningLen:  len(reasoning),
		ReasoningText: truncateStormText(reasoning),
	}

	// If the last entry in the window is for the same iteration, update
	// it (multiple tool calls in one iteration -- we only need to know
	// that a tool was called and track the first tool name for diversity).
	if len(s.window) > 0 && s.window[len(s.window)-1].Iter == iter {
		return
	}

	s.window = append(s.window, entry)
	if len(s.window) > stormWindowSize {
		s.window = s.window[1:]
	}
}

// recordNoTool marks that an iteration completed without a tool call.
// This breaks any active storm window because storms require all
// iterations to have tool calls.
func (s *toolStormState) recordNoTool(iter int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.window) > 0 && s.window[len(s.window)-1].Iter == iter {
		return // already recorded
	}

	entry := stormIterEntry{
		Iter:         iter,
		HasToolCall:  false,
		ReasoningLen: len(s.pendingReasoning),
	}
	s.window = append(s.window, entry)
	if len(s.window) > stormWindowSize {
		s.window = s.window[1:]
	}
}

// maybeWarn evaluates the current window for the storm pattern and
// returns a guidance message if detected. Returns empty string otherwise.
func (s *toolStormState) maybeWarn() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Need a full window to evaluate.
	if len(s.window) < stormWindowSize {
		return ""
	}

	// All iterations in the window must have tool calls.
	for _, e := range s.window {
		if !e.HasToolCall {
			return ""
		}
	}

	// Calculate average reasoning length.
	totalReasoning := 0
	for _, e := range s.window {
		totalReasoning += e.ReasoningLen
	}
	avgReasoning := totalReasoning / len(s.window)

	// Check tool diversity within the window.
	toolSet := make(map[string]bool)
	for _, e := range s.window {
		toolSet[e.ToolName] = true
	}
	minDistinct := int(math.Ceil(float64(len(s.window)) * stormMinDiversityRatio))
	if minDistinct < 1 {
		minDistinct = 1
	}

	// Storm condition: thin reasoning (average below threshold OR absolute
	// total very low) AND sufficient tool diversity.
	thinAvg := avgReasoning < stormMinReasoningAvg
	thinAbs := totalReasoning < stormMinReasoningAbs
	diverse := len(toolSet) >= minDistinct

	if !(thinAvg || thinAbs) || !diverse {
		return ""
	}

	// Don't re-warn for the same burst or exceed injection cap.
	lastIter := s.window[len(s.window)-1].Iter
	if s.injectionCount >= stormMaxInjections {
		return ""
	}
	if lastIter-s.lastWarnedIter < stormWindowSize {
		return ""
	}

	s.injectionCount++
	s.lastWarnedIter = lastIter

	debug.Log("agent", "Tool storm detected: iter %d, avg_reasoning=%d, diverse_tools=%d/%d",
		lastIter, avgReasoning, len(toolSet), len(s.window))

	return fmt.Sprintf(
		"[tool-storm] %d consecutive tool calls (avg %d chars/iter, %d tools). Pause and synthesize before next call.",
		len(s.window), avgReasoning, len(toolSet),
	)
}

// truncateStormText truncates reasoning text for diagnostic logging.
func truncateStormText(s string) string {
	const maxLen = 60
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
