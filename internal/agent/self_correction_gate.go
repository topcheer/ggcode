package agent

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	scGateMinRounds = 3

	scGateStabilityRatio = 1.2
)

type selfCorrectionGateState struct {
	totalRounds int

	totalNewErrors int

	totalResolvedErrors int

	totalPersistent int

	fired bool
}

func newSelfCorrectionGateState() *selfCorrectionGateState {
	return &selfCorrectionGateState{}
}

func (s *selfCorrectionGateState) reset() {
	if s == nil {
		return
	}
	s.totalRounds = 0
	s.totalNewErrors = 0
	s.totalResolvedErrors = 0
	s.totalPersistent = 0
	s.fired = false
}

func (s *selfCorrectionGateState) recordRound(newErrors, persistentErrors, resolvedErrors int) string {
	if s == nil || s.fired {

		if s != nil {
			s.totalRounds++
			s.totalNewErrors += newErrors
			s.totalResolvedErrors += resolvedErrors
			s.totalPersistent += persistentErrors
		}
		return ""
	}

	s.totalRounds++
	s.totalNewErrors += newErrors
	s.totalResolvedErrors += resolvedErrors
	s.totalPersistent += persistentErrors

	if s.totalRounds < scGateMinRounds {
		return ""
	}

	if s.totalNewErrors == 0 {
		return ""
	}

	eir := float64(s.totalNewErrors) / float64(s.totalRounds)
	ecr := float64(s.totalResolvedErrors) / float64(s.totalRounds)

	if eir <= 0 {
		return ""
	}

	ratio := ecr / eir

	debug.Log("self-correction-gate", "rounds=%d new=%d resolved=%d persistent=%d EIR=%.1f ECR=%.1f ratio=%.2f threshold=%.2f",
		s.totalRounds, s.totalNewErrors, s.totalResolvedErrors, s.totalPersistent, eir, ecr, ratio, scGateStabilityRatio)

	if ratio < scGateStabilityRatio {
		s.fired = true
		return selfCorrectionUnstableGuidance(s.totalRounds, s.totalNewErrors, s.totalResolvedErrors, ratio)
	}

	return ""
}

func selfCorrectionUnstableGuidance(rounds, newErrors, resolvedErrors int, ratio float64) string {
	return fmt.Sprintf(
		"[self-correction-unstable] %d cycles: %d newly-visible errors vs %d resolved (ratio %.1f < %.1f). Error counts keep growing across fix rounds - some may be newly introduced by recent fixes, others merely exposed now that earlier errors stopped masking them. Step back and re-verify the plan before writing another fix.",
		rounds, newErrors, resolvedErrors, ratio, scGateStabilityRatio,
	)
}

func (a *Agent) selfCorrectionGateRecordRound(newErrors, persistentErrors, resolvedErrors int) string {
	if a.selfCorrectionGate == nil {
		return ""
	}
	return a.selfCorrectionGate.recordRound(newErrors, persistentErrors, resolvedErrors)
}

func (a *Agent) resetSelfCorrectionGate() {
	if a.selfCorrectionGate != nil {
		a.selfCorrectionGate.reset()
	}
}
