//go:build goolm

package agent

import (
	"strings"
	"testing"
)

func TestIssue4515_RecordIterationFeedsRealToolActivity(t *testing.T) {
	b, err := readAgentAndProcessingSources()
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	recIdx := strings.Index(s, "a.trajectoryHealth.recordIteration(")
	branchIdx := strings.Index(s, "if len(toolCalls) == 0 {")
	if recIdx < 0 || branchIdx < 0 {
		t.Fatal("agent.go structure changed: expected record site and empty-branch marker")
	}
	if recIdx > branchIdx {
		t.Fatal("#4515 regression: recordIteration is back inside the empty-toolCalls branch")
	}

	st := newTrajectoryHealthState()
	for i := 0; i < 4; i++ {
		st.recordIteration(0, 0, 2, 2, 0)
	}
	dims, score := st.assess()
	found := false
	for _, d := range dims {
		if d.name == "explore-heavy" {
			found = true
		}
	}
	if !found || score == 0 {
		t.Fatalf("explore-heavy samples produced no explore dimension / zero score: dims=%v score=%d", dims, score)
	}
}

func TestIssue4516_SubagentInjectionDoesNotFlagBudget(t *testing.T) {
	b, err := readAgentAndProcessingSources()
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	idx := strings.Index(s, "injectSubagentCompletions()")
	if idx < 0 {
		t.Fatal("injectSubagentCompletions site not found")
	}
	end := idx + 800
	if end > len(s) {
		end = len(s)
	}
	window := s[idx:end]
	if strings.Contains(window, "guidanceAtBudget = true") {
		t.Fatal("#4516 regression: subagent injection sets guidanceAtBudget again")
	}

	sets := strings.Count(s, "guidanceAtBudget = true")
	if sets < 2 {
		t.Fatalf("guidance/interruption paths lost their flag sets (found %d)", sets)
	}
}
