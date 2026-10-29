package agent

import (
	"strings"
	"testing"
)

// #4182: a FAILED mutation has no side effects and must not advance
// lastMutationIter - otherwise failed edits manufacture a "mutatedAfter"
// that fires the stale-trap with a false "file changes have happened since"
// assertion.
func TestReverifyFailedMutationDoesNotAdvance(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"foo"}`, "No matches found", false, 2)
	// Six failed edits (old_text mismatch): zero file changes.
	for i := 3; i <= 8; i++ {
		s.recordToolResult("edit_file", `{"path":"a.go"}`, "old_text not found", true, i)
	}
	// Aged 7>=6 but NOTHING mutated after the observation: must NOT fire.
	if msg := s.checkStaleTrap("The fix is complete, everything works now.", 9); msg != "" {
		t.Fatalf("failed mutations must not stale the observation, got: %s", msg)
	}
}

// Control: a SUCCESSFUL mutation still advances the tracker (old semantics).
func TestReverifySuccessfulMutationStillAdvances(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"foo"}`, "No matches found", false, 2)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 3)
	msg := s.checkStaleTrap("The fix is complete, everything works now.", 9)
	if msg == "" {
		t.Fatal("successful mutation must still stale the observation")
	}
}

// #4181: "还是错" embedded in the neutral compound "还是错误处理" is a
// technical recommendation, not a rejection of the agent's output. The
// carrier masks only the embedded occurrence.
func TestIssue4181_HaishiCuowuCarrier(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool // whether rejection should fire
	}{
		{"neutral recommendation, short", "还是错误处理更简单，直接 try-catch 吧", false},
		{"neutral error-code compound", "看下还是错误码没变，日志里有线索", false},
		{"genuine bare 还是错 still fires", "还是错，你改的地方不对", true},
	}
	for _, tc := range cases {
		got := detectNegativeFeedback(tc.msg)
		if (got != "") != tc.want {
			t.Errorf("%s: detectNegativeFeedback(%q) = %q, want fired=%v", tc.name, tc.msg, got, tc.want)
		}
	}
}

// The carrier must not blank OTHER negatives on the same line (#4149
// carrier contract: mask only the embedded trigger).
func TestIssue4181_CarrierDoesNotBlankOtherNegatives(t *testing.T) {
	msg := "还是错误处理更简单，这个方案真的是垃圾实现"
	if got := detectNegativeFeedback(msg); got == "" {
		t.Fatal("carrier must not blank the independent 垃圾 negative on the same line")
	} else if !strings.Contains(got, "frustration") && !strings.Contains(got, "rejection") {
		t.Fatalf("unexpected category: %s", got)
	}
}
