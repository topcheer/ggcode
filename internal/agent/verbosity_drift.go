package agent

import (
	"fmt"
	"strings"
	"sync"
)

// verbosityDriftState detects when the agent's per-iteration context
// consumption is growing while productive output (file edits, successful
// commands) remains flat — a failure mode identified in the Agent Drift
// paper (arXiv:2601.04170) as the B_length (Output Length Stability) metric:
//
//	"Increased token usage without commensurate performance gains suggests
//	 drift manifests as verbose, circuitous reasoning — agents spinning
//	 wheels while losing strategic focus."
//
// This is distinct from:
//   - analysis paralysis (NO modify tools at all)
//   - tool diversity (category balance)
//   - convergence lock (post-verification unnecessary edits)
//   - thermal profile (cross-tool distribution)
//
// Verbosity drift specifically catches the productivity-rate-decline pattern:
// the agent IS calling tools and making edits, but its token-to-output ratio
// is degrading — each successive iteration consumes more context while
// producing fewer tangible results.
// vdWaitingTools are tools that indicate the agent is legitimately blocked
// waiting for external state (CI, background commands, user input). When
// the most recent tool call is one of these, verbosity drift should not
// fire -- zero edits during a wait is expected, not drift.
var vdWaitingTools = map[string]bool{
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

type verbosityDriftState struct {
	mu sync.Mutex

	// lastToolName tracks the most recent tool called, to detect
	// legitimate waiting/blocking scenarios.
	lastToolName string

	// Per-iteration records within a sliding window.
	records []vdRecord

	// Previous iteration's cumulative token count (to compute delta).
	prevTokens int

	// Previous iteration's cumulative file-edit count (to compute delta).
	prevEdits int

	// Sliding window configuration.
	windowSize int

	// Caps.
	maxWarnings int
	warnCount   int
	fired       bool

	// Whether prevTokens has been initialized.
	initialized bool
}

type vdRecord struct {
	tokenDelta      int // context tokens consumed this iteration
	editDelta       int // new files edited this iteration
	productiveRatio int // tokenDelta / max(1, editDelta), for trend analysis
}

func newVerbosityDriftState() *verbosityDriftState {
	return &verbosityDriftState{
		records:     make([]vdRecord, 0, 16),
		windowSize:  8,
		maxWarnings: 2,
	}
}

func (v *verbosityDriftState) reset() {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.records = v.records[:0]
	v.prevTokens = 0
	v.prevEdits = 0
	v.warnCount = 0
	v.fired = false
	v.initialized = false
}

// recordIteration captures the token and edit deltas for the current
// iteration. tokenCount is the current cumulative context token count;
// editCount is the current cumulative distinct file-edit count.
func (v *verbosityDriftState) recordIteration(tokenCount, editCount int) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	// If the agent's last tool was a waiting/blocking tool, skip recording
	// this iteration. Zero edits during a legitimate wait (CI polling, sleep,
	// background command) is expected behavior, not verbosity drift.
	if vdWaitingTools[v.lastToolName] {
		v.prevTokens = tokenCount
		v.prevEdits = editCount
		return
	}

	if !v.initialized {
		v.prevTokens = tokenCount
		v.prevEdits = editCount
		v.initialized = true
		return
	}

	tokenDelta := tokenCount - v.prevTokens
	if tokenDelta < 0 {
		tokenDelta = 0 // compaction shrank context; reset baseline
	}
	editDelta := editCount - v.prevEdits
	if editDelta < 0 {
		editDelta = 0
	}

	v.records = append(v.records, vdRecord{
		tokenDelta:      tokenDelta,
		editDelta:       editDelta,
		productiveRatio: tokenDelta / max1(editDelta),
	})
	if len(v.records) > v.windowSize {
		v.records = v.records[1:]
	}

	v.prevTokens = tokenCount
	v.prevEdits = editCount
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// maybeWarn checks for verbosity drift and returns a guidance message
// if detected. Called at iteration start with the 0-based iteration index.
func (v *verbosityDriftState) maybeWarn(iteration int) string {
	if v == nil {
		return ""
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	// Need at least windowSize records for trend analysis.
	if len(v.records) < v.windowSize {
		return ""
	}
	if v.fired || v.warnCount >= v.maxWarnings {
		return ""
	}

	// Split the window into first half and second half.
	half := len(v.records) / 2
	firstHalf := v.records[:half]
	secondHalf := v.records[half:]

	firstAvgTokens := avgTokenDelta(firstHalf)
	secondAvgTokens := avgTokenDelta(secondHalf)

	firstAvgEdits := avgEditDelta(firstHalf)
	secondAvgEdits := avgEditDelta(secondHalf)

	// Verbosity drift conditions:
	// 1. Token consumption in second half is significantly higher than first
	//    (>= 1.5x increase, indicating growing verbosity).
	// 2. Productive output (edits) is NOT keeping pace — second half edits
	//    are less than or equal to first half edits.
	// 3. Absolute token consumption is non-trivial (not just tiny iterations).
	tokenGrowthRatio := 0.0
	if firstAvgTokens > 0 {
		tokenGrowthRatio = float64(secondAvgTokens) / float64(firstAvgTokens)
	}

	if tokenGrowthRatio < 1.5 || secondAvgTokens < 2000 {
		return ""
	}
	if secondAvgEdits > firstAvgEdits && secondAvgEdits > 0 {
		return "" // productivity is increasing, not drifting
	}

	v.warnCount++
	v.fired = v.warnCount >= v.maxWarnings
	_ = iteration // tracked for future use

	var sb strings.Builder
	sb.WriteString("[verbosity-drift] Context consumption is increasing while ")
	sb.WriteString("productive output stays flat (token-to-output ratio degrading). ")
	sb.WriteString(fmt.Sprintf(
		"Recent iterations average %d tokens consumed vs %d earlier (%.1fx increase), ",
		secondAvgTokens, firstAvgTokens, tokenGrowthRatio,
	))
	sb.WriteString(fmt.Sprintf(
		"but file edits declined from %d to %d per iteration. ",
		firstAvgEdits, secondAvgEdits,
	))
	sb.WriteString("This pattern — verbose, circuitous reasoning without tangible progress — ")
	sb.WriteString("is a known agent drift failure mode. ")
	sb.WriteString("Refocus: identify the single highest-impact next action and execute it directly ")
	sb.WriteString("rather than generating more analysis or explanation. ")
	sb.WriteString("If the task is genuinely blocked, state the blocker explicitly instead of ")
	sb.WriteString("repeating exploratory steps.")

	return sb.String()
}

func avgTokenDelta(records []vdRecord) int {
	if len(records) == 0 {
		return 0
	}
	sum := 0
	for _, rec := range records {
		sum += rec.tokenDelta
	}
	return sum / len(records)
}

func avgEditDelta(records []vdRecord) int {
	if len(records) == 0 {
		return 0
	}
	sum := 0
	for _, rec := range records {
		sum += rec.editDelta
	}
	return sum / len(records)
}
