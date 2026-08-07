package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrajIntel_ExtractInsights_NilOrShort(t *testing.T) {
	s := newTrajIntelState()

	// Nil stats -> no learnings
	if l := s.extractInsights(nil); l != nil {
		t.Errorf("nil stats should produce no learnings, got %d", len(l))
	}

	// Short run (< 3 iterations) -> no learnings
	stats := &RunStats{Iterations: 2}
	if l := s.extractInsights(stats); l != nil {
		t.Errorf("short run should produce no learnings, got %d", len(l))
	}
}

func TestTrajIntel_ExtractInsights_EfficientSuccess(t *testing.T) {
	s := newTrajIntelState()
	stats := &RunStats{
		Iterations:  5,
		Success:     true,
		ToolCalls:   map[string]int{"edit_file": 2, "read_file": 1},
		Errors:      []string{},
		FilesEdited: []string{"a.go", "b.go"},
	}

	learnings := s.extractInsights(stats)
	if len(learnings) == 0 {
		t.Fatal("expected at least 1 learning for efficient success")
	}

	found := false
	for _, l := range learnings {
		if l.Type == "strategy" && l.Category == "efficient-completion" {
			found = true
		}
	}
	if !found {
		t.Error("expected strategy/efficient-completion learning")
	}
}

func TestTrajIntel_ExtractInsights_Recovery(t *testing.T) {
	s := newTrajIntelState()
	stats := &RunStats{
		Iterations:  6,
		Success:     true,
		ToolCalls:   map[string]int{"edit_file": 3, "read_file": 2, "run_command": 1},
		Errors:      []string{"edit failed: old_text not found"},
		FilesEdited: []string{"a.go"},
	}

	learnings := s.extractInsights(stats)
	found := false
	for _, l := range learnings {
		if l.Type == "recovery" {
			found = true
		}
	}
	if !found {
		t.Error("expected recovery learning for successful run with errors")
	}
}

func TestTrajIntel_ExtractInsights_OverExploration(t *testing.T) {
	s := newTrajIntelState()
	stats := &RunStats{
		Iterations:  20,
		Success:     true,
		ToolCalls:   map[string]int{"read_file": 15, "grep": 5, "search_files": 3},
		Errors:      []string{},
		FilesEdited: []string{"a.go"}, // only 1 edit
	}

	learnings := s.extractInsights(stats)
	found := false
	for _, l := range learnings {
		if l.Category == "over-exploration" {
			found = true
		}
	}
	if !found {
		t.Error("expected over-exploration optimization learning")
	}
}

func TestTrajIntel_ExtractInsights_HighErrorRate(t *testing.T) {
	s := newTrajIntelState()
	stats := &RunStats{
		Iterations:  8,
		Success:     false,
		ToolCalls:   map[string]int{"edit_file": 6, "run_command": 4}, // 10 total
		Errors:      []string{"err1", "err2", "err3"},                 // 30% error rate
		FilesEdited: []string{},
	}

	learnings := s.extractInsights(stats)
	found := false
	for _, l := range learnings {
		if l.Category == "high-error-rate" {
			found = true
		}
	}
	if !found {
		t.Error("expected high-error-rate optimization learning")
	}
}

func TestTrajIntel_ExtractInsights_ContextPressure(t *testing.T) {
	s := newTrajIntelState()
	stats := &RunStats{
		Iterations:        5,
		Success:           true,
		ToolCalls:         map[string]int{"edit_file": 1},
		FilesEdited:       []string{"a.go"},
		ContextPeakTokens: 90000,
		ContextWindow:     100000, // 90% usage
	}

	learnings := s.extractInsights(stats)
	found := false
	for _, l := range learnings {
		if l.Category == "context-pressure" {
			found = true
		}
	}
	if !found {
		t.Error("expected context-pressure optimization learning")
	}
}

func TestTrajIntel_ExtractInsights_NoProgressFailure(t *testing.T) {
	s := newTrajIntelState()
	stats := &RunStats{
		Iterations:  5,
		Success:     false,
		ToolCalls:   map[string]int{"read_file": 3},
		Errors:      []string{"permission denied"},
		FilesEdited: []string{},
	}

	learnings := s.extractInsights(stats)
	found := false
	for _, l := range learnings {
		if l.Category == "no-progress-failure" {
			found = true
		}
	}
	if !found {
		t.Error("expected no-progress-failure learning")
	}
}

func TestTrajIntel_Persist(t *testing.T) {
	dir := t.TempDir()
	s := newTrajIntelState()
	s.filePath = filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl")

	stats := &RunStats{
		Iterations:  5,
		Success:     true,
		ToolCalls:   map[string]int{"edit_file": 2},
		Errors:      []string{},
		FilesEdited: []string{"a.go"},
		UserPrompt:  "fix the bug",
	}

	s.maybeExtractAndPersist(dir, stats)

	// Verify file was created
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty learnings file")
	}
}

func TestTrajIntel_RollingWindowTrim(t *testing.T) {
	dir := t.TempDir()
	s := newTrajIntelState()
	s.filePath = filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl")

	// Write 60 entries (should trim to 50).
	for i := 0; i < 60; i++ {
		stats := &RunStats{
			Iterations:  5,
			Success:     true,
			ToolCalls:   map[string]int{"edit_file": 1},
			Errors:      []string{},
			FilesEdited: []string{filepath.Join("dir", string(rune('a'+i%26))+".go")},
			UserPrompt:  "task",
		}
		s.maybeExtractAndPersist(dir, stats)
	}

	// Read and count entries.
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines > trajIntelMaxEntries {
		t.Errorf("expected at most %d entries, got %d", trajIntelMaxEntries, lines)
	}
}

func TestIsReadHeavy(t *testing.T) {
	tests := []struct {
		name     string
		tools    map[string]int
		expected bool
	}{
		{"mostly reads", map[string]int{"read_file": 5, "grep": 3, "edit_file": 1}, true},
		{"mostly edits", map[string]int{"edit_file": 5, "read_file": 1}, false},
		{"balanced", map[string]int{"read_file": 3, "edit_file": 3}, false},
		{"empty", map[string]int{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isReadHeavy(tt.tools); got != tt.expected {
				t.Errorf("isReadHeavy(%v) = %v, want %v", tt.tools, got, tt.expected)
			}
		})
	}
}

func TestSummarizeErrors(t *testing.T) {
	// Empty
	if got := summarizeErrors([]string{}); got != "none" {
		t.Errorf("empty errors should return 'none', got %q", got)
	}

	// Single error
	if got := summarizeErrors([]string{"file not found"}); got != "file not found" {
		t.Errorf("single error mismatch: got %q", got)
	}

	// Multiple errors
	got := summarizeErrors([]string{"error one", "error two", "error three"})
	if got == "none" || got == "error one" {
		t.Errorf("expected summary with 'more', got %q", got)
	}

	// Long error truncation
	longErr := ""
	for i := 0; i < 200; i++ {
		longErr += "x"
	}
	got = summarizeErrors([]string{longErr})
	if len(got) > 110 {
		t.Errorf("expected truncated to ~100 chars, got %d", len(got))
	}
}
