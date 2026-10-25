//go:build goolm

package agent

// #4421 probes (follow-ups to the #4409 sentinel):
//  1. post-loop budget-stop rescue - a stop armed on the final iteration
//     is swallowed by the loop condition; the post-loop block must check
//     sessionBudgetStop (before the generic max-iterations error) and
//     return ErrSessionBudgetExhausted.
//  2. isCancelled must exempt the budget sentinel - post-run reflection
//     must not fire LLM calls after the budget is exhausted ("No further
//     LLM calls this run" contract).

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4421_PostLoopBudgetStopRescue(t *testing.T) {
	raw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	// Locate the post-loop block (after the loop-top gate).
	probe := "budget stop armed on final iteration; sentinel surfaced post-loop"
	if !strings.Contains(src, probe) {
		t.Fatal("post-loop sessionBudgetStop rescue missing (#4421)")
	}
	// It must precede the generic max-iterations error inside the post-loop
	// region.
	maxIterIdx := strings.Index(src, `err := fmt.Errorf("max iterations (%d) reached"`)
	rescueIdx := strings.Index(src, probe)
	if maxIterIdx < 0 || rescueIdx > maxIterIdx {
		t.Fatalf("budget-stop rescue must come before the generic max-iterations error, rescue=%d maxIter=%d (#4421)", rescueIdx, maxIterIdx)
	}
	// And it must return the sentinel, not a generic error.
	if !strings.Contains(src[maxIterIdx-3000:maxIterIdx], "return ErrSessionBudgetExhausted") {
		t.Fatal("post-loop budget rescue must return ErrSessionBudgetExhausted (#4421)")
	}
}

func TestIssue4421_IsCancelledExemptsBudgetSentinel(t *testing.T) {
	raw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	i := strings.Index(src, "isCancelled := errors.Is(err, context.Canceled)")
	if i < 0 {
		t.Fatal("isCancelled definition missing")
	}
	window := src[i : i+400]
	if !strings.Contains(window, "ErrSessionBudgetExhausted") {
		t.Fatalf("isCancelled must exempt ErrSessionBudgetExhausted (no post-run LLM calls after budget exhaustion), window:\n%s (#4421)", window)
	}
}
