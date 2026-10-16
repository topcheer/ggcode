package agent

import (
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

// #4226: mid-run injected guidance must feed the constraint-amnesia ledger.
// Before the fix, recordConstraints was only called from Run() on the entry
// userText; onInterrupt drains queued messages (they never replay as a
// Run()-entry userText), so steering constraints like "do not modify the auth
// module" scrolled out of attention untracked and maybeWarn stayed silent in
// exactly the long-run scenario the detector exists for.
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
	// Aging is measured from the injection iteration: recorded at 10, warned
	// at 10+12 iterations minimum.
	if got := a.constraintAmnesia.constraints[0].messageIdx; got != 10 {
		t.Fatalf("messageIdx = %d, want injection iteration 10", got)
	}
	if w := a.constraintAmnesia.maybeWarn(22); w == "" {
		t.Fatalf("reminder did not fire 12 iterations after mid-run constraint")
	}
}

// No blank drain: whitespace-only queued blocks must not record anything.
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

// Constraint-free steering text must not create entries (ledger stays clean).
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
