package agent

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4409_BudgetStopReturnsSentinel(t *testing.T) {
	// #5543: the budget-stop gate stayed in the main loop (agent.go);
	// scan the stream-loop source files (shape-agnostic file targeting).
	src, err := readStreamLoopSource("agent.go", "run_turn_start_gate.go")
	if err != nil {
		t.Fatal(err)
	}

	gate := strings.Index(src, "if sessionBudgetStop {")
	if gate < 0 {
		t.Fatal("budget stop gate missing")
	}

	// #5543: the iteration bookkeeping moved into runTurnStartGate; the
	// gate body now ends at its own sentinel return.
	end := gate + 2200
	if to := strings.Index(src[gate:], "return ErrSessionBudgetExhausted"); to > 0 && gate+to < end {
		end = gate + to + len("return ErrSessionBudgetExhausted")
	}
	body := src[gate:end]
	if !strings.Contains(body, "return ErrSessionBudgetExhausted") {
		t.Fatalf("gate must return ErrSessionBudgetExhausted, gate body:\n%s", body)
	}

	for _, ln := range strings.Split(body, "\n") {
		if strings.TrimSpace(ln) == "return nil" {
			t.Fatalf("gate must not return nil (silent truncation), gate body:\n%s", body)
		}
	}

	if !strings.Contains(body, "StreamEventError") {
		t.Fatal("gate must emit a StreamEventError")
	}

	raw2, err := os.ReadFile("session_timeout.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw2), "var ErrSessionBudgetExhausted = errors.New(") {
		t.Fatal("ErrSessionBudgetExhausted sentinel missing from session_timeout.go")
	}
}
