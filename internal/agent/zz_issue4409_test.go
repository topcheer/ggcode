package agent

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

	end := gate + 2200
	if to := strings.Index(src[gate:], "runStats.Iterations = i + 1"); to > 0 && gate+to < end {
		end = gate + to
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
