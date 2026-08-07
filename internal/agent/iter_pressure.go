package agent

import (
	"fmt"
	"math"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Iteration Pressure Degradation Detector
//
// Research basis: Metacognitive monitoring in LLMs (arxiv 2505.13763,
// "Language Models Are Capable of Metacognitive Monitoring and Control")
// and ReMA: Learning to Meta-think for LLMs (NeurIPS 2025). These works
// establish that LLM agents exhibit degraded calibration under resource
// pressure - specifically, as they approach iteration or token limits,
// they tend to skip verification steps, rush summaries, and reduce
// exploration depth. This is a form of "resource scarcity myopia":
// the agent abandons good practices (build/test verification, multi-step
// exploration) in favor of quick closure when it senses the budget is
// running out.
//
// The gap: ggcode has many behavior detectors but NONE specifically
// track whether the agent's verification-to-edit ratio DEGRADES as it
// approaches the iteration ceiling. An agent that verifies thoroughly
// in iterations 1-10 but stops verifying in iterations 15-20 (because
// it knows it's near the limit) is exhibiting deadline-induced
// calibration failure. This is distinct from:
//   - Verification Debt (#6): tracks cumulative unverified edits, not
//     temporal degradation near the deadline
//   - Strategy Stagnation (#29): tracks same-tool retries, not phase shifts
//   - Unverified Success Claim (#13): checks text claims vs tools, not
//     behavioral ratio changes
//
// This detector uses a deterministic, zero-LLM-cost heuristic:
//
// This detector uses a deterministic, zero-LLM-cost heuristic:
// 1. Track edit-tool calls and verify-tool calls per iteration
// 2. Compare verify/edit ratio in the first half vs the last 25% of budget
// 3. If the ratio drops significantly (e.g., from 0.5 to 0.0) AND we're
//    in the last 25% of iterations, inject a calibration reminder

const (
	// iterPressureThreshold is the fraction of maxIter at which we start
	// checking for degradation (75% of budget consumed).
	iterPressureThreshold = 0.75

	// iterPressureVerifyDrop is the minimum verify/edit ratio drop
	// (early vs late) to trigger a warning. 0.3 means the late ratio
	// must be at least 0.3 lower than the early ratio.
	iterPressureVerifyDrop = 0.3

	// maxIterPressureWarnings caps the number of warnings per run.
	maxIterPressureWarnings = 1
)

// editToolSet contains tools that modify code (productive actions).
var editToolSet = map[string]bool{
	"edit_file":       true,
	"write_file":      true,
	"multi_edit_file": true,
	"multi_file_edit": true,
}

// verifyToolSet contains tools that verify code correctness.
var verifyToolSet = map[string]bool{
	"run_command":     true,
	"lsp_diagnostics": true,
	"code_health":     true,
	"review_changes":  true,
	"scan_todos":      true,
}

// iterPressureState tracks edit/verify tool calls across two windows:
// the "early" window (first 75% of iterations) and the "late" window
// (last 25%). It fires when the verify/edit ratio drops sharply.
type iterPressureState struct {
	maxIter       int
	earlyEdits    int
	earlyVerifies int
	lateEdits     int
	lateVerifies  int
	warningsFired int
	currentIter   int
	recordedUpTo  int // highest iteration we've recorded
}

func newIterPressureState(maxIter int) *iterPressureState {
	return &iterPressureState{maxIter: maxIter}
}

func (s *iterPressureState) reset(maxIter int) {
	s.maxIter = maxIter
	s.earlyEdits = 0
	s.earlyVerifies = 0
	s.lateEdits = 0
	s.lateVerifies = 0
	s.warningsFired = 0
	s.currentIter = 0
	s.recordedUpTo = 0
}

// recordToolCall classifies a tool call as edit/verify/other and
// accumulates it in the appropriate window. Called once per tool call.
func (s *iterPressureState) recordToolCall(toolName string, iter int) {
	s.currentIter = iter
	isEdit := editToolSet[toolName]
	isVerify := verifyToolSet[toolName]
	if !isEdit && !isVerify {
		return
	}

	thresholdIter := int(math.Round(float64(s.maxIter) * iterPressureThreshold))
	if iter <= thresholdIter {
		if isEdit {
			s.earlyEdits++
		}
		if isVerify {
			s.earlyVerifies++
		}
	} else {
		if isEdit {
			s.lateEdits++
		}
		if isVerify {
			s.lateVerifies++
		}
	}
	s.recordedUpTo = iter
}

// maybeWarn checks if the verify/edit ratio has degraded significantly
// in the late window compared to the early window. Returns a non-empty
// guidance message if degradation is detected.
func (a *Agent) maybeWarnIterPressure(iter int) string {
	s := a.iterPressure
	if s == nil || s.warningsFired >= maxIterPressureWarnings {
		return ""
	}
	if s.maxIter <= 0 {
		return ""
	}

	thresholdIter := int(math.Round(float64(s.maxIter) * iterPressureThreshold))
	if iter <= thresholdIter {
		return "" // not in the pressure zone yet
	}

	// Need enough data: at least 2 edits in the early window to have
	// a meaningful baseline, and at least 1 edit in the late window.
	if s.earlyEdits < 2 || s.lateEdits < 1 {
		return ""
	}

	earlyRatio := float64(s.earlyVerifies) / float64(s.earlyEdits)
	lateRatio := 0.0
	if s.lateEdits > 0 {
		lateRatio = float64(s.lateVerifies) / float64(s.lateEdits)
	}

	drop := earlyRatio - lateRatio
	if drop < iterPressureVerifyDrop {
		return ""
	}

	// Significant degradation detected.
	s.warningsFired++
	debug.Log("iter-pressure",
		"verification degradation: early ratio=%.2f (%d verify / %d edit), late ratio=%.2f (%d verify / %d edit), drop=%.2f",
		earlyRatio, s.earlyVerifies, s.earlyEdits,
		lateRatio, s.lateVerifies, s.lateEdits, drop)

	earlyPct := int(iterPressureThreshold * 100)
	return fmt.Sprintf(
		"[Iteration Pressure] Verification rate dropped significantly in recent iterations "+
			"(early verify/edit ratio: %.1f, recent: %.1f). You are past %d%% of the iteration budget. "+
			"Do not skip verification just because you are approaching the iteration limit - "+
			"unverified edits risk silent regressions. Run build/test before completing.",
		earlyRatio, lateRatio, earlyPct)
}

// classifyToolForPressure returns "edit", "verify", or "other" for logging.
func classifyToolForPressure(name string) string {
	name = strings.ToLower(name)
	if editToolSet[name] {
		return "edit"
	}
	if verifyToolSet[name] {
		return "verify"
	}
	return "other"
}
