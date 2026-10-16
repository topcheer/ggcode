package agent

import "testing"

const zz4255Plan = `Plan:
1. Fix auth middleware
2. Update test suite
3. Add API documentation
4. Update changelog`

// #4255: record side must store 1-based iters (maybeWarn side is 1-based).
func TestIssue4255_RecordRadixNormalized(t *testing.T) {
	s := newSubgoalState()
	s.recordAssistantText(zz4255Plan, 0) // 0-based call site
	if len(s.subgoals) < 3 {
		t.Fatalf("precondition: plan not captured (%d subgoals)", len(s.subgoals))
	}
	if s.planIter != 1 {
		t.Fatalf("planIter must be normalized to 1-based (expected 1, got %d)", s.planIter)
	}
}

// #4255: ratio == threshold (exactly 0.4) must NOT fire - the contract is
// ">40%", strict comparison.
func TestIssue4255_ThresholdStrict(t *testing.T) {
	s := newSubgoalState()
	s.subgoals = []subgoalEntry{{number: 1, text: "a", keywords: []string{"auth"}}, {number: 2, text: "b", keywords: []string{"test"}}, {number: 3, text: "c", keywords: []string{"api"}}, {number: 4, text: "d", keywords: []string{"changelog"}}, {number: 5, text: "e", keywords: []string{"deploy"}}}
	s.planIter = 1
	s.subgoals[0].addressed = true
	s.subgoals[1].addressed = true
	s.subgoals[2].addressed = true // 3/5 -> 0.4 exactly
	if w := s.maybeWarn(sgMinIterGap + 2); w != "" {
		t.Fatalf("ratio exactly at threshold must not fire (contract >40%%), got %q", w)
	}
	s.subgoals[2].addressed = false // 4/5 -> 0.8 > 0.4
	if w := s.maybeWarn(sgMinIterGap + 3); w == "" {
		t.Fatal("ratio above threshold must fire")
	}
}
