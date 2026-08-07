package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Iteration Momentum Loss Detector -- Late-Phase Productivity Collapse
//
// Research basis:
//   - "The Agent Last Mile Failure Problem" (2026): agents complete early
//     sub-tasks at 80-90% but stall at 30-50% on the final integration/terminal
//     phase. Per-step 95% success compounds to only 36% task-level completion.
//   - "How Do LLMs Fail In Agentic Scenarios?" (arXiv:2512.07497): "recovery
//     skill" -- sustained productivity across the full run lifecycle -- is the
//     single biggest differentiator between high- and low-performing agents.
//   - "Measuring AI Ability to Complete Long Software Tasks" (arXiv:2503.14499):
//     the 50%-task-completion time horizon shows that reliability degradation
//     in late iterations is the dominant failure mode, not raw capability.
//
// The gap: ggcode has many per-failure-mode detectors (edit oscillation, tool
// overuse, convergence lock, etc.) but NONE track the aggregate productivity
// trajectory across the run lifecycle. An agent can shift from productive
// editing to pure read-only/exploratory tool calls in the final iterations --
// the "last mile stall" -- without triggering any existing detector, because
// each individual read-only call looks normal in isolation.
//
// This detector:
//   1. Tracks per-iteration productive actions (edits, writes, commands) vs
//      consumptive actions (reads, searches, explorations).
//   2. When the run enters the terminal phase (>= 60% of max iterations) AND
//      has accumulated prior productive work, checks if recent iterations
//      show a sharp productivity collapse (0 productive actions in the last
//      2+ iterations while continuing high tool-call volume).
//   3. Fires ONCE to inject a targeted reminder about finishing the last mile.
//
// Key distinction from existing detectors:
//   - Tool Diversity Gate: checks for tool-type stagnation (same tools)
//   - Strategy Stagnation: retries of failed same-tool+target
//   - Analysis Paralysis: too many reads with no edits ever
//   - This detector: tracks the PRODUCTIVITY RATIO TRAJECTORY across the
//     run lifecycle -- specifically the late-phase collapse after early
//     productivity, which is a different and complementary signal.

const (
	maxMomentumWarnings  = 1
	momentumTerminalFrac = 0.6 // terminal phase starts at 60% of max iterations
	momentumMinHistory   = 3   // need at least 3 iterations of history
	momentumStallWindow  = 2   // consecutive unproductive iterations to trigger
)

// Productive tools that change state or move the task forward.
var momentumProductiveTools = map[string]bool{
	"edit_file":        true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"write_file":       true,
	"multi_file_write": true,
	"notebook_edit":    true,
	"run_command":      true,
	"start_command":    true,
	"git_add":          true,
	"git_commit":       true,
	"git_checkout":     true,
	"git_revert":       true,
	"git_stash":        true,
	"git_reset":        true,
	"file_ops":         true,
	"batch_replace":    true,
}

// momentumIterRecord tracks one iteration's tool call composition.
type momentumIterRecord struct {
	iter        int
	productive  int
	consumptive int
	total       int
}

// momentumLossState tracks the productivity trajectory across iterations.
type momentumLossState struct {
	fired       bool
	iterations  []momentumIterRecord
	currentIter int
}

func newMomentumLossState() *momentumLossState {
	return &momentumLossState{}
}

func (m *momentumLossState) reset() {
	m.fired = false
	m.iterations = m.iterations[:0]
	m.currentIter = 0
}

// startIteration begins tracking a new iteration.
func (m *momentumLossState) startIteration(iter int) {
	m.currentIter = iter
	// Pre-append a record for this iteration.
	if len(m.iterations) == 0 || m.iterations[len(m.iterations)-1].iter != iter {
		m.iterations = append(m.iterations, momentumIterRecord{iter: iter})
	}
}

// recordToolCall classifies a tool call as productive or consumptive.
func (m *momentumLossState) recordToolCall(toolName string) {
	if len(m.iterations) == 0 {
		return
	}
	idx := len(m.iterations) - 1
	r := &m.iterations[idx]
	r.total++
	if momentumProductiveTools[toolName] {
		r.productive++
	} else {
		r.consumptive++
	}
}

// checkMomentumLoss evaluates whether the late-phase productivity has collapsed.
// Returns a non-empty guidance message if the last-mile stall pattern is detected.
//
// Parameters:
//   - maxIter: the maximum iteration budget for the run
//
// The pattern we look for:
//  1. We're in the terminal phase (>= 60% of max iterations)
//  2. There was meaningful prior productivity (>= 1 productive action in
//     the first half of the run)
//  3. The last N iterations have had 0 productive actions despite continued
//     tool-call activity (i.e., the agent is still working but not producing)
func (m *momentumLossState) checkMomentumLoss(maxIter int) string {
	if m.fired {
		return ""
	}
	if len(m.iterations) < momentumMinHistory {
		return ""
	}

	// Only check in the terminal phase.
	terminalStart := int(float64(maxIter) * momentumTerminalFrac)
	if m.currentIter < terminalStart {
		return ""
	}

	// Check for prior productivity: was there productive work in early iterations?
	earlyHalf := m.currentIter / 2
	if earlyHalf < 1 {
		earlyHalf = 1
	}
	hadPriorProductivity := false
	for _, r := range m.iterations {
		if r.iter <= earlyHalf && r.productive > 0 {
			hadPriorProductivity = true
			break
		}
	}
	if !hadPriorProductivity {
		return ""
	}

	// Check the last N iterations for productivity collapse.
	n := len(m.iterations)
	if n < momentumStallWindow {
		return ""
	}

	stallCount := 0
	recentActivity := 0
	for i := n - 1; i >= 0 && i >= n-momentumStallWindow; i-- {
		r := m.iterations[i]
		if r.productive == 0 && r.total > 0 {
			stallCount++
		}
		recentActivity += r.total
	}

	// Need all recent iterations to be unproductive, AND there must be
	// continued tool-call activity (otherwise the agent is just done/returning).
	if stallCount < momentumStallWindow || recentActivity == 0 {
		return ""
	}

	m.fired = true
	debug.Log("momentum-loss", "late-phase productivity collapse detected at iter %d/%d (stall=%d)", m.currentIter, maxIter, stallCount)

	return fmt.Sprintf(
		"[Last-Mile Stall] You were productive in earlier iterations (edits/commands) but the last %d iterations "+
			"contain only read-only/exploratory actions. If the task is not yet complete, re-engage with concrete actions "+
			"(edits, fixes, test runs) to finish the work. If the task IS complete, provide a clear summary of what was accomplished. "+
			"Avoid drifting into exploratory loops at the end of a run.",
		stallCount,
	)
}

// isMomentumProductiveTool returns true for tools that make concrete state changes.
func isMomentumProductiveTool(name string) bool {
	return momentumProductiveTools[name]
}

// hasRecentProductiveActivity is a helper used in tests.
func hasRecentProductiveActivity(records []momentumIterRecord, windowSize int) bool {
	n := len(records)
	if n == 0 {
		return false
	}
	start := n - windowSize
	if start < 0 {
		start = 0
	}
	for i := start; i < n; i++ {
		if records[i].productive > 0 {
			return true
		}
	}
	return false
}

// formatMomentumSummary creates a human-readable summary for debugging.
func formatMomentumSummary(records []momentumIterRecord) string {
	parts := make([]string, len(records))
	for i, r := range records {
		parts[i] = fmt.Sprintf("iter%d:p=%d/c=%d", r.iter, r.productive, r.consumptive)
	}
	return strings.Join(parts, " ")
}
