package agent

import (
	"strings"
	"testing"
)

func TestIssue3153_WarnedClearedOnSuccess(t *testing.T) {
	p := newPivotDecisionTracker()
	args := `{"command":"go test ./pkg/"}`
	const tool = "run_command"

	fail := func() { p.recordToolCall(tool, args, true, "exit status 1") }
	ok := func() { p.recordToolCall(tool, args, false, "ok") }

	for i := 0; i < pivotFirstThreshold; i++ {
		fail()
	}
	first := p.checkAndWarn()
	if first == "" || !strings.Contains(first, "pivot-decision") {
		t.Fatalf("first streak should warn, got %q", first)
	}
	if !strings.Contains(first, "REPAIR") {
		t.Fatalf("first warning should be the REPAIR-vs-PIVOT prompt, got %q", first)
	}

	ok()

	for i := 0; i < pivotFirstThreshold; i++ {
		fail()
	}
	second := p.checkAndWarn()
	if second == "" {
		t.Fatal("recurrent failure streak hit silent dead zone: no warning after success-reset (issue 3153)")
	}
	if !strings.Contains(second, "REPAIR") {
		t.Fatalf("recurrent warning should restart at the decision prompt, got %q", second)
	}
}

func TestIssue3153_ForcedPathStillWorks(t *testing.T) {
	p := newPivotDecisionTracker()
	args := `{"command":"make verify"}`
	const tool = "run_command"

	for i := 0; i < pivotFirstThreshold; i++ {
		p.recordToolCall(tool, args, true, "exit status 2")
	}
	if w := p.checkAndWarn(); w == "" || !strings.Contains(w, "REPAIR") {
		t.Fatalf("expected first warning, got %q", w)
	}
	for i := 0; i < pivotForcedThreshold-pivotFirstThreshold; i++ {
		p.recordToolCall(tool, args, true, "exit status 2")
	}
	w := p.checkAndWarn()
	if w == "" || !strings.Contains(w, "PIVOT") {
		t.Fatalf("expected forced PIVOT at threshold %d, got %q", pivotForcedThreshold, w)
	}
}
