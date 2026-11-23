package agent

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
