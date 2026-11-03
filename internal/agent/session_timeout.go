package agent

import (
	"errors"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

var ErrSessionTimeout = errors.New("session wall-clock timeout exceeded")

var ErrGuidanceAtBudget = errors.New("mid-run guidance arrived at iteration budget: not consumed this run, will be consumed next run")

var ErrSessionBudgetExhausted = errors.New("session budget exhausted (token/cost/delegated/time limit reached)")

const (
	defaultAutopilotSessionTimeout = 600 * time.Minute
)

type sessionTimeoutState struct {
	timeout time.Duration

	runTimeout time.Duration

	startTime time.Time

	warned80 bool
	warned95 bool
}

func newSessionTimeoutState(timeout time.Duration) *sessionTimeoutState {
	return &sessionTimeoutState{timeout: timeout}
}

func (s *sessionTimeoutState) start(isAutopilot bool) {
	if s == nil {
		return
	}
	runTimeout := effectiveTimeout(s.timeout, isAutopilot)
	s.runTimeout = runTimeout
	if runTimeout <= 0 {
		return
	}
	s.startTime = time.Now()
	s.warned80 = false
	s.warned95 = false
}

func (s *sessionTimeoutState) check() string {
	if s == nil || s.runTimeout <= 0 || s.startTime.IsZero() {
		return ""
	}

	elapsed := time.Since(s.startTime)
	ratio := float64(elapsed) / float64(s.runTimeout)

	if ratio >= 1.0 {
		return fmt.Sprintf(
			"[session-timeout] Wall-clock limit of %s reached (elapsed: %s). "+
				"Stopping the agent loop to prevent unbounded resource consumption.",
			s.runTimeout.Round(time.Second), elapsed.Round(time.Second),
		)
	}

	if ratio >= 0.95 && !s.warned95 {
		s.warned95 = true
		remaining := s.runTimeout - elapsed
		return fmt.Sprintf(
			"[session-timeout] 95%% of the %s wall-clock budget consumed (%.0fs remaining). "+
				"Prioritize completing the current task — finalize changes and run verification now.",
			s.runTimeout.Round(time.Second), remaining.Seconds(),
		)
	}

	if ratio >= 0.80 && !s.warned80 {
		s.warned80 = true
		remaining := s.runTimeout - elapsed
		debug.Log("session-timeout", "80%% warning at %s elapsed, %s remaining", elapsed.Round(time.Second), remaining.Round(time.Second))
		return fmt.Sprintf(
			"[session-timeout] 80%% of the %s wall-clock budget consumed (%.0fs remaining). "+
				"Wrap up secondary work and focus on the core deliverables.",
			s.runTimeout.Round(time.Second), remaining.Seconds(),
		)
	}

	return ""
}

func (s *sessionTimeoutState) shouldStop() bool {

	if s == nil || s.runTimeout <= 0 || s.startTime.IsZero() {
		return false
	}
	return time.Since(s.startTime) >= s.runTimeout
}

func effectiveTimeout(configured time.Duration, isAutopilot bool) time.Duration {
	if configured > 0 {
		return configured
	}
	if isAutopilot {
		return defaultAutopilotSessionTimeout
	}
	return 0
}

func EffectiveSessionTimeout(configured time.Duration, isAutopilot bool) time.Duration {
	return effectiveTimeout(configured, isAutopilot)
}
