//go:build goolm

package agent

import (
	"os"
	"strings"
	"testing"
)

// #4515 pin: the trajectoryHealth.recordIteration call must run on EVERY
// iteration, BEFORE the len(toolCalls)==0 branch - it used to sit inside
// that branch where toolCalls is always empty, so the composite health
// score was built from zero-activity samples (every dimension dead).
func TestIssue4515_RecordIterationFeedsRealToolActivity(t *testing.T) {
	b, err := os.ReadFile("agent.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	recIdx := strings.Index(s, "a.trajectoryHealth.recordIteration(")
	branchIdx := strings.Index(s, "// No tool calls → done unless autopilot should continue")
	if recIdx < 0 || branchIdx < 0 {
		t.Fatal("agent.go structure changed: expected record site and empty-branch marker")
	}
	if recIdx > branchIdx {
		t.Fatal("#4515 regression: recordIteration is back inside the empty-toolCalls branch")
	}

	// State-level sanity: explore-heavy iterations (the inputs the old
	// placement never delivered) must produce a non-trivial assessment -
	// read-only tool dominance fires the explore-heavy dimension
	// (iterations>=3 && totalTools>=6 && readRatio>0.7).
	st := newTrajectoryHealthState()
	for i := 0; i < 4; i++ {
		st.recordIteration(0, 0, 2, 2, 0) // 2 tools, both read-only
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

// #4516 pin: the subagent-completion injection site must NOT set
// guidanceAtBudget - the notice is consumed by the SAME iteration (no
// continue), so the "no budget left to act on it" claim was false and
// errors.Is(ErrGuidanceAtBudget) callers misjudged the run.
func TestIssue4516_SubagentInjectionDoesNotFlagBudget(t *testing.T) {
	b, err := os.ReadFile("agent.go")
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
	// The flag must still exist for the legitimate guidance/interruption
	// paths (they DO continue past the injection).
	sets := strings.Count(s, "guidanceAtBudget = true")
	if sets < 2 {
		t.Fatalf("guidance/interruption paths lost their flag sets (found %d)", sets)
	}
}
