package agent

import (
	"strings"
	"testing"
	"time"
)

func TestAnalyzeEfficiency_GoodRun(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"read_file":   3,
			"edit_file":   2,
			"run_command": 1,
		},
		FilesEdited:       []string{"a.go", "b.go"},
		Iterations:        4,
		Success:           true,
		Duration:          2 * time.Minute,
		ContextPeakTokens: 30000,
		ContextWindow:     128000,
	}

	r := AnalyzeEfficiency(stats)
	if r.Level != efficiencyGood {
		t.Errorf("expected efficiencyGood, got %d (patterns: %v)", r.Level, r.AntiPatterns)
	}
	if r.Score != 100 {
		t.Errorf("expected score 100, got %d", r.Score)
	}
	// Format should return empty for good runs
	if out := r.Format(stats); out != "" {
		t.Errorf("expected empty output for good run, got: %s", out)
	}
}

func TestAnalyzeEfficiency_LowEditRatio(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"read_file": 10,
			"grep":      5,
		},
		FilesEdited: []string{"a.go"},
		Iterations:  15,
		Errors:      []string{},
	}

	r := AnalyzeEfficiency(stats)
	if r.Level < efficiencyFair {
		t.Errorf("expected at least efficiencyFair, got %d", r.Level)
	}
	found := false
	for _, p := range r.AntiPatterns {
		if strings.Contains(p, "edit-to-iteration") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected low edit-to-iteration ratio pattern, got: %v", r.AntiPatterns)
	}
}

func TestAnalyzeEfficiency_HighErrorRate(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"edit_file": 10,
		},
		FilesEdited: []string{},
		Iterations:  10,
		Errors: []string{
			"edit_file: not found",
			"edit_file: not found",
			"edit_file: not found",
			"edit_file: not found",
			"edit_file: not found",
		},
	}

	r := AnalyzeEfficiency(stats)
	found := false
	for _, p := range r.AntiPatterns {
		if strings.Contains(p, "error rate") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected high error rate pattern, got: %v", r.AntiPatterns)
	}
}

func TestAnalyzeEfficiency_ContextPressure(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"read_file": 3,
			"edit_file": 2,
		},
		FilesEdited:       []string{"a.go", "b.go"},
		Iterations:        5,
		ContextPeakTokens: 110000,
		ContextWindow:     128000,
	}

	r := AnalyzeEfficiency(stats)
	found := false
	for _, p := range r.AntiPatterns {
		if strings.Contains(p, "Context near capacity") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected context pressure pattern, got: %v", r.AntiPatterns)
	}
}

func TestAnalyzeEfficiency_CompactionWaste(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"read_file": 3,
			"edit_file": 2,
		},
		FilesEdited:     []string{"a.go"},
		Iterations:      5,
		CompactionCount: 3,
	}

	r := AnalyzeEfficiency(stats)
	found := false
	for _, p := range r.AntiPatterns {
		if strings.Contains(p, "compaction") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected compaction waste pattern, got: %v", r.AntiPatterns)
	}
}

func TestAnalyzeEfficiency_ReadAmplification(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"read_file": 12,
		},
		FilesEdited: nil,
		Iterations:  10,
		Errors:      []string{"some error"},
	}

	r := AnalyzeEfficiency(stats)
	found := false
	for _, p := range r.AntiPatterns {
		if strings.Contains(p, "High read count") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected read amplification pattern, got: %v", r.AntiPatterns)
	}
}

func TestEfficiencyReport_Format(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"read_file": 15,
			"edit_file": 1,
		},
		FilesEdited: []string{"a.go"},
		Iterations:  20,
		Errors: []string{
			"edit_file: not found",
			"edit_file: not found",
			"edit_file: not found",
			"edit_file: not found",
		},
	}

	r := AnalyzeEfficiency(stats)
	out := r.Format(stats)
	if out == "" {
		t.Fatal("expected non-empty output for poor efficiency run")
	}
	if !strings.Contains(out, "[Efficiency Analysis]") {
		t.Errorf("expected header, got: %s", out)
	}
	if !strings.Contains(out, "Score:") {
		t.Errorf("expected score line, got: %s", out)
	}
	if !strings.Contains(out, "Recommendations:") {
		t.Errorf("expected recommendations section, got: %s", out)
	}
}

func TestEfficiencyReport_FormatSkipsShortRuns(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"read_file": 2,
		},
		Iterations: 2,
	}

	r := AnalyzeEfficiency(stats)
	out := r.Format(stats)
	if out != "" {
		t.Errorf("expected empty output for trivial run, got: %s", out)
	}
}

func TestEfficiencyReport_ScoreClamped(t *testing.T) {
	// Run with multiple anti-patterns should clamp at 0
	stats := RunStats{
		ToolCalls: map[string]int{
			"edit_file": 10,
			"read_file": 12,
		},
		FilesEdited:       []string{"a.go"},
		Iterations:        20,
		Errors:            []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"},
		ContextPeakTokens: 115000,
		ContextWindow:     128000,
		CompactionCount:   3,
	}

	r := AnalyzeEfficiency(stats)
	if r.Score < 0 {
		t.Errorf("score should be clamped at 0, got %d", r.Score)
	}
	if r.Level != efficiencyPoor {
		t.Errorf("expected efficiencyPoor for multi-pattern run, got %d", r.Level)
	}
}

func TestGenerateInsights_IncludesEfficiency(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"read_file": 15,
			"edit_file": 1,
		},
		FilesEdited: []string{"a.go"},
		Iterations:  20,
		Errors:      []string{"e1", "e2", "e3", "e4", "e5"},
		Success:     true,
		Duration:    10 * time.Minute,
	}

	insights := GenerateInsights(stats)
	if !strings.Contains(insights, "Run Reflection") {
		t.Fatalf("expected reflection header, got: %s", insights)
	}
	if !strings.Contains(insights, "[Efficiency Analysis]") {
		t.Errorf("expected efficiency analysis in insights, got: %s", insights)
	}
}
