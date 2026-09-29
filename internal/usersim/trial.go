package usersim

// Agent is the system under test: given a user message it returns its
// next message. The zero-return convention is a normal reply; harnesses
// that surface tool errors may embed the error text in the message so
// the simulator reacts exactly as it would to a confused agent.
type Agent func(userMsg string) string

// RunTrial simulates one full conversation: the agent sees the opening
// message first, then agent and user alternate until the goal is
// achieved or the turn budget is exhausted. The returned transcript is
// the behavioral oracle for regression-testing agent UX flows.
func RunTrial(p Persona, a Agent) (Report, []Turn) {
	s := New(p)
	if reply := s.Open(); a != nil {
		s.Reply(a(reply))
	}
	for s.status == Ongoing {
		last := s.turns[len(s.turns)-1]
		s.Reply(a(last.User))
	}
	return s.Report(), s.Transcript()
}

// AgentFactory builds one Agent per trial. Stateful agents under test
// must return a fresh instance so trials stay independent.
type AgentFactory func() Agent

// PassAtK runs k independent trials and returns the fraction achieving
// the goal — the tau-bench pass^k reliability metric, reduced to a
// deterministic harness. Deterministic agents yield 0 or 1; stochastic
// agents under test produce graded reliability.
func PassAtK(p Persona, af AgentFactory, k int) float64 {
	if k <= 0 || af == nil {
		return 0
	}
	passed := 0
	for i := 0; i < k; i++ {
		if r, _ := RunTrial(p, af()); r.Status == GoalAchieved {
			passed++
		}
	}
	return float64(passed) / float64(k)
}
