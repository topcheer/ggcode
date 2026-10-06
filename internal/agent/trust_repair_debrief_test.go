package agent

import (
	"strings"
	"testing"
)

func newDebriefTestAgent() *Agent {
	return &Agent{
		trustRepair:      newTrustRepairState(),
		compoundedUncert: newCompoundedUncertaintyState(),
	}
}

func recErr(s *trustRepairState, step int, tool, path, content string) {
	s.record(trustRepairEvent{Step: step, Tool: tool, Arguments: `{"file_path":"` + path + `"}`, IsError: true, Content: content})
}

func recOK(s *trustRepairState, step int, tool, path string) {
	s.record(trustRepairEvent{Step: step, Tool: tool, Arguments: `{"file_path":"` + path + `"}`, IsError: false, Content: "ok"})
}

// 1. Empty run: no failures recorded, no debrief.
func TestDebrief_EmptyRun_NoOutput(t *testing.T) {
	a := newDebriefTestAgent()
	if got := a.maybeEmitTrustRepairDebrief(); got != "" {
		t.Fatalf("expected empty debrief on clean run, got: %s", got)
	}
}

// 2. Single failure stays below the noise threshold.
func TestDebrief_SingleToolError_BelowThreshold(t *testing.T) {
	a := newDebriefTestAgent()
	recErr(a.trustRepair, 1, "edit_file", "/a.go", "anchor not found")
	if got := a.maybeEmitTrustRepairDebrief(); got != "" {
		t.Fatalf("single failure must not trigger debrief, got: %s", got)
	}
}

// 3. Two failures trigger, with the failure count surfaced.
func TestDebrief_TwoFailures_Triggers(t *testing.T) {
	a := newDebriefTestAgent()
	recErr(a.trustRepair, 1, "edit_file", "/a.go", "anchor not found")
	recErr(a.trustRepair, 2, "run_command", "", "exit status 1")
	got := a.maybeEmitTrustRepairDebrief()
	if got == "" {
		t.Fatal("two failures must trigger debrief")
	}
	if !strings.Contains(got, "2 failed tool call") {
		t.Errorf("debrief must state the failure count, got: %s", got)
	}
}

// 4. A long error streak is narrated with its length.
func TestDebrief_ErrStreak_Narrated(t *testing.T) {
	a := newDebriefTestAgent()
	for i := 1; i <= 5; i++ {
		recErr(a.trustRepair, i, "run_command", "", "boom")
	}
	got := a.maybeEmitTrustRepairDebrief()
	if !strings.Contains(got, "error streak of 5") {
		t.Errorf("debrief must narrate the streak length, got: %s", got)
	}
}

// 5. Root-cause section names the streak origin tool and quotes the error.
func TestDebrief_IncludesRootCause(t *testing.T) {
	a := newDebriefTestAgent()
	recErr(a.trustRepair, 1, "edit_file", "/a.go", "anchor not found")
	recErr(a.trustRepair, 2, "edit_file", "/a.go", "anchor still not found")
	got := a.maybeEmitTrustRepairDebrief()
	for _, want := range []string{"Root cause", "edit_file", "/a.go", "anchor not found"} {
		if !strings.Contains(got, want) {
			t.Errorf("root-cause section missing %q, got: %s", want, got)
		}
	}
}

// 6. Counterfactual section contrasts a failed tool with a later
// successful different tool on the same path.
func TestDebrief_IncludesCounterfactual_DifferentTool(t *testing.T) {
	a := newDebriefTestAgent()
	recErr(a.trustRepair, 1, "edit_file", "/a.go", "anchor not found")
	recErr(a.trustRepair, 2, "run_command", "", "build failed")
	recOK(a.trustRepair, 3, "multi_edit_file", "/a.go")
	got := a.maybeEmitTrustRepairDebrief()
	if !strings.Contains(got, "Counterfactual") || !strings.Contains(got, "multi_edit_file") {
		t.Errorf("counterfactual section must contrast tools on same path, got: %s", got)
	}
}

// 7. Same tool eventually succeeding on the same path gets retry wording.
func TestDebrief_IncludesCounterfactual_SameToolRetry(t *testing.T) {
	a := newDebriefTestAgent()
	recErr(a.trustRepair, 1, "edit_file", "/a.go", "anchor not found")
	recErr(a.trustRepair, 2, "run_command", "", "build failed")
	recOK(a.trustRepair, 3, "edit_file", "/a.go")
	got := a.maybeEmitTrustRepairDebrief()
	if !strings.Contains(got, "same tool succeeding") {
		t.Errorf("same-tool retry must produce retry wording, got: %s", got)
	}
}

// 8. Boundary section mirrors the compounded-uncertainty ledger (0.85^w).
func TestDebrief_IncludesReliabilityBound(t *testing.T) {
	a := newDebriefTestAgent()
	a.compoundedUncert.totalWeight = 5.5 // 0.85^5.5 ~ 0.40
	recErr(a.trustRepair, 1, "edit_file", "/a.go", "anchor not found")
	recErr(a.trustRepair, 2, "run_command", "", "build failed")
	got := a.maybeEmitTrustRepairDebrief()
	if !strings.Contains(got, "~40%") || !strings.Contains(got, "0.85^5.5") {
		t.Errorf("boundary section must mirror compounded reliability, got: %s", got)
	}
}

// 9. Without epistemic events the boundary falls back to provisional wording.
func TestDebrief_NoEpistemicEvents_ProvisionalWording(t *testing.T) {
	a := newDebriefTestAgent()
	recErr(a.trustRepair, 1, "edit_file", "/a.go", "anchor not found")
	recErr(a.trustRepair, 2, "run_command", "", "build failed")
	got := a.maybeEmitTrustRepairDebrief()
	if !strings.Contains(got, "provisional") {
		t.Errorf("no-ledger boundary must use provisional wording, got: %s", got)
	}
}

// 10. One-shot: a second call after firing returns "".
func TestDebrief_OneShot(t *testing.T) {
	a := newDebriefTestAgent()
	recErr(a.trustRepair, 1, "edit_file", "/a.go", "anchor not found")
	recErr(a.trustRepair, 2, "edit_file", "/a.go", "still not found")
	if first := a.maybeEmitTrustRepairDebrief(); first == "" {
		t.Fatal("expected first emit to fire")
	}
	if second := a.maybeEmitTrustRepairDebrief(); second != "" {
		t.Fatalf("one-shot violated: second emit returned %s", second)
	}
}

// 11. New user turn resets the ledger and re-arms the debrief.
func TestDebrief_ResetOnNewUserTurn(t *testing.T) {
	a := newDebriefTestAgent()
	recErr(a.trustRepair, 1, "edit_file", "/a.go", "anchor not found")
	recErr(a.trustRepair, 2, "edit_file", "/a.go", "still not found")
	a.maybeEmitTrustRepairDebrief()
	a.trustRepair.reset()
	if got := a.maybeEmitTrustRepairDebrief(); got != "" {
		t.Fatalf("after reset with empty ledger, emit must be empty, got: %s", got)
	}
	recErr(a.trustRepair, 1, "edit_file", "/b.go", "anchor not found")
	if got := a.maybeEmitTrustRepairDebrief(); got != "" {
		t.Fatalf("single failure after reset must not trigger, got: %s", got)
	}
}

// 12. Nil ledger (zero-value Agent) must not panic on either entry point.
func TestDebrief_NilState_NoPanic(t *testing.T) {
	a := &Agent{}
	a.trustRepair.record(trustRepairEvent{Step: 1, Tool: "x", IsError: true}) // nil receiver
	if got := a.maybeEmitTrustRepairDebrief(); got != "" {
		t.Fatalf("nil ledger must yield empty debrief, got: %s", got)
	}
}
