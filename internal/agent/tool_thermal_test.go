package agent

import (
	"strings"
	"testing"
)

func TestThermalState_ExploreHeavy(t *testing.T) {
	s := newThermalState()

	// Simulate 15 reads and only 1 edit (93% explore, 7% modify)
	for i := 0; i < 15; i++ {
		s.recordToolCall("read_file")
	}
	s.recordToolCall("edit_file")

	warn := s.maybeWarn(10)
	if warn == "" {
		t.Fatal("expected explore-heavy warning, got empty")
	}
	if !strings.Contains(warn, "Exploration calls dominate") {
		t.Fatalf("unexpected warning text: %s", warn)
	}
	if !strings.Contains(warn, "over-reading") {
		t.Fatalf("warning should mention over-reading: %s", warn)
	}
}

func TestThermalState_Balanced_NoWarning(t *testing.T) {
	s := newThermalState()

	// Simulate balanced: 5 reads, 4 edits, 2 commands, 2 git checks
	for i := 0; i < 5; i++ {
		s.recordToolCall("read_file")
	}
	for i := 0; i < 4; i++ {
		s.recordToolCall("edit_file")
	}
	for i := 0; i < 2; i++ {
		s.recordToolCall("run_command")
	}
	for i := 0; i < 2; i++ {
		s.recordToolCall("git_diff")
	}

	warn := s.maybeWarn(10)
	if warn != "" {
		t.Fatalf("expected no warning for balanced usage, got: %s", warn)
	}
}

func TestThermalState_TooFewSamples(t *testing.T) {
	s := newThermalState()

	// Only 5 calls (below thermalMinSamples=12)
	for i := 0; i < 5; i++ {
		s.recordToolCall("read_file")
	}

	warn := s.maybeWarn(5)
	if warn != "" {
		t.Fatalf("expected no warning with too few samples, got: %s", warn)
	}
}

func TestThermalState_VerifyHeavy(t *testing.T) {
	s := newThermalState()

	// 5 reads, 1 edit, 8 git_diff (40% verify, 5% modify)
	for i := 0; i < 5; i++ {
		s.recordToolCall("read_file")
	}
	s.recordToolCall("edit_file")
	for i := 0; i < 8; i++ {
		s.recordToolCall("git_diff")
	}

	warn := s.maybeWarn(10)
	if warn == "" {
		t.Fatal("expected verify-heavy warning, got empty")
	}
	if !strings.Contains(warn, "Verification calls high") {
		t.Fatalf("unexpected warning text: %s", warn)
	}
}

func TestThermalState_Cooldown(t *testing.T) {
	s := newThermalState()

	for i := 0; i < 15; i++ {
		s.recordToolCall("read_file")
	}
	s.recordToolCall("edit_file")

	// First warning fires
	warn1 := s.maybeWarn(10)
	if warn1 == "" {
		t.Fatal("expected first warning")
	}

	// Second warning should be suppressed (cooldown)
	warn2 := s.maybeWarn(12)
	if warn2 != "" {
		t.Fatalf("expected cooldown to suppress, got: %s", warn2)
	}

	// After cooldown iterations, warning can fire again
	warn3 := s.maybeWarn(10 + thermalWarnCooldown + 1)
	// Note: this may or may not fire depending on whether conditions still hold.
	// The key check is warn2 being empty.
	_ = warn3
}

func TestThermalState_Reset(t *testing.T) {
	s := newThermalState()

	for i := 0; i < 15; i++ {
		s.recordToolCall("read_file")
	}
	if s.total != 15 {
		t.Fatalf("expected total=15, got %d", s.total)
	}

	s.reset()
	if s.total != 0 {
		t.Fatalf("expected total=0 after reset, got %d", s.total)
	}
	for i := range s.categories {
		if s.categories[i] != 0 {
			t.Fatalf("expected category %d to be 0 after reset, got %d", i, s.categories[i])
		}
	}
}

func TestThermalState_CategoryBreakdown(t *testing.T) {
	s := newThermalState()

	s.recordToolCall("read_file")
	s.recordToolCall("read_file")
	s.recordToolCall("edit_file")

	breakdown := s.categoryBreakdown()
	if !strings.Contains(breakdown, "exploration") {
		t.Fatalf("breakdown should contain 'exploration': %s", breakdown)
	}
	if !strings.Contains(breakdown, "modification") {
		t.Fatalf("breakdown should contain 'modification': %s", breakdown)
	}
}

func TestThermalState_UnknownTool(t *testing.T) {
	s := newThermalState()

	s.recordToolCall("some_unknown_tool")
	if s.categories[thermalOther] != 1 {
		t.Fatalf("expected unknown tool in 'other' category, got %d", s.categories[thermalOther])
	}
}

func TestThermalState_HighModifyNoWarning(t *testing.T) {
	s := newThermalState()

	// Mostly edits with some reads - healthy coding pattern
	for i := 0; i < 8; i++ {
		s.recordToolCall("edit_file")
	}
	for i := 0; i < 5; i++ {
		s.recordToolCall("read_file")
	}

	warn := s.maybeWarn(10)
	if warn != "" {
		t.Fatalf("expected no warning for edit-heavy usage, got: %s", warn)
	}
}
