package agent

import "testing"

func TestIssue2647_LegitReasoningNotFlagged(t *testing.T) {
	probes := []string{
		"In order to fix the race condition, we should handle the mutex ordering carefully.",
		"To resolve the build error, we need to address the missing import.",
		"The reason for this check is to detect nil pointers early.",
		"We need more context because we require the config file to parse the endpoints.",
		"Since authentication is required, the middleware must be implemented first.",
	}
	for _, p := range probes {
		if got := scanCircularReasoning(p); len(got) != 0 {
			t.Errorf("legit reasoning flagged: %q -> %v", p, got)
		}
	}
}

func TestIssue2647_TrueTautologiesStillFlagged(t *testing.T) {
	probes := []string{
		"To fix the build error, we should fix the build error.",
		"We need more context because we need more context.",
		"Since auth gating is required, auth gating must be implemented.",
	}
	for _, p := range probes {
		if got := scanCircularReasoning(p); len(got) == 0 {
			t.Errorf("true tautology not flagged: %q", p)
		}
	}
}
