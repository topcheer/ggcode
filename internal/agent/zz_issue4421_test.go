//go:build goolm

package agent

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

	probe := "budget stop armed on final iteration; sentinel surfaced post-loop"
	if !strings.Contains(src, probe) {
		t.Fatal("post-loop sessionBudgetStop rescue missing (#4421)")
	}

	maxIterIdx := strings.Index(src, `err := fmt.Errorf("max iterations (%d) reached"`)
	rescueIdx := strings.Index(src, probe)
	if maxIterIdx < 0 || rescueIdx > maxIterIdx {
		t.Fatalf("budget-stop rescue must come before the generic max-iterations error, rescue=%d maxIter=%d (#4421)", rescueIdx, maxIterIdx)
	}

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
