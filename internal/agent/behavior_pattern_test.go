package agent

import (
	"strings"
	"testing"
	"time"
)

func TestBehaviorPattern_NoPatternsWithFewRuns(t *testing.T) {
	b := newBehaviorPatternState()
	b.recordRun(&RunStats{Iterations: 5, FilesEdited: []string{"a.go"}})
	b.recordRun(&RunStats{Iterations: 3, FilesEdited: []string{"b.go"}})

	patterns := b.detectPatterns()
	if len(patterns) != 0 {
		t.Fatalf("expected no patterns with <3 runs, got %d", len(patterns))
	}
}

func TestBehaviorPattern_VerificationSkip(t *testing.T) {
	b := newBehaviorPatternState()
	// 3 runs with file edits but no build/test commands
	for i := 0; i < 3; i++ {
		b.recordRun(&RunStats{
			Iterations:  5,
			FilesEdited: []string{"a.go"},
			ToolCalls:   map[string]int{"edit_file": 2},
			Success:     true,
		})
	}

	patterns := b.detectPatterns()
	found := false
	for _, p := range patterns {
		if p.Type == "verification_skip" {
			found = true
			if p.Severity != "high" {
				t.Errorf("expected high severity, got %s", p.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("expected verification_skip pattern, got %v", patterns)
	}
}

func TestBehaviorPattern_NoVerificationSkipWithBuild(t *testing.T) {
	b := newBehaviorPatternState()
	for i := 0; i < 3; i++ {
		b.recordRun(&RunStats{
			Iterations:  5,
			FilesEdited: []string{"a.go"},
			ToolCalls:   map[string]int{"edit_file": 2},
			CommandsRun: []string{"# test\ngo test ./..."},
			Success:     true,
		})
	}

	patterns := b.detectPatterns()
	for _, p := range patterns {
		if p.Type == "verification_skip" {
			t.Fatalf("should not detect verification_skip when build/test was run")
		}
	}
}

func TestBehaviorPattern_HighEditFailureRate(t *testing.T) {
	b := newBehaviorPatternState()
	// 3 runs with high edit failure rate
	for i := 0; i < 3; i++ {
		stats := &RunStats{
			Iterations: 5,
			ToolCalls:  map[string]int{"edit_file": 4},
			Errors: []string{
				"edit_file: old_text not found",
				"edit_file: old_text not unique",
			},
			Success: true,
		}
		b.recordRun(stats)
	}

	patterns := b.detectPatterns()
	found := false
	for _, p := range patterns {
		if p.Type == "high_edit_failure" {
			found = true
			if !strings.Contains(p.Description, "50%") && !strings.Contains(p.Description, "67%") && !strings.Contains(p.Description, "%") {
				t.Errorf("expected failure rate in description, got %s", p.Description)
			}
		}
	}
	if !found {
		t.Fatalf("expected high_edit_failure pattern, got %v", patterns)
	}
}

func TestBehaviorPattern_HighIterationCount(t *testing.T) {
	b := newBehaviorPatternState()
	for i := 0; i < 3; i++ {
		b.recordRun(&RunStats{
			Iterations:  20,
			FilesEdited: []string{"a.go"},
			CommandsRun: []string{"go test"},
			Success:     true,
		})
	}

	patterns := b.detectPatterns()
	found := false
	for _, p := range patterns {
		if p.Type == "high_iteration_count" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected high_iteration_count pattern, got %v", patterns)
	}
}

func TestBehaviorPattern_ChronicFailure(t *testing.T) {
	b := newBehaviorPatternState()
	for i := 0; i < 3; i++ {
		b.recordRun(&RunStats{
			Iterations: 5,
			Success:    false,
		})
	}

	patterns := b.detectPatterns()
	found := false
	for _, p := range patterns {
		if p.Type == "chronic_failure" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected chronic_failure pattern, got %v", patterns)
	}
}

func TestBehaviorPattern_RingBufferLimit(t *testing.T) {
	b := newBehaviorPatternState()
	// Record more runs than the window size
	for i := 0; i < behaviorPatternWindowSize+3; i++ {
		b.recordRun(&RunStats{Iterations: 1, Success: true})
	}
	if len(b.recent) > behaviorPatternWindowSize {
		t.Errorf("ring buffer exceeded window size: %d > %d", len(b.recent), behaviorPatternWindowSize)
	}
}

func TestBehaviorPattern_ShouldInjectRateLimit(t *testing.T) {
	b := newBehaviorPatternState()
	if !b.shouldInject() {
		t.Error("first call should allow injection")
	}
	if b.shouldInject() {
		t.Error("second call should be rate-limited")
	}
	b.reset()
	if !b.shouldInject() {
		t.Error("after reset, should allow injection again")
	}
}

func TestBehaviorPattern_NilSafe(t *testing.T) {
	var b *behaviorPatternState
	if patterns := b.detectPatterns(); patterns != nil {
		t.Error("nil state should return nil patterns")
	}
	if b.shouldInject() {
		t.Error("nil state should not allow injection")
	}
	b.recordRun(nil)
}

func TestIsBuildTestCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", true},
		{"go build ./...", true},
		{"make build", true},
		{"make test", true},
		{"cargo build", true},
		{"npm run test", true},
		{"pytest", true},
		{"jest", true},
		{"flutter test", true},
		{"go test -tags goolm ./...", true},
		{"npm install", false},
		{"echo hello", false},
		{"git status", false},
		{"ls -la", false},
	}
	for _, tt := range tests {
		got := isBuildTestCommand(tt.cmd)
		if got != tt.want {
			t.Errorf("isBuildTestCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestIsEditError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"edit_file: old_text not found", true},
		{"write_file: permission denied", true},
		{"multi_edit_file: edit failed", true},
		{"run_command: exit code 1", false},
		{"read_file: not found", false},
	}
	for _, tt := range tests {
		got := isEditError(tt.msg)
		if got != tt.want {
			t.Errorf("isEditError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestSnapshotFromRunStats(t *testing.T) {
	stats := &RunStats{
		Iterations:  10,
		FilesEdited: []string{"a.go", "b.go"},
		ToolCalls: map[string]int{
			"edit_file":  3,
			"read_file":  5,
			"write_file": 2,
		},
		CommandsRun: []string{"# test\ngo test ./..."},
		Errors:      []string{"edit_file: not found"},
		Success:     true,
		Duration:    5 * time.Second,
	}

	snap := snapshotFromRunStats(stats)
	if snap.Iterations != 10 {
		t.Errorf("Iterations = %d, want 10", snap.Iterations)
	}
	if snap.FilesEdited != 2 {
		t.Errorf("FilesEdited = %d, want 2", snap.FilesEdited)
	}
	if snap.ToolCalls != 10 {
		t.Errorf("ToolCalls = %d, want 10", snap.ToolCalls)
	}
	if snap.EditAttempts != 5 {
		t.Errorf("EditAttempts = %d, want 5", snap.EditAttempts)
	}
	if snap.EditFailures != 1 {
		t.Errorf("EditFailures = %d, want 1", snap.EditFailures)
	}
	if !snap.HadBuildTest {
		t.Error("HadBuildTest should be true")
	}
	if !snap.HadFileEdits {
		t.Error("HadFileEdits should be true")
	}
	if !snap.Success {
		t.Error("Success should be true")
	}
}
