package agent

import (
	"strings"
	"testing"
	"time"
)

// helper: build a RunStats and score it
func scoreRunForTest(s *ResponseQualityScorer, iterations int, errCount int, success bool, provider, model string) {
	stats := &RunStats{
		ToolCalls:         map[string]int{"read_file": 2, "edit_file": 1},
		FilesEdited:       []string{"foo.go"},
		Duration:          10 * time.Second,
		Iterations:        iterations,
		Success:           success,
		UserPrompt:        "test",
		ContextPeakTokens: 5000,
		ContextWindow:     200000,
	}
	for i := 0; i < errCount; i++ {
		stats.Errors = append(stats.Errors, "err")
	}
	s.ScoreRun(stats, provider, model)
}

func TestDetectRegression_InsufficientHistory(t *testing.T) {
	s := NewResponseQualityScorer(50)
	// Only 1 prior run - below regressionMinHistory (3)
	scoreRunForTest(s, 3, 0, true, "anthropic", "claude")
	scoreRunForTest(s, 3, 0, true, "anthropic", "claude")

	rep := s.DetectRegression()
	if rep.Detected {
		t.Error("should not detect regression with insufficient history")
	}
}

func TestDetectRegression_NoRegression(t *testing.T) {
	s := NewResponseQualityScorer(50)
	for i := 0; i < 5; i++ {
		scoreRunForTest(s, 3, 0, true, "anthropic", "claude")
	}
	rep := s.DetectRegression()
	if rep.Detected {
		t.Errorf("should not detect regression on consistent runs: %+v", rep)
	}
}

func TestDetectRegression_ScoreDrop(t *testing.T) {
	s := NewResponseQualityScorer(50)
	// Build a healthy baseline (4 successful runs)
	for i := 0; i < 4; i++ {
		scoreRunForTest(s, 3, 0, true, "anthropic", "claude")
	}
	// Now a bad run: high iterations, errors, failure
	scoreRunForTest(s, 12, 4, false, "anthropic", "claude")

	rep := s.DetectRegression()
	if !rep.Detected {
		t.Fatal("expected regression to be detected")
	}
	if rep.Severity == SeverityNone {
		t.Error("expected non-none severity")
	}
	if rep.RunCount < regressionMinHistory {
		t.Errorf("RunCount %d < min %d", rep.RunCount, regressionMinHistory)
	}
	if rep.BaselineMean <= rep.CurrentScore {
		t.Errorf("baseline mean %.3f should be > current %.3f for regression",
			rep.BaselineMean, rep.CurrentScore)
	}
	formatted := rep.Format()
	if formatted == "" {
		t.Error("Format() should return non-empty string when detected")
	}
	if !strings.Contains(formatted, "regression") {
		t.Errorf("Format() should contain 'regression': %s", formatted)
	}
}

func TestDetectRegression_IterationInflation(t *testing.T) {
	s := NewResponseQualityScorer(50)
	// Baseline: low iterations, success, no errors -> high score
	for i := 0; i < 4; i++ {
		scoreRunForTest(s, 3, 0, true, "anthropic", "claude")
	}
	// Current: succeeds but takes many more iterations (iteration inflation)
	scoreRunForTest(s, 10, 0, true, "anthropic", "claude")

	rep := s.DetectRegression()
	if !rep.Detected {
		t.Fatal("expected iteration-inflation regression to be detected")
	}
	found := false
	for _, sig := range rep.Signals {
		if strings.Contains(sig, "iteration") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an iteration-related signal, got %v", rep.Signals)
	}
}

func TestDetectRegression_ErrorRateEmergence(t *testing.T) {
	s := NewResponseQualityScorer(50)
	// Clean baseline
	for i := 0; i < 4; i++ {
		scoreRunForTest(s, 3, 0, true, "anthropic", "claude")
	}
	// Current: errors emerge
	scoreRunForTest(s, 5, 2, true, "anthropic", "claude")

	rep := s.DetectRegression()
	if !rep.Detected {
		t.Fatal("expected error-rate regression to be detected")
	}
	found := false
	for _, sig := range rep.Signals {
		if strings.Contains(sig, "error rate") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error-rate signal, got %v", rep.Signals)
	}
}

func TestDetectRegression_DifferentProvidersIsolated(t *testing.T) {
	s := NewResponseQualityScorer(50)
	// Build baseline for provider A
	for i := 0; i < 4; i++ {
		scoreRunForTest(s, 3, 0, true, "anthropic", "claude")
	}
	// First run for provider B (should not cross-contaminate A's baseline)
	scoreRunForTest(s, 3, 0, true, "openai", "gpt-4o")

	rep := s.DetectRegression()
	// The current run is provider B with only 1 run - no baseline for B yet.
	if rep.Detected {
		t.Error("should not detect regression for a new provider with no history")
	}
	if rep.RunCount != 0 {
		t.Errorf("expected RunCount=0 for new provider, got %d", rep.RunCount)
	}
}

func TestDetectRegression_SeveritySevere(t *testing.T) {
	s := NewResponseQualityScorer(50)
	// Perfect baseline
	for i := 0; i < 4; i++ {
		scoreRunForTest(s, 3, 0, true, "anthropic", "claude")
	}
	// Catastrophic run
	scoreRunForTest(s, 15, 6, false, "anthropic", "claude")

	rep := s.DetectRegression()
	if rep.Severity != SeveritySevere {
		t.Errorf("expected severe, got %s (scoreDrop would be large)", rep.Severity)
	}
}

func TestMaybeDetectRegression_NilSafe(t *testing.T) {
	// Should not panic when scorer is nil
	var s *ResponseQualityScorer
	s.maybeDetectRegression() //nolint:staticcheck // intentional nil test
}

func TestMaybeDetectRegression_StoresLatest(t *testing.T) {
	s := NewResponseQualityScorer(50)
	for i := 0; i < 4; i++ {
		scoreRunForTest(s, 3, 0, true, "anthropic", "claude")
	}
	scoreRunForTest(s, 15, 6, false, "anthropic", "claude")

	s.maybeDetectRegression()

	s.mu.RLock()
	stored := s.latestRegression
	s.mu.RUnlock()
	if !stored.Detected {
		t.Error("expected latestRegression to be populated with a detected regression")
	}
}

func TestClassifyRegression_Tiers(t *testing.T) {
	meanScore := 0.8
	tests := []struct {
		drop     float64
		expected RegressionSeverity
	}{
		{0.05, SeverityNone},
		{0.15, SeverityMinor},
		{0.25, SeverityModerate},
		{0.35, SeveritySevere},
		{0.50, SeveritySevere},
	}
	for _, tc := range tests {
		current := QualityEntry{Score: meanScore - tc.drop}
		bs := baselineStats{meanScore: meanScore, minScore: meanScore}
		got := classifyRegression(tc.drop, current, bs)
		if got != tc.expected {
			t.Errorf("drop %.2f: expected %s, got %s", tc.drop, tc.expected, got)
		}
	}
}

func TestFormat_NoDetection(t *testing.T) {
	rep := RegressionReport{Detected: false}
	if rep.Format() != "" {
		t.Error("Format() should return empty string when not detected")
	}
}
