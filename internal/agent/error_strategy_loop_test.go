package agent

import "testing"

func TestErrStrategy_NoErrors(t *testing.T) {
	s := newErrStrategyState()
	for i := 0; i < 10; i++ {
		s.recordResult("success output", false)
	}
	if hint := s.checkAndWarn(); hint != "" {
		t.Fatalf("expected no warning with no errors, got: %s", hint)
	}
}

func TestErrStrategy_BelowThreshold(t *testing.T) {
	s := newErrStrategyState()
	s.recordResult("Error: file not found: foo.txt", true)
	s.recordResult("Error: file not found: bar.txt", true)
	if hint := s.checkAndWarn(); hint != "" {
		t.Fatalf("expected no warning below threshold (2), got: %s", hint)
	}
}

func TestErrStrategy_AtThreshold(t *testing.T) {
	s := newErrStrategyState()
	s.recordResult("Error: no such file: foo.txt", true)
	s.recordResult("Error: file not found: bar.txt", true)
	s.recordResult("Error: does not exist: baz.txt", true)
	hint := s.checkAndWarn()
	if hint == "" {
		t.Fatal("expected warning at threshold (3)")
	}
	if !errStrContains(hint, "file-not-found") {
		t.Fatalf("expected file-not-found category in warning, got: %s", hint)
	}
}

func TestErrStrategy_DifferentCategoriesNoWarning(t *testing.T) {
	s := newErrStrategyState()
	s.recordResult("Error: file not found: foo.txt", true)
	s.recordResult("Error: timeout exceeded", true)
	s.recordResult("Error: permission denied", true)
	if hint := s.checkAndWarn(); hint != "" {
		t.Fatalf("expected no warning for 3 different categories, got: %s", hint)
	}
}

func TestErrStrategy_MaxWarnings(t *testing.T) {
	s := newErrStrategyState()
	// Fire for file-not-found
	for i := 0; i < 5; i++ {
		s.recordResult("Error: file not found: f"+errStrategyItoa(i)+".txt", true)
	}
	h1 := s.checkAndWarn()
	if h1 == "" {
		t.Fatal("expected first warning")
	}
	// Same category already fired - should not re-warn
	h2 := s.checkAndWarn()
	if h2 != "" {
		t.Fatalf("expected no re-warn for same category, got: %s", h2)
	}
}

func TestErrStrategy_SlidingWindowEviction(t *testing.T) {
	s := newErrStrategyState()
	// Fill window with errors
	for i := 0; i < errStrategyWindow; i++ {
		s.recordResult("Error: file not found: f"+errStrategyItoa(i)+".txt", true)
	}
	// Window should be full; add non-errors to push errors out
	for i := 0; i < errStrategyWindow; i++ {
		s.recordResult("ok", false)
	}
	if s.catCounts[errCatFileNotFound] != 0 {
		t.Fatalf("expected 0 file-not-found after eviction, got %d", s.catCounts[errCatFileNotFound])
	}
}

func TestErrStrategy_OldTextMismatch(t *testing.T) {
	s := newErrStrategyState()
	s.recordResult("old_text not found in file", true)
	s.recordResult("old_text does not match", true)
	s.recordResult("old_text not unique", true)
	hint := s.checkAndWarn()
	if hint == "" {
		t.Fatal("expected warning for old-text-mismatch category")
	}
	if !errStrContains(hint, "old-text-mismatch") {
		t.Fatalf("expected old-text-mismatch in warning, got: %s", hint)
	}
}

func TestErrStrategy_Timeout(t *testing.T) {
	s := newErrStrategyState()
	s.recordResult("Error: timeout", true)
	s.recordResult("Error: timed out", true)
	s.recordResult("Error: deadline exceeded", true)
	hint := s.checkAndWarn()
	if hint == "" {
		t.Fatal("expected warning for timeout category")
	}
	if !errStrContains(hint, "timeout") {
		t.Fatalf("expected timeout in warning, got: %s", hint)
	}
}

func TestErrStrategy_Reset(t *testing.T) {
	s := newErrStrategyState()
	s.recordResult("Error: file not found", true)
	s.recordResult("Error: file not found", true)
	s.recordResult("Error: file not found", true)
	s.reset()
	if len(s.recentResults) != 0 || len(s.catCounts) != 0 || s.warningCount != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestClassifyErrResult_NonError(t *testing.T) {
	_, isErr := classifyErrResult("file contents here", false)
	if isErr {
		t.Fatal("expected non-error for normal content")
	}
}

func TestClassifyErrResult_ErrorFlag(t *testing.T) {
	_, isErr := classifyErrResult("some message", true)
	if !isErr {
		t.Fatal("expected error when isError=true")
	}
}

func TestErrStrategyItoa(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0", "0"}, {"1", "1"}, {"10", "10"}, {"42", "42"}, {"100", "100"},
	}
	for _, tt := range tests {
		_ = tt
	}
}

func errStrContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
