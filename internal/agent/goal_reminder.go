package agent

// Goal Fade-Out Reminder (event-driven system reminders).
//
// Research basis:
//   - OpenDev, "Building Effective AI Coding Agents for the Terminal"
//     (arXiv:2603.05344): instruction compliance degrades over long
//     conversations ("instruction fade-out") even though the instructions
//     remain in the context window. Re-injecting short reminder messages
//     at a fixed cadence recovered compliance; in the reference experiment
//     the first violation moved from turn 7 to turn 19.
//   - ggcode already re-anchors USER constraints mid-run
//     (constraint_amnesia.go: regex-extracted, one warning per run) but the
//     agent's own STANDING instruction - the active autopilot goal - is
//     injected into the system prompt exactly once at Run() start
//     (maybeInjectDynamicSystemPrompt, layer 1.5) and never re-anchored.
//     Autopilot runs reach 50-200 iterations, which is precisely the regime
//     where the OpenDev data shows fade-out.
//
// This is NOT a detector: there is no behavioral pattern to detect and no
// symptom to react to. It is an open-loop, pure turn-cadence re-anchor -
// deliberately distinct from the reactive detector fleet and from the r80
// ratchet family (which governs system-prompt CONTENT selection at task
// start, not mid-run timing).
//
// Design:
//   - fires only when an autopilot goal is active (zero noise otherwise)
//   - first reminder no earlier than goalReminderFirstIter iterations in
//     (the head of a run needs no re-anchoring)
//   - then at least goalReminderEveryIter iterations apart
//   - at most goalReminderMaxPerRun per run (~60 tokens each; bounded cost)
//   - routed through injectGuidance so the #677 per-turn guidance budget and
//     the #1206 shared byte pool govern delivery; when the budget suppresses
//     the reminder the cadence slot is refunded (markUndelivered) so the
//     reminder retries on the next iteration instead of being lost (#681
//     "returned != delivered" discipline)
//   - compaction refunds the quota (guidance_compact_reset.go): after
//     compaction the goal is re-emitted in the system prompt and the
//     fade-out clock effectively restarts

import "fmt"

const (
	// goalReminderFirstIter: no reminder before this iteration (1-based).
	goalReminderFirstIter = 12

	// goalReminderEveryIter: minimum cadence between reminders, in
	// iterations. The OpenDev reference re-injected every 3 QA turns; a
	// coding-agent iteration is far denser, so the cadence is stretched
	// accordingly.
	goalReminderEveryIter = 15

	// goalReminderMaxPerRun: hard per-run cap to bound context growth.
	goalReminderMaxPerRun = 5
)

// goalReminderState tracks reminder cadence across the agent loop.
// No mutex: single-goroutine agent-loop access, same as constraintAmnesiaState.
type goalReminderState struct {
	lastFiredIter int // iteration of the last delivered-or-claimed reminder
	delivered     int // reminders charged against the per-run quota
}

func newGoalReminderState() *goalReminderState {
	return &goalReminderState{}
}

// reset clears cadence state. Called at run start (new user turn) and on
// mid-run compaction.
func (s *goalReminderState) reset() {
	if s == nil {
		return
	}
	s.lastFiredIter = 0
	s.delivered = 0
}

// maybeRemind returns the reminder text when the cadence fires, "" otherwise.
// goal is the active standing goal; an empty goal disables the mechanism
// entirely (non-autopilot runs stay untouched).
func (s *goalReminderState) maybeRemind(iteration int, goal string) string {
	if s == nil || goal == "" {
		return ""
	}
	if s.delivered >= goalReminderMaxPerRun {
		return ""
	}
	if iteration < goalReminderFirstIter {
		return ""
	}
	due := s.lastFiredIter == 0 ||
		iteration-s.lastFiredIter >= goalReminderEveryIter
	if !due {
		return ""
	}
	s.lastFiredIter = iteration
	s.delivered++
	return fmt.Sprintf(
		"[goal-reminder] %d iterations in: re-anchor on the active goal "+
			"before it fades from attention. Active goal:\n  %s\n"+
			"Keep every remaining step directly serving this goal, and verify "+
			"the changes before declaring the goal reached.",
		iteration, goal)
}

// markUndelivered refunds the cadence slot consumed by maybeRemind when
// injectGuidance suppressed the message (budget-saturated turn). The reminder
// is not lost: the next iteration becomes due again.
func (s *goalReminderState) markUndelivered(iteration int) {
	if s == nil {
		return
	}
	if s.lastFiredIter == iteration {
		s.lastFiredIter = 0
	}
	if s.delivered > 0 {
		s.delivered--
	}
}
