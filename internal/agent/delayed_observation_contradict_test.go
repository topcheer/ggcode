package agent

import (
	"strings"
	"testing"
)

func TestDelayedObservation_NoMatchDelayedContradiction(t *testing.T) {
	s := newDelayedObservationState()
	// Turn 0: grep returns "No matches found" as a SUCCESSFUL result.
	s.recordToolResult("grep", "No matches found for pattern \"foobar\"", false)

	// Turns 1..3: intervening tool calls, no contradiction.
	for i := 0; i < 3; i++ {
		s.advanceTurn()
		if msg := s.checkDelayedContradiction("some neutral text"); msg != "" {
			t.Fatalf("turn %d: unexpected early firing: %s", i+1, msg)
		}
	}

	// Turn 4 (delay >= 3): agent claims it found matches.
	s.advanceTurn()
	msg := s.checkDelayedContradiction("I found 5 matches for the pattern in the codebase")
	if msg == "" {
		t.Fatal("expected delayed contradiction hint for no-match vs found-claim, got empty")
	}
	if !strings.Contains(msg, "Delayed Observation Contradiction") {
		t.Errorf("hint missing header, got: %s", msg)
	}
}

func TestDelayedObservation_TooFreshDoesNotFire(t *testing.T) {
	s := newDelayedObservationState()
	s.recordToolResult("grep", "No matches found", false)
	s.advanceTurn() // delay=1
	s.advanceTurn() // delay=2

	msg := s.checkDelayedContradiction("found 3 matches for the pattern")
	if msg != "" {
		t.Fatalf("should not fire before min turns (adjacent-turn detectors handle), got: %s", msg)
	}
}

func TestDelayedObservation_NotFoundDelayedContradiction(t *testing.T) {
	s := newDelayedObservationState()
	s.recordToolResult("read_file", "no such file or directory", false)
	for i := 0; i < 4; i++ {
		s.advanceTurn()
	}
	msg := s.checkDelayedContradiction("The file contains the configuration we need")
	if msg == "" {
		t.Fatal("expected contradiction for not-found vs file-contains claim")
	}
}

func TestDelayedObservation_BuildFailDelayedContradiction(t *testing.T) {
	s := newDelayedObservationState()
	s.recordToolResult("run_command", "build failed: exit code 1", false)
	for i := 0; i < 3; i++ {
		s.advanceTurn()
	}
	msg := s.checkDelayedContradiction("the build passes and tests succeed")
	if msg == "" {
		t.Fatal("expected contradiction for build-failure content vs build-pass claim")
	}
}

func TestDelayedObservation_EmptyResultDelayedContradiction(t *testing.T) {
	s := newDelayedObservationState()
	s.recordToolResult("glob", "", false) // empty glob output
	for i := 0; i < 3; i++ {
		s.advanceTurn()
	}
	msg := s.checkDelayedContradiction("found 2 files matching the glob pattern")
	if msg == "" {
		t.Fatal("expected contradiction for empty-result vs found-N claim")
	}
}

func TestDelayedObservation_IgnoresErrorResults(t *testing.T) {
	s := newDelayedObservationState()
	// isError=true results are handled by false_premise_check; we skip them.
	s.recordToolResult("grep", "no matches found", true)
	for i := 0; i < 4; i++ {
		s.advanceTurn()
	}
	msg := s.checkDelayedContradiction("found 3 matches")
	if msg != "" {
		t.Fatalf("should ignore isError results, got: %s", msg)
	}
}

func TestDelayedObservation_NoPositiveClaimNoFire(t *testing.T) {
	s := newDelayedObservationState()
	s.recordToolResult("grep", "No matches found", false)
	for i := 0; i < 4; i++ {
		s.advanceTurn()
	}
	msg := s.checkDelayedContradiction("The search did not find anything, so we need a different approach")
	if msg != "" {
		t.Fatalf("no positive claim -> should not fire, got: %s", msg)
	}
}

func TestDelayedObservation_MaxWarningsCap(t *testing.T) {
	s := newDelayedObservationState()
	// Two separate observations for two independent contradictions.
	s.recordToolResult("grep", "No matches found", false)
	s.recordToolResult("read_file", "no such file", false)
	for i := 0; i < 4; i++ {
		s.advanceTurn()
	}
	msg1 := s.checkDelayedContradiction("found 1 match")
	if msg1 == "" {
		t.Fatal("first warning expected")
	}
	msg2 := s.checkDelayedContradiction("the file exists and contains data")
	if msg2 == "" {
		t.Fatal("second warning expected")
	}
	msg3 := s.checkDelayedContradiction("found 3 more matches")
	if msg3 != "" {
		t.Fatalf("should cap at %d warnings, got third: %s", delayedContradictionMaxWarnings, msg3)
	}
}

func TestDelayedObservation_Reset(t *testing.T) {
	s := newDelayedObservationState()
	s.recordToolResult("grep", "No matches found", false)
	s.advanceTurn()
	s.warningCount = 1
	s.reset()
	if len(s.observations) != 0 || s.warningCount != 0 || s.currentTurn != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestDelayedObservation_MatchedObsNotReflagged(t *testing.T) {
	s := newDelayedObservationState()
	s.recordToolResult("grep", "No matches found", false)
	for i := 0; i < 3; i++ {
		s.advanceTurn()
	}
	msg1 := s.checkDelayedContradiction("found 1 match")
	if msg1 == "" {
		t.Fatal("first warning expected")
	}
	// Same observation should not be matched again.
	msg2 := s.checkDelayedContradiction("found 99 matches")
	if msg2 != "" {
		t.Fatalf("already-matched observation should not re-fire (within cap), got: %s", msg2)
	}
}

func TestDocCategorizeNegative(t *testing.T) {
	cases := []struct {
		tool    string
		content string
		want    docObsCategory
	}{
		{"grep", "No matches found", docObsNoMatch},
		{"grep", "0 results", docObsNoMatch},
		{"read_file", "no such file", docObsNotFound},
		{"read_file", "file not found", docObsNotFound},
		{"glob", "", docObsEmpty},
		{"glob", "0 files", docObsEmpty},
		{"run_command", "build failed: exit code 1", docObsBuildFail},
		{"run_command", "tests failed:", docObsBuildFail},
		{"run_command", "all good no errors", docObsNone},
		{"edit_file", "successfully edited", docObsNone},
	}
	for _, c := range cases {
		got := docCategorizeNegative(c.tool, c.content)
		if got != c.want {
			t.Errorf("docCategorizeNegative(%q, %q) = %v, want %v", c.tool, c.content, got, c.want)
		}
	}
}
