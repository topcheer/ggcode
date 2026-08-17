package agent

// #613: foresight_calibrate empty-result regex used bare-word substring
// matching (false mismatches on ordinary source code containing "empty"),
// and predictions were hard-bound to toolCalls[0] in multi-tool turns.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// Defect 1: a successful read_file returning ordinary Go code that contains
// the word "empty" (ErrEmpty, "empty string") must NOT be judged empty.
func TestIssue613_EmptyWordInContentNoFalseMismatch(t *testing.T) {
	s := newForesightCalibrateState()
	text := "This should contain the config definition."
	calls := []provider.ToolCallDelta{{Name: "read_file"}}
	s.recordPrediction(text, calls, 1)
	if len(s.predictions) != 1 {
		t.Fatalf("expected 1 prediction, got %d", len(s.predictions))
	}

	code := `package main
import "errors"
var ErrEmpty = errors.New("empty string")
func f() error { return ErrEmpty }
func g() string { return "" } // empty on purpose
`
	hint := s.checkCalibration("read_file", code, false, 1)
	if hint != "" {
		t.Fatalf("unexpected guidance: %s", hint)
	}
	if s.mismatches != 0 {
		t.Fatalf("expected 0 mismatches for non-empty result containing the word 'empty', got %d", s.mismatches)
	}
}

// Defect 1 counterpart: structurally empty results still count as empty.
func TestIssue613_StructurallyEmptyStillMismatch(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"no matches found",
		"0 matches",
		"nothing found",
	}
	for _, content := range cases {
		s := newForesightCalibrateState()
		text := "This should contain the config definition."
		calls := []provider.ToolCallDelta{{Name: "read_file"}}
		s.recordPrediction(text, calls, 1)
		s.checkCalibration("read_file", content, false, 1)
		if s.mismatches != 1 {
			t.Fatalf("content %q: expected 1 mismatch, got %d", content, s.mismatches)
		}
	}
}

// Defect 1: helper directly — long content mentioning "empty" is not empty;
// short structured markers are.
func TestIssue613_ForesightResultEmptyHelper(t *testing.T) {
	if !foresightResultEmpty("") {
		t.Fatal("empty string must be empty")
	}
	if foresightResultEmpty(strings.Repeat("return ErrEmpty\n", 20)) {
		t.Fatal("long content containing 'empty' must not be empty")
	}
	if !foresightResultEmpty("no results found") {
		t.Fatal("structured no-results marker must be empty")
	}
	if foresightResultEmpty("package main\nfunc f() {}") {
		t.Fatal("short real content must not be empty")
	}
}

// Defect 2: multi-tool turns must not bind the prediction to toolCalls[0].
// A [read_file, run_command] turn with "The tests should pass" must not be
// consumed by the read_file result (no misattribution, no false mismatch,
// no double-counting).
func TestIssue613_MultiToolTurnNotReconciled(t *testing.T) {
	s := newForesightCalibrateState()
	text := "The tests should pass after my fix."
	calls := []provider.ToolCallDelta{{Name: "read_file"}, {Name: "run_command"}}
	s.recordPrediction(text, calls, 1)
	if len(s.predictions) != 0 {
		t.Fatalf("multi-tool turn must not record a prediction, got %d", len(s.predictions))
	}
	// read_file result (success, real content) — nothing to consume.
	if hint := s.checkCalibration("read_file", "file contents here", false, 1); hint != "" {
		t.Fatalf("unexpected guidance: %s", hint)
	}
	// run_command fails — still nothing pending, no mismatch recorded.
	s.checkCalibration("run_command", "exit status 1: tests failed", false, 1)
	if s.mismatches != 0 {
		t.Fatalf("expected 0 mismatches (prediction never attributed), got %d", s.mismatches)
	}
}

// Defect 2: single-tool turns still reconcile normally (regression guard).
func TestIssue613_SingleToolTurnStillReconciled(t *testing.T) {
	s := newForesightCalibrateState()
	text := "The tests should pass after my fix."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)
	if len(s.predictions) != 1 {
		t.Fatalf("expected 1 prediction for single-tool turn, got %d", len(s.predictions))
	}
	s.checkCalibration("run_command", "exit status 1: tests failed", false, 1)
	if s.mismatches != 1 {
		t.Fatalf("expected 1 mismatch for genuine wrong prediction, got %d", s.mismatches)
	}
}

// Defect 2: iteration consistency — a different tool's result on the same
// iteration must not consume a pending prediction.
func TestIssue613_WrongToolNotConsumed(t *testing.T) {
	s := newForesightCalibrateState()
	text := "The tests should pass after my fix."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	// A read_file result arrives first (same iteration) — must not consume.
	s.checkCalibration("read_file", "some file content", false, 1)
	if len(s.predictions) != 1 {
		t.Fatalf("read_file result must not consume a run_command prediction, %d left", len(s.predictions))
	}
	if s.mismatches != 0 {
		t.Fatalf("expected 0 mismatches, got %d", s.mismatches)
	}

	// The actual run_command result (same name, later iteration fallback)
	// consumes it and records the genuine mismatch.
	s.checkCalibration("run_command", "exit status 1: tests failed", false, 2)
	if len(s.predictions) != 0 {
		t.Fatalf("expected prediction consumed, %d left", len(s.predictions))
	}
	if s.mismatches != 1 {
		t.Fatalf("expected 1 mismatch, got %d", s.mismatches)
	}
}
