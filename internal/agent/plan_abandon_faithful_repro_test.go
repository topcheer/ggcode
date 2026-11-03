package agent

import (
	"strings"
	"testing"
)

func TestPlanAbandonFaithfulExecutionFalsePositive(t *testing.T) {
	a := &Agent{planAbandon: newPlanAbandonState()}

	plan := "Here's my plan:\n" +
		"1. I'll read the auth module\n" +
		"2. Next, I'll fix the token refresh logic\n" +
		"3. Finally, I'll run the tests to verify"
	if hint := a.maybeWarnPlanAbandon(plan, nil); hint != "" {
		t.Fatalf("expected no warning on plan declaration, got: %s", hint)
	}

	execution := []string{
		"I read the auth module; the refresh path is in token.go line 42.",
		"I fixed the token refresh logic and updated both callers.",
		"I ran the test suite: all 12 tests pass.",
	}
	for i, txt := range execution {
		if hint := a.maybeWarnPlanAbandon(txt, nil); hint != "" {
			t.Fatalf("turn %d: unexpected warning during execution: %s", i+2, hint)
		}
	}

	evidence := &RunStats{
		FilesEdited: []string{"internal/auth/token.go", "internal/auth/callers.go"},
		CommandsRun: []string{"go test ./internal/auth/..."},
	}
	hint := a.maybeWarnPlanAbandon("The task is now complete.", evidence)
	if hint != "" {
		t.Fatalf("false positive on faithful completion (#490): warning must be suppressed with matching execution evidence, got: %s", hint)
	}

	b := &Agent{planAbandon: newPlanAbandonState()}
	if h := b.maybeWarnPlanAbandon(plan, nil); h != "" {
		t.Fatalf("plan declaration must not warn, got: %s", h)
	}
	emptyStats := &RunStats{}
	hint = b.maybeWarnPlanAbandon("The task is now complete.", emptyStats)
	if hint == "" {
		t.Fatal("true abandonment (declared edit+run steps, zero evidence) must still trigger")
	}
	if !strings.Contains(hint, "[plan-abandon]") {
		t.Fatalf("expected [plan-abandon] tag in warning, got: %s", hint)
	}
}
