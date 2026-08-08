package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestForesightCalibrate_NoPredictions(t *testing.T) {
	s := newForesightCalibrateState()
	text := "Let me read the config file."
	calls := []provider.ToolCallDelta{{Name: "read_file"}}
	s.recordPrediction(text, calls, 1)

	// No prediction language -> no pending predictions
	if len(s.predictions) != 0 {
		t.Fatalf("expected 0 predictions, got %d", len(s.predictions))
	}
}

func TestForesightCalibrate_SuccessPrediction_MatchResult(t *testing.T) {
	s := newForesightCalibrateState()
	text := "The test should pass after my fix."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	if len(s.predictions) != 1 {
		t.Fatalf("expected 1 prediction, got %d", len(s.predictions))
	}
	if !s.predictions[0].predictedOK {
		t.Fatal("expected predictedOK=true")
	}

	// Actual result: success -> no mismatch
	hint := s.checkCalibration("run_command", "All tests passed", false, 1)
	if hint != "" {
		t.Fatalf("expected no hint on match, got: %s", hint)
	}
	if s.mismatches != 0 {
		t.Fatalf("expected 0 mismatches, got %d", s.mismatches)
	}
}

func TestForesightCalibrate_SuccessPrediction_FailureResult(t *testing.T) {
	s := newForesightCalibrateState()
	text := "The build should succeed now."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	// Actual result: failure -> mismatch
	hint := s.checkCalibration("run_command", "Error: compilation failed", false, 1)
	if s.mismatches != 1 {
		t.Fatalf("expected 1 mismatch, got %d", s.mismatches)
	}
	// Only 1 mismatch, threshold is 3 -> no hint yet
	if hint != "" {
		t.Fatalf("expected no hint at 1 mismatch, got: %s", hint)
	}
}

func TestForesightCalibrate_ThresholdReached(t *testing.T) {
	s := newForesightCalibrateState()

	// Generate 3 mismatches
	for i := 0; i < 3; i++ {
		text := "This should pass."
		calls := []provider.ToolCallDelta{{Name: "run_command"}}
		s.recordPrediction(text, calls, i+1)
		s.checkCalibration("run_command", "Error: test failed", false, i+1)
	}

	if s.mismatches != 3 {
		t.Fatalf("expected 3 mismatches, got %d", s.mismatches)
	}

	// 4th mismatch should trigger hint (threshold=3, but hint fires when reaching 3)
	// Actually the 3rd mismatch should have already triggered it
}

func TestForesightCalibrate_FailurePrediction_SuccessResult(t *testing.T) {
	s := newForesightCalibrateState()
	text := "The test should fail with a type error."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	if len(s.predictions) != 1 {
		t.Fatalf("expected 1 prediction, got %d", len(s.predictions))
	}
	if s.predictions[0].predictedOK {
		t.Fatal("expected predictedOK=false for failure prediction")
	}

	// Actual result: success -> mismatch
	s.checkCalibration("run_command", "Build succeeded", false, 1)
	if s.mismatches != 1 {
		t.Fatalf("expected 1 mismatch, got %d", s.mismatches)
	}
}

func TestForesightCalibrate_EmptyResult_Mismatch(t *testing.T) {
	s := newForesightCalibrateState()
	text := "This should contain the config definition."
	calls := []provider.ToolCallDelta{{Name: "read_file"}}
	s.recordPrediction(text, calls, 1)
	if len(s.predictions) != 1 {
		t.Fatalf("expected 1 prediction, got %d", len(s.predictions))
	}

	// Actual result: empty/nothing -> mismatch (predicted content, got nothing)
	s.checkCalibration("read_file", "", false, 1)
	if s.mismatches != 1 {
		t.Fatalf("expected 1 mismatch for empty result, got %d", s.mismatches)
	}
}

func TestForesightCalibrate_AmbiguousPrediction_Skipped(t *testing.T) {
	s := newForesightCalibrateState()
	// Both success and failure language -> ambiguous, skip
	text := "This should pass but might fail."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	if len(s.predictions) != 0 {
		t.Fatalf("expected 0 predictions for ambiguous text, got %d", len(s.predictions))
	}
}

func TestForesightCalibrate_MaxWarnings(t *testing.T) {
	s := newForesightCalibrateState()

	// Generate enough mismatches to trigger hint twice
	for i := 0; i < 8; i++ {
		text := "This should pass."
		calls := []provider.ToolCallDelta{{Name: "run_command"}}
		s.recordPrediction(text, calls, i+1)
		s.checkCalibration("run_command", "Error: failed", false, i+1)
	}

	// warnCount should be capped at 2
	if s.warnCount != 2 {
		t.Fatalf("expected warnCount=2, got %d", s.warnCount)
	}
}

func TestForesightCalibrate_Reset(t *testing.T) {
	s := newForesightCalibrateState()
	text := "This should work."
	calls := []provider.ToolCallDelta{{Name: "run_command"}}
	s.recordPrediction(text, calls, 1)

	s.reset()
	if len(s.predictions) != 0 {
		t.Fatalf("expected 0 predictions after reset, got %d", len(s.predictions))
	}
}

func TestForesightCalibrate_NilSafety(t *testing.T) {
	var s *foresightCalibrateState
	// Should not panic
	s.recordPrediction("text", nil, 1)
	hint := s.checkCalibration("tool", "result", false, 1)
	if hint != "" {
		t.Fatalf("expected empty hint for nil state, got: %s", hint)
	}
	s.reset()
}

func TestExtractForesightSnippet(t *testing.T) {
	text := "I will read the file. The test should pass after my fix. Let me continue."
	snippet := extractForesightSnippet(text)
	if snippet == "" {
		t.Fatal("expected non-empty snippet")
	}
	// Should contain "should pass"
	if !contains(snippet, "should pass") {
		t.Fatalf("snippet should contain prediction text, got: %s", snippet)
	}
}

// contains/containsStr are already defined in reflection_test.go
