package agent

import (
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

type verifyDebtState struct {
	mu                sync.Mutex
	editsSinceGreen   int
	totalEdits        int
	totalGreenBuilds  int
	lastGreenBuildCmd string
	warningsIssued    int
}

const (
	verifyDebtWarn1 = 7
	verifyDebtWarn2 = 12
	verifyDebtMax   = 1
)

func newVerifyDebtState() *verifyDebtState {
	return &verifyDebtState{}
}

func (s *verifyDebtState) recordSourceEdit() {
	s.mu.Lock()
	s.editsSinceGreen++
	s.totalEdits++
	s.mu.Unlock()
}

func (s *verifyDebtState) recordVerifyCommand(cmd string, failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !failed {
		debug.Log("agent", "verify debt cleared: green build after %d unverified edits (cmd=%q)",
			s.editsSinceGreen, cmd)
		s.editsSinceGreen = 0
		s.totalGreenBuilds++
		s.lastGreenBuildCmd = cmd
	}

}

func (s *verifyDebtState) maybeWarn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warningsIssued >= verifyDebtMax {
		return ""
	}

	debt := s.editsSinceGreen
	if debt < verifyDebtWarn1 {
		return ""
	}

	s.warningsIssued++

	var msg string
	if debt >= verifyDebtWarn2 {

		prob := compoundSuccessProb(0.95, debt)
		msg = fmt.Sprintf(
			"[Verification debt: %d source edits since the last *successful* build. "+
				"At typical per-step accuracy, the probability all %d changes are correct is ~%.0f%%. "+
				"Run a full build+test now to establish a verified baseline before making further changes.]",
			debt, debt, prob*100,
		)
	} else {
		msg = fmt.Sprintf(
			"[Verification debt: %d source edits accumulated since the last successful build. "+
				"Run a build+test to verify accumulated changes before proceeding further.]",
			debt,
		)
	}

	debug.Log("agent", "verify debt warning #%d: %d edits since green build (iter=%d)",
		s.warningsIssued, debt, iteration)

	return msg
}

func (s *verifyDebtState) reset() {
	s.mu.Lock()
	s.editsSinceGreen = 0
	s.totalEdits = 0
	s.totalGreenBuilds = 0
	s.lastGreenBuildCmd = ""
	s.warningsIssued = 0
	s.mu.Unlock()
}

func compoundSuccessProb(p float64, n int) float64 {
	result := 1.0
	for i := 0; i < n; i++ {
		result *= p
	}
	return result
}
