package agent

import (
	"testing"
	"time"
)

func TestResponseQuality_ScoreRun_Success(t *testing.T) {
	scorer := NewResponseQualityScorer(50)
	stats := &RunStats{
		ToolCalls: map[string]int{
			"read_file": 2,
			"edit_file": 1,
		},
		FilesEdited:       []string{"foo.go"},
		Errors:            []string{},
		Duration:          10 * time.Second,
		Iterations:        2,
		Success:           true,
		UserPrompt:        "fix the bug",
		ContextPeakTokens: 5000,
		ContextWindow:     200000,
	}
	stats.runID = "test-run-1"

	entry := scorer.ScoreRun(stats, "anthropic", "claude-sonnet-4")

	if !entry.Signals.Success {
		t.Error("expected success=true")
	}
	if entry.Signals.ToolEfficiency != 1.0 {
		t.Errorf("expected tool efficiency 1.0, got %.2f", entry.Signals.ToolEfficiency)
	}
	if entry.Score < 0.5 || entry.Score > 1.0 {
		t.Errorf("expected score in [0.5, 1.0], got %.3f", entry.Score)
	}
	if entry.Provider != "anthropic" || entry.Model != "claude-sonnet-4" {
		t.Errorf("unexpected provider/model: %s/%s", entry.Provider, entry.Model)
	}
}

func TestResponseQuality_ScoreRun_WithErrors(t *testing.T) {
	scorer := NewResponseQualityScorer(50)
	stats := &RunStats{
		ToolCalls: map[string]int{
			"edit_file": 3,
		},
		FilesEdited: []string{"a.go"},
		Errors:      []string{"edit failed", "another error"},
		Duration:    30 * time.Second,
		Iterations:  8,
		Success:     false,
	}
	stats.runID = "test-run-2"

	entry := scorer.ScoreRun(stats, "openai", "gpt-4o")

	if entry.Signals.Success {
		t.Error("expected success=false")
	}
	if entry.Signals.ToolEfficiency >= 1.0 {
		t.Error("expected tool efficiency < 1.0 with errors")
	}
	if entry.Score >= 0.5 {
		t.Errorf("expected low score for failed run, got %.3f", entry.Score)
	}
}

func TestResponseQuality_Compare(t *testing.T) {
	scorer := NewResponseQualityScorer(100)

	// Provider A: 2 successful runs
	for i := 0; i < 2; i++ {
		stats := &RunStats{
			ToolCalls:         map[string]int{"read_file": 1},
			Duration:          5 * time.Second,
			Iterations:        2,
			Success:           true,
			ContextWindow:     200000,
			ContextPeakTokens: 1000,
		}
		stats.runID = "a-run"
		scorer.ScoreRun(stats, "anthropic", "claude-sonnet-4")
	}

	// Provider B: 1 failed run
	stats := &RunStats{
		ToolCalls:  map[string]int{"edit_file": 1},
		Errors:     []string{"failed"},
		Duration:   60 * time.Second,
		Iterations: 10,
		Success:    false,
	}
	stats.runID = "b-run"
	scorer.ScoreRun(stats, "openai", "gpt-4o")

	comps := scorer.Compare()
	if len(comps) != 2 {
		t.Fatalf("expected 2 comparisons, got %d", len(comps))
	}

	// Anthropic should rank first (higher avg score)
	if comps[0].Provider != "anthropic" {
		t.Errorf("expected anthropic first, got %s", comps[0].Provider)
	}
	if comps[0].AvgScore <= comps[1].AvgScore {
		t.Error("expected first provider to have higher score")
	}
}

func TestResponseQuality_MaxRuns(t *testing.T) {
	scorer := NewResponseQualityScorer(3)

	for i := 0; i < 5; i++ {
		stats := &RunStats{
			Iterations: 1,
			Success:    true,
		}
		stats.runID = "run"
		scorer.ScoreRun(stats, "test", "model")
	}

	if len(scorer.runs) != 3 {
		t.Errorf("expected 3 runs (capped), got %d", len(scorer.runs))
	}
}

func TestResponseQuality_Recent(t *testing.T) {
	scorer := NewResponseQualityScorer(100)

	for i := 0; i < 5; i++ {
		stats := &RunStats{
			Iterations: 1,
			Success:    true,
		}
		stats.runID = "run"
		scorer.ScoreRun(stats, "test", "model")
	}

	recent := scorer.Recent(2)
	if len(recent) != 2 {
		t.Errorf("expected 2 recent entries, got %d", len(recent))
	}
}

func TestResponseQuality_FormatComparison(t *testing.T) {
	scorer := NewResponseQualityScorer(50)
	stats := &RunStats{
		Iterations: 2,
		Success:    true,
	}
	stats.runID = "run"
	scorer.ScoreRun(stats, "anthropic", "claude-sonnet-4")

	output := scorer.FormatComparison()
	if output == "" {
		t.Error("expected non-empty output")
	}
}

func TestResponseQuality_Clamp01(t *testing.T) {
	tests := []struct {
		input, expected float64
	}{
		{-1.0, 0.0},
		{0.0, 0.0},
		{0.5, 0.5},
		{1.0, 1.0},
		{2.0, 1.0},
	}
	for _, tc := range tests {
		got := clamp01(tc.input)
		if got != tc.expected {
			t.Errorf("clamp01(%.1f) = %.1f, expected %.1f", tc.input, got, tc.expected)
		}
	}
}
