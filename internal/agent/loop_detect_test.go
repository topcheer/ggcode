package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestLoopDetector_ConsecutiveErrors(t *testing.T) {
	var ld loopDetector

	// First error — no guidance
	g := ld.recordResult(true, "edit_file")
	if g != "" {
		t.Errorf("expected no guidance on first error, got: %s", g)
	}

	// Second error — no guidance yet
	g = ld.recordResult(true, "run_command")
	if g != "" {
		t.Errorf("expected no guidance on second error, got: %s", g)
	}

	// Third error — still no guidance (threshold is 4)
	g = ld.recordResult(true, "write_file")
	if g != "" {
		t.Errorf("expected no guidance on third error, got: %s", g)
	}

	// Fourth error — guidance should appear
	g = ld.recordResult(true, "edit_file")
	if g == "" {
		t.Error("expected guidance on fourth consecutive error, got empty string")
	}
	if !strings.Contains(g, "consecutive tool calls") {
		t.Errorf("expected guidance to mention consecutive errors, got: %s", g)
	}

	// Fifth error — guidance already given, should not repeat
	g = ld.recordResult(true, "edit_file")
	if g != "" {
		t.Errorf("expected no repeat guidance after already given, got: %s", g)
	}
}

func TestLoopDetector_ErrorStreakResetsOnSuccess(t *testing.T) {
	var ld loopDetector

	// Three errors, then a success
	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "run_command")

	g := ld.recordResult(false, "read_file")
	if g != "" {
		t.Errorf("expected no guidance on success, got: %s", g)
	}

	// After reset, need 4 more errors to trigger again
	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "edit_file")
	g = ld.recordResult(true, "edit_file")
	if g == "" {
		t.Error("expected guidance after 4 new errors post-reset")
	}
}

func TestLoopDetector_MixedErrorsAndDuplicates(t *testing.T) {
	var ld loopDetector

	// Simulate: duplicate call (exact same args), then different error calls
	tc1 := testToolCall("edit_file", `{"file_path":"/tmp/a.go","old_text":"x","new_text":"y"}`)
	tc2 := testToolCall("edit_file", `{"file_path":"/tmp/b.go","old_text":"z","new_text":"w"}`)

	// First call — check duplicate (should not warn, first time)
	g1 := ld.checkDuplicate(tc1)
	if g1 != "" {
		t.Errorf("first call should not trigger duplicate warning: %s", g1)
	}

	// Different call — resets fingerprint streak
	g2 := ld.checkDuplicate(tc2)
	if g2 != "" {
		t.Errorf("different call should not trigger duplicate warning: %s", g2)
	}

	// Record some errors
	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "edit_file")
	ld.recordResult(true, "run_command")
	ld.recordResult(true, "write_file")

	// Should have triggered error-streak guidance already
	if !ld.errorGuidanceGiven {
		t.Error("expected errorGuidanceGiven to be true after 4 errors")
	}
}

// testToolCall creates a ToolCallDelta for testing.
func testToolCall(name string, args string) provider.ToolCallDelta {
	return provider.ToolCallDelta{
		Name:      name,
		Arguments: []byte(args),
	}
}
