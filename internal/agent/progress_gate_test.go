package agent

import (
	"strings"
	"testing"
)

// sa-138: progress gate state machine tests (ReflexGrad-style mode switch).

func TestProgressGateTriggersOnCrossCategoryStreak(t *testing.T) {
	g := newProgressGateState()
	// 3 failures spanning 2 categories, no interleaved success.
	g.recordToolResult("old_text not found in file", true)
	g.recordToolResult("no such file: /x/y.go", true)
	g.recordToolResult("old_text does not match", true)
	if !g.shouldTrigger(5) {
		t.Fatal("expected trigger on 3-failure streak across 2 categories")
	}
	g.trigger(5)
	if !g.freezingMutations() {
		t.Fatal("expected freeze after trigger")
	}
	msg := g.consumePendingReplan()
	if msg == "" || !strings.Contains(msg, "REPLAN CHECKPOINT") {
		t.Fatalf("expected non-empty replan directive, got %q", msg)
	}
	// Directive must carry both category names for the model to reason about.
	if !strings.Contains(msg, "file-not-found") || !strings.Contains(msg, "old-text-mismatch") {
		t.Fatalf("replan directive missing categories: %q", msg)
	}
	if g.consumePendingReplan() != "" {
		t.Fatal("consumePendingReplan must clear the queued directive")
	}
	// Streak consumed; freeze ends with the batch.
	if g.shouldTrigger(6) {
		t.Fatal("streak must be consumed on trigger")
	}
	g.endBatch()
	if g.freezingMutations() {
		t.Fatal("endBatch must clear freeze")
	}
}

func TestProgressGateSuccessResetsStreak(t *testing.T) {
	g := newProgressGateState()
	g.recordToolResult("old_text not found", true)
	g.recordToolResult("no such file", true)
	// A single success means the agent is making progress - not stuck.
	g.recordToolResult("ok", false)
	g.recordToolResult("timeout after 30s", true)
	g.recordToolResult("permission denied", true)
	if g.shouldTrigger(9) {
		t.Fatal("interleaved success must reset the streak: no trigger after only 2 trailing failures")
	}
}

func TestProgressGateSingleCategoryDoesNotTrigger(t *testing.T) {
	g := newProgressGateState()
	// Same-category recurrence is error_strategy_loop's domain (advisory);
	// the behavioral gate needs cross-category evidence.
	for i := 0; i < 4; i++ {
		g.recordToolResult("no such file: /a/b.go", true)
	}
	if g.shouldTrigger(7) {
		t.Fatal("same-category streak must NOT trigger the progress gate")
	}
}

func TestProgressGateFireBudgetAndRefractory(t *testing.T) {
	g := newProgressGateState()
	fire := func(iter int) {
		g.recordToolResult("no such file", true)
		g.recordToolResult("old_text mismatch", true)
		g.recordToolResult("timeout", true)
		if !g.shouldTrigger(iter) {
			t.Fatalf("expected trigger at iter %d", iter)
		}
		g.trigger(iter)
	}
	fire(3)
	fire(10) // outside refractory window, budget 2/2
	// Third fire attempt: budget exhausted.
	g.recordToolResult("no such file", true)
	g.recordToolResult("old_text mismatch", true)
	g.recordToolResult("timeout", true)
	if g.shouldTrigger(20) {
		t.Fatal("fire budget of 2 per run must be enforced")
	}
}

func TestProgressGateRefractoryWindow(t *testing.T) {
	g := newProgressGateState()
	g.recordToolResult("no such file", true)
	g.recordToolResult("old_text mismatch", true)
	g.recordToolResult("timeout", true)
	g.trigger(5)
	// New streak inside the refractory window must not re-trigger while the
	// replan directive is still being tried.
	g.recordToolResult("no such file", true)
	g.recordToolResult("old_text mismatch", true)
	g.recordToolResult("timeout", true)
	if g.shouldTrigger(7) {
		t.Fatal("refractory window (4 iters) must suppress immediate re-trigger")
	}
	if !g.shouldTrigger(10) {
		t.Fatal("outside refractory window a fresh cross-category streak must trigger again (budget permitting)")
	}
}

func TestProgressGateFreezePlaceholderIsErrorText(t *testing.T) {
	g := newProgressGateState()
	g.trigger(1)
	ph := g.freezePlaceholder("edit_file")
	if !strings.Contains(ph, "frozen by progress gate") || !strings.Contains(ph, "edit_file") {
		t.Fatalf("placeholder must name the tool and the freeze: %q", ph)
	}
}
