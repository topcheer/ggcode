package agent

// #4409 probe: the session-budget hard-stop gate must return an explicit
// sentinel error (ErrSessionBudgetExhausted), not nil - headless callers
// (cron, sub-agents, ACP loops) judge completion by err and misjudged
// half-finished work as successful. Pinned at source level, same
// disclosure as the #3945 probe above (the wiring lives inside a
// 4000-line run loop; a behavioral harness for full budget exhaustion is
// disproportionate).

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4409_BudgetStopReturnsSentinel(t *testing.T) {
	raw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	gate := strings.Index(src, "if sessionBudgetStop {")
	if gate < 0 {
		t.Fatal("budget stop gate missing")
	}
	// The gate body must return the sentinel, not nil. Cut the window at
	// the next statement after the gate block to avoid matching unrelated
	// `return nil`s deeper in the loop body.
	end := gate + 2200
	if to := strings.Index(src[gate:], "runStats.Iterations = i + 1"); to > 0 && gate+to < end {
		end = gate + to
	}
	body := src[gate:end]
	if !strings.Contains(body, "return ErrSessionBudgetExhausted") {
		t.Fatalf("gate must return ErrSessionBudgetExhausted, gate body:\n%s", body)
	}
	// Line-precise: no bare `return nil` STATEMENT (comment prose may
	// mention the old behavior).
	for _, ln := range strings.Split(body, "\n") {
		if strings.TrimSpace(ln) == "return nil" {
			t.Fatalf("gate must not return nil (silent truncation), gate body:\n%s", body)
		}
	}
	// Mirror of the #611 timeout pattern: an error StreamEvent so
	// interactive/TUI consumers also see the terminal state.
	if !strings.Contains(body, "StreamEventError") {
		t.Fatal("gate must emit a StreamEventError")
	}
	// Sentinel is defined and exported for callers to errors.Is.
	raw2, err := os.ReadFile("session_timeout.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw2), "var ErrSessionBudgetExhausted = errors.New(") {
		t.Fatal("ErrSessionBudgetExhausted sentinel missing from session_timeout.go")
	}
}
