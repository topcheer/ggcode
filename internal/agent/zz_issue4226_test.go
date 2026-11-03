package agent

import (
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestInjectedInterruptionRecordsConstraints(t *testing.T) {
	a := &Agent{
		contextManager:    ctxpkg.NewManager(200000),
		constraintAmnesia: new(constraintAmnesiaState),
	}
	a.mu.Lock()
	a.onInterrupt = func() []provider.ContentBlock {
		return []provider.ContentBlock{{
			Type: "text",
			Text: "quick steering: do not modify the auth module while fixing the tests",
		}}
	}
	a.mu.Unlock()

	injected, _ := a.injectPendingInterruptions(10)
	if !injected {
		t.Fatal("expected injection to happen")
	}
	if len(a.constraintAmnesia.constraints) == 0 {
		t.Fatal("mid-run constraint was not recorded - detector stays blind (#4226 regression)")
	}

	if got := a.constraintAmnesia.constraints[0].messageIdx; got != 10 {
		t.Fatalf("messageIdx = %d, want injection iteration 10", got)
	}

	a.constraintAmnesia.noteEdit(21)
	if w := a.constraintAmnesia.maybeWarn(22); w == "" {
		t.Fatalf("reminder did not fire 12 iterations after mid-run constraint")
	}
}

func TestInjectedInterruptionBlankRecordsNothing(t *testing.T) {
	a := &Agent{
		contextManager:    ctxpkg.NewManager(200000),
		constraintAmnesia: new(constraintAmnesiaState),
	}
	a.mu.Lock()
	a.onInterrupt = func() []provider.ContentBlock {
		return []provider.ContentBlock{{Type: "text", Text: "   "}}
	}
	a.mu.Unlock()

	if injected, _ := a.injectPendingInterruptions(5); injected {
		t.Fatal("blank block should not inject")
	}
	if n := len(a.constraintAmnesia.constraints); n != 0 {
		t.Fatalf("recorded %d constraints from blank payload", n)
	}
}

func TestInjectedInterruptionNoConstraintStaysEmpty(t *testing.T) {
	a := &Agent{
		contextManager:    ctxpkg.NewManager(200000),
		constraintAmnesia: new(constraintAmnesiaState),
	}
	a.mu.Lock()
	a.onInterrupt = func() []provider.ContentBlock {
		return []provider.ContentBlock{{
			Type: "text",
			Text: "the build output looks good so far, keep going",
		}}
	}
	a.mu.Unlock()

	if injected, _ := a.injectPendingInterruptions(3); !injected {
		t.Fatal("expected injection to happen")
	}
	if n := len(a.constraintAmnesia.constraints); n != 0 {
		t.Fatalf("recorded %d constraints from constraint-free text", n)
	}
	if w := a.constraintAmnesia.maybeWarn(30); w != "" {
		t.Fatalf("reminder fired with empty ledger: %q", strings.TrimSpace(w))
	}
}
