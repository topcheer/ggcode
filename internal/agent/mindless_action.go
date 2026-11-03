package agent

import "strconv"

const (
	mindlessTextThreshold = 50

	mindlessStreakThreshold = 4

	mindlessMaxWarnings = 2
)

type mindlessActionState struct {
	streak   int
	warnings int
}

func newMindlessActionState() *mindlessActionState {
	return &mindlessActionState{}
}

func (s *mindlessActionState) reset() {
	s.streak = 0
	s.warnings = 0
}

func (s *mindlessActionState) recordStep(reasoningLen int, hasToolCall bool) bool {
	if !hasToolCall {

		s.streak = 0
		return false
	}

	if reasoningLen < mindlessTextThreshold {
		s.streak++
	} else {

		s.streak = 0
	}

	if s.streak >= mindlessStreakThreshold && s.warnings < mindlessMaxWarnings {
		s.warnings++
		return true
	}

	return false
}

func mindlessActionWarning(streak int) string {
	return "[INFO-mindless-action] You have fired " + strconv.Itoa(streak) +
		" consecutive tool calls with minimal reasoning between them. " +
		"You may be operating in autopilot mode without adequately " +
		"reflecting on results. Pause and review the output of your " +
		"recent tool calls before proceeding. " +
		"Consider: Did the last action succeed? Are you acting on " +
		"verified information? Is each step moving toward the goal?"
}
