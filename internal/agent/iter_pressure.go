package agent

import (
	"fmt"
	"math"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	iterPressureThreshold = 0.75

	iterPressureVerifyDrop = 0.3

	maxIterPressureWarnings = 1
)

var editToolSet = sourceMutatingTools

var verifyToolSet = map[string]bool{
	"run_command":     true,
	"lsp_diagnostics": true,
	"code_health":     true,
	"review_changes":  true,
	"scan_todos":      true,
}

type iterPressureState struct {
	maxIter       int
	earlyEdits    int
	earlyVerifies int
	lateEdits     int
	lateVerifies  int
	warningsFired int
	currentIter   int
	recordedUpTo  int
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
		return ""
	}

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
