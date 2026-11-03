package agent

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	stalledMinSamples = 2

	maxStalledWarnings = 2

	stalledDecelRatio = 0.3
)

func (a *Agent) stalledConvergenceCheckCommand(toolName string, args []byte, content string, isError bool) string {
	if a.stalledConvergence == nil {
		return ""
	}
	if toolName != "run_command" {
		return ""
	}
	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isVerifyCommand(cmd) {
		return ""
	}
	return a.stalledConvergence.recordVerify(content, isError)
}

type stalledConvergenceState struct {
	errorHistory []int

	hadEdits bool

	warningCount int
}

func newStalledConvergenceState() *stalledConvergenceState {
	return &stalledConvergenceState{}
}

func (s *stalledConvergenceState) reset() {
	s.errorHistory = nil
	s.hadEdits = false
	s.warningCount = 0
}

func (s *stalledConvergenceState) recordEdit() {
	s.hadEdits = true
}

func (s *stalledConvergenceState) recordVerify(content string, failed bool) string {
	currentErrors := countVerifyErrors(content)

	if !failed {
		s.errorHistory = nil
		s.hadEdits = false
		return ""
	}

	if !s.hadEdits {
		s.errorHistory = append(s.errorHistory, currentErrors)
		s.hadEdits = false
		return ""
	}

	s.errorHistory = append(s.errorHistory, currentErrors)
	s.hadEdits = false

	if len(s.errorHistory) < stalledMinSamples+1 {
		return ""
	}

	if len(s.errorHistory) > 10 {
		s.errorHistory = s.errorHistory[len(s.errorHistory)-10:]
	}

	if s.warningCount >= maxStalledWarnings {
		return ""
	}

	if isStalledConvergence(s.errorHistory) {
		s.warningCount++
		debug.Log("stalled-convergence",
			"error history %v shows stalled convergence -- agent may be patching symptoms",
			s.errorHistory)
		return stalledGuidance(s.errorHistory)
	}

	return ""
}

func isStalledConvergence(history []int) bool {
	n := len(history)
	if n < stalledMinSamples+1 {
		return false
	}

	deltas := make([]int, 0, n-1)
	for i := 1; i < n; i++ {
		deltas = append(deltas, history[i]-history[i-1])
	}

	for _, d := range deltas {
		if d > 0 {
			return false
		}
	}

	peakImprovement := 0
	for _, d := range deltas {
		if d < peakImprovement {
			peakImprovement = d
		}
	}

	if peakImprovement > -1 {
		return false
	}

	lastDelta := deltas[len(deltas)-1]
	peakAbs := -peakImprovement
	if peakAbs == 0 {
		return false
	}

	lastRatio := float64(absInt(lastDelta)) / float64(peakAbs)

	if lastRatio >= stalledDecelRatio {
		return false
	}

	if len(deltas) >= 3 {
		prevDelta := deltas[len(deltas)-2]
		prevRatio := float64(absInt(prevDelta)) / float64(peakAbs)
		if prevRatio >= stalledDecelRatio {
			return false
		}
	}

	return true
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func stalledGuidance(history []int) string {
	last := history[len(history)-1]
	histStr := make([]string, len(history))
	for i, v := range history {
		histStr[i] = strconv.Itoa(v)
	}
	return "[STALLED CONVERGENCE] " +
		"Error count trend: " + strings.Join(histStr, " -> ") +
		" -- improvements are decelerating. You are likely patching surface " +
		"symptoms rather than fixing the root cause. " +
		fmt.Sprintf("%d errors remain after multiple fix attempts with diminishing returns.\n\n", last) +
		"Recommended actions:\n" +
		"1. STOP making incremental fixes to individual errors.\n" +
		"2. Re-read the FULL error output and look for a COMMON root cause.\n" +
		"   (e.g., a missing import, wrong type signature, API change)\n" +
		"3. Fix the root cause -- multiple errors may disappear at once.\n" +
		"4. If errors are genuinely independent, consider reverting recent\n" +
		"   changes and taking a different approach entirely."
}
