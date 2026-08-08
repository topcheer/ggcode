package agent

import (
	"strings"
	"testing"
)

func TestNarrativeEvidence_TestPassClaimVsFailure(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("run_command", "FAIL  test_foo\n--- FAIL: TestBar\n3 tests failed", 1, false)

	hint := s.checkContradiction("All tests passed successfully!", 2)
	if hint == "" {
		t.Fatal("expected contradiction hint for tests-pass claim vs failure output")
	}
	if !strings.Contains(hint, "tests pass") {
		t.Errorf("hint should mention test contradiction, got: %s", hint)
	}
}

func TestNarrativeEvidence_BuildPassClaimVsFailure(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("run_command", "main.go:42: undefined: foo\nBUILD FAILURE", 1, false)

	hint := s.checkContradiction("The build compiles cleanly without errors", 2)
	if hint == "" {
		t.Fatal("expected contradiction hint for build claim vs failure output")
	}
}

func TestNarrativeEvidence_FoundClaimVsNoResult(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("search_files", "No results found\n0 matches", 1, false)

	hint := s.checkContradiction("Found 5 occurrences of the pattern", 2)
	if hint == "" {
		t.Fatal("expected contradiction hint for found claim vs no-results output")
	}
	if !strings.Contains(hint, "no matches") {
		t.Errorf("hint should mention no matches, got: %s", hint)
	}
}

func TestNarrativeEvidence_CommandSuccessVsError(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("run_command", "some output", 1, true)

	hint := s.checkContradiction("The command executed successfully without errors", 2)
	if hint == "" {
		t.Fatal("expected contradiction hint for success claim vs error output")
	}
}

func TestNarrativeEvidence_NoContradictionWhenLegitimate(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("run_command", "PASS\nok\tall tests passed", 1, false)

	hint := s.checkContradiction("All tests passed!", 2)
	if hint != "" {
		t.Errorf("expected no contradiction when tests actually pass, got: %s", hint)
	}
}

func TestNarrativeEvidence_NoContradictionDifferentCategory(t *testing.T) {
	s := newNarrativeEvidenceState()
	// A search tool returning no results should NOT trigger test-pass contradictions.
	s.recordToolResult("search_files", "0 matches", 1, false)

	hint := s.checkContradiction("All tests passed!", 2)
	if hint != "" {
		t.Errorf("expected no contradiction when search fails but tests claim pass, got: %s", hint)
	}
}

func TestNarrativeEvidence_MaxWarningsCap(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("run_command", "FAIL\n3 tests failed", 1, false)

	hint1 := s.checkContradiction("All tests passed!", 2)
	if hint1 == "" {
		t.Fatal("expected first contradiction hint")
	}
	hint2 := s.checkContradiction("All tests passed!", 3)
	if hint2 == "" {
		t.Fatal("expected second contradiction hint")
	}
	hint3 := s.checkContradiction("All tests passed!", 4)
	if hint3 != "" {
		t.Error("expected no third hint due to warning cap")
	}
}

func TestNarrativeEvidence_Reset(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("run_command", "FAIL", 1, false)
	_ = s.checkContradiction("All tests passed!", 2)
	s.reset()
	if s.warnings != 0 || len(s.recentOuts) != 0 {
		t.Error("reset should clear warnings and recent outputs")
	}
}

func TestNarrativeEvidence_OldResultsIgnored(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("run_command", "FAIL\n3 tests failed", 1, false)

	// currentIter is far beyond the lookback window
	hint := s.checkContradiction("All tests passed!", 100)
	if hint != "" {
		t.Error("expected no contradiction when old results are outside window")
	}
}

func TestNarrativeEvidence_EmptyTextNoCrash(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("run_command", "FAIL", 1, false)
	hint := s.checkContradiction("", 2)
	if hint != "" {
		t.Error("expected no hint for empty text")
	}
}

func TestNarrativeEvidence_EmptyResultsNoCrash(t *testing.T) {
	s := newNarrativeEvidenceState()
	hint := s.checkContradiction("All tests passed!", 2)
	if hint != "" {
		t.Error("expected no hint with no recent outputs")
	}
}

func TestNarrativeEvidence_MaybeWarnNilSafe(t *testing.T) {
	a := &Agent{}
	hint := a.maybeWarnNarrativeEvidence("All tests pass!", 1)
	if hint != "" {
		t.Error("expected empty hint when detector is nil")
	}
}

func TestNarrativeEvidence_AllFoundClaim(t *testing.T) {
	s := newNarrativeEvidenceState()
	s.recordToolResult("grep", "No files found", 1, false)

	hint := s.checkContradiction("Found all references to the symbol", 2)
	if hint == "" {
		t.Fatal("expected contradiction for 'found all' claim vs no results")
	}
}

func TestNarrativeEvidence_RingBufferTrims(t *testing.T) {
	s := newNarrativeEvidenceState()
	for i := 0; i < narrativeEvidenceMaxRecent+5; i++ {
		s.recordToolResult("run_command", "output", i+1, false)
	}
	if len(s.recentOuts) != narrativeEvidenceMaxRecent {
		t.Errorf("expected ring buffer capped at %d, got %d", narrativeEvidenceMaxRecent, len(s.recentOuts))
	}
}
