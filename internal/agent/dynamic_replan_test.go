package agent

import (
	"testing"
)

func TestDynamicReplan_ConsecutiveFailure(t *testing.T) {
	s := newReplanState()

	// First failure should not trigger (below threshold)
	if g := s.recordResult("grep", false, "no match"); g != "" {
		t.Fatalf("first failure should not trigger replan, got: %s", g)
	}

	// Second failure should trigger
	if g := s.recordResult("grep", false, "no match"); g == "" {
		t.Fatal("second consecutive failure should trigger replan")
	}
}

func TestDynamicReplan_ResetOnSuccess(t *testing.T) {
	s := newReplanState()

	// First failure
	s.recordResult("grep", false, "no match")

	// Success resets counter
	if g := s.recordResult("grep", true, ""); g != "" {
		t.Fatalf("success should not trigger replan, got: %s", g)
	}

	// Need another 2 failures after success
	s.recordResult("grep", false, "no match")
	if g := s.recordResult("grep", false, "no match"); g == "" {
		t.Fatal("2 failures after success should trigger replan")
	}
}

func TestDynamicReplan_MaxWarnings(t *testing.T) {
	s := newReplanState()

	// Trigger first warning
	s.recordResult("grep", false, "no match")
	w1 := s.recordResult("grep", false, "no match")
	if w1 == "" {
		t.Fatal("first failure pair should trigger")
	}

	s.recordResult("grep", true, "") // reset
	s.recordResult("read_file", false, "not found")
	w2 := s.recordResult("read_file", false, "not found")
	if w2 == "" {
		t.Fatal("second failure pair should trigger")
	}

	// Third pair should be capped
	s.recordResult("read_file", true, "") // reset
	s.recordResult("edit_file", false, "permission denied")
	if g := s.recordResult("edit_file", false, "permission denied"); g != "" {
		t.Fatalf("should cap at %d warnings, got: %s", maxReplanWarnings, g)
	}
}

func TestDynamicReplan_DifferentTools(t *testing.T) {
	s := newReplanState()

	// Different tools each need their own threshold
	s.recordResult("grep", false, "no match")
	s.recordResult("grep", false, "no match") // triggers for grep

	s.recordResult("read_file", false, "not found") // 1st for read_file
	if g := s.recordResult("read_file", false, "not found"); g == "" {
		t.Fatal("second consecutive failure for read_file should trigger")
	}
}

func TestDynamicReplan_ErrorClassification(t *testing.T) {
	tests := []struct {
		err          string
		wantContains string
	}{
		{"permission denied", "permission"},
		{"file not found", "Verify the file path"},
		{"context timeout", "breaking down the task"},
		{"syntax error", "Validate your inputs"},
		{"no matches found", "Broaden your search"},
	}

	for _, tt := range tests {
		t.Run(tt.err, func(t *testing.T) {
			s := newReplanState()
			s.recordResult("test", false, tt.err)
			g := s.recordResult("test", false, tt.err)
			if g == "" {
				t.Fatal("should trigger replan")
			}
			if !contains(g, tt.wantContains) {
				t.Errorf("guidance should contain %q, got: %s", tt.wantContains, g)
			}
		})
	}
}

func TestDynamicReplan_DuplicatePrevention(t *testing.T) {
	s := newReplanState()

	s.recordResult("grep", false, "no match")
	g1 := s.recordResult("grep", false, "no match")
	if g1 == "" {
		t.Fatal("first trigger should produce guidance")
	}

	// Same error pattern without success in between should not duplicate
	if g2 := s.recordResult("grep", false, "no match"); g2 != "" && g2 == g1 {
		t.Fatal("should not duplicate guidance without intervening success")
	}
}

func TestDynamicReplan_Reset(t *testing.T) {
	s := newReplanState()

	// Trigger a warning
	s.recordResult("grep", false, "no match")
	s.recordResult("grep", false, "no match")
	if s.warningCount == 0 {
		t.Fatal("should have a warning")
	}

	// Reset
	s.reset()

	if s.warningCount != 0 {
		t.Fatalf("reset should clear warning count, got %d", s.warningCount)
	}

	if len(s.failCount) != 0 {
		t.Fatal("reset should clear failure counts")
	}

	// Should be able to trigger again after reset
	s.recordResult("grep", false, "no match")
	if g := s.recordResult("grep", false, "no match"); g == "" {
		t.Fatal("should trigger again after reset")
	}
}
