package agent

import (
	"fmt"
	"strings"
)

type correctionSpiralState struct {
	errorSequence    []int
	lastWarnedSeqLen int

	totalCorrections int
	lastWarnedTotal  int

	warningCount int

	lastWarnedAt int

	lastEditIteration int

	pendingEdit bool
}

func newCorrectionSpiralState() *correctionSpiralState {
	return &correctionSpiralState{
		errorSequence: make([]int, 0, 16),
	}
}

func (s *correctionSpiralState) reset() {
	s.errorSequence = s.errorSequence[:0]
	s.totalCorrections = 0
	s.warningCount = 0
	s.lastWarnedAt = 0
	s.lastWarnedSeqLen = 0
	s.lastEditIteration = 0
	s.pendingEdit = false
}

const (
	sevNone    = 0
	sevLint    = 1
	sevParse   = 2
	sevCompile = 3
	sevRuntime = 4
	sevTest    = 5
	sevCrash   = 6
)

func (s *correctionSpiralState) recordEdit(iteration int) {
	s.pendingEdit = true
	s.lastEditIteration = iteration
}

func (s *correctionSpiralState) recordVerifyResult(_ string, output string, isError bool, _ int) {
	if !s.pendingEdit || !isError {
		if !isError {

			s.pendingEdit = false
			s.errorSequence = s.errorSequence[:0]

			s.lastWarnedSeqLen = 0
			s.lastWarnedTotal = 0
		}
		return
	}

	severity := csClassifySeverity(output)

	s.errorSequence = append(s.errorSequence, severity)
	s.totalCorrections++

	if len(s.errorSequence) > 12 {
		s.errorSequence = s.errorSequence[len(s.errorSequence)-12:]
	}
	s.pendingEdit = false
}

func (s *correctionSpiralState) maybeWarn(iteration int) string {

	if len(s.errorSequence) < 3 {
		return ""
	}

	if s.warningCount >= 2 {
		return ""
	}

	if s.warningCount > 0 && iteration-s.lastWarnedAt < 5 {
		return ""
	}

	if s.warningCount > 0 && len(s.errorSequence) <= s.lastWarnedSeqLen && s.totalCorrections <= s.lastWarnedTotal {
		return ""
	}

	seq := s.errorSequence

	escalations := 0
	plateaus := 0
	regressions := 0
	for i := 1; i < len(seq); i++ {
		switch {
		case seq[i] > seq[i-1]:
			escalations++
		case seq[i] == seq[i-1]:
			plateaus++
		default:
			regressions++
		}
	}

	if escalations < 2 || regressions > 1 {
		return ""
	}

	if seq[len(seq)-1] < seq[0] {
		return ""
	}

	s.warningCount++
	s.lastWarnedAt = iteration
	s.lastWarnedSeqLen = len(s.errorSequence)
	s.lastWarnedTotal = s.totalCorrections

	labels := make([]string, len(seq))
	for i, v := range seq {
		labels[i] = csSeverityLabel(v)
	}
	trajectory := strings.Join(labels, " → ")

	var action string
	if s.warningCount == 1 {
		action = "Your corrections are making the problem progressively worse. " +
			"STOP incremental fixes. Step back, read the full error context, " +
			"and address the root cause rather than symptoms."
	} else {
		action = "CRITICAL: correction spiral persists. Your fix attempts keep " +
			"introducing higher-severity errors. Strongly recommend reverting to " +
			"the last known-good state (git checkout / undo) and re-approaching " +
			"the problem from scratch with a different strategy."
	}

	return fmt.Sprintf(
		"[correction-spiral] Error severity escalating across %d correction attempts "+
			"(%d escalations, %d regressions). Severity trajectory: %s. "+
			"Feedback control theory shows that unstable correction loops degrade "+
			"monotonically without intervention. %s",
		len(seq), escalations, regressions, trajectory, action,
	)
}

func csClassifySeverity(output string) int {
	low := strings.ToLower(output)

	if strings.Contains(low, "fail") && (strings.Contains(low, "test") ||
		strings.Contains(low, "assert") || strings.Contains(low, "expect")) {
		return sevTest
	}

	if strings.Contains(low, "segfault") ||
		strings.Contains(low, "received signal") || strings.Contains(low, "signal:") ||
		strings.Contains(low, "core dumped") || strings.Contains(low, "fatal error") ||
		strings.Contains(low, "goroutine") && strings.Contains(low, "panic") {
		return sevCrash
	}

	if strings.Contains(low, "panic:") || strings.Contains(low, "nil pointer") ||
		strings.Contains(low, "index out of range") || strings.Contains(low, "runtime error") ||
		strings.Contains(low, "deadlock") || strings.Contains(low, "timeout") {
		return sevRuntime
	}

	if strings.Contains(low, "syntax error") || strings.Contains(low, "parse error") ||
		strings.Contains(low, "unexpected token") || strings.Contains(low, "expected") {
		return sevParse
	}

	if strings.Contains(low, "compile error") || strings.Contains(low, "cannot find symbol") ||
		strings.Contains(low, "undefined:") || strings.Contains(low, "type error") ||
		strings.Contains(low, "build failed") {
		return sevCompile
	}

	if strings.Contains(low, "lint") || strings.Contains(low, "warning:") ||
		strings.Contains(low, "deprecated") || strings.Contains(low, "format") {
		return sevLint
	}

	return sevCompile
}

func csSeverityLabel(level int) string {
	switch level {
	case sevCrash:
		return "crash"
	case sevTest:
		return "test-fail"
	case sevRuntime:
		return "runtime-err"
	case sevCompile:
		return "compile-err"
	case sevParse:
		return "syntax-err"
	case sevLint:
		return "lint"
	default:
		return "unknown"
	}
}

func csIsEditTool(toolName string) bool {
	return sourceMutatingTools[toolName]
}
