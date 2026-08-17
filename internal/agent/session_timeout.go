package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// ErrSessionTimeout is the sentinel error returned when an agent run is
// terminated by the session wall-clock timeout. Callers (sub-agents, ACP
// loops, cron schedulers) should use errors.Is to distinguish this terminal
// state from normal completion (nil), iteration exhaustion ("max iterations
// reached"), and context cancellation (#611).
var ErrSessionTimeout = errors.New("session wall-clock timeout exceeded")

// Session Wall-Clock Timeout
//
// Research basis: While ggcode has iteration limits (maxIter), tool call budgets,
// token/cost budgets, and pattern-based stall detection (overseer, loop_detect),
// there is no wall-clock timeout for an agent run. In autopilot mode (maxIter=0,
// unlimited iterations), a runaway agent can run indefinitely -- burning API
// credits and blocking the terminal session.
//
// Competitor analysis:
//   - Claude Code: hard per-request timeout + max_turns
//   - Cursor: subscription-based request limits (implicit time cap)
//   - OpenHands: configurable max_iterations + runtime budget (seconds)
//   - Devin: explicit session time limit on plans
//   - Cline: VS Code task timeout setting (configurable per-task)
//
// Gap: No wall-clock guardrail. A model stuck in a verification-retry loop, or
// one where each iteration succeeds but the task never converges (e.g. repeatedly
// refactoring, adding/removing code), can accumulate massive cost without
// hitting any existing limit. This is especially dangerous for cron-triggered
// autonomous runs.
//
// Implementation:
//   - Records the wall-clock start time of each RunStream call
//   - Checks elapsed time at the top of every iteration
//   - Progressive warnings at 80% and 95% of the timeout
//   - Graceful stop at 100% with a clear convergence message
//   - Configurable via session_timeout config option (0 = disabled)
//   - Default: 0 (disabled) for interactive; 30m for autopilot
//   - Non-blocking: relies on existing ctx cancellation, no goroutine needed
//   - Works alongside (not instead of) iteration/tool/token budgets

const (
	// defaultAutopilotSessionTimeout is the default wall-clock timeout for
	// autopilot/cron runs where maxIter=0 and there is no natural stopping point.
	// Set to 600 minutes (10 hours) so that complex autonomous tasks -- deep
	// research, multi-file implementations, long sub-agent chains -- are not
	// killed prematurely while still providing an ultimate backstop against
	// truly runaway processes.
	defaultAutopilotSessionTimeout = 600 * time.Minute
)

// sessionTimeoutState tracks wall-clock elapsed time for the current run.
type sessionTimeoutState struct {
	// timeout is the maximum wall-clock duration for a single run. 0 = disabled.
	timeout time.Duration

	// startTime is when RunStreamWithContent began (set at run start).
	startTime time.Time

	// warned80 and warned95 track whether the progressive warnings have fired.
	warned80 bool
	warned95 bool
}

// newSessionTimeoutState creates a new session timeout state.
func newSessionTimeoutState(timeout time.Duration) *sessionTimeoutState {
	return &sessionTimeoutState{timeout: timeout}
}

// start records the beginning of a new agent run. If the timeout is unset (0)
// but the agent is in autopilot mode, a default timeout is applied.
func (s *sessionTimeoutState) start(isAutopilot bool) {
	if s == nil {
		return
	}
	if s.timeout <= 0 && isAutopilot {
		s.timeout = defaultAutopilotSessionTimeout
	}
	if s.timeout <= 0 {
		return
	}
	s.startTime = time.Now()
	s.warned80 = false
	s.warned95 = false
}

// check returns a non-empty message if the session should stop due to wall-clock
// timeout. It also returns progressive warning messages at 80% and 95% of the
// timeout. The 80%/95% warnings are injected into LLM context so the model can
// wrap up before the deadline. The 100% stop message, however, is a user-facing
// notification only — the loop has already ended, so injecting a "summarize"
// directive would never be consumed in-run and would merely confuse the next
// session turn (same rationale as the toolCallBudget hard stop, #367/#611).
// Returns empty string if no action is needed.
func (s *sessionTimeoutState) check() string {
	if s == nil || s.timeout <= 0 || s.startTime.IsZero() {
		return ""
	}

	elapsed := time.Since(s.startTime)
	ratio := float64(elapsed) / float64(s.timeout)

	if ratio >= 1.0 {
		return fmt.Sprintf(
			"[session-timeout] Wall-clock limit of %s reached (elapsed: %s). "+
				"Stopping the agent loop to prevent unbounded resource consumption.",
			s.timeout.Round(time.Second), elapsed.Round(time.Second),
		)
	}

	if ratio >= 0.95 && !s.warned95 {
		s.warned95 = true
		remaining := s.timeout - elapsed
		return fmt.Sprintf(
			"[session-timeout] 95%% of the %s wall-clock budget consumed (%.0fs remaining). "+
				"Prioritize completing the current task — finalize changes and run verification now.",
			s.timeout.Round(time.Second), remaining.Seconds(),
		)
	}

	if ratio >= 0.80 && !s.warned80 {
		s.warned80 = true
		remaining := s.timeout - elapsed
		debug.Log("session-timeout", "80%% warning at %s elapsed, %s remaining", elapsed.Round(time.Second), remaining.Round(time.Second))
		return fmt.Sprintf(
			"[session-timeout] 80%% of the %s wall-clock budget consumed (%.0fs remaining). "+
				"Wrap up secondary work and focus on the core deliverables.",
			s.timeout.Round(time.Second), remaining.Seconds(),
		)
	}

	return ""
}

// shouldStop returns true when the wall-clock timeout has been exceeded.
// Used to break the agent loop.
func (s *sessionTimeoutState) shouldStop() bool {
	if s == nil || s.timeout <= 0 || s.startTime.IsZero() {
		return false
	}
	return time.Since(s.startTime) >= s.timeout
}

// effectiveTimeout returns the timeout to use given the mode and config.
// In autopilot mode with no explicit timeout, a sensible default is applied.
func effectiveTimeout(configured time.Duration, isAutopilot bool) time.Duration {
	if configured > 0 {
		return configured
	}
	if isAutopilot {
		return defaultAutopilotSessionTimeout
	}
	return 0
}

// EffectiveSessionTimeout is the exported wrapper for agentruntime wiring.
func EffectiveSessionTimeout(configured time.Duration, isAutopilot bool) time.Duration {
	return effectiveTimeout(configured, isAutopilot)
}

// withSessionTimeout derives a child context that is cancelled when the session
// wall-clock timeout expires. This provides a hard backstop even if the agent
// is blocked in an LLM call or a long-running tool execution.
func (a *Agent) withSessionTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if a.sessionTimeout == nil || a.sessionTimeout.timeout <= 0 {
		return ctx, func() {}
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, a.sessionTimeout.timeout)
	return timeoutCtx, cancel
}
