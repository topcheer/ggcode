package agent

import (
	"strings"
	"testing"
)

func TestRecoveryBudgetEscalationLadder(t *testing.T) {
	rb := newRecoveryBudget()

	// Failures 1-2: within retry budget, no hint.
	if h := rb.observeFailure("run_command"); h != "" {
		t.Fatalf("failure 1 should be silent, got %q", h)
	}
	if h := rb.observeFailure("run_command"); h != "" {
		t.Fatalf("failure 2 should be silent, got %q", h)
	}
	// Failure 3: substitute rung.
	h := rb.observeFailure("run_command")
	if h == "" || !strings.Contains(h, "substitute") {
		t.Fatalf("failure 3 should fire substitute hint, got %q", h)
	}
	// Failure 4: rung already fired this run, silent.
	if h := rb.observeFailure("run_command"); h != "" {
		t.Fatalf("failure 4 should be silent (rung fired), got %q", h)
	}
	// Failure 5: escalate rung.
	h = rb.observeFailure("run_command")
	if h == "" || !strings.Contains(h, "exhausted") {
		t.Fatalf("failure 5 should fire escalate hint, got %q", h)
	}
	// Failure 6: terminal rung fired, silent.
	if h := rb.observeFailure("run_command"); h != "" {
		t.Fatalf("failure 6 should be silent, got %q", h)
	}
}

func TestRecoveryBudgetSuccessResets(t *testing.T) {
	rb := newRecoveryBudget()
	rb.observeFailure("grep")
	rb.observeFailure("grep")
	rb.observeSuccess("grep")
	if rb.counts("grep") != 0 {
		t.Fatalf("success must reset consecutive count, got %d", rb.counts("grep"))
	}
	if h := rb.observeFailure("grep"); h != "" {
		t.Fatalf("count after reset should be 1 (silent), got %q", h)
	}
}

func TestRecoveryBudgetPerToolIsolation(t *testing.T) {
	rb := newRecoveryBudget()
	rb.observeFailure("grep")
	rb.observeFailure("grep")
	// Different tool unaffected.
	if h := rb.observeFailure("edit_file"); h != "" {
		t.Fatalf("edit_file failure 1 should be silent, got %q", h)
	}
	if rb.counts("grep") != 2 {
		t.Fatalf("grep count changed by edit_file failures, got %d", rb.counts("grep"))
	}
}

func TestActionForCategoryMapping(t *testing.T) {
	cases := map[string]string{
		"edit_anchor_mismatch": "arg_repair",
		"file_not_found":       "refresh",
		"permission_denied":    "escalate",
		"timeout_network":      "retry",
		"unknown_category":     "retry",
	}
	for name, want := range cases {
		if got := actionForCategory(name); got != want {
			t.Errorf("actionForCategory(%q) = %q, want %q", name, got, want)
		}
	}
}
