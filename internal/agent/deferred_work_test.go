package agent

import (
	"strings"
	"testing"
)

func TestScanDeferrals(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		wantN int // minimum number of deferrals expected
	}{
		{
			name:  "explicit later deferral",
			text:  "I'll handle the error cases later in the next iteration.",
			wantN: 1,
		},
		{
			name:  "follow-up deferral",
			text:  "We can address the validation in a follow-up.",
			wantN: 1,
		},
		{
			name:  "next iteration deferral",
			text:  "In the next iteration, I will add the tests.",
			wantN: 1,
		},
		{
			name:  "after this deferral",
			text:  "After this is done, I'll fix the edge case.",
			wantN: 1,
		},
		{
			name:  "still need to",
			text:  "We still need to add error handling for the parser.",
			wantN: 1,
		},
		{
			name:  "remaining work",
			text:  "The remaining items are tests and documentation.",
			wantN: 1,
		},
		{
			name:  "skip for now",
			text:  "I'll skip the migration for now.",
			wantN: 1,
		},
		{
			name:  "no deferral",
			text:  "I have completed all the requested changes and verified them.",
			wantN: 0,
		},
		{
			name:  "multiple deferrals",
			text:  "I'll add tests later. We still need to handle the edge case. In a follow-up, we can add validation.",
			wantN: 2,
		},
		{
			name:  "empty text",
			text:  "",
			wantN: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := scanDeferrals(tt.text)
			if len(items) < tt.wantN {
				t.Errorf("scanDeferrals() got %d items, want at least %d", len(items), tt.wantN)
			}
		})
	}
}

func TestHasCompletionLanguage(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"I've completed all the changes.", true},
		{"The task is done.", true},
		{"That's it for now.", true},
		{"All set.", true},
		{"I need to fix the bug.", false},
		{"Let me check the output.", false},
		{"", false},
	}

	for _, tt := range tests {
		got := hasCompletionLanguage(tt.text)
		if got != tt.want {
			t.Errorf("hasCompletionLanguage(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestHasResolutionLanguage(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"Now let me handle the remaining validation logic.", true},
		{"As promised, I'm adding the tests now.", true},
		{"I've now addressed the deferred items.", true},
		{"Going back to the error handling now.", true},
		{"Let me check something.", false},
		{"", false},
	}

	for _, tt := range tests {
		got := hasResolutionLanguage(tt.text)
		if got != tt.want {
			t.Errorf("hasResolutionLanguage(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestDeferredWorkStateRecordAndResolve(t *testing.T) {
	s := newDeferredWorkState()

	// Iteration 0: agent defers some work
	s.recordDeferrals("I'll add error handling later.", 0)
	if len(s.items) < 1 {
		t.Fatalf("expected at least 1 deferred item, got %d", len(s.items))
	}
	if s.items[0].resolved {
		t.Error("item should not be resolved yet")
	}

	// Iteration 1: agent resolves it
	s.recordDeferrals("Now let me handle the error handling.", 1)
	if !s.items[0].resolved {
		t.Error("item should be resolved after resolution language")
	}
}

func TestDeferredWorkStateOpenDeferrals(t *testing.T) {
	s := newDeferredWorkState()

	// Iteration 0: deferral
	s.recordDeferrals("I'll add tests later.", 0)

	// Iteration 1: not yet stale (age=1 < threshold=2)
	open := s.openDeferrals(1)
	if len(open) != 0 {
		t.Errorf("expected 0 open at age 1, got %d", len(open))
	}

	// Iteration 2: now stale (age=2 >= threshold=2)
	open = s.openDeferrals(2)
	if len(open) < 1 {
		t.Errorf("expected at least 1 open at age 2, got %d", len(open))
	}
}

func TestMaybeWarnDeferredWork(t *testing.T) {
	a := &Agent{
		deferredWork: newDeferredWorkState(),
	}

	// Iteration 0: deferral recorded
	hint := a.maybeWarnDeferredWork("I'll add error handling later.", 0)
	if hint != "" {
		t.Error("expected no warning on first iteration (deferral just made)")
	}

	// Iteration 1: still not stale
	hint = a.maybeWarnDeferredWork("Checking something else.", 1)
	if hint != "" {
		t.Error("expected no warning at age 1")
	}

	// Iteration 2: now stale, should warn
	hint = a.maybeWarnDeferredWork("Let me continue.", 2)
	if hint == "" {
		t.Error("expected warning for stale deferral")
	}
	if !strings.Contains(hint, "DEFERRED-WORK") {
		t.Errorf("warning should contain DEFERRED-WORK tag, got: %s", hint)
	}
}

func TestMaybeWarnDeferredWorkCompletionWithOpenItems(t *testing.T) {
	a := &Agent{
		deferredWork: newDeferredWorkState(),
	}

	// Iteration 0: deferral
	a.maybeWarnDeferredWork("I'll add tests in a follow-up.", 0)

	// Iteration 1: agent says "done" but hasn't resolved deferral
	hint := a.maybeWarnDeferredWork("I've completed the task. That's it.", 1)
	if hint == "" {
		t.Error("expected warning when completing with open deferrals")
	}
	if !strings.Contains(hint, "wrapping up") {
		t.Errorf("warning should mention wrapping up, got: %s", hint)
	}
}

func TestMaybeWarnDeferredWorkMaxWarnings(t *testing.T) {
	a := &Agent{
		deferredWork: newDeferredWorkState(),
	}

	// Create a deferral
	a.maybeWarnDeferredWork("I'll add tests later.", 0)

	// Trigger warning 1
	hint1 := a.maybeWarnDeferredWork("Continuing.", 2)
	if hint1 == "" {
		t.Error("expected first warning")
	}

	// Trigger warning 2
	a.deferredWork.items = append(a.deferredWork.items, deferredItem{
		patternID: "test",
		excerpt:   "I'll fix later",
		iteration: 1,
	})
	hint2 := a.maybeWarnDeferredWork("Continuing.", 3)
	if hint2 == "" {
		t.Error("expected second warning")
	}

	// Third should be suppressed
	hint3 := a.maybeWarnDeferredWork("Continuing.", 4)
	if hint3 != "" {
		t.Error("expected third warning to be suppressed")
	}
}

func TestDeferredWorkStateReset(t *testing.T) {
	s := newDeferredWorkState()
	s.recordDeferrals("I'll add tests later.", 0)
	s.warnings = 1

	s.reset()

	if len(s.items) != 0 {
		t.Error("items should be cleared after reset")
	}
	if s.warnings != 0 {
		t.Error("warnings should be cleared after reset")
	}
}

func TestMaybeWarnDeferredWorkNil(t *testing.T) {
	a := &Agent{}
	// Should not panic with nil state
	hint := a.maybeWarnDeferredWork("test", 0)
	if hint != "" {
		t.Error("expected empty hint with nil state")
	}
}
