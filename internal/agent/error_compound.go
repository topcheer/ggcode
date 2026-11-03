package agent

import (
	"fmt"
	"strings"
	"sync"
)

const ecWindow = 12

const ecWarnDensity = 0.30

const (
	ecCatastrophicDensity  = 0.75
	ecCatastrophicMinSteps = 3
)

type ecStep struct {
	hasError    bool
	verifyFails int
	editFails   int
	toolErrors  int
}

type errorCompoundState struct {
	mu sync.Mutex

	window       []ecStep
	totalSteps   int
	verifyFails  int
	editFails    int
	toolErrors   int
	warningCount int
	lastWarnedAt int

	pendingVerify int
	pendingEdit   int
	pendingTool   int

	newErrSinceWarn int

	canRevertWarn    bool
	prevLastWarnedAt int
	prevNewErrSince  int
}

func newErrorCompoundState() *errorCompoundState {
	return &errorCompoundState{}
}

func (s *errorCompoundState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.window = nil
	s.totalSteps = 0
	s.verifyFails = 0
	s.editFails = 0
	s.toolErrors = 0
	s.warningCount = 0
	s.lastWarnedAt = 0
	s.pendingVerify = 0
	s.pendingEdit = 0
	s.pendingTool = 0
	s.newErrSinceWarn = 0
}

func (s *errorCompoundState) recordStep(hasError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	step := ecStep{
		hasError:    hasError,
		verifyFails: s.pendingVerify,
		editFails:   s.pendingEdit,
		toolErrors:  s.pendingTool,
	}
	s.pendingVerify, s.pendingEdit, s.pendingTool = 0, 0, 0

	s.totalSteps++
	if hasError {
		s.newErrSinceWarn++
	}
	s.window = append(s.window, step)
	if len(s.window) > ecWindow {
		s.window = s.window[len(s.window)-ecWindow:]
	}
}

func (s *errorCompoundState) recordResult(toolName string, isError bool, iteration int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	hadError := false

	if isError {
		hadError = true
		if ecIsVerifyCmd(toolName) {
			s.verifyFails++
			s.pendingVerify++
		} else if ecIsEditTool(toolName) {
			s.editFails++
			s.pendingEdit++
		} else {
			s.toolErrors++
			s.pendingTool++
		}
	}

	return hadError
}

func (s *errorCompoundState) windowStats() (steps, errs, verifyFails, editFails, toolErrors int) {
	for _, st := range s.window {
		steps++
		if st.hasError {
			errs++
		}
		verifyFails += st.verifyFails
		editFails += st.editFails
		toolErrors += st.toolErrors
	}
	return
}

func (s *errorCompoundState) maybeWarn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	catastrophic := s.totalSteps >= ecCatastrophicMinSteps &&
		float64(s.errorStepsInWindowLocked()) >= ecCatastrophicDensity*float64(len(s.window))
	if !catastrophic && s.totalSteps < 8 {
		return ""
	}

	if s.warningCount >= 2 {
		return ""
	}

	if s.warningCount > 0 && iteration-s.lastWarnedAt < 5 {
		return ""
	}

	wSteps, wErrs, wVerify, wEdit, wTool := s.windowStats()

	density := 0.0
	if wSteps > 0 {
		density = float64(wErrs) / float64(wSteps)
	}

	if density <= ecWarnDensity {
		return ""
	}

	if s.newErrSinceWarn == 0 {
		return ""
	}

	s.canRevertWarn = true
	s.prevLastWarnedAt = s.lastWarnedAt
	s.prevNewErrSince = s.newErrSinceWarn
	s.warningCount++
	s.lastWarnedAt = iteration
	s.newErrSinceWarn = 0

	return s.formatWarning(wSteps, wErrs, density, wVerify, wEdit, wTool)
}

func (s *errorCompoundState) markUndelivered() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.canRevertWarn || s.warningCount == 0 {
		return
	}
	s.canRevertWarn = false
	s.warningCount--
	s.lastWarnedAt = s.prevLastWarnedAt
	s.newErrSinceWarn = s.prevNewErrSince
}

func (s *errorCompoundState) formatWarning(wSteps, wErrs int, density float64, wVerify, wEdit, wTool int) string {
	var severity, action string
	if s.warningCount == 1 {
		severity = "moderate"
		action = "Consider pausing to run a holistic verification pass (build + test + lint) before continuing."
	} else {
		severity = "critical"
		action = "Strongly recommend stopping incremental edits and running full verification. The trajectory may be building on compounding errors."
	}

	return fmt.Sprintf(`[error-compounding] %s risk detected: %d errors in last %d steps `+
		`(window density=%.0f%%). Error breakdown (window): %s. `+
		`Per-step errors compound -- a sustained high error rate erodes overall `+
		`trajectory reliability. %s`,
		severity, wErrs, wSteps, density*100,
		ecWindowBreakdown(wVerify, wEdit, wTool), action,
	)
}

func ecWindowBreakdown(wVerify, wEdit, wTool int) string {
	var parts []string
	if wVerify > 0 {
		parts = append(parts, fmt.Sprintf("%d verify failures", wVerify))
	}
	if wEdit > 0 {
		parts = append(parts, fmt.Sprintf("%d edit failures", wEdit))
	}
	if wTool > 0 {
		parts = append(parts, fmt.Sprintf("%d tool errors", wTool))
	}
	if s := strings.Join(parts, ", "); s != "" {
		return s
	}
	return "none classified (raw step errors only)"
}

func (s *errorCompoundState) errorStepsInWindowLocked() int {
	n := 0
	for _, st := range s.window {
		if st.hasError {
			n++
		}
	}
	return n
}

func ecIsVerifyCmd(toolName string) bool {
	switch toolName {
	case "run_command", "start_command":
		return true
	default:
		return false
	}
}

func ecIsEditTool(toolName string) bool {
	return sourceMutatingTools[toolName]
}
