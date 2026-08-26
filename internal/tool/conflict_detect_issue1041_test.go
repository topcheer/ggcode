package tool

import (
	"strings"
	"testing"
)

// Test #1041(a): multi-conflict with unclosed region should not render 'Lines X-0'
func TestFormatConflictWarning_multipleWithUnclosed(t *testing.T) {
	regions := []ConflictRegion{
		{StartLine: 3, EndLine: 7, Branch1: "HEAD", Branch2: "dev"},
		{StartLine: 10, EndLine: 0, Branch1: "HEAD", Branch2: "", Unclosed: true},
	}
	warning := FormatConflictWarning(regions)
	if !strings.Contains(warning, "2 unresolved merge conflicts") {
		t.Errorf("warning should mention multiple conflicts: %q", warning)
	}
	// Should contain "unclosed" but NOT "Lines 10-0"
	if !strings.Contains(warning, "unclosed") {
		t.Errorf("warning should mention unclosed conflict: %q", warning)
	}
	if strings.Contains(warning, "Lines 10-0") {
		t.Errorf("warning should NOT contain 'Lines 10-0': %q", warning)
	}
}

// Test #1041(b): consecutive start markers set Unclosed flag on previous region
func TestDetectMergeConflicts_consecutiveStartMarkers(t *testing.T) {
	content := "package main\n\n<<<<<<< HEAD\nprintln(\"first\")\n<<<<<<< HEAD\nprintln(\"second\")\n"
	regions := DetectMergeConflicts(content)
	if len(regions) != 2 {
		t.Fatalf("expected 2 regions (consecutive starts), got %d", len(regions))
	}
	// First region should be marked as unclosed
	if !regions[0].Unclosed {
		t.Errorf("first region should be Unclosed: %+v", regions[0])
	}
	if regions[0].StartLine != 3 {
		t.Errorf("first region StartLine: expected 3, got %d", regions[0].StartLine)
	}
	// Second region should also be unclosed (reached EOF)
	if !regions[1].Unclosed {
		t.Errorf("second region should be Unclosed: %+v", regions[1])
	}
	if regions[1].StartLine != 5 {
		t.Errorf("second region StartLine: expected 5, got %d", regions[1].StartLine)
	}
}
