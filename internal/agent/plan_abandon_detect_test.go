package agent

import (
	"strings"
	"testing"
)

func TestExtractFuturePlanSteps(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{
			name: "numbered list with future tense",
			text: "Here's my plan:\n1. I'll read the file\n2. Next, I'll fix the bug\n3. Then I'll run the tests\n4. Finally, I'll verify the output",
			want: 4,
		},
		{
			name: "numbered list with paren",
			text: "Plan:\n1) I need to read auth.go\n2) I will fix the token logic\n3) I'm going to update callers",
			want: 3,
		},
		{
			name: "mixed - only future tense counted",
			text: "1. I read the file\n2. I'll fix the bug\n3. The code is broken",
			want: 1,
		},
		{
			name: "no numbered list",
			text: "I'll read the file and fix the bug. Then I'll run the tests.",
			want: 0,
		},
		{
			name: "empty text",
			text: "",
			want: 0,
		},
		{
			name: "past tense steps not counted",
			text: "1. I read the file\n2. I fixed the bug\n3. I ran the tests",
			want: 0,
		},
		{
			name: "let me pattern",
			text: "1. Let me read the config\n2. Let me fix the handler\n3. Let me check the output",
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFuturePlanSteps(tt.text)
			if len(got) != tt.want {
				t.Errorf("extractFuturePlanSteps() got %d steps, want %d: %v", len(got), tt.want, got)
			}
		})
	}
}

func TestHasCompletionSignal(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"done", "The task is now done.", true},
		{"complete", "The fix is complete.", true},
		{"finished", "I've finished the implementation.", true},
		{"all done", "All done!", true},
		{"no signal", "I need to continue working on this.", false},
		{"empty", "", false},
		{"wraps up", "That should wrap it up.", true},
		{"resolved", "The bug has been resolved.", true},
		{"applied", "The change has been applied.", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasCompletionSignal(tt.text)
			if got != tt.want {
				t.Errorf("hasCompletionSignal(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestPlanAbandonDetection(t *testing.T) {
	// Test the full pattern: plan declared, then completion in later iteration.
	a := &Agent{planAbandon: newPlanAbandonState()}

	// Iteration 1: agent declares a plan with 4 future-tense steps.
	planText := "Here's my plan:\n" +
		"1. I'll read the auth module\n" +
		"2. Next, I'll fix the token refresh logic\n" +
		"3. Then, I'll update all callers\n" +
		"4. Finally, I'll run the tests to verify"

	hint := a.maybeWarnPlanAbandon(planText, nil)
	if hint != "" {
		t.Errorf("Expected no warning on plan declaration, got: %s", hint)
	}

	// Iteration 2: agent claims completion without evidence of executing all steps.
	completionText := "The task is now complete. The fix has been applied."

	hint = a.maybeWarnPlanAbandon(completionText, nil)
	if hint == "" {
		t.Fatal("Expected plan abandonment warning, got empty string")
	}

	if !strings.Contains(hint, "plan-abandon") {
		t.Errorf("Warning should contain [plan-abandon] tag: %s", hint)
	}
	if !strings.Contains(hint, "4 plan step") {
		t.Errorf("Warning should mention 4 plan steps: %s", hint)
	}
	if !strings.Contains(hint, "Declared steps") {
		t.Errorf("Warning should list declared steps: %s", hint)
	}
}

func TestPlanAbandonNoCompletion(t *testing.T) {
	a := &Agent{planAbandon: newPlanAbandonState()}

	planText := "Plan:\n1. I'll read the file\n2. I'll fix the bug\n3. I'll run tests"
	a.maybeWarnPlanAbandon(planText, nil)

	// Next iteration: no completion signal.
	workText := "I've read the file and I'm working on the fix now."
	hint := a.maybeWarnPlanAbandon(workText, nil)
	if hint != "" {
		t.Errorf("Expected no warning without completion signal, got: %s", hint)
	}
}

func TestPlanAbandonSameIterationCompletion(t *testing.T) {
	a := &Agent{planAbandon: newPlanAbandonState()}

	// Plan and completion in the same text - should not trigger because
	// the plan hasn't had time to be abandoned yet.
	text := "Plan:\n1. I'll read the file\n2. I'll fix the bug\n3. I'll run tests\n\nThe task is now complete."
	hint := a.maybeWarnPlanAbandon(text, nil)
	if hint != "" {
		t.Errorf("Expected no warning when plan and completion in same iteration, got: %s", hint)
	}
}

func TestPlanAbandonTooFewSteps(t *testing.T) {
	a := &Agent{planAbandon: newPlanAbandonState()}

	// Only 2 steps - below threshold of 3.
	planText := "Plan:\n1. I'll read the file\n2. I'll fix the bug"
	a.maybeWarnPlanAbandon(planText, nil)

	completionText := "The task is complete."
	hint := a.maybeWarnPlanAbandon(completionText, nil)
	if hint != "" {
		t.Errorf("Expected no warning for <3 plan steps, got: %s", hint)
	}
}

func TestPlanAbandonMaxWarnings(t *testing.T) {
	a := &Agent{planAbandon: newPlanAbandonState()}

	for round := 0; round < 3; round++ {
		// Declare plan.
		planText := "Plan:\n1. I'll read the file\n2. I'll fix the bug\n3. I'll run tests"
		a.maybeWarnPlanAbandon(planText, nil)

		// Claim completion.
		completionText := "The task is complete."
		hint := a.maybeWarnPlanAbandon(completionText, nil)

		if round < planAbandonMaxWarnings {
			if hint == "" {
				t.Errorf("Expected warning on round %d, got empty string", round)
			}
		} else {
			if hint != "" {
				t.Errorf("Expected no warning after max warnings on round %d, got: %s", round, hint)
			}
		}
	}
}

func TestPlanAbandonReset(t *testing.T) {
	a := &Agent{planAbandon: newPlanAbandonState()}

	// Set up a plan.
	planText := "Plan:\n1. I'll read the file\n2. I'll fix the bug\n3. I'll run tests"
	a.maybeWarnPlanAbandon(planText, nil)

	if len(a.planAbandon.declaredSteps) != 3 {
		t.Fatalf("Expected 3 declared steps, got %d", len(a.planAbandon.declaredSteps))
	}

	a.planAbandon.reset()

	if len(a.planAbandon.declaredSteps) != 0 {
		t.Errorf("Expected 0 declared steps after reset, got %d", len(a.planAbandon.declaredSteps))
	}
	if a.planAbandon.planIter != -1 {
		t.Errorf("Expected planIter=-1 after reset, got %d", a.planAbandon.planIter)
	}
}

func TestPlanStepExcerpt(t *testing.T) {
	short := "short line"
	if got := planStepExcerpt(short); got != short {
		t.Errorf("Expected %q, got %q", short, got)
	}

	long := strings.Repeat("a", 100)
	got := planStepExcerpt(long)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("Expected excerpt to end with '...', got %q", got)
	}
	if len(got) > 64 {
		t.Errorf("Excerpt too long: %d", len(got))
	}
}
