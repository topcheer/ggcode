package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4747-A: stale EXPECT declarations (naming a tool NOT called this turn)
// must not swallow the whole round - the free-regex prediction in the same
// text still records.
func TestIssue4747_StaleExpectDoesNotSwallowFreeRegexPrediction(t *testing.T) {
	s := newForesightCalibrateState()
	text := "The previous EXPECT(read_file): contains config is done. Now the build might fail."
	// Only run_command is called: the read_file declaration is stale.
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	if len(s.predictions) != 1 {
		t.Fatalf("stale EXPECT must not swallow the free-regex prediction: want 1, got %d", len(s.predictions))
	}
	if s.predictions[0].predictedOK {
		t.Fatal("might-fail prediction must record failure polarity")
	}
}

// #4747-A control: an attributable declaration still takes priority and the
// free-regex path stays suppressed for that turn.
func TestIssue4747_AttributableExpectStillTakesPriority(t *testing.T) {
	s := newForesightCalibrateState()
	text := "EXPECT(run_command): exit code 0. This should pass."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	// One attributable declaration recorded as pending expectation; the
	// free-regex "should pass" in the same text must not add a second one.
	if len(s.predictions) != 1 {
		t.Fatalf("attributable EXPECT must take priority (exactly 1 pending), got %d", len(s.predictions))
	}
}

// #4747-B: "should be empty or missing" hits both the empty and failure
// polarity regexes; the coexistence must take the ambiguous skip instead of
// hard-wiring the empty polarity.
func TestIssue4747_EmptyPlusFailureIsAmbiguous(t *testing.T) {
	s := newForesightCalibrateState()
	text := "The output should be empty; otherwise the lookup might fail."
	calls := []provider.ToolCallDelta{{Name: "read_file"}}
	s.recordPrediction(text, calls, 1)

	if len(s.predictions) != 0 {
		t.Fatalf("empty+failure coexistence must be skipped as ambiguous, got %d predictions", len(s.predictions))
	}
}

// #4747-B control: a pure empty prediction still records with the empty
// polarity intact.
func TestIssue4747_PureEmptyStillRecords(t *testing.T) {
	s := newForesightCalibrateState()
	text := "The grep output should be empty because nothing matches."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	if len(s.predictions) != 1 {
		t.Fatalf("pure empty prediction must still record, got %d", len(s.predictions))
	}
	if !s.predictions[0].predictedEmpty {
		t.Fatal("pure empty prediction must keep the empty polarity")
	}
}

// #4747-C: uncertainty-worded predictions (might/may) must pass the gate -
// they are the most valuable calibration samples.
func TestIssue4747_MightAndMayPredictionsRecord(t *testing.T) {
	for _, text := range []string{
		"The build might fail because of the missing import.",
		"It may throw a nil-pointer error.",
	} {
		s := newForesightCalibrateState()
		calls := []provider.ToolCallDelta{{Name: "run_command"}}
		s.recordPrediction(text, calls, 1)
		if len(s.predictions) != 1 {
			t.Fatalf("uncertainty prediction must record: %q -> %d predictions", text, len(s.predictions))
		}
		if s.predictions[0].predictedOK {
			t.Fatalf("uncertainty prediction must be failure-polar: %q", text)
		}
	}
}

// #4747-C: bare-subject empty phrasing ("It should be empty") records too.
func TestIssue4747_BareSubjectEmptyRecords(t *testing.T) {
	s := newForesightCalibrateState()
	text := "It should be empty after the cleanup step."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	if len(s.predictions) != 1 {
		t.Fatalf("bare-subject empty must record, got %d", len(s.predictions))
	}
	if !s.predictions[0].predictedEmpty {
		t.Fatal("bare-subject empty must keep the empty polarity")
	}
}
