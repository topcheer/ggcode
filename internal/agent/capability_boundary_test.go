package agent

import (
	"strings"
	"testing"
)

func TestScanApproachPivots(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{
			name: "empty text",
			text: "",
			want: 0,
		},
		{
			name: "no pivots",
			text: "I will implement the function now.",
			want: 0,
		},
		{
			name: "let me try a different approach",
			text: "That didn't work. Let me try a different approach to this problem.",
			want: 1,
		},
		{
			name: "alternatively",
			text: "The build failed. Alternatively, we could use a different library.",
			want: 1,
		},
		{
			name: "another approach would",
			text: "Another approach would be to refactor the entire module.",
			want: 1,
		},
		{
			name: "let me reconsider",
			text: "Let me reconsider the problem from scratch.",
			want: 1,
		},
		{
			name: "I'll try a different strategy",
			text: "I'll try a different strategy this time.",
			want: 1,
		},

		{
			name: "I'll try a different way",
			text: "I'll try a different way of parsing the input.",
			want: 1,
		},
		{
			name: "I'll try another tactic",
			text: "I'll try another tactic if this fails again.",
			want: 1,
		},
		{
			name: "I'll try an alternative approach",
			text: "I'll try an alternative approach here.",
			want: 1,
		},
		{
			name: "switching to",
			text: "Switching to a more robust solution now.",
			want: 1,
		},
		{
			name: "different tack",
			text: "Let's take a different tack here.",
			want: 1,
		},
		{
			name: "multiple pivots in same text",
			text: "Let me try a different approach. That failed too. Alternatively, we could switch to another method.",
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := scanApproachPivots(tt.text)
			if len(hits) != tt.want {
				t.Errorf("scanApproachPivots() got %d hits, want %d", len(hits), tt.want)
			}
		})
	}
}

func TestCapabilityBoundaryState(t *testing.T) {
	s := newCapabilityBoundaryState()

	if s.warnings != 0 || len(s.pivots) != 0 || s.failErrors != 0 {
		t.Fatal("initial state not zero")
	}

	s.recordApproachPivot("pivot 1")
	s.recordApproachPivot("pivot 2")
	if len(s.pivots) != 2 {
		t.Errorf("expected 2 pivots, got %d", len(s.pivots))
	}

	s.recordToolResult(true)
	s.recordToolResult(true)
	if s.failErrors != 2 {
		t.Errorf("expected 2 failErrors, got %d", s.failErrors)
	}

	s.recordToolResult(false)
	if s.failErrors != 0 {
		t.Errorf("expected 0 failErrors after success, got %d", s.failErrors)
	}

	s.reset()
	if s.warnings != 0 || len(s.pivots) != 0 || s.failErrors != 0 {
		t.Errorf("reset failed: warnings=%d pivots=%d failErrors=%d", s.warnings, len(s.pivots), s.failErrors)
	}
}

func TestMaybeWarnCapabilityBoundary_NoWarning(t *testing.T) {
	a := &Agent{capBoundary: newCapabilityBoundaryState()}

	hint := a.maybeWarnCapabilityBoundary("Let me try a different approach.")
	if hint != "" {
		t.Errorf("expected empty hint with <3 pivots, got: %s", hint)
	}
}

func TestMaybeWarnCapabilityBoundary_NoErrors(t *testing.T) {
	a := &Agent{capBoundary: newCapabilityBoundaryState()}

	a.maybeWarnCapabilityBoundary("Let me try a different approach.")
	a.maybeWarnCapabilityBoundary("Alternatively, let me try another strategy.")
	a.maybeWarnCapabilityBoundary("Let me reconsider this from scratch.")

	hint := a.maybeWarnCapabilityBoundary("Taking a different tack now.")
	if hint != "" {
		t.Errorf("expected empty hint when no errors recorded, got: %s", hint)
	}
}

func TestMaybeWarnCapabilityBoundary_FiresWithPivotsAndErrors(t *testing.T) {
	a := &Agent{capBoundary: newCapabilityBoundaryState()}

	a.capBoundary.recordToolResult(true)
	a.capBoundary.recordToolResult(true)

	hint := a.maybeWarnCapabilityBoundary("Let me try a different approach.")
	if hint != "" {
		t.Fatalf("expected empty hint with only 1 pivot, got: %s", hint)
	}

	hint = a.maybeWarnCapabilityBoundary("Alternatively, let me try another strategy.")
	if hint == "" {
		t.Fatal("expected non-empty hint with 3 pivots and 2+ errors")
	}

	if !strings.Contains(hint, "capability-boundary") {
		t.Errorf("hint should contain 'capability-boundary' tag: %s", hint)
	}
	if !strings.Contains(strings.ToLower(hint), "escalate") {
		t.Errorf("hint should mention escalation: %s", hint)
	}
}

func TestMaybeWarnCapabilityBoundary_MaxOncePerRun(t *testing.T) {
	a := &Agent{capBoundary: newCapabilityBoundaryState()}

	a.capBoundary.recordToolResult(true)
	a.capBoundary.recordToolResult(true)
	a.capBoundary.recordApproachPivot("p1")
	a.capBoundary.recordApproachPivot("p2")
	a.capBoundary.recordApproachPivot("p3")

	hint1 := a.maybeWarnCapabilityBoundary("")
	if hint1 == "" {
		t.Fatal("expected first warning to fire")
	}

	a.capBoundary.recordToolResult(true)
	a.capBoundary.recordApproachPivot("p4")
	hint2 := a.maybeWarnCapabilityBoundary("")
	if hint2 != "" {
		t.Errorf("expected no second warning (max 1 per run), got: %s", hint2)
	}
}

func TestCapabilityBoundaryReset(t *testing.T) {
	a := &Agent{capBoundary: newCapabilityBoundaryState()}

	a.capBoundary.recordToolResult(true)
	a.capBoundary.recordApproachPivot("p1")
	a.capBoundary.warnings = 1

	a.capBoundary.reset()

	if a.capBoundary.warnings != 0 || len(a.capBoundary.pivots) != 0 || a.capBoundary.failErrors != 0 {
		t.Errorf("reset should clear all state")
	}
}
