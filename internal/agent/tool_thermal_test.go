package agent

import (
	"strings"
	"testing"
)

func TestThermalState_ExploreHeavy(t *testing.T) {
	s := newThermalState()

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

	warn1 := s.maybeWarn(10)
	if warn1 == "" {
		t.Fatal("expected first warning")
	}

	warn2 := s.maybeWarn(12)
	if warn2 != "" {
		t.Fatalf("expected cooldown to suppress, got: %s", warn2)
	}

	warn3 := s.maybeWarn(10 + thermalWarnCooldown + 1)

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

func TestThermalSeparateModeCooldowns1855(t *testing.T) {
	ts := newThermalState()

	ts.lastExploreWarnIter = 10

	ts.categories[thermalVerify] = 50
	ts.categories[thermalModify] = 1
	ts.total = 51
	msg := ts.maybeWarn(11)
	if msg == "" {
		t.Fatal("verify-heavy warning must not be cooled down by an explore warning")
	}
	if !strings.Contains(msg, "Verification") {
		t.Fatalf("expected verify-heavy message, got: %s", msg)
	}
}
